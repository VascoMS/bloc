"""Acquire month-bounded Ethereum MEV labels from Dune with hashed evidence."""
from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from datetime import date, datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import re
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen


SCHEMA = 'bloc-dune-month/v1'
DEFAULT_API_ROOT = 'https://api.dune.com/api/v1'
QUERY_DIRECTORY = Path(__file__).with_name('queries')


@dataclass(frozen=True)
class MonthQuery:
    name: str
    start: str
    end: str
    sql: str
    required_columns: frozenset[str]


REQUIRED = {
    'sandwiches': frozenset({
        'leg_role', 'blockchain', 'project', 'version', 'block_time',
        'block_number', 'block_hash', 'tx_hash', 'tx_index', 'tx_from',
        'tx_to', 'tx_success', 'gas_used', 'priority_fee_per_gas', 'tx_value',
        'project_contract_address', 'token_sold_address',
        'token_bought_address', 'token_sold_symbol', 'token_bought_symbol',
        'token_sold_amount_raw', 'token_bought_amount_raw',
        'token_sold_amount', 'token_bought_amount', 'amount_usd', 'evt_index',
    }),
    'atomic_arbitrages': frozenset({
        'blockchain', 'project', 'version', 'block_time', 'block_number',
        'block_hash', 'tx_hash', 'tx_index', 'tx_from', 'tx_to', 'tx_success',
        'gas_used', 'priority_fee_per_gas', 'tx_value',
        'project_contract_address', 'token_sold_address',
        'token_bought_address', 'token_sold_symbol', 'token_bought_symbol',
        'token_sold_amount_raw', 'token_bought_amount_raw',
        'token_sold_amount', 'token_bought_amount', 'amount_usd', 'evt_index',
    }),
    'liquidations': frozenset({
        'liquidation_side', 'blockchain', 'project', 'version', 'block_time',
        'block_number', 'block_hash', 'tx_hash', 'tx_index', 'tx_from',
        'tx_to', 'tx_success', 'gas_used', 'priority_fee_per_gas', 'tx_value',
        'liquidator', 'borrower', 'depositor', 'on_behalf_of', 'repayer',
        'withdrawn_to', 'token_address', 'symbol', 'amount', 'amount_raw',
        'amount_usd', 'project_contract_address', 'evt_index',
    }),
}


def _month_bounds(month: str) -> tuple[str, str]:
    if not re.fullmatch(r'\d{4}-\d{2}', month):
        raise ValueError('month must use YYYY-MM')
    start = date.fromisoformat(month + '-01')
    end = date(start.year + (start.month == 12), start.month % 12 + 1, 1)
    return start.isoformat(), end.isoformat()


def query_plan(month: str) -> list[MonthQuery]:
    """Render the three fixed source queries for one complete calendar month."""
    start, end = _month_bounds(month)
    plan = []
    for name in ('sandwiches', 'atomic_arbitrages', 'liquidations'):
        template = (QUERY_DIRECTORY / f'{name}.sql').read_text()
        sql = template.replace('__START_DATE__', start).replace('__END_DATE__', end)
        if '__START_DATE__' in sql or '__END_DATE__' in sql:
            raise ValueError(f'unrendered date in {name} query')
        plan.append(MonthQuery(name, start, end, sql, REQUIRED[name]))
    return plan


def _parse_time(value: str) -> datetime:
    rendered = value.strip()
    if rendered.endswith(' UTC'):
        rendered = rendered[:-4] + '+00:00'
    parsed = datetime.fromisoformat(rendered)
    return parsed.replace(tzinfo=parsed.tzinfo or timezone.utc).astimezone(timezone.utc)


def validate_export(query: MonthQuery, body: bytes) -> dict:
    """Reject incomplete, malformed, or out-of-period CSV before evidence use."""
    try:
        text = body.decode('utf-8-sig')
    except UnicodeDecodeError as error:
        raise ValueError('Dune result is not UTF-8 CSV') from error
    reader = csv.DictReader(io.StringIO(text), strict=True)
    columns = reader.fieldnames or []
    if len(columns) != len(set(columns)):
        raise ValueError('duplicate CSV columns')
    missing = sorted(query.required_columns - set(columns))
    if missing:
        raise ValueError(f'missing required columns: {", ".join(missing)}')
    start, end = date.fromisoformat(query.start), date.fromisoformat(query.end)
    first = last = None
    count = 0
    for line, row in enumerate(reader, 2):
        if row.get(None):
            raise ValueError(f'row {line}: more values than columns')
        if row['blockchain'] != 'ethereum':
            raise ValueError(f'row {line}: non-Ethereum record')
        try:
            timestamp = _parse_time(row['block_time'])
            identities = (int(row['block_number']), int(row['tx_index']), int(row['evt_index']))
        except (TypeError, ValueError) as error:
            raise ValueError(f'row {line}: invalid block/event identity') from error
        if not start <= timestamp.date() < end:
            raise ValueError(f'row {line}: outside requested month')
        if identities[0] <= 0 or identities[1] < 0 or identities[2] < 0:
            raise ValueError(f'row {line}: invalid block/event identity')
        transaction_fields = ('tx_success', 'gas_used', 'priority_fee_per_gas',
                              'tx_value', 'tx_from', 'tx_to')
        if any(row[field] is None or not row[field].strip() for field in transaction_fields):
            raise ValueError(f'row {line}: missing canonical transaction field')
        if not re.fullmatch(r'0x[0-9a-fA-F]{64}', row['block_hash']):
            raise ValueError(f'row {line}: invalid block hash')
        if not re.fullmatch(r'0x[0-9a-fA-F]{64}', row['tx_hash']):
            raise ValueError(f'row {line}: invalid transaction hash')
        if any(not re.fullmatch(r'0x[0-9a-fA-F]{40}', row[field])
               for field in ('tx_from', 'tx_to')):
            raise ValueError(f'row {line}: invalid transaction address')
        try:
            gas_used = int(row['gas_used'])
            priority_fee = int(row['priority_fee_per_gas'])
            tx_value = int(row['tx_value'])
        except ValueError as error:
            raise ValueError(f'row {line}: invalid transaction fee field') from error
        if (row['tx_success'].lower() != 'true' or gas_used <= 0
                or priority_fee < 0 or tx_value < 0):
            raise ValueError(f'row {line}: invalid transaction fee field')
        observed = (timestamp, row['block_time'])
        first = min(first, observed) if first else observed
        last = max(last, observed) if last else observed
        count += 1
    if count == 0:
        raise ValueError(f'{query.name}: empty month export')
    return {
        'columns': columns,
        'row_count': count,
        'first_block_time': first[1],
        'last_block_time': last[1],
    }


def _sha(body: bytes) -> str:
    return hashlib.sha256(body).hexdigest()


def _utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def _request(url: str, api_key: str, *, method='GET', payload=None) -> tuple[int, dict, bytes]:
    body = None if payload is None else json.dumps(payload, separators=(',', ':')).encode()
    headers = {'X-Dune-Api-Key': api_key}
    if body is not None:
        headers['Content-Type'] = 'application/json'
    request = Request(url, data=body, headers=headers, method=method)
    try:
        with urlopen(request, timeout=120) as response:
            return response.status, dict(response.headers.items()), response.read()
    except HTTPError as error:
        return error.code, dict(error.headers.items()), error.read()


def _save(path: Path, body: bytes) -> dict:
    path.write_bytes(body)
    return {'file': path.name, 'sha256': _sha(body), 'bytes': len(body)}


def _save_headers(path: Path, status: int, headers: dict) -> dict:
    body = (json.dumps({
        'retrieved_at': _utc(), 'http_status': status, 'headers': headers,
    }, indent=2, sort_keys=True) + '\n').encode()
    return _save(path, body)


def collect_month(output: Path, month: str, *, api_key: str,
                  api_root=DEFAULT_API_ROOT, datasets=None, performance='medium',
                  poll_interval=2, timeout=1800, requester=_request) -> dict:
    """Run selected Dune SQL exports and preserve query/response provenance."""
    if not api_key:
        raise ValueError('DUNE_API_KEY is required')
    if performance not in ('small', 'medium', 'large'):
        raise ValueError('performance must be small, medium, or large')
    plan = query_plan(month)
    selected = set(datasets or (query.name for query in plan))
    unknown = selected - {query.name for query in plan}
    if unknown:
        raise ValueError(f'unknown datasets: {", ".join(sorted(unknown))}')
    plan = [query for query in plan if query.name in selected]
    if not plan:
        raise ValueError('at least one dataset is required')
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    expected = ['manifest.json', 'failure-manifest.json']
    for query in plan:
        expected.extend(f'{query.name}.{suffix}' for suffix in
                        ('sql', 'request.json', 'execute.json',
                         'execute.headers.json', 'csv', 'csv.headers.json'))
    conflicts = [name for name in expected if (output / name).exists()]
    conflicts.extend(path.name for query in plan
                     for path in output.glob(f'{query.name}.status-*.json'))
    if conflicts:
        raise FileExistsError(f'refusing to replace evidence files: {", ".join(conflicts)}')

    start, end = _month_bounds(month)
    sources = []
    root = api_root.rstrip('/')
    manifest = {
        'schema': SCHEMA,
        'period': {'month': month, 'start': start, 'end': end},
        'datasets': [query.name for query in plan],
        'selection': 'All rows returned by the selected Dune curated tables for Ethereum in the half-open UTC month.',
        'coverage_limit': 'Curated labels are detector outputs, not exhaustive MEV ground truth. Volume fields are not profit.',
        'redistribution_status': 'Unverified; retain locally for research and do not publish raw exports.',
        'sources': sources,
    }
    try:
        for query in plan:
            source = {'dataset': query.name, 'provider': 'Dune Analytics',
                      'status_responses': []}
            sources.append(source)
            source['query'] = _save(output / f'{query.name}.sql', query.sql.encode())
            payload = {'sql': query.sql, 'performance': performance}
            request_record = {
                'retrieved_at': _utc(), 'method': 'POST',
                'url': f'{root}/sql/execute',
                'headers': {'Content-Type': 'application/json',
                            'X-Dune-Api-Key': '[redacted]'},
                'body': payload,
            }
            request_bytes = (json.dumps(request_record, indent=2, sort_keys=True) + '\n').encode()
            source['request'] = _save(
                output / f'{query.name}.request.json', request_bytes)
            status, headers, body = requester(
                request_record['url'], api_key, method='POST', payload=payload)
            source['execute_response'] = _save(
                output / f'{query.name}.execute.json', body)
            source['execute_response_headers'] = _save_headers(
                output / f'{query.name}.execute.headers.json', status, headers)
            if status != 200:
                raise RuntimeError(f'{query.name}: execution returned HTTP {status}')
            try:
                execution = json.loads(body)
                execution_id = execution['execution_id']
            except (json.JSONDecodeError, KeyError, TypeError) as error:
                raise RuntimeError(f'{query.name}: invalid Dune execution response') from error
            source['execution_id'] = execution_id

            deadline = time.monotonic() + timeout
            poll = 0
            while True:
                poll += 1
                status_code, status_headers, status_body = requester(
                    f'{root}/execution/{execution_id}/status', api_key)
                prefix = output / f'{query.name}.status-{poll:04d}'
                response_evidence = {
                    'http_status': status_code,
                    'body': _save(Path(str(prefix) + '.json'), status_body),
                    'headers': _save_headers(
                        prefix.with_name(prefix.name + '.headers.json'),
                        status_code, status_headers),
                }
                source['status_responses'].append(response_evidence)
                if status_code != 200:
                    raise RuntimeError(f'{query.name}: status returned HTTP {status_code}')
                try:
                    state = json.loads(status_body)['state']
                except (json.JSONDecodeError, KeyError, TypeError) as error:
                    raise RuntimeError(f'{query.name}: invalid Dune status response') from error
                response_evidence['state'] = state
                if state == 'QUERY_STATE_COMPLETED':
                    break
                if state in ('QUERY_STATE_FAILED', 'QUERY_STATE_CANCELLED',
                             'QUERY_STATE_EXPIRED'):
                    raise RuntimeError(f'{query.name}: Dune execution ended in {state}')
                if time.monotonic() >= deadline:
                    raise TimeoutError(f'{query.name}: Dune execution timed out')
                time.sleep(poll_interval)
            source['execution_state'] = state
            result_status, result_headers, csv_body = requester(
                f'{root}/execution/{execution_id}/results/csv', api_key)
            source['csv'] = _save(output / f'{query.name}.csv', csv_body)
            source['result_response_headers'] = _save_headers(
                output / f'{query.name}.csv.headers.json',
                result_status, result_headers)
            if result_status != 200:
                raise RuntimeError(f'{query.name}: result returned HTTP {result_status}')
            source.update(validate_export(query, csv_body))
    except Exception as error:
        failure = {
            **manifest,
            'status': 'failed',
            'failed_at': _utc(),
            'error': {'type': type(error).__name__, 'message': str(error)},
        }
        failure_bytes = (json.dumps(failure, indent=2, sort_keys=True) + '\n').encode()
        (output / 'failure-manifest.json').write_bytes(failure_bytes)
        raise

    manifest.update(status='complete', created_at=_utc())
    manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + '\n').encode()
    (output / 'manifest.json').write_bytes(manifest_bytes)
    return manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path)
    parser.add_argument('--month', required=True, help='complete UTC calendar month, YYYY-MM')
    parser.add_argument('--dataset', action='append', choices=tuple(REQUIRED), dest='datasets')
    parser.add_argument('--performance', choices=('small', 'medium', 'large'), default='medium')
    args = parser.parse_args(argv)
    key = os.environ.get('DUNE_API_KEY')
    if not key:
        parser.error('DUNE_API_KEY is not set')
    collect_month(args.output, args.month, api_key=key, datasets=args.datasets,
                  performance=args.performance)


if __name__ == '__main__':
    main()
