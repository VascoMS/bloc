"""Hand-checked detected-value concentration examples, not BLOC revenue."""
from copy import deepcopy
import json
import subprocess
import sys
import pytest


def tx(n):
    return '0x' + f'{n:064x}'


def sample():
    return {'schema': 'bloc-mev-concentration/v1',
        'provenance': {'evidence_class': 'fixture', 'sample_description': 'Hand example',
                       'label_coverage': 'partial', 'value_semantics': 'gross_token_delta_before_fees',
                       'source_files': []},
        'blocks': [{'number': 1, 'hash': tx(100), 'transaction_hashes': [tx(i) for i in range(1, 5)],
                    'gas_used_by_transaction': [10, 20, 30, 40]}],
        'strategies': [
            {'id': 's', 'block_number': 1, 'category': 'sandwich',
             'transaction_hashes': [tx(1), tx(3)], 'profit_token': 'token-a', 'profit_amount': '60'},
            {'id': 'a', 'block_number': 1, 'category': 'arbitrage',
             'transaction_hashes': [tx(4)], 'profit_token': 'token-a', 'profit_amount': '40'}]}


def analyze(data):
    from bloc_economics.concentration import analyze
    return analyze(data, first_k=[1, 3, 4], gas_fractions=['0.5', '1'])


def row(result, kind, cutoff, token='token-a'):
    return next(r for r in result['concentration'] if r['axis'] == kind and
                r['cutoff'] == str(cutoff) and r['profit_token'] == token)


def test_multi_leg_profit_is_bracketed_without_splitting_or_double_counting():
    result = analyze(sample())
    first = row(result, 'first_k_transactions', 1)
    assert first['positive_profit_total'] == '100'
    assert first['all_legs_profit'] == '0'
    assert first['any_leg_profit'] == '60'
    assert first['all_legs_share'] == '0'
    assert first['any_leg_share'] == '0.6'
    assert row(result, 'first_k_transactions', 3)['all_legs_profit'] == '60'
    assert row(result, 'first_k_transactions', 4)['all_legs_profit'] == '100'
    # Half of used gas fits only txs 1 and 2; tx 3 straddles the boundary.
    assert row(result, 'fraction_of_block_gas_used', '0.5')['all_legs_profit'] == '0'
    assert row(result, 'fraction_of_block_gas_used', '0.5')['any_leg_profit'] == '60'


def test_tokens_are_never_summed_and_losses_missing_values_remain_visible():
    data = sample()
    data['strategies'][0]['profit_amount'] = None
    data['strategies'][1]['profit_amount'] = '-4'
    data['strategies'].append({'id': 'other', 'block_number': 1, 'category': 'arbitrage',
        'transaction_hashes': [tx(2)], 'profit_token': 'token-b', 'profit_amount': '9007199254740993'})
    result = analyze(data)
    coverage = result['coverage']['assets']['token-a']
    assert coverage['unknown_profit_count'] == 1
    assert coverage['negative_profit_sum'] == '-4'
    assert row(result, 'first_k_transactions', 4)['all_legs_share'] is None
    assert row(result, 'first_k_transactions', 4, 'token-b')['positive_profit_total'] == '9007199254740993'


def test_missing_receipts_withhold_gas_curve():
    data = sample()
    data['blocks'][0]['gas_used_by_transaction'] = None
    result = analyze(data)
    assert result['coverage']['gas_concentration_status'] == 'unavailable'
    assert all(r['axis'] == 'first_k_transactions' for r in result['concentration'])


@pytest.mark.parametrize('change', [
    lambda d: d['strategies'].append(deepcopy(d['strategies'][0])),
    lambda d: d['strategies'][1].update(transaction_hashes=[tx(1)]),
    lambda d: d['strategies'][1].update(transaction_hashes=[tx(99)]),
    lambda d: d['strategies'][1].update(profit_amount=1.5),
    lambda d: d['strategies'][1].update(profit_amount='NaN'),
    lambda d: d['blocks'][0].update(gas_used_by_transaction=[10]),
    lambda d: d['blocks'][0].update(transaction_hashes=[tx(1), tx(1)]),
])
def test_invalid_or_ambiguous_input_rejected(change):
    data = sample()
    change(data)
    with pytest.raises(ValueError):
        analyze(data)


def test_single_leg_and_zero_profit():
    data = sample()
    data['strategies'][0]['transaction_hashes'] = [tx(1)]
    data['strategies'][1]['profit_amount'] = '0'
    result = analyze(data)
    assert row(result, 'first_k_transactions', 1)['all_legs_share'] == '1'
    assert row(result, 'first_k_transactions', 1)['any_leg_share'] == '1'
    assert result['coverage']['assets']['token-a']['zero_profit_count'] == 1


def test_cli_reproducibility_and_raw_hash_validation(tmp_path):
    source = tmp_path / 'input.json'
    data = sample()
    source.write_text(json.dumps(data))
    for name in ('one', 'two'):
        run = subprocess.run([sys.executable, '-m', 'bloc_economics.concentration', str(source),
            '--output', str(tmp_path / name)], capture_output=True, text=True)
        assert run.returncode == 0, run.stderr
    assert (tmp_path / 'one/concentration.csv').read_bytes() == (tmp_path / 'two/concentration.csv').read_bytes()
    report = (tmp_path / 'one/REPORT.md').read_text()
    assert 'fixture' in report and 'proposer' in report and 'partial' in report
    assert (tmp_path / 'one/concentration.png').stat().st_size > 1000
    data['provenance']['source_files'] = [{'path': 'input.json', 'sha256': '0' * 64}]
    source.write_text(json.dumps(data))
    run = subprocess.run([sys.executable, '-m', 'bloc_economics.concentration', str(source),
        '--output', str(tmp_path / 'bad')], capture_output=True, text=True)
    assert run.returncode != 0
    assert not (tmp_path / 'bad').exists()
