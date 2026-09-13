"""Synthetic source rows exercise joins and evidence exclusions, not real MEV claims."""
from copy import deepcopy
import csv
import hashlib
import json

import pytest

from bloc_economics.mev_inspect import import_directory, normalize, validate_blocks, WETH
from test_economics import tx


def inputs():
    arbitrages = [{'id': 'arb', 'block_number': '100', 'transaction_hash': tx(4),
                   'profit_token_address': WETH, 'profit_amount': '40', 'error': ''}]
    sandwiches = [{'id': 'sandwich', 'block_number': '100',
        'frontrun_swap_transaction_hash': tx(1), 'backrun_swap_transaction_hash': tx(3),
        'profit_token_address': WETH, 'profit_amount': '60'}]
    victims = [{'sandwich_id': 'sandwich', 'block_number': '100', 'transaction_hash': tx(2)}] * 2
    enrichment = {'selection': 'synthetic fixture', 'finalized_reference': {'number': 200, 'hash': tx(200)},
                  'blocks': [{'number': 100, 'hash': tx(100), 'timestamp': 1,
                    'transaction_hashes': [tx(i) for i in range(1, 5)],
                    'gas_used_by_transaction': None, 'block_gas_used': 100}]}
    return arbitrages, sandwiches, victims, enrichment


def test_join_deduplicates_internal_victim_swaps_without_splitting_profit():
    result = normalize(*inputs())
    sandwich = next(r for r in result['strategies'] if r['category'] == 'sandwich')
    assert sandwich['transaction_hashes'] == [tx(1), tx(2), tx(3)]
    assert sandwich['profit_amount'] == '60'
    assert result['provenance']['label_coverage'] == 'partial; per-block completeness unproven'
    assert result['provenance']['import_coverage']['retained_strategy_count'] == 2


def test_all_ambiguous_overlaps_excluded_independent_of_source_order():
    args = inputs()
    other = dict(args[0][0], id='overlap', transaction_hash=tx(1), profit_amount='20')
    args[0].append(other)
    first = normalize(*args)
    args[0].reverse()
    second = normalize(*args)
    assert first['strategies'] == second['strategies']
    assert len(first['strategies']) == 1
    excluded = first['provenance']['import_coverage']['excluded_records']
    assert {r['id'] for r in excluded} == {'arbitrage:overlap', 'sandwich:sandwich'}
    assert {r['reason'] for r in excluded} == {'overlapping_strategy_transactions'}


def test_unknown_negative_and_source_exclusions_are_visible():
    args = inputs()
    args[0][0]['profit_amount'] = ''
    args[1][0]['profit_amount'] = '-5'
    args[0].extend([dict(args[0][0], id='error', error='detector error'),
                    dict(args[0][0], id='other-token', profit_token_address='other'),
                    dict(args[0][0], id='outside', block_number='101')])
    result = normalize(*args)
    assert {s['profit_amount'] for s in result['strategies']} == {None, '-5'}
    counts = result['provenance']['import_coverage']['excluded_by_reason']
    assert counts == {'source_error': 1, 'other_profit_token': 1, 'outside_selected_blocks': 1}


@pytest.mark.parametrize('change,reason', [
    (lambda a: a[2].clear(), 'missing_observed_victim'),
    (lambda a: a[2][0].update(transaction_hash=tx(99)), 'unresolved_transaction'),
    (lambda a: a[2][0].update(transaction_hash=tx(4)), 'invalid_sandwich_order'),
    (lambda a: a[2][0].update(block_number='101'), 'victim_block_mismatch'),
])
def test_incomplete_or_inconsistent_sandwich_does_not_silently_enter_sample(change, reason):
    args = inputs()
    change(args)
    result = normalize(*args)
    assert len(result['strategies']) == 1
    assert result['provenance']['import_coverage']['excluded_by_reason'] == {reason: 1}


def test_duplicate_source_id_rejected():
    args = inputs()
    args[0].append(deepcopy(args[0][0]))
    with pytest.raises(ValueError, match='duplicate'):
        normalize(*args)


def test_canonical_order_and_finality_are_checked_against_raw_responses():
    enrichment = inputs()[3]
    raw = {'100': {'number': hex(100), 'hash': tx(100), 'timestamp': '0x1',
                   'transactions': [tx(i) for i in range(1, 5)], 'gasUsed': hex(100)},
           'finalized': {'number': hex(200), 'hash': tx(200)}}
    validate_blocks(enrichment, raw)
    wrong = deepcopy(enrichment)
    wrong['blocks'][0]['transaction_hashes'].reverse()
    with pytest.raises(ValueError, match='canonical block'):
        validate_blocks(wrong, raw)
    raw['finalized']['number'] = hex(99)
    with pytest.raises(ValueError, match='finalized'):
        validate_blocks(enrichment, raw)


def test_gas_requires_validated_receipts_not_just_an_array():
    enrichment = inputs()[3]
    raw = {'100': {'number': hex(100), 'hash': tx(100), 'timestamp': '0x1',
                   'transactions': [tx(i) for i in range(1, 5)], 'gasUsed': hex(100)},
           'finalized': {'number': hex(200), 'hash': tx(200)}}
    enrichment['blocks'][0]['gas_used_by_transaction'] = [10, 20, 30, 40]
    with pytest.raises(ValueError, match='receipt'):
        validate_blocks(enrichment, raw)


def acquisition(tmp_path):
    """Save synthetic source bytes and RPC acquisition evidence, without a network."""
    args = inputs()
    metadata = {'sources': []}
    sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
    for name, rows in zip(('arbitrages', 'sandwiches', 'sandwiched_swaps'), args[:3]):
        path = tmp_path / f'{name}.csv'
        with path.open('w', newline='') as stream:
            writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
            writer.writeheader()
            writer.writerows(rows)
        raw = tmp_path / f'{name}.raw'
        raw.write_bytes(path.read_bytes() + b'incomplete-next-row')
        metadata['sources'].append({'url': f'https://example.invalid/{name}.csv',
            'raw_file': raw.name, 'raw_sha256': sha(raw), 'complete_rows_file': path.name,
            'complete_rows_sha256': sha(path), 'row_count': len(rows), 'is_full_object': False})
    (tmp_path / 'provenance.json').write_text(json.dumps(metadata))
    enrichment = args[3]
    manifest = []
    for param, result in [('finalized', {'number': hex(200), 'hash': tx(200)}),
                         (hex(100), {'number': hex(100), 'hash': tx(100), 'timestamp': '0x1',
                          'transactions': [tx(i) for i in range(1, 5)], 'gasUsed': hex(100)})]:
        request = {'jsonrpc': '2.0', 'id': 1, 'method': 'eth_getBlockByNumber', 'params': [param, False]}
        reqpath, respath = tmp_path / f'{param}.request.json', tmp_path / f'{param}.response.json'
        reqpath.write_text(json.dumps(request))
        respath.write_text(json.dumps({'jsonrpc': '2.0', 'id': 1, 'result': result}))
        manifest.append({'request': request, 'request_file': str(reqpath), 'request_sha256': sha(reqpath),
            'response_file': str(respath), 'response_sha256': sha(respath), 'http_status': 200})
    manifest_path = tmp_path / 'requests.json'
    manifest_path.write_text(json.dumps(manifest))
    enrichment['request_manifest'] = str(manifest_path)
    (tmp_path / 'enrichment.json').write_text(json.dumps(enrichment))
    return metadata


def test_import_directory_binds_extracted_rows_to_downloaded_bytes(tmp_path):
    metadata = acquisition(tmp_path)
    result = import_directory(tmp_path, tmp_path / 'valid.json')
    assert len(result['strategies']) == 2
    assert result['provenance']['source_files']
    extracted = tmp_path / 'arbitrages.csv'
    extracted.write_text(extracted.read_text().replace(',40,', ',999999999999999999999,'))
    metadata['sources'][0]['complete_rows_sha256'] = hashlib.sha256(extracted.read_bytes()).hexdigest()
    (tmp_path / 'provenance.json').write_text(json.dumps(metadata))
    with pytest.raises(ValueError, match='raw source bytes'):
        import_directory(tmp_path, tmp_path / 'invalid.json')
    assert not (tmp_path / 'invalid.json').exists()


@pytest.mark.parametrize('change', [
    lambda r: r[0].update(blockHash=tx(999)),
    lambda r: r[1].update(transactionIndex='0x0'),
    lambda r: r[2].update(cumulativeGasUsed='0x1'),
    lambda r: r.pop(),
])
def test_complete_receipts_require_identity_order_and_cumulative_gas(change):
    enrichment = inputs()[3]
    enrichment['blocks'][0]['gas_used_by_transaction'] = [10, 20, 30, 40]
    receipts = [{'blockNumber': hex(100), 'blockHash': tx(100), 'transactionIndex': hex(i),
                 'transactionHash': tx(i + 1), 'gasUsed': hex(g), 'cumulativeGasUsed': hex(total)}
                for i, (g, total) in enumerate(zip([10, 20, 30, 40], [10, 30, 60, 100]))]
    raw = {'100': {'number': hex(100), 'hash': tx(100), 'timestamp': '0x1',
                   'transactions': [tx(i) for i in range(1, 5)], 'gasUsed': hex(100)},
           'finalized': {'number': hex(200), 'hash': tx(200)}, 'receipts-100': receipts}
    validate_blocks(enrichment, raw)
    change(receipts)
    with pytest.raises(ValueError, match='receipt'):
        validate_blocks(enrichment, raw)
