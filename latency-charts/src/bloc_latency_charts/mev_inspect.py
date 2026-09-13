"""Import a bounded Flashbots MEV-inspect CSV sample and saved canonical RPC data.

This adapter is offline. Acquisition metadata and raw responses must already exist.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import csv
import json
from pathlib import Path

from .economics import SCHEMA, _sha, analyze

WETH = '0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2'


def normalize(arbitrages, sandwiches, victims, enrichment, *, token=WETH):
    """Join observed strategy legs; retain all exclusion reasons and raw amounts."""
    blocks = enrichment['blocks']
    positions = {b['number']: {h.lower(): i for i, h in enumerate(b['transaction_hashes'])} for b in blocks}
    victim_rows = defaultdict(list)
    for row in victims:
        victim_rows[row['sandwich_id']].append(row)
    candidates, excluded, seen = [], [], set()

    def exclude(record, reason):
        excluded.append({**record, 'reason': reason})

    for category, rows in (('arbitrage', arbitrages), ('sandwich', sandwiches)):
        for row in rows:
            ident = f'{category}:{row["id"]}'
            if not row['id'] or ident in seen:
                raise ValueError('missing/duplicate source strategy id')
            seen.add(ident)
            number = int(row['block_number'])
            record = {'id': ident, 'category': category, 'block_number': number,
                      'profit_token': row['profit_token_address'].lower(),
                      'profit_amount': row['profit_amount'] or None}
            if number not in positions:
                exclude(record, 'outside_selected_blocks')
                continue
            if token is not None and record['profit_token'] != token.lower():
                exclude(record, 'other_profit_token')
                continue
            if row.get('error'):
                exclude({**record, 'source_error': row['error']}, 'source_error')
                continue
            if category == 'arbitrage':
                hashes = [row['transaction_hash'].lower()]
            else:
                observed = victim_rows[row['id']]
                if not observed:
                    exclude(record, 'missing_observed_victim')
                    continue
                if any(int(v['block_number']) != number for v in observed):
                    exclude(record, 'victim_block_mismatch')
                    continue
                front, back = (row[f'{leg}_swap_transaction_hash'].lower() for leg in ('frontrun', 'backrun'))
                victim_hashes = sorted({v['transaction_hash'].lower() for v in observed})
                hashes = [front, *victim_hashes, back]
            if any(h not in positions[number] for h in hashes):
                exclude({**record, 'transaction_hashes': hashes}, 'unresolved_transaction')
                continue
            if category == 'sandwich' and not all(
                positions[number][front] < positions[number][h] < positions[number][back]
                for h in victim_hashes
            ):
                exclude({**record, 'transaction_hashes': hashes}, 'invalid_sandwich_order')
                continue
            candidates.append({**record, 'transaction_hashes': sorted(hashes, key=positions[number].get)})

    # A shared transaction can contain repeated classifier cycles or overlapping
    # strategy labels. Exclude every ambiguous record, independently of row order.
    owners = defaultdict(set)
    for record in candidates:
        for h in record['transaction_hashes']:
            owners[(record['block_number'], record['profit_token'], h)].add(record['id'])
    ambiguous = set().union(*(ids for ids in owners.values() if len(ids) > 1))
    retained = []
    for record in candidates:
        if record['id'] in ambiguous:
            exclude(record, 'overlapping_strategy_transactions')
        else:
            retained.append(record)
    provenance = {
        'evidence_class': 'historical_convenience_sample',
        'sample_description': enrichment['selection'],
        'label_coverage': 'partial; per-block completeness unproven',
        'value_semantics': 'gross_token_delta_before_fees',
        'profit_token_filter': token,
        'finalized_reference': enrichment['finalized_reference'],
        'finality_basis': 'Canonical block-by-number responses below the saved finalized height; RPC trust, not a consensus proof.',
        'victim_coverage': 'All observed victim transaction hashes are joined; completeness of victim labels is unproven.',
        'dataset_license': 'No explicit license verified for the S3 data; code licensing does not license the dataset. Local research probe only.',
        'classifier_limitations': 'Historical MEV-inspect labels, not exhaustive MEV detection. Token deltas omit fees, bribes, off-chain hedges and inventory valuation.',
        'import_coverage': {
            'source_strategy_count': len(arbitrages) + len(sandwiches),
            'source_victim_row_count': len(victims),
            'retained_strategy_count': len(retained),
            'excluded_by_reason': dict(sorted(Counter(r['reason'] for r in excluded).items())),
            'excluded_records': sorted(excluded, key=lambda r: r['id']),
        },
    }
    result = {'schema': SCHEMA, 'provenance': provenance, 'blocks': blocks,
              'strategies': sorted(retained, key=lambda r: (r['block_number'], r['id']))}
    analyze(result)
    return result


def validate_blocks(enrichment, raw):
    """Bind normalized ordering and optional gas maps to saved RPC results."""
    final = enrichment['finalized_reference']
    if (int(raw['finalized']['number'], 16), raw['finalized']['hash']) != (final['number'], final['hash']):
        raise ValueError('finalized reference mismatch')
    for block in enrichment['blocks']:
        number = block['number']
        source = raw[str(number)]
        if number > final['number']:
            raise ValueError('block above finalized reference')
        if (int(source['number'], 16), source['hash'], source['transactions'], int(source['gasUsed'], 16),
            int(source['timestamp'], 16)) != (number, block['hash'], block['transaction_hashes'],
                                           block['block_gas_used'], block['timestamp']):
            raise ValueError(f'canonical block mismatch: {number}')
        gas = block.get('gas_used_by_transaction')
        if gas is not None:
            receipts = raw.get(f'receipts-{number}')
            if (not isinstance(receipts, list) or len(receipts) != len(block['transaction_hashes'])
                    or len(gas) != len(receipts)):
                raise ValueError('gas map requires complete raw receipts')
            ordered = sorted(receipts, key=lambda r: int(r['transactionIndex'], 16))
            total = 0
            for i, receipt in enumerate(ordered):
                total += int(receipt['gasUsed'], 16)
                if (int(receipt['blockNumber'], 16), receipt['blockHash'], int(receipt['transactionIndex'], 16),
                    receipt['transactionHash'], int(receipt['gasUsed'], 16), int(receipt['cumulativeGasUsed'], 16)) != (
                    number, block['hash'], i, block['transaction_hashes'][i], gas[i], total):
                    raise ValueError('receipt identity/order/gas mismatch')
            if total != block['block_gas_used']:
                raise ValueError('receipt gas total mismatch')


def import_directory(source_dir: Path, output: Path, *, token=WETH):
    """Verify acquisition manifests before writing one normalized JSON artifact."""
    source_dir = source_dir.resolve()
    source_files = {}

    def checked(path, expected=None):
        path = source_dir / path
        digest = _sha(path)
        if expected is not None and digest != expected:
            raise ValueError(f'source checksum mismatch: {path}')
        source_files[str(path)] = digest
        return path

    metadata = json.loads(checked('provenance.json').read_text())
    tables = {}
    for source in metadata['sources']:
        raw_bytes = checked(source['raw_file'], source['raw_sha256']).read_bytes()
        path = checked(source['complete_rows_file'], source['complete_rows_sha256'])
        expected = raw_bytes if source['is_full_object'] else raw_bytes[:raw_bytes.rfind(b'\n') + 1]
        if not expected or path.read_bytes() != expected:
            raise ValueError('extracted CSV does not match raw source bytes')
        with path.open(newline='') as stream:
            rows = list(csv.DictReader(stream, strict=True))
        if len(rows) != source['row_count']:
            raise ValueError('source CSV row count mismatch')
        name = source['url'].rsplit('/', 1)[-1]
        if name in tables:
            raise ValueError('duplicate source table')
        tables[name] = rows
    enrichment = json.loads(checked('enrichment.json').read_text())
    requests = json.loads(checked(enrichment['request_manifest']).read_text())
    raw = {}
    for entry in requests:
        request = json.loads(checked(entry['request_file'], entry['request_sha256']).read_text())
        if request != entry['request']:
            raise ValueError('saved RPC request mismatch')
        if entry.get('http_status') != 200 or not entry.get('response_file'):
            continue
        response = json.loads(checked(entry['response_file'], entry['response_sha256']).read_text())
        if response.get('id') != request.get('id'):
            raise ValueError('RPC response id mismatch')
        if response.get('error') or response.get('result') is None:
            continue
        if request['method'] == 'eth_getBlockByNumber':
            param, full = request['params']
            if full is not False:
                raise ValueError('expected canonical transaction-hash block response')
            key = 'finalized' if param == 'finalized' else str(int(param, 16))
        elif request['method'] == 'eth_getBlockReceipts':
            key = f'receipts-{int(request["params"][0], 16)}'
        else:
            continue
        if key in raw:
            raise ValueError('duplicate canonical RPC result')
        raw[key] = response['result']
    validate_blocks(enrichment, raw)
    result = normalize(tables['arbitrages.csv'], tables['sandwiches.csv'],
                       tables['sandwiched_swaps.csv'], enrichment, token=token)
    provenance = result['provenance']
    provenance['acquisition'] = metadata
    provenance['liquidation_rows_without_profit_field'] = len(tables.get('liquidations.csv', []))
    provenance['adapter_source_sha256'] = _sha(Path(__file__))
    provenance['source_files'] = [{'path': p, 'sha256': digest} for p, digest in sorted(source_files.items())]
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open('x') as stream:
        stream.write(json.dumps(result, indent=2, sort_keys=True) + '\n')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source_dir', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--token', default=WETH, help='Profit-token address; default WETH (no asset conversion).')
    args = parser.parse_args()
    try:
        result = import_directory(args.source_dir, args.output, token=args.token)
    except (ValueError, KeyError, TypeError, OSError, csv.Error) as error:
        parser.exit(1, f'Invalid MEV-inspect sample: {error}\n')
    print(f'Imported {len(result["strategies"])} strategies to {args.output}')


if __name__ == '__main__':
    main()
