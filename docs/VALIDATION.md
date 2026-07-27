# Validation

## Evidence Policy

Module tests are the default preflight. Local `eval-local` and `eval-suite` runs
provide correctness, configuration, and artifact-contract preflight under
controlled conditions. Docker Compose validates local deployment mechanics but
is not an independent-machine performance substrate.

VM/EC2-per-sidecar campaigns provide the distributed evidence shape: one machine
per operator and a separate controller running `eval-remote`, artifact
collection, and Prometheus/Grafana. The accepted M3 three-region campaign is the
current distributed baseline. Its scope is honest-path p50/p95 latency and
resource/network observation, not Byzantine safety, production confidentiality,
or a causal topology comparison.

Charts are a reporting step after valid evidence exists. They are not a reason
to run an experiment or a substitute for raw accepted artifacts.

Local `eval-suite` output is validation-only. It may establish execution,
outcome retention, consistency, provenance, schema completeness, timing
additivity, and chart-loader compatibility, but it must not support local
quantile, maximum, throughput, scaling, topology, resource, or local-versus-VM
claims. Final RQ1/RQ2 performance evidence is VM-only.

Resource evidence is collected only in dedicated `resource-measured` phases,
never while primary latency/p99 observations run. Acceptance requires 250 ms
host-local raw samples for every node/configuration, contiguous sample indexes,
monotonic CPU/network counters, no restart/OOM signal, and separate per-node and
cluster summaries. Cluster memory fields are sums of per-node maxima/peaks, not
temporally synchronized readings. Container network bytes must not be presented
as protocol message bytes. Accepted historical M3 `resource-samples.csv`
artifacts retain their original running/restart/OOM stability gate but are coarse
evidence and must not yield a host-resource summary.

## Frozen Evaluation Release Candidate

Final M5/M6 evidence is bound to this release-candidate contract:

- source: `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`;
- image: `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`;
- image platform/user/entrypoint: `linux/amd64`, `10001:10001`,
  `["bloc-node"]`;
- validation root:
  `results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/`;
- ACS safety root: `results/local/acs-common-subset-safety/rc-2bc8efc/`;
- evaluator artifact schema: `bloc-eval-suite/v3`.

The final VM primary honest-path configuration is `n=4,t=3` and `n=7,t=5`,
batches `8/32/128`, persistent execution, 10 warmups, 1,000 measured attempts
per scenario, 10 balanced repetition blocks, seed `20260621`, and a 12-second
completed-within-deadline boundary. Failed, inconsistent, and timed-out
attempts remain in the artifact but never enter latency quantiles. The guarded
VM `n=10,t=7` and batch-512 extension uses its separate 30-observation
pilot/continuation rule and requires `BMax` to cover the selected batch.

Issue #8's local distributed-campaign preflight runs `n=4,t=3` and `n=7,t=5`,
batches `8/32/128`, with 1 warmup and 1 measured observation per cell. Its
extension runs `n=10,t=7`, batches `8/32/128`, and batch `512` at `n=4/7/10`,
with 1 warmup and 3 measured observations per unique extension cell. Primary
measurements must succeed consistently within 12 seconds and be artifact-valid;
extension measurements may miss the boundary when they terminate consistently
with a complete retained outcome. The preflight retains
`classification=validation-only` and `performance_claims_allowed=false` under
`results/local/distributed-preflight-2bc8efc/`.

Corpus-backed protocol campaigns use `tx_source=mock-placeholder` and the
committed 100-target `28/50/12/8/2` workload. The balanced 500-target issue #13
corpus is only for per-class client-overhead evidence and must not be substituted
for the protocol workload. Manifests and CSV/JSON rows must retain the source
SHA, image tag/digest, seed, block/order metadata, planned scenario count,
transaction source, configuration, attempt outcome, and schema version.

No final campaign may mix source revisions or image digests. A code,
configuration, corpus, or schema change requires an explicit invalidation
decision, a newly frozen release candidate, and rerunning every affected phase.
Measurements from the old and new candidate must not be merged into one
campaign, even when their CSV columns remain compatible.

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

Use local evaluation for controlled protocol behavior and preflight validation:

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

M1 remains a historical local baseline. It is not an outstanding campaign
requirement: final p99/scaling evidence is VM-only under M5, and issue #8 uses
local evaluation only to validate the distributed-campaign contract.

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

The gate includes mixed-root RBC rejection and post-decode Merkle commitment
checks. It does not cover conflicting AUX equivocation or sufficiently delayed
future-epoch messages.

The runner records its host OS in `manifest.json`. When the full campaign runs
on a non-Linux host, retain a separate Linux execution of the same
`Test(RBC|ACS|BBA|SlotACS)` race selection and bind that result to the same clean
commit and RBC source hashes. A passing non-Linux race stage alone does not
satisfy the Linux acceptance item.

## Terminal Failure Gate

Changes to slot lifecycle, protocol failure handling, `/result`, or evaluator
polling must preserve these outcomes:

- pending returns HTTP 202 and is distinguishable from unavailable transport;
- success returns HTTP 200 with the stable materialized `Result`;
- the first terminal failure returns HTTP 422 with stable slot, bounded reason,
  and failure timestamp on every read;
- a synchronous failure reached through `/start` returns a bounded 2xx notice
  and remains available as the authoritative 422 `/result`, rather than leaking
  the underlying dynamic error or bypassing evaluator polling;
- a wrong-slot read remains HTTP 409;
- late protocol progress cannot replace a terminal failure with success, and
  local start cannot replace a peer-driven terminal success or failure;
- a failed slot can be replaced only by a strictly greater slot; and
- evaluator JSON, JSONL, and both CSV formats retain a run-level failure reason
  even with zero successful node results, while successful-only latency
  summaries receive no sample from that run.

Run both the normal and race suites:

```sh
cd bloc-node
go test ./...
go test -race ./...
```

## Mempool Provider Gate

Changes to the `mempool-http` provider or cluster configuration must preserve:

- one node-owned client and one request attempt, with no retry loop;
- a generated and old-config compatibility default of 2,000 ms;
- startup rejection of negative or duration-overflowing bounds;
- successful response decoding and ordinary non-2xx status/body diagnostics;
- caller cancellation through the request context; and
- a blocking upstream terminating within the configured client timeout.

Run the focused provider/configuration tests and the complete module suites:

```sh
cd bloc-node
go test ./internal/app -run 'TestMempoolProvider|TestProviderConfig|TestGenConfig|TestParseEC2ConfigRejectsNegativeMempoolTimeout'
go test ./...
go test -race ./...
```

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

The module tests cover ordinary and mixed-root RBC behavior, post-decode Merkle
commitment verification, BBA, ACS, queue, and slot-adapter behavior. They do not
establish the missing equivocation, future-message, or cryptographic-common-coin
properties.

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

The evaluated system begins with mempool inclusion-list collection and ends when
the distributed BLOC sidecars publish the same deterministic ordered plaintext
transaction set. Builder API adaptation, execution payload construction,
DVT/SSV signing, and block publication are not part of any measured interval.

### Shared Final-Campaign Contract

The primary honest-path matrix is:

| Dimension | Values |
|---|---|
| Operators and threshold | `n=4,t=3`; `n=7,t=5` |
| Batch | `8`, `32`, `128` |
| Environment | matched same-region VM; matched three-region VM |
| Sampling | 10 retained warmups; 1,000 measured observations per scenario |
| Scheduling | balanced measurement blocks; scenario order and seed retained |

The VM scale extension adds `n=10,t=7` at batches `8/32/128` and batch `512` at
`n=4/7/10`. Each extension scenario begins with a separate 30-observation pilot.
If the pilot is viable, run 1,000 independent final observations. If it clearly
exceeds the 12-second envelope or fails frequently, retain 100 independent
boundary observations and report that no p99 feasibility claim is made.

Matched VM campaigns use the same source SHA, image digest, instance class,
transaction source and corpus, configuration, warmup policy, and block schedule.
Warmups, failures, inconsistent runs, and timeouts are retained. Successful-run
latency quantiles never hide the attempted-run completion rate.

Headline statistics are Type-7 p50, p95, and p99, maximum, attempted/successful
counts, and the fraction completing consistently within 12 seconds. Quantile
tables include two-sided 95% non-parametric order-statistic confidence
intervals whose one-based ranks are selected from the exact
`Binomial(n, quantile)` CDF. The chart implementation uses pandas linear
interpolation (Type 7) for point estimates and Python `math.comb` for interval
ranks, without SciPy. p99 values and intervals are blank unless the successful,
consistent scenario sample contains at least 1,000 observations; lower-count
artifacts retain p50/p95 compatibility but cannot support a p99 claim.

Evaluator schema `bloc-eval-suite/v3` and its run/node CSVs retain
`measurement_block`, `block_iteration`, schedule seed, planned scenario count,
and the realized run order. Every measured attempt is retained and classified
as completed, failed, or timed out. Attempted, completed, completed consistently
within the deadline, failed, and timed-out counts are reported separately; only
successful, consistent observations enter latency or stage distributions. A
fully retained failed/timeout schedule is complete negative evidence, not an
incomplete artifact.

A scenario supports a positive timing conclusion only when at least 99% of
attempts finish consistently within 12 seconds, empirical p99 is below 12
seconds, and no successful attempt contains divergent outputs. Campaign
acceptance is based on artifact integrity and completeness, not on obtaining a
positive result.

The final VM runners accept only `n=4/7/10` and batches `8/32/128/512`; the
configured/generated `BMax` must be at least the largest requested batch.
`--repetitions` must divide evenly by
`--repetition-blocks`. Stable seeded blocks balance scenario order while making
the order reproducible from the manifest.

#### Local Distributed-Campaign Preflight

Issue #8 runs the final VM configuration space locally without producing a
performance dataset. Its primary matrix is `n=4/7`, batches `8/32/128`, with 1
warmup and 1 measured observation per cell. Its unique extension matrix is
`n=10`, batches `8/32/128`, plus batch `512` at `n=4/7/10`, with 1 warmup and 3
measured observations per cell. Every primary measurement must be successful,
cross-node consistent, within 12 seconds, and artifact-valid. Every extension
measurement must terminate consistently with a complete retained outcome; a
deadline miss alone does not fail the preflight.

The preflight validates startup/teardown, attempt retention and classification,
cross-node consistency, timing additivity, release-candidate provenance, schema
completeness, and chart-loader compatibility. It binds the exact source, image,
corpus, configuration, seed, and schema in a manifest below
`results/local/distributed-preflight-2bc8efc/`, which must state
`classification=validation-only` and `performance_claims_allowed=false`. Do not
collect local CPU, memory, or network evidence. Do not report local quantiles,
throughput, scaling, topology, resource, or local-versus-VM comparisons.

### RQ1: Sidecar Timing Feasibility

Question: can the distributed sidecar produce a common deterministic plaintext
transaction set within an Ethereum-slot-compatible time budget?

Measure critical-path latency from the slot trigger until the slowest correct
node publishes its result, commit-to-plaintext latency, every existing protocol
stage, deadline completion, and cross-node consistency. Report matched
same-region and three-region VM results separately and express latency as both
milliseconds and a fraction of the 12-second slot. Local preflight output is
not RQ1 performance evidence.

The answer applies only to the BLOC sidecar. It does not establish complete
block building, signing, publication, or execution-client feasibility.

### RQ2: Coordination And Cryptographic Overhead

Question: what latency, communication, throughput, and resource overhead do
distributed sidecar coordination and BTE introduce?

For every VM primary scenario record proposal, ACS, merge/planning, share
generation, threshold wait, combine, and materialization time; ACS/share message
and byte counts; CPU seconds; peak resident memory; selected work; and derived
transactions per second. Stage totals and merge/plan substages must satisfy their
documented additivity tolerances.

Benchmark client encryption, ciphertext expansion, share generation,
reconstruction, allocations, and normal/`sqrt(B)`/`2*sqrt(B)`/parallel BTE
variants. Use a benchmark-only plaintext/raw-submission control for relative
client overhead. Do not add a second production protocol mode solely to create a
baseline.

The answer identifies dominant stages, computational versus network effects,
and how overhead changes with batch, operator count, threshold, topology, and
BTE optimization from VM evidence. It does not include DVT threshold-signing
overhead or local preflight performance statistics.

### RQ3: Faults And Adversarial Behavior

Question: how robust is the sidecar under operator faults and adversarial
behavior?

The experimental fault model is `n=3f+1`, `t=2f+1`: `n=4,f=1,t=3` and
`n=7,f=2,t=5`, with selected `n=10,f=3,t=7` threshold confirmation. Run 30
measured repetitions for operational proposal omission, target omission by
faulty operators, share withholding/corruption, and bounded delay. Use
deterministic tests and reordered-delivery simulations for mixed RBC roots,
equivocation, malformed encodings/proofs, wrong slot or cluster scope,
commitment mismatch, conflicting shares, and future/conflicting BBA messages.

Within-bound liveness scenarios must complete with identical outputs at all
correct nodes. A target present in every correct proposal must survive up to
`f` faulty omissions. Up to `f` withheld shares must reconstruct. A
threshold-breaking case may fail, but it must publish a bounded durable failure
and never an inconsistent success. Malformed inputs must be rejected or fail the
slot without producing divergent accepted output.

Experiments demonstrate only exercised behavior. Pending-plaintext secrecy
relies on the BTE construction and state-transition tests; it is not proven by a
campaign. General asynchronous Byzantine liveness is not claimed while the
common coin remains deterministic. Trusted setup and absent public share proofs
remain explicit prototype limitations.

### RQ4: Submission And Operational Cost

Question: what incremental submission and operational costs does BLOC impose on
users and sidecar operators?

Use 100 deterministic valid signed transactions in each of the transfer,
128-byte, 256-byte, 1,024-byte, and 4,096-byte payload classes. Measure client
encryption latency, raw transaction bytes, BTE ciphertext bytes,
placeholder/carrier bytes, submission expansion, and a gas-equivalent carrier
estimate. Keep results per class without a weighted or pooled client summary.
The carrier estimate is not reported as paid gas unless a future on-chain path
actually includes it; target execution gas remains outside this off-chain
sidecar measurement.

#### RQ4 Client-Overhead Corpus

The issue #13 corpus is accepted only when its test contract proves:

- exactly 500 valid signed EIP-1559 transactions on development chain 1337;
- unique transaction hashes and recoverable senders;
- exactly 100 rows for each transfer, 128, 256, 1,024, and 4,096-byte target
  calldata class;
- matching JSONL class labels and decoded target sizes; and
- target gas limits at or above the EIP-7623 data-only floor.

Generate local public cluster material and 500 raw client measurements from the
repository root:

```sh
mkdir -p results/issue-13-client-overhead
cd bloc-node
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 128 \
  --out ../results/issue-13-client-overhead/cluster.json

cd ../mempool-il
go run ./cmd/corpus-report \
  -corpus ../deploy/docker-compose/corpus/client-overhead-targets.jsonl \
  -cluster-config ../results/issue-13-client-overhead/cluster.json \
  -out ../results/issue-13-client-overhead/client_overhead.csv \
  -slot 1 \
  -samples-per-class 100
```

The accepted CSV has one header plus 500 data rows, exactly 100 per class and
500 distinct target hashes. Every client target is measured once; cycling and
weighted or pooled summaries are rejected. The schema is:

```text
class,sample_index,target_hash,raw_bytes,ciphertext_bytes,placeholder_bytes,calldata_bytes,carrier_gas_estimate,encryption_us,submission_serialization_us
```

`encryption_us` times BTE encryption plus canonical ciphertext binary encoding.
Placeholder construction and signing occur after that timer.
`submission_serialization_us` times raw transaction hex encoding and JSON
serialization without network I/O. The two paths use the same signed target
bytes. Ciphertext contents and timings are raw randomized measurements; they
are not expected to repeat exactly.

`carrier_gas_estimate` uses the post-Pectra EIP-7623 data-only floor:

```text
tokens = zero placeholder calldata bytes + 4 * nonzero placeholder calldata bytes
carrier_gas_estimate = 21,000 + 10 * tokens
```

This is not paid gas, a receipt, or an estimate of target execution gas. The
CSV, generated CRS/configuration, and development operator secrets remain under
the ignored `results/issue-13-client-overhead/` root.

#### Full-Protocol Transaction-Size Workload

The separate `deploy/docker-compose/corpus/mock-targets.jsonl` file contains
100 valid signed targets distributed `28/50/12/8/2` across the same five
classes. It is the mock-placeholder input for full-path ACS and BTE evaluation;
it is not the client-overhead sampling frame.

The protocol-workload weighting comes from a mainnet observation at
`2026-07-26T15:01:55Z`. Using full transaction objects returned by
`eth_getBlockByNumber` from `ethereum-rpc.publicnode.com`, the analysis skipped
64 blocks behind the reported head and sampled every twentieth block from
25,617,666 through 25,610,486: 360 blocks, approximately 24 hours, 74,383
transactions, and 55 contract creations.

Input bytes were calculated as `(len(input) - 2) / 2`. Empty input was zero;
contract-creation initcode was retained as payload. For each bin, transaction
share is its transaction count divided by 74,383, and calldata byte share is
its summed input bytes divided by all sampled input bytes:

| Input bytes | Transaction share | Calldata byte share |
|---|---:|---:|
| 0 | 28.408% | 0.000% |
| 1–127 | 43.388% | 5.621% |
| 128–255 | 6.114% | 2.187% |
| 256–1,023 | 11.919% | 12.798% |
| 1,024–4,095 | 7.924% | 29.444% |
| 4,096+ | 2.246% | 49.951% |

The protocol workload maps zero to transfer, 1–255 to 128, 256–1,023 to 256,
1,024–4,095 to 1,024, and 4,096+ to 4,096 bytes. Rounding transaction shares
gives `28/50/12/8/2`. Byte share motivates retaining the rare large classes but
does not set client measurement counts. Treat this as a dated one-day
full-protocol workload approximation, not a universal Ethereum transaction
distribution.

Translate accepted RQ2 measurements into CPU seconds, peak memory, inbound and
outbound bytes, dedicated-cluster hourly cost, and amortized cost per slot and
transaction. Record the provider, region, instance type, pricing date, transfer
assumptions, and formula. Separate dedicated provisioning cost from truly
incremental resource use.

Do not claim proposer profitability, lost MEV, PBS competitiveness, historical
congestion effects, or actual on-chain user fees.

## Milestone Evidence Map

| Milestone | Primary evidence |
|---|---|
| `M0. Current Prototype Baseline` | module tests, demo smoke, documented protocol boundary |
| `M1. Slot Timing and Baseline Latency Evidence` | historical local `eval-suite` baseline |
| `M2. Distributed Deployment-Ready BLOC Sidecar` | Compose rehearsal, Prometheus/Grafana, `eval-remote` |
| `M3. Distributed Sidecar Metrics Collection` | accepted three-region VM/EC2 campaign and raw artifacts |
| `M4. Evaluation Readiness And Prototype Hardening` | correctness blockers, terminal failures, mempool timeout, release-candidate validation and freeze |
| `M5. Performance, Scaling, And Resource Evidence` | validation-only local distributed-campaign preflight; final same-region/three-region VM p99 and resource evidence, BTE/client benchmarks, `n=10`/batch-512 extension |
| `M6. Fault And Adversarial Robustness Evidence` | deterministic adversarial regressions and 30-observation operational fault campaigns |
| `M7. Cost Analysis And Thesis Evidence Synthesis` | user/operator cost model, RQ answer matrix, figures, limitations, checksummed final archive |

M4 is complete and M5 is active; see [STATUS.md](STATUS.md). Granular task
state is tracked in the [BLOC Thesis
Prototype GitHub Project](https://github.com/users/VascoMS/projects/1).

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
