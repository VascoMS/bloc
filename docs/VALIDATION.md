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
- realistic network-loss or WAN deployment evidence.

## Milestone Evidence Map

| Milestone | Primary evidence |
|---|---|
| `M0. Current Prototype Baseline` | module tests, `bloc-node` demo smoke flow, documented command paths |
| `M1. Slot Timing and Baseline Latency Evidence` | `bloc-node` `eval-suite` repeated timing runs with explicit configuration fields plus raw per-node and aggregated per-scenario results |
| `M2. Coordination and Cryptographic Overhead Characterization` | evaluator message/byte counters plus BTE full-path benchmarks and optimization sweeps over normal, `sqrt(B)`, `2*sqrt(B)`, parallel combine, and larger `BMax`/batch sizes |
| `M3. Fault and Adversarial Robustness Validation` | fault-injection runs and targeted correctness tests |
| `M4. Economic and Resource Cost Characterization` | byte-size, bandwidth, and resource measurements tied to prototype runs |
| `M5. Distributed Evaluation and Dissertation-Ready Evidence` | repeated local or distributed runs, aggregated metrics, and plot-ready outputs |

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
