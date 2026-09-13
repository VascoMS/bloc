"""Contracts for reproducible, month-bounded Dune MEV exports."""
import csv
import io
import json

import pytest

from bloc_economics.dune_collect import collect_month, query_plan, validate_export


def csv_bytes(headers, row):
    stream = io.StringIO(newline='')
    writer = csv.DictWriter(stream, fieldnames=headers, lineterminator='\n')
    writer.writeheader()
    writer.writerow(row)
    return stream.getvalue().encode()


def test_query_plan_emits_three_complete_half_open_month_exports():
    plan = query_plan('2026-07')

    assert [query.name for query in plan] == [
        'sandwiches', 'atomic_arbitrages', 'liquidations'
    ]
    assert {query.start for query in plan} == {'2026-07-01'}
    assert {query.end for query in plan} == {'2026-08-01'}
    assert all("block_time >= TIMESTAMP '2026-07-01'" in query.sql for query in plan)
    assert all("block_time < TIMESTAMP '2026-08-01'" in query.sql for query in plan)
    assert all("tx.block_date >= DATE '2026-07-01'" in query.sql for query in plan)
    assert all("tx.block_date < DATE '2026-08-01'" in query.sql for query in plan)
    assert 'dex.sandwiches' in plan[0].sql and 'dex.sandwiched' in plan[0].sql
    assert 'dex.atomic_arbitrages' in plan[1].sql
    assert 'lending.borrow' in plan[2].sql and 'lending.supply' in plan[2].sql


def test_export_validation_rejects_missing_identity_and_rows_outside_month():
    query = query_plan('2026-07')[1]
    headers = sorted(query.required_columns)
    row = {name: 'value' for name in headers}
    row.update(blockchain='ethereum', block_time='2026-07-15 12:00:00.000 UTC',
               block_number='22900000', block_hash='0x' + 'ab' * 32,
               tx_hash='0x' + '12' * 32, tx_index='3', evt_index='7',
               tx_from='0x' + '13' * 20, tx_to='0x' + '14' * 20,
               tx_success='true', gas_used='21000', priority_fee_per_gas='1000000',
               tx_value='0')

    metadata = validate_export(query, csv_bytes(headers, row))
    assert metadata == {'columns': headers, 'row_count': 1,
                        'first_block_time': '2026-07-15 12:00:00.000 UTC',
                        'last_block_time': '2026-07-15 12:00:00.000 UTC'}

    outside = dict(row, block_time='2026-08-01 00:00:00.000 UTC')
    with pytest.raises(ValueError, match='outside requested month'):
        validate_export(query, csv_bytes(headers, outside))

    with pytest.raises(ValueError, match='invalid block hash'):
        validate_export(query, csv_bytes(headers, dict(row, block_hash='0xdead')))

    with pytest.raises(ValueError, match='missing canonical transaction field'):
        validate_export(query, csv_bytes(headers, dict(row, gas_used='')))

    incomplete_headers = headers[:-1]
    with pytest.raises(ValueError, match='missing required columns'):
        validate_export(query, csv_bytes(
            incomplete_headers, {name: row[name] for name in incomplete_headers}
        ))


class ScriptedDune:
    """Replace only the external HTTP boundary with complete API responses."""

    def __init__(self, result, states=('QUERY_STATE_COMPLETED',)):
        self.result = result
        self.states = list(states)
        self.requests = []

    def __call__(self, url, api_key, *, method='GET', payload=None):
        self.requests.append((url, api_key, method, payload))
        if url.endswith('/sql/execute'):
            return 200, {'Content-Type': 'application/json'}, \
                b'{"execution_id":"exec-1","state":"QUERY_STATE_PENDING"}'
        if url.endswith('/status'):
            state = self.states.pop(0)
            return 200, {'Content-Type': 'application/json'}, \
                json.dumps({'execution_id': 'exec-1', 'state': state}).encode()
        return 200, {'Content-Type': 'text/csv'}, self.result


def test_collection_preserves_auditable_api_evidence_without_the_secret(tmp_path):
    query = query_plan('2026-07')[1]
    headers = sorted(query.required_columns)
    row = {name: 'value' for name in headers}
    row.update(blockchain='ethereum', block_time='2026-07-15 12:00:00.000 UTC',
               block_number='22900000', block_hash='0x' + 'cd' * 32,
               tx_hash='0x' + '34' * 32, tx_index='4', evt_index='8',
               tx_from='0x' + '35' * 20, tx_to='0x' + '36' * 20,
               tx_success='true', gas_used='42000', priority_fee_per_gas='2000000',
               tx_value='0')
    result = csv_bytes(headers, row)

    api = ScriptedDune(result, states=(
        'QUERY_STATE_PENDING', 'QUERY_STATE_COMPLETED'
    ))
    manifest = collect_month(tmp_path, '2026-07', api_key='test-secret',
                             api_root='https://example.invalid/api/v1',
                             datasets=('atomic_arbitrages',), poll_interval=0,
                             requester=api)

    assert [request[0] for request in api.requests] == [
        'https://example.invalid/api/v1/sql/execute',
        'https://example.invalid/api/v1/execution/exec-1/status',
        'https://example.invalid/api/v1/execution/exec-1/status',
        'https://example.invalid/api/v1/execution/exec-1/results/csv'
    ]
    assert all(request[1] == 'test-secret' for request in api.requests)
    assert manifest['schema'] == 'bloc-dune-month/v1'
    assert manifest['period'] == {
        'month': '2026-07', 'start': '2026-07-01', 'end': '2026-08-01'
    }
    assert manifest['datasets'] == ['atomic_arbitrages']
    assert manifest['sources'][0]['row_count'] == 1
    assert (tmp_path / 'atomic_arbitrages.csv').read_bytes() == result
    assert json.loads((tmp_path / 'atomic_arbitrages.status-0001.json').read_text())[
        'state'] == 'QUERY_STATE_PENDING'
    assert json.loads((tmp_path / 'atomic_arbitrages.status-0002.json').read_text())[
        'state'] == 'QUERY_STATE_COMPLETED'
    saved = b''.join(path.read_bytes() for path in tmp_path.iterdir() if path.is_file())
    assert b'test-secret' not in saved


def test_failed_execution_preserves_terminal_status_response(tmp_path):
    api = ScriptedDune(b'unused', states=('QUERY_STATE_FAILED',))

    with pytest.raises(RuntimeError, match='QUERY_STATE_FAILED'):
        collect_month(tmp_path, '2026-07', api_key='test-secret',
                      api_root='https://example.invalid/api/v1',
                      datasets=('atomic_arbitrages',), requester=api)

    assert json.loads((tmp_path / 'atomic_arbitrages.status-0001.json').read_text()) == {
        'execution_id': 'exec-1', 'state': 'QUERY_STATE_FAILED'
    }
    headers = json.loads((tmp_path / 'atomic_arbitrages.status-0001.headers.json').read_text())
    assert headers['http_status'] == 200
    assert headers['headers']['Content-Type'] == 'application/json'
    failure = json.loads((tmp_path / 'failure-manifest.json').read_text())
    assert failure['status'] == 'failed'
    source = failure['sources'][0]
    assert source['status_responses'][0]['body']['sha256']
    assert source['status_responses'][0]['headers']['sha256']
