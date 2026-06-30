# Validation

## Validation Matrix

| Change area | Minimum validation | When to go further |
|---|---|---|
| `bloc-node` logic | `go test ./...` in `bloc-node` | Run `eval-local` or demo flow for consensus, transport, or end-to-end behavior |
| `mempool-il` logic | `go test ./...` in `mempool-il` | Add a local service smoke check when API or source handling changes |
| BTE library logic | `go test ./...` in `bte/btd-impl-main` | Run full-path benchmark when performance or batch planning changes |
| `sbc/hbbft` logic | `go test ./...` in `sbc/hbbft` | Run simulation or bench when consensus behavior or throughput assumptions change |
| Latency charts | `python -m pytest` in `latency-charts` | Render charts from an `eval-suite` directory after schema or presentation changes |
| Cross-module protocol behavior | Relevant module tests plus `bloc-node` smoke flow | Use the demo flow for reproducible end-to-end evidence |
| Deployment artifacts | Docker build plus Compose smoke | Run remote evaluator and Prometheus scrape checks before cloud claims |
| Documentation-only changes | Link and ownership review | No code validation required unless commands were edited |

## Canonical Commands

### Module Unit Tests

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
```

### bloc-node Local Evaluation

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8 \
  --tx-size 256 \
  --bmax 16 \
  --out-dir results/local
```

### M1 Evidence Campaign (Not a Per-Change Test)

This is a long-running milestone evidence campaign, not a mandatory validation
step for ordinary code changes. Run it only when collecting or refreshing the
M1 baseline, or after a change that can materially affect the evaluator's
latency results. For routine `bloc-node` changes, use `go test ./...` and, when
the end-to-end path is relevant, the reduced smoke check below.

The named profile executes 4/7/10-node and 8/32/128-transaction scenarios over
libp2p, with five warmups and thirty measured repetitions per scenario: 9
scenarios and 315 runs. It defaults to persistent execution: one cluster is
started for each operator count and each sample receives a fresh, sequential
slot state. Use `--execution-mode isolated` only to validate complete process
startup and teardown for every sample.

The M1 profile measures the integrated `bloc-node` path with the BTE library's
default deterministic sub-batching enabled. Concretely, `PlanBatch` uses
`alpha = ceil(2*sqrt(B))`, matching the BEAT-MEV paper's `Opt-2` sub-batching
choice, and increases `alpha` only when index collisions require more
sub-batches. Therefore M1 is not an unoptimized BTE-combine benchmark, and it is
also not a comparison of BEAT-MEV optimization variants. The profile fixes
`BMax=128`, so it intentionally excludes the paper's 512-transaction stress
point.

```sh
cd bloc-node
go run ./cmd/bloc-node eval-suite \
  --profile m1-baseline \
  --experiment-id m1-baseline \
  --out-dir results/m1-local/baseline-persistent
```

The result directory contains a small manifest with the exact command and
configuration matrix, append-only raw run records, per-node and per-run CSV
files, p50/p95 scenario summaries, cluster-generation logs, and
`cluster_measurements.csv`. Run rows identify their slot and cluster generation
and keep preparation/submission overhead separate from the existing protocol
timings. Any failed or inconsistent run is retained and makes the command fail;
the evaluator restarts that cluster and does not replace the failed observation.

For a short end-to-end validation of the harness itself:

```sh
go run ./cmd/bloc-node eval-suite \
  --profile m1-baseline \
  --execution-mode persistent \
  --node-counts 4 \
  --batch-sizes 8,128 \
  --warmups 0 \
  --repetitions 2 \
  --out-dir results/m1-smoke
```

After changes that can affect ACS/BBA liveness, run the targeted 7/10-node
stress before refreshing M1:

```sh
go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent \
  --node-counts 7,10 \
  --batch-sizes 8,32,128 \
  --warmups 0 \
  --repetitions 5 \
  --out-dir results/acs-liveness-stress
```

### M1 Timing Definitions

- `proposal_preparation_us`: local slot start through encoded proposal readiness.
- `acs_us`: proposal readiness through local ACS decision.
- `merge_plan_us`: ACS decision through deterministic merge, ciphertext decoding, and BTE batch planning.
- `share_generation_us`: local share creation and encoding; overlaps with network share collection.
- `threshold_wait_us`: plan readiness through threshold-share availability.
- `combine_us`: threshold availability through BTE combination.
- `materialization_us`: BTE combination through transaction parsing and final materialized output.
- `commit_to_plaintext_us`: ACS decision through completed materialization.
- `total_slot_us`: local slot start through completed materialization.

All durations are derived from process-local monotonic clocks. Per-run headline latency is the slowest correct
node's `total_slot_us`; that same node supplies the run's stage breakdown.
Stages can overlap and must not be summed. Warmups and failures are excluded
from distributions but remain present in raw outputs and failure counts. The
suite reports Type-7 p50/p95; p99 is deferred until a 100+ repetition campaign.
`cluster_measurements.csv`, `prepare_us`, and `submission_us` describe harness
overhead and are not included in `total_slot_us`.

### M1 Latency Charts

Set up the chart module once:

```powershell
cd latency-charts
python -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install -e ".[test]"
```

Generate SVG and PNG figures in the repository chart directory:

```powershell
python -m bloc_latency_charts ..\bloc-node\results\m1-local\baseline-persistent
```

The command produces end-to-end p50/p95 scaling, mean sequential critical-path
breakdown, and raw-run distribution figures under
`results/charts/m1-baseline/`. It reads `run_measurements.csv`, excludes warmups
and unsuccessful or inconsistent runs, validates that stacked stages are
additive, and does not modify the source dataset.

### bloc-node Demo Smoke Test

```sh
cd bloc-node
./scripts/demo-local.sh
```

### Distributed Sidecar Deployment Smoke

Build the sidecar image from the repository root:

```sh
docker build -f bloc-node/Dockerfile -t bloc-node:local .
```

Run the local 4-node sidecar deployment with Prometheus and Grafana:

```sh
cd deploy/docker-compose
docker compose up --build
```

Useful local URLs:

- sidecar HTTP APIs: `http://127.0.0.1:18000` through `http://127.0.0.1:18003`
- Prometheus: `http://127.0.0.1:19090`
- Grafana: `http://127.0.0.1:13000`

Confirm Prometheus can scrape sidecars:

```sh
curl -s http://127.0.0.1:18000/metrics
curl -s "http://127.0.0.1:19090/api/v1/targets"
```

The `/metrics` endpoint is the live Prometheus/Grafana interface, not a mirror
of evaluator CSV fields. It must use Prometheus base units and bounded labels:

| Metric | Type | Purpose |
|---|---|---|
| `bloc_node_info{cluster_id,node_id}` | gauge | static sidecar identity |
| `bloc_slot_phase{cluster_id,node_id,phase}` | gauge | one-hot current slot phase |
| `bloc_slot_current{cluster_id,node_id}` | gauge | current slot id |
| `bloc_slot_started_total{cluster_id,node_id}` | counter | started slots |
| `bloc_slot_completed_total{cluster_id,node_id}` | counter | completed slots |
| `bloc_slot_failed_total{cluster_id,node_id,reason}` | counter | bounded failure reasons |
| `bloc_slot_result_available{cluster_id,node_id}` | gauge | active-slot result availability |
| `bloc_slot_stage_duration_seconds{cluster_id,node_id,stage}` | histogram | completed slot stage durations |
| `bloc_slot_selected_transactions{cluster_id,node_id}` | gauge | latest selected transaction count |
| `bloc_slot_selected_gas{cluster_id,node_id}` | gauge | latest selected gas |
| `bloc_protocol_messages_total{cluster_id,node_id,direction,kind}` | counter | protocol message count |
| `bloc_protocol_message_bytes_total{cluster_id,node_id,direction,kind}` | counter | protocol message bytes |
| `bloc_http_requests_total{cluster_id,node_id,method,handler,code}` | counter | normalized HTTP requests |
| `bloc_http_request_duration_seconds{cluster_id,node_id,method,handler,code}` | histogram | normalized HTTP latency |

Prometheus labels must not include slot IDs, batch IDs, transaction hashes, raw
URLs, peer IDs, or free-form error strings. Evaluator outputs keep microsecond
columns such as `total_slot_us` for chart compatibility.

Grafana p50/p95 panels must query histograms, for example:

```promql
histogram_quantile(
  0.95,
  sum by (le) (rate(bloc_slot_stage_duration_seconds_bucket{stage="total"}[5m]))
)
```

If `promtool` is available in the Prometheus container, a scraped metrics file
can be checked with:

```sh
docker compose exec prometheus promtool check metrics < scraped-metrics.txt
```

Run the remote evaluator against the already-running Compose sidecars:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-remote \
  --config ../deploy/docker-compose/remote-eval.compose.json \
  --experiment-id compose-smoke \
  --batch-size 8 \
  --warmups 0 \
  --repetitions 1 \
  --out-dir results/distributed/compose-smoke
```

Generate charts from the distributed output using the same chart module:

```powershell
cd latency-charts
python -m bloc_latency_charts ..\bloc-node\results\distributed\compose-smoke
```

### Mock Placeholder Mempool Smoke

Use this when validating realistic transaction-source behavior. Public mempool
transactions are treated as target payloads, not as already-valid BLOC
placeholders. `mempool-il` encrypts each corpus target once, signs a mock
placeholder transaction, parses that placeholder transaction's calldata, and
sidecars consume the derived `encrypted_payload_hex` from the inclusion-list
API.

```sh
cd deploy/docker-compose
docker compose -f compose.yaml -f compose.mock-placeholder.yaml up --build
```

Then run the remote evaluator without direct `/tx` submissions:

```sh
cd bloc-node
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

Acceptance criteria:

- all sidecars report success and cross-node consistency;
- materialized `ethereum_tx_hashes` match target transactions from the corpus;
- inclusion-list responses expose encrypted payload and target metadata derived
  from placeholder calldata, but not raw target transaction bytes;
- evaluator manifests record `tx_source=mock-placeholder`;
- chart generation remains compatible with the resulting evaluator directory.

### Kubernetes Deployment Shape

Generate the prototype cluster config outside git so trusted-dealer shares and
libp2p private keys are not committed:

```sh
cd bloc-node
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 128 \
  --slot 1 \
  --cluster-id bloc-k8s \
  --address-mode kubernetes \
  --out cluster.k8s.local.json
```

Create the Kubernetes config and deploy the sidecars:

```sh
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl -n bloc create configmap bloc-cluster-config \
  --from-file=cluster.json=bloc-node/cluster.k8s.local.json \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/k8s/10-services.yaml
kubectl apply -f deploy/k8s/20-statefulset.yaml
```

If the Prometheus operator and Grafana sidecar dashboard discovery are
available, also apply:

```sh
kubectl apply -f deploy/k8s/30-servicemonitor.yaml
kubectl apply -f deploy/k8s/40-grafana-dashboard-configmap.yaml
```

The example remote-evaluator config in
`deploy/k8s/remote-eval.k8s.example.json` uses in-cluster DNS names. When
running the evaluator outside the cluster, port-forward each pod or replace the
URLs with externally reachable addresses.

### BTE Benchmarks

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath' -benchtime=1x
```

The cluster-facing full-path benchmarks exercise the same deterministic
sub-batched planning boundary used by `bloc-node`. The inherited BEAT-MEV
microbenchmarks in `main_test.go` are the right place to compare normal,
`sqrt(B)`, `2*sqrt(B)`, and parallel combine behavior. Treat that comparison as
M2 cryptographic-overhead evidence rather than M1 slot-latency evidence.

### hbbft Bench and Simulation

```sh
cd sbc/hbbft
go test ./...
go run simulation/main.go
go run bench/main.go
```

## What Existing Coverage Proves

### bloc-node

- deterministic inclusion-list merge behavior,
- wire/protobuf round-trips,
- deterministic signed Ethereum transaction generation for the evaluator,
- wrong-batch share filtering before threshold combination.

### BTE

- ciphertext verification rejects mutations,
- hybrid encryption/decryption round-trip works,
- deterministic `BatchPlan` behavior,
- BEAT-MEV-style `2*sqrt(B)` sub-batching is the default cluster planning mode,
- threshold enforcement and duplicate-share handling,
- full-path benchmark coverage for several batch sizes.

### hbbft

- RBC, BBA, and ACS behavior are covered by module tests,
- slot-scoped BLOC adapter behavior is covered separately from the original HoneyBadger driver.

## Research-Question-Oriented Validation

The current prototype can support:

- slot-level latency experiments under local-process conditions,
- coordination and message-overhead measurement,
- fault-injection scenarios such as omitted proposals or withheld shares,
- blockspace-cap experiments and related materialization metrics.

The current prototype does not yet support stronger deployment claims involving:

- DKG-generated shares,
- public share-verifiability,
- real proposer signing,
- execution-client validation of decrypted transactions,
- Builder API compatibility,
- realistic network-loss or WAN deployment evidence until a distributed run has
  actually been collected.

## Milestone Evidence Map

| Milestone | Primary evidence |
|---|---|
| `M0. Current Prototype Baseline` | module tests, `bloc-node` demo smoke flow, documented command paths |
| `M1. Slot Timing and Baseline Latency Evidence` | `bloc-node` `eval-suite` repeated timing runs with explicit configuration fields plus raw per-node and aggregated per-scenario results |
| `M2. Distributed Deployment-Ready BLOC Sidecar` | Docker/Compose/Kubernetes deployment checks, Prometheus `/metrics`, Grafana dashboard, and `eval-remote` outputs |
| `M3. Distributed Sidecar Metrics Collection` | repeated remote-evaluator campaigns, distributed manifests/CSVs, Prometheus/Grafana observations, and plot-ready outputs |
| `M4. Coordination, Cryptographic, and Resource Overhead Characterization` | evaluator message/byte counters plus BTE full-path benchmarks and optimization sweeps over normal, `sqrt(B)`, `2*sqrt(B)`, parallel combine, and larger `BMax`/batch sizes |
| `M5. Fault and Adversarial Robustness Validation` | fault-injection runs and targeted correctness tests |
| `M6. Builder API Boundary` | future Builder-facing development adapter serving real BLOC-agreed transaction sets |

## Last Known Good State Guidance

When `docs/STATUS.md` is updated with a stronger baseline, include:

- date,
- the exact command set used,
- what the baseline proves,
- and where the evidence or result artifacts live.

## Review Checklist

- Did the change touch protocol behavior, ordering, hashing, or threshold logic?
- Did the validation match the affected module and behavior?
- If a command changed, was this file updated?
- If a new failure mode was discovered, was it recorded in `docs/CHANGELOG.md` or `docs/DECISIONS.md` as appropriate?
- If the baseline improved or milestone evidence changed, was `docs/STATUS.md` updated?
