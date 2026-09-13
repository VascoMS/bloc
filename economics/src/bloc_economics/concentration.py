"""Offline concentration of detected positive token profit; not causal BLOC loss."""
from __future__ import annotations

import argparse
from collections import defaultdict
import csv
from decimal import Decimal, localcontext
import hashlib
from importlib.metadata import version
import json
from pathlib import Path
import re
import sys

SCHEMA = 'bloc-mev-concentration/v1'


def _integer(value, name, minimum=0):
    if type(value) is not int or value < minimum:
        raise ValueError(f'{name}: expected integer >= {minimum}')
    return value


def _hash(value):
    if not isinstance(value, str) or not re.fullmatch(r'0x[0-9a-fA-F]{64}', value):
        raise ValueError('invalid block/transaction hash')
    return value.lower()


def _ratio(a, b):
    if not b:
        return None
    with localcontext() as ctx:
        ctx.prec = 50
        if a == 0 or a == b:
            return '0' if a == 0 else '1'
        return format(Decimal(a) / Decimal(b), 'f').rstrip('0').rstrip('.')


def analyze(data, *, first_k=(1, 5, 10, 25, 50), gas_fractions=('0.05', '0.1', '0.25', '0.5', '1')):
    """Bracket strategy involvement in whole-transaction prefixes, per asset.

    Any-leg/all-legs are positional views, never bounds on causal revenue loss.
    The gas axis uses actual gas consumed by the full original block, not its cap.
    """
    if data.get('schema') != SCHEMA:
        raise ValueError('unsupported schema')
    provenance = data['provenance']
    for field in ('evidence_class', 'sample_description', 'label_coverage', 'value_semantics'):
        if not isinstance(provenance.get(field), str) or not provenance[field].strip():
            raise ValueError(f'missing provenance: {field}')
    if provenance['value_semantics'] != 'gross_token_delta_before_fees':
        raise ValueError('unsupported value semantics; do not mix profit definitions')
    if provenance['evidence_class'] not in ('fixture', 'historical_convenience_sample', 'historical_sample'):
        raise ValueError('unsupported evidence class')
    ks = sorted(set(_integer(k, 'first_k', 1) for k in first_k))
    fractions = sorted(set(Decimal(str(f)) for f in gas_fractions))
    if any(not f.is_finite() or not 0 < f <= 1 for f in fractions):
        raise ValueError('gas fractions must be in (0, 1]')
    blocks = {}
    for block in data['blocks']:
        number = _integer(block['number'], 'block number')
        if number in blocks:
            raise ValueError('duplicate block number')
        _hash(block['hash'])
        hashes = [_hash(h) for h in block['transaction_hashes']]
        if len(hashes) != len(set(hashes)):
            raise ValueError('duplicate block transaction')
        gas = block.get('gas_used_by_transaction')
        cumulative = None
        if gas is not None:
            if len(gas) != len(hashes):
                raise ValueError('partial gas receipts')
            cumulative, total = [], 0
            for value in gas:
                total += _integer(value, 'transaction gas', 1)
                cumulative.append(total)
        blocks[number] = {'positions': {h: i + 1 for i, h in enumerate(hashes)},
                          'cumulative': cumulative, 'transaction_count': len(hashes)}
    records, identifiers, occupied = [], set(), set()
    assets = defaultdict(lambda: {'strategy_count': 0, 'unknown_profit_count': 0,
        'zero_profit_count': 0, 'negative_profit_count': 0, 'negative_profit_sum': 0,
        'positive_profit_count': 0, 'positive_profit_total': 0})
    for strategy in data['strategies']:
        ident = strategy['id']
        if not isinstance(ident, str) or not ident or ident in identifiers:
            raise ValueError('missing/duplicate strategy id')
        identifiers.add(ident)
        block_number = _integer(strategy['block_number'], 'strategy block number')
        if block_number not in blocks:
            raise ValueError(f'{ident}: missing block')
        block = blocks[block_number]
        token = strategy['profit_token']
        if not isinstance(token, str) or not token.strip():
            raise ValueError('missing profit token')
        token = token.lower()
        hashes = [_hash(h) for h in strategy['transaction_hashes']]
        if not hashes or len(hashes) != len(set(hashes)):
            raise ValueError(f'{ident}: empty/duplicate strategy transactions')
        positions = []
        for h in hashes:
            if h not in block['positions']:
                raise ValueError(f'{ident}: unresolved transaction position')
            key = (block_number, token, h)
            if key in occupied:
                raise ValueError('overlapping strategies in one profit asset; resolve attribution first')
            occupied.add(key)
            positions.append(block['positions'][h])
        raw = strategy['profit_amount']
        if raw is not None and (not isinstance(raw, str) or not re.fullmatch(r'-?\d+', raw)):
            raise ValueError('profit amount must be an integer decimal string or null')
        amount = None if raw is None else int(raw)
        coverage = assets[token]
        coverage['strategy_count'] += 1
        if amount is None:
            coverage['unknown_profit_count'] += 1
        elif amount < 0:
            coverage['negative_profit_count'] += 1
            coverage['negative_profit_sum'] += amount
        elif amount == 0:
            coverage['zero_profit_count'] += 1
        else:
            coverage['positive_profit_count'] += 1
            coverage['positive_profit_total'] += amount
        records.append({'id': ident, 'block_number': block_number, 'category': strategy['category'],
            'profit_token': token, 'profit_amount': raw, 'first_position': min(positions),
            'last_position': max(positions), 'leg_transaction_count': len(positions),
            'block_transaction_count': block['transaction_count']})
    used_blocks = {r['block_number'] for r in records}
    gas_ready = bool(used_blocks) and all(blocks[n]['cumulative'] for n in used_blocks)
    axes = [('first_k_transactions', str(k), {n: k for n in used_blocks}) for k in ks]
    if gas_ready:
        for fraction in fractions:
            cutoffs = {}
            for n in used_blocks:
                cumulative = blocks[n]['cumulative']
                # Only whole original transactions ending within the boundary qualify.
                cutoffs[n] = sum(Decimal(g) <= fraction * cumulative[-1] for g in cumulative)
            axes.append(('fraction_of_block_gas_used', str(fraction), cutoffs))
    rows = []
    for token, counts in sorted(assets.items()):
        positive = [r for r in records if r['profit_token'] == token and r['profit_amount'] is not None
                    and int(r['profit_amount']) > 0]
        denominator = counts['positive_profit_total']
        for axis, cutoff, limits in axes:
            any_value = sum(int(r['profit_amount']) for r in positive if r['first_position'] <= limits[r['block_number']])
            all_value = sum(int(r['profit_amount']) for r in positive if r['last_position'] <= limits[r['block_number']])
            rows.append({'profit_token': token, 'axis': axis, 'cutoff': cutoff,
                'positive_profit_total': str(denominator), 'all_legs_profit': str(all_value),
                'any_leg_profit': str(any_value), 'all_legs_share': _ratio(all_value, denominator),
                'any_leg_share': _ratio(any_value, denominator)})
        counts['positive_profit_total'] = str(denominator)
        counts['negative_profit_sum'] = str(counts['negative_profit_sum'])
    return {'coverage': {'block_count': len(blocks), 'strategy_block_count': len(used_blocks),
        'strategy_count': len(records), 'gas_concentration_status': 'available' if gas_ready else 'unavailable',
        'assets': dict(sorted(assets.items())), 'provenance': provenance},
        'strategies': sorted(records, key=lambda r: (r['block_number'], r['first_position'], r['id'])),
        'concentration': rows}


def _sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _csv(path, rows):
    if not rows:
        path.write_text('')
        return
    with path.open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def report(input_path: Path, output: Path):
    data = json.loads(input_path.read_text())
    sources = data['provenance'].get('source_files', [])
    if data['provenance']['evidence_class'] != 'fixture' and not sources:
        raise ValueError('historical input requires hashed source files')
    for source in sources:
        path = input_path.parent / source['path']
        if _sha(path) != source['sha256']:
            raise ValueError(f'source checksum mismatch: {source["path"]}')
    result = analyze(data)
    if output.exists():
        raise ValueError('output already exists; use a new artifact directory')
    output.mkdir(parents=True)
    _csv(output / 'concentration.csv', result['concentration'])
    _csv(output / 'strategy_positions.csv', result['strategies'])
    (output / 'coverage.json').write_text(json.dumps(result['coverage'], indent=2, sort_keys=True) + '\n')
    provenance = data['provenance']
    table = ['| Profit token | First k transactions | All legs inside | Any leg inside |',
             '|---|---:|---:|---:|']
    for row in result['concentration']:
        if row['axis'] == 'first_k_transactions':
            shares = ['unavailable' if row[key] is None else f'{Decimal(row[key]) * 100:.2f}%'
                      for key in ('all_legs_share', 'any_leg_share')]
            table.append(f'| `{row["profit_token"]}` | {row["cutoff"]} | {shares[0]} | {shares[1]} |')
    table_text = '\n'.join(table) if result['concentration'] else 'No retained strategies.'
    text = f'''# Historical detected-MEV concentration pilot

Evidence: `{provenance['evidence_class']}`. Sample: {provenance['sample_description']}.
Label coverage: **{provenance['label_coverage']}**.

{result['coverage']['strategy_count']} retained strategies across {result['coverage']['strategy_block_count']} blocks.
Gas-position concentration: **{result['coverage']['gas_concentration_status']}**.

## Sample results

{table_text}

## Interpretation

Values are gross detected token deltas before fees, in each token's base units.
Assets are never pooled. The denominator is the sum of strictly positive known
profits among retained sampled strategies. Missing, zero, and negative profits
remain in coverage; this is not a net-profit share or an estimate of all MEV.

At each cutoff, the all-legs curve counts strategies wholly inside the prefix;
the any-leg curve counts strategies touching it. These are positional involvement
views, not causal bounds on BLOC revenue loss. No profit is allocated across legs.
Transaction positions are one-based. Gas cutoffs use actual original block gas
consumption and include only whole transactions ending within the cutoff.

These are detected strategy values, not builder receipts or proposer payments.
The sample does not establish BLOC profitability, mainnet-wide concentration,
independent sampled opportunities, or a compensation rate. Partial label coverage
cannot establish complete per-block MEV totals. No confidence interval is claimed
for a convenience sample. Finality and source-coverage assertions remain limited
to the supplied provenance, which is retained in coverage.json.

## Files

- concentration.csv: exact integer amounts and decimal positional shares.
- strategy_positions.csv: resolved first/last positions and source strategy IDs.
- coverage.json: asset-specific unknown/loss/zero counts and input provenance.
- concentration.png: descriptive curves for positive observed value only.
'''
    (output / 'REPORT.md').write_text(text)
    _plot(result, output / 'concentration.png')
    manifest = {'schema': 'bloc-mev-report/v1', 'input_sha256': _sha(input_path),
                'analysis_source_sha256': _sha(Path(__file__)), 'python_version': sys.version.split()[0],
                'matplotlib_version': version('matplotlib'),
                'input_path': str(input_path.resolve()),
                'source_files': sources,
                'output_sha256': {p.name: _sha(p) for p in sorted(output.iterdir()) if p.is_file()}}
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    return result


def _plot(result, destination):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    assets = list(result['coverage']['assets'])
    fig, axes = plt.subplots(max(1, len(assets)), 2, figsize=(10, max(3, len(assets) * 3)), squeeze=False)
    for i, token in enumerate(assets):
        for j, axis in enumerate(('first_k_transactions', 'fraction_of_block_gas_used')):
            rows = [r for r in result['concentration'] if r['profit_token'] == token and r['axis'] == axis]
            ax = axes[i, j]
            label = token if len(token) < 24 else token[:10] + '…' + token[-6:]
            ax.set_title(label)
            ax.set_xlabel('First k transactions' if j == 0 else 'Fraction of original block gas used')
            ax.set_ylabel('Share of sampled positive value')
            ax.set_ylim(0, 1.05)
            if rows and rows[0]['all_legs_share'] is not None:
                x = [float(r['cutoff']) for r in rows]
                ax.plot(x, [float(r['all_legs_share']) for r in rows], 'o-', label='All legs inside')
                ax.plot(x, [float(r['any_leg_share']) for r in rows], 's--', label='Any leg inside')
                ax.legend(fontsize=8)
            else:
                message = ('Complete receipts unavailable' if j == 1 and not rows
                           else 'No known positive value')
                ax.text(.5, .5, message, ha='center', transform=ax.transAxes)
            ax.grid(alpha=.2)
    if not assets:
        for ax in axes[0]:
            ax.set_axis_off()
            ax.text(.5, .5, 'No retained strategies', ha='center', transform=ax.transAxes)
    fig.suptitle('Detected positive gross value — descriptive sample only', fontsize=12)
    fig.tight_layout()
    fig.savefig(destination, dpi=150)
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('input', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        result = report(args.input, args.output)
    except (ValueError, KeyError, TypeError, OSError) as error:
        parser.exit(1, f'Invalid economics evidence: {error}\n')
    print(f'Retained {result["coverage"]["strategy_count"]} strategies; report: {args.output / "REPORT.md"}')


if __name__ == '__main__':
    main()
