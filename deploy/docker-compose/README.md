# Docker Compose Rehearsal

This directory runs a local four-operator BLOC sidecar cluster with Prometheus
and Grafana. It validates container configuration, service discovery, metrics,
and remote-evaluator mechanics. It is not an independent-machine performance
environment and does not replace the accepted VM/EC2 evidence.

## Prerequisites

- Docker with Compose support
- local ports `18000`–`18003`, `19090`, and `13000`
- the Go toolchain required by `bloc-node` for evaluator commands

## Standard Rehearsal

From this directory:

```sh
docker compose up --build
```

Useful endpoints:

- sidecars: `http://127.0.0.1:18000` through `http://127.0.0.1:18003`
- Prometheus: `http://127.0.0.1:19090`
- Grafana: `http://127.0.0.1:13000`

Verify health and metric discovery:

```sh
curl -s http://127.0.0.1:18000/healthz
curl -s http://127.0.0.1:18000/metrics
curl -s http://127.0.0.1:19090/api/v1/targets
```

Run the evaluator from `bloc-node/`:

```sh
go run ./cmd/bloc-node eval-remote \
  --config ../deploy/docker-compose/remote-eval.compose.json \
  --experiment-id compose-smoke \
  --batch-size 8 \
  --warmups 0 \
  --repetitions 1 \
  --out-dir results/distributed/compose-smoke
```

Acceptance requires four healthy sidecars, four Prometheus targets up, a
successful and cross-node-consistent evaluator result, and chart-compatible
CSV/JSON output. Compose latency is diagnostic only.

Stop the rehearsal with:

```sh
docker compose down
```

## Mock-Placeholder Rehearsal

This overlay uses a deterministic corpus and `mempool-il` as a mock external
submitter. It encrypts each target once, signs a placeholder transaction, and
serves the encrypted payload parsed from placeholder calldata.

```sh
docker compose -f compose.yaml -f compose.mock-placeholder.yaml up --build
```

Run the evaluator from `bloc-node/` without direct `/tx` submissions:

```sh
go run ./cmd/bloc-node eval-remote \
  --config ../deploy/docker-compose/remote-eval.mock-placeholder.json \
  --experiment-id compose-mock-placeholder \
  --tx-source mock-placeholder \
  --mempool-url http://127.0.0.1:18080 \
  --batch-size 4 \
  --warmups 0 \
  --repetitions 1 \
  --out-dir results/distributed/compose-mock-placeholder
```

Acceptance additionally requires materialized Ethereum hashes to match corpus
targets, inclusion-list responses to omit raw target bytes, and the evaluator
manifest to record `tx_source=mock-placeholder`.

Stop both files explicitly:

```sh
docker compose -f compose.yaml -f compose.mock-placeholder.yaml down
```

## Metrics And Charts

Prometheus metrics use base units and bounded labels. Exact metric contracts and
Grafana query requirements are in
[docs/VALIDATION.md](../../docs/VALIDATION.md). Evaluator CSV/JSON remains the
offline chart interface.

When chart compatibility itself changed, render the Compose output from
`latency-charts/`:

```sh
python -m bloc_latency_charts ../bloc-node/results/distributed/compose-smoke
```

Do not present Compose charts as distributed thesis evidence.
