# Validation

## Evidence Policy

Module tests are the default preflight. Local `eval-local` and `eval-suite` runs
provide the clean protocol baseline for ACS/BTE behavior under controlled
conditions. Docker Compose validates local deployment mechanics but is not an
independent-machine performance substrate.

VM/EC2-per-sidecar campaigns provide the distributed evidence shape: one machine
per operator and a separate controller running `eval-remote`, artifact
collection, and Prometheus/Grafana. The accepted M3 three-region campaign is the
current distributed baseline. Its scope is honest-path p50/p95 latency and
resource/network observation, not Byzantine safety, production confidentiality,
or a causal topology comparison.

Charts are a reporting step after valid evidence exists. They are not a reason
to run an experiment or a substitute for raw accepted artifacts.

## Validation Matrix

| Change area | Minimum validation | When to go further |
|---|---|---|
| `bloc-node` logic | `go test ./...` in `bloc-node` | Run `eval-local` or the demo for consensus, transport, or end-to-end changes |
| `mempool-il` logic | `go test ./...` in `mempool-il` | Add a service or mock-placeholder smoke for API/source changes |
| BTE library logic | `go test ./...` in `bte/btd-impl-main` | Run the full-path benchmark for performance/planning changes |
| `sbc/hbbft` logic | `go test ./...` in `sbc/hbbft` | Run the ACS safety campaign for safety/liveness changes |
| Latency charts | `python -m pytest` in `latency-charts` | Render from representative accepted-schema artifacts |
| Cross-module protocol behavior | Affected module tests plus `bloc-node` smoke | Use `eval-suite` for a local baseline; collect new cloud evidence only when explicitly authorized |
| Deployment artifacts | Runner portability/validation and Terraform validation | Use the relevant deployment runbook and acceptance contract |
| Documentation only | Local link, ownership, command, and coherence review | No code tests unless documented commands or behavior changed |

## Canonical Module Commands

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

Run commands from the relevant module root; the repository contains multiple Go
modules rather than one root workspace.

## `bloc-node` Local Evaluation

Use local evaluation for controlled protocol behavior and baseline timing:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8 \
  --tx-size 256 \
  --bmax 16 \
  --out-dir results/local
```

For a fast integrated smoke:

```sh
cd bloc-node
./scripts/demo-local.sh
```

The demo runs normal, blockspace-cap, and share-withholding scenarios. It is
honest-path/fault-injection prototype evidence and must not be phrased as a proof
of Byzantine ACS safety.

## M1 Evidence Campaign

The named M1 profile runs 4/7/10 operators and batches `8/32/128` over libp2p,
with five warmups and thirty measured repetitions: nine scenarios and 315 runs.
It is a deliberate evidence refresh, not a per-change test.

```sh
cd bloc-node
go run ./cmd/bloc-node eval-suite \
  --profile m1-baseline \
  --experiment-id m1-baseline \
  --out-dir results/m1-local/baseline-persistent
```

Persistent mode starts one cluster per operator count and gives every sample a
fresh sequential slot. Failed and inconsistent observations remain in raw output
and make the command fail; the evaluator restarts the affected cluster rather
than replacing the observation.

For a short harness check:

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

The complete corrected 315-sample baseline remains outstanding. Existing
reduced local matrices are safety/liveness preflight evidence, not a replacement
for the full M1 distribution.

## ACS Safety Gate

After ACS/BBA safety or liveness changes, run:

```sh
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

Acceptance requires:

- 1,000 fixed reordered RBC/BBA delivery schedules;
- Linux race validation;
- an `n=4`, batch-128 persistent gate with 100 measured slots;
- an `n=4/n=7`, batch `8/32/128` matrix with 30 measured slots per scenario;
- every measured slot successful and cross-node consistent; and
- retained failure diagnostics containing RBC outputs, BBA decisions, truthy
  proposer IDs, and the ACS waiting reason.

The current gate does not cover mixed-root RBC reconstruction, conflicting AUX
equivocation, or sufficiently delayed future-epoch messages.

## Resource-Safety Gate

Changes to proposal ingestion, transport envelopes, share admission, or BTE
combination must pass module and race suites:

```sh
cd bloc-node && go test ./... && go test -race ./...
cd bte/btd-impl-main && go test ./... && go test -race ./...
```

Acceptance requires coverage proving:

- old v2 configs receive the 8 MiB proposal, 16 MiB envelope, and 256-attempt
  defaults while invalid overrides fail at startup;
- proposals and envelopes accept the exact limit and reject one byte over;
- provider/direct proposals cannot exceed `BMax` or the encoded proposal cap;
- authenticated operators cannot retain conflicting batch identities,
  out-of-plan sub-batches, or replacement points;
- retention stays within `N*BMax` before planning and `N*alpha` afterward;
- operator identity matches the Kyber public-share index;
- subset enumeration is deterministic and stops at its cumulative attempt cap;
  and
- the `n=10/t=7` case with three invalid candidates recovers in 165 attempts
  under the default budget.

## Timing And Statistical Definitions

- `proposal_preparation_us`: local slot start through proposal readiness.
- `acs_us`: proposal readiness through the local ACS decision.
- `merge_plan_us`: ACS decision through merge, ciphertext decoding, and planning.
- `acs_output_decode_us`: accepted-proposal decoding inside `merge_plan_us`.
- `agreed_set_us`: canonical hashing and agreed-set construction.
- `merge_us`: placeholder validation, deduplication, ordering, and selection.
- `ciphertext_decode_us`: selected BTE ciphertext deserialization.
- `batch_plan_us`: deterministic sub-batch layout and `BatchID` construction.
- `share_generation_us`: local share creation and encoding; overlaps collection.
- `threshold_wait_us`: plan readiness through threshold-share availability.
- `combine_us`: threshold availability through BTE combination.
- `materialization_us`: combination through parsed materialized output.
- `commit_to_plaintext_us`: ACS decision through materialization.
- `total_slot_us`: local slot start through materialization.

Durations use process-local monotonic clocks. Run-level headline latency is the
slowest correct node's `total_slot_us`, and that node supplies the stage
breakdown. Stages can overlap and must not generally be summed. Warmups and
failed/inconsistent runs remain in raw output but are excluded from accepted
distributions. Thirty observations support Type-7 p50/p95, not p99.

The five Merge + Plan substages must sum to `merge_plan_us` within 20
microseconds. Harness `prepare_us`, `submission_us`, and cluster startup are not
part of `total_slot_us`.

## Merge + Plan Optimization

Changes to inclusion-list hashing, deterministic merge, ciphertext decoding, or
BTE planning use the local paired campaign:

```sh
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase baseline --campaign-id <id>
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase optimized --campaign-id <id>
```

It writes ignored artifacts under
`results/local/merge-plan-optimization/<id>/`. Retain an optimization only when
protocol identities remain exact and no batch-32/128 pipeline median regresses
by more than 5%.

The EC2 attribution runner and its operational constraints are documented in
[deploy/ec2/README.md](../deploy/ec2/README.md). Its accepted phases require 30
successful and consistent observations per batch, one image digest, finalized
metrics, selected-ciphertext counts equal to the batch, additive substages,
healthy Prometheus targets, and authenticated empty cleanup. A failed optional
T3 phase remains diagnostic and is excluded from headline statistics.

## Prometheus Contract

The `/metrics` endpoint is the live Prometheus/Grafana interface, not a mirror of
evaluator CSV fields. It uses Prometheus base units and bounded labels:

| Metric | Type | Purpose |
|---|---|---|
| `bloc_node_info{cluster_id,node_id}` | gauge | static sidecar identity |
| `bloc_slot_phase{cluster_id,node_id,phase}` | gauge | one-hot current slot phase |
| `bloc_slot_current{cluster_id,node_id}` | gauge | current slot ID |
| `bloc_slot_started_total{cluster_id,node_id}` | counter | started slots |
| `bloc_slot_completed_total{cluster_id,node_id}` | counter | completed slots |
| `bloc_slot_failed_total{cluster_id,node_id,reason}` | counter | bounded failure reasons |
| `bloc_slot_result_available{cluster_id,node_id}` | gauge | result availability |
| `bloc_slot_stage_duration_seconds{cluster_id,node_id,stage}` | histogram | slot-stage durations |
| `bloc_slot_selected_transactions{cluster_id,node_id}` | gauge | selected transaction count |
| `bloc_slot_selected_gas{cluster_id,node_id}` | gauge | selected gas |
| `bloc_protocol_messages_total{cluster_id,node_id,direction,kind}` | counter | protocol messages |
| `bloc_protocol_message_bytes_total{cluster_id,node_id,direction,kind}` | counter | protocol bytes |
| `bloc_protocol_envelopes_rejected_total{cluster_id,node_id,direction,reason}` | counter | envelope rejections |
| `bloc_decryption_shares_accepted_total{cluster_id,node_id}` | counter | admitted shares |
| `bloc_decryption_shares_rejected_total{cluster_id,node_id,reason}` | counter | rejected shares |
| `bloc_decryption_share_subset_attempts_total{cluster_id,node_id}` | counter | recovery attempts |
| `bloc_http_requests_total{cluster_id,node_id,method,handler,code}` | counter | normalized HTTP requests |
| `bloc_http_request_duration_seconds{cluster_id,node_id,method,handler,code}` | histogram | HTTP latency |

Labels must not contain slot IDs, batch IDs, transaction hashes, raw URLs, peer
IDs, or free-form errors. Grafana quantiles query histogram buckets, for example:

```promql
histogram_quantile(
  0.95,
  sum by (le) (rate(bloc_slot_stage_duration_seconds_bucket{stage="total"}[5m]))
)
```

Evaluator CSV/JSON retains microsecond columns for offline analysis.

## Compose And Mock-Placeholder Acceptance

Operational commands live in
[deploy/docker-compose/README.md](../deploy/docker-compose/README.md).

The standard rehearsal requires four healthy sidecars, four Prometheus targets
up, successful cross-node-consistent evaluator output, and chart-compatible
artifacts. Compose latency remains diagnostic.

Mock-placeholder acceptance additionally requires:

- materialized Ethereum hashes match target corpus transactions;
- inclusion-list responses expose encrypted payload and target metadata derived
  from placeholder calldata without exposing raw target bytes; and
- evaluator manifests record `tx_source=mock-placeholder`.

## VM/EC2 Evidence Acceptance

Operational procedures, IAM constraints, and cleanup commands live in
[deploy/ec2/README.md](../deploy/ec2/README.md).

Every accepted VM/EC2 phase requires:

- one sidecar per independent operator machine and a separate controller;
- a committed source SHA and one recorded image/binary identity;
- every measured run `success=true` and `consistent=true`;
- finalized per-node metrics and expected selected-ciphertext counts;
- all Prometheus targets up;
- recorded environment, node count, threshold, batch size, endpoint mode, and
  region/AZ placement;
- operator secrets excluded from artifacts; and
- authenticated absence of all scoped resources after teardown.

### Accepted M3 Three-Region Baseline

Source `8de4af179465f9cd77920eacdcca163ca5cef01d` completed the canonical
`n=4/n=7`, batch `8/32/128`, five-warmup/thirty-measurement matrix across
`us-east-1`, `eu-west-1`, and `eu-central-1` on `t3.small`.

Acceptance retained exactly 180 measured slots and 990 finalized measured node
rows, all successful and consistent; requested selected-ciphertext counts; one
image digest; correct `2/1/1` and `3/2/2` placements; complete pre/post
five-attempt ordered-pair health matrices; all Prometheus targets up; no
restart/OOM evidence; and authenticated empty cleanup after 40- and 43-resource
Terraform destroys.

The four-stage report adds to `total_slot_us` within 20 microseconds:

1. Proposal: `proposal_preparation_us`.
2. ACS: `acs_us`.
3. Merge + Plan: `merge_plan_us`.
4. Decryption + Materialization: `threshold_wait_us + combine_us + materialization_us`.

The accepted artifact root is
`results/ec2/m3-three-region-synthetic-accepted-20260718-1/`. It supports scoped
p50/p95 latency, pairwise-network, stage, and critical-node-region claims. A
matched current-build same-region control is required before causal topology
claims.

## BTE Benchmarks

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath' -benchtime=1x
```

Cluster-facing benchmarks exercise the deterministic sub-batched path used by
`bloc-node`. The inherited root benchmarks compare normal, `sqrt(B)`,
`2*sqrt(B)`, and parallel combination. Treat those comparisons as cryptographic
overhead evidence, not M1 slot-latency evidence.

Detailed tests, fuzz entry points, and benchmark interpretation are in
[bte/btd-impl-main/TESTING.md](../bte/btd-impl-main/TESTING.md).

## `hbbft` Bench And Simulation

```sh
cd sbc/hbbft
go test ./...
go run simulation/main.go
go run bench/main.go
```

The module tests cover ordinary RBC, BBA, ACS, queue, and slot-adapter behavior.
They do not establish the missing mixed-root, equivocation, future-message, or
cryptographic-common-coin properties.

## Campaign Runner Portability

```sh
bash scripts/test-campaign-runners.sh
```

Run this after changing a runner or structured-artifact helper. It validates Bash
syntax, Python fixtures, CLI behavior, paths containing spaces, UTF-8 output, and
all eight side-effect-free `--validate-only` paths on macOS Bash 3.2 and Linux
Bash 5. Terraform formatting/validation remains a separate gate.

The migrated Bash runners have produced the accepted M3 three-region evidence;
they are no longer awaiting their first real EC2 pilot.

## Research-Question-Oriented Validation

### RQ1: Slot-Level Timing

The prototype supports local multi-slot p50/p95 evaluation and accepted
three-region honest-path p50/p95 measurement. Available intervals cover proposal,
ACS, Merge + Plan and its substages, share generation, threshold wait, combine,
materialization, and total slot time.

It does not include DVT threshold signing, block publication, or
execution-client validation latency.

### RQ2: Coordination And Cryptographic Overhead

Evaluator and Prometheus outputs expose ACS/share message and byte counts,
selected work, stage timing, and bounded recovery attempts. BTE benchmarks cover
hybrid full-path and optimization dimensions. Complete M4-style CPU, memory,
bandwidth, plaintext-baseline, and signing-overhead characterization is not yet
accepted evidence.

### RQ3: Faults And Adversarial Behavior

Local fault modes cover omitted/empty proposals, withheld shares, corrupted
shares, and fixed send delay. Consistency checks compare `BatchID`, merged-set
identity, plaintext ordering, and parsed Ethereum hashes.

The prototype does not yet provide targeted ciphertext censorship, public share
attribution, a cryptographic common coin, mixed-root RBC protection, or complete
Byzantine equivocation/future-message tests.

### RQ4: Economic And Resource Cost

Existing evidence records ciphertext size, protocol bytes/messages, selected gas
and transactions, stage timing, process resource observations, and EC2 campaign
metadata. It does not yet provide a complete proposer-reward model, historical
fee-market analysis, or a controlled infrastructure-cost comparison.

## Milestone Evidence Map

| Milestone | Primary evidence |
|---|---|
| `M0. Current Prototype Baseline` | module tests, demo smoke, documented protocol boundary |
| `M1. Slot Timing and Baseline Latency Evidence` | local `eval-suite`; full corrected 315-sample refresh remains outstanding |
| `M2. Distributed Deployment-Ready BLOC Sidecar` | Compose rehearsal, Prometheus/Grafana, `eval-remote` |
| `M3. Distributed Sidecar Metrics Collection` | accepted three-region VM/EC2 campaign and raw artifacts |
| `M4. Coordination, Cryptographic, and Resource Overhead Characterization` | future evaluator counters, BTE sweeps, and CPU/memory/bandwidth evidence |
| `M5. Fault and Adversarial Robustness Validation` | future adversarial tests and fault campaigns |
| `M6. Builder API Boundary` | future Builder-facing adapter serving agreed transaction sets |

M3 is the latest completed milestone. No next active milestone is currently
selected; see [STATUS.md](STATUS.md).

## Review Checklist

- Did validation match the affected module and behavior?
- Did protocol ordering, hashing, identity, or threshold changes receive
  cross-module validation?
- If a command or acceptance rule changed, was its canonical owner updated?
- Are failed or inconsistent observations retained and excluded from accepted
  summaries?
- Was `STATUS.md` reviewed for milestone, blocker, evidence, baseline, and
  next-action changes?
- Does the handoff state whether status required an update?
