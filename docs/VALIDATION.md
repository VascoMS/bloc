# Validation

## Evidence Policy

Local module tests are preflight checks. Local `eval-local` and `eval-suite`
runs are the clean protocol baseline for ACS/BTE behavior under controlled
conditions. Docker Compose validates local deployment mechanics, but it should
not be the primary distributed performance substrate.

The current distributed thesis evidence target is VM/EC2-per-sidecar behavior:
one independent machine per BLOC operator, plus a separate controller running
`eval-remote`, artifact collection, and optional Prometheus/Grafana or
OpenTelemetry collection.

Use charts only after there is a local baseline or VM-distributed evidence worth
presenting. Chart generation is a reporting step, not a reason to run
experiments.

## Validation Matrix

| Change area | Minimum validation | When to go further |
|---|---|---|
| `bloc-node` logic | `go test ./...` in `bloc-node` | Run `eval-local` or demo flow for consensus, transport, or end-to-end behavior |
| `mempool-il` logic | `go test ./...` in `mempool-il` | Add a local service smoke check when API or source handling changes |
| BTE library logic | `go test ./...` in `bte/btd-impl-main` | Run full-path benchmark when performance or batch planning changes |
| `sbc/hbbft` logic | `go test ./...` in `sbc/hbbft` | Run simulation or bench when consensus behavior or throughput assumptions change |
| Latency charts | `python -m pytest` in `latency-charts` | Render charts from an `eval-suite` directory after schema or presentation changes |
| Cross-module protocol behavior | Relevant module tests plus `bloc-node` smoke flow | Use local `eval-suite` for protocol-baseline evidence, then VM/EC2-per-sidecar `eval-remote` for distributed evidence |
| Deployment artifacts | Docker build plus Compose smoke | Use VM/EC2-per-sidecar deployment for the main distributed evidence path |
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

Use local evaluation for controlled protocol behavior and baseline timing. Local
numbers are not a substitute for distributed VM evidence, but they are the clean
baseline against which distributed runs should be interpreted.

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
step for ordinary code changes. Run it when deliberately refreshing the clean
local protocol baseline, or after a change that can materially affect the
evaluator's local latency results. For routine `bloc-node` changes, use
`go test ./...` and, when the end-to-end path is relevant, the reduced smoke
check below.

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

After changes that affect ACS/BBA safety or liveness, run the local safety
campaign before collecting distributed latency evidence:

```sh
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

The campaign requires 1,000 fixed reordered RBC/BBA delivery schedules, Linux
race validation, a persistent n4/batch-128 gate with 5 warmups and 100 measured
slots, and an n4/n7 matrix over batches `8,32,128` with 3 warmups and 30 measured
slots per scenario. Every measured slot must succeed and be cross-node
consistent. Failed runs retain `slot-status.json`, including RBC output IDs,
the BBA decision map, truthy BBA proposer IDs, and the ACS waiting reason. Results
are ignored under `results/local/acs-common-subset-safety/<campaign-id>/`.

For a shorter liveness-only diagnostic, use:

```sh
go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent \
  --node-counts 7,10 \
  --batch-sizes 8,32,128 \
  --warmups 0 \
  --repetitions 5 \
  --out-dir results/acs-liveness-stress
```

### Resource-safety hardening gate

Changes to proposal ingestion, transport envelopes, share admission, or BTE
combination must pass both module suites and their Linux race variants:

```sh
cd bloc-node && go test ./... && go test -race ./...
cd bte/btd-impl-main && go test ./... && go test -race ./...
```

Acceptance requires tests proving:

- old v2 configs receive the 8 MiB/16 MiB/256 defaults and invalid overrides
  fail at startup;
- proposals and envelopes accept the exact limit and reject one byte over;
- provider/direct proposals cannot exceed `BMax` or the encoded proposal cap;
- authenticated operators cannot retain alternate batch identities, out-of-plan
  sub-batches, or conflicting replacement points;
- retention never exceeds `N*BMax` before planning or `N*alpha` afterward;
- operator identity equals the Kyber public-share index at the node and BTE
  boundaries;
- subset enumeration is deterministic, reports attempts, and stops at its
  cumulative per-sub-batch budget; and
- the n10/t7 case with three invalid shares recovers in 165 attempts under the
  default 256-attempt budget.

Before EC2 evidence collection, also run the complete local ACS safety campaign
because the hardened transport/share path is exercised by its persistent gate
and matrix even though RBC/BBA logic is unchanged.

### M1 Timing Definitions

- `proposal_preparation_us`: local slot start through encoded proposal readiness.
- `acs_us`: proposal readiness through local ACS decision.
- `merge_plan_us`: ACS decision through deterministic merge, ciphertext decoding, and BTE batch planning.
- `acs_output_decode_us`: accepted ACS proposal decoding inside `merge_plan_us`.
- `agreed_set_us`: canonical inclusion-list hashing and agreed-set construction inside `merge_plan_us`.
- `merge_us`: placeholder validation, deduplication, ordering, and blockspace selection inside `merge_plan_us`.
- `ciphertext_decode_us`: selected BTE ciphertext deserialization inside `merge_plan_us`.
- `batch_plan_us`: deterministic BTE sub-batch arrangement and batch-ID construction inside `merge_plan_us`.
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

The five merge/plan substages are monotonic, bounded Prometheus stage labels
and must sum to `merge_plan_us` within 20 microseconds. Empty selected batches
record completed decode/merge boundaries and zero ciphertext-planning time.
Older CSVs without the optional five columns remain chart-compatible.

### Merge/Plan Optimization Campaign

Use the cross-platform local campaign when changing inclusion-list hashing,
deterministic merge, ciphertext decoding, or BTE batch planning. It captures
ten-sample allocation benchmarks, CPU/memory profiles, and a 4/7-node local
evaluator matrix without allocating cloud resources:

```sh
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase baseline --campaign-id <id>
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase optimized --campaign-id <id>
```

Artifacts are ignored under `results/local/merge-plan-optimization/<id>/`.
Retain an optimization only when semantic identities remain exact and no
batch-32/128 pipeline median regresses by more than 5%.

### M1 Latency Charts

These charts are for the clean local protocol baseline. For distributed sidecar
work, generate charts only after a VM/EC2-per-sidecar remote-evaluator campaign
has produced result artifacts worth reporting.

Set up the chart module once:

```sh
cd latency-charts
python -m venv .venv
. .venv/bin/activate
python -m pip install -e ".[test]"
```

Generate SVG and PNG figures in the repository chart directory:

```sh
python -m bloc_latency_charts ../bloc-node/results/m1-local/baseline-persistent
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
| `bloc_protocol_envelopes_rejected_total{cluster_id,node_id,direction,reason}` | counter | bounded envelope rejection reasons |
| `bloc_decryption_shares_accepted_total{cluster_id,node_id}` | counter | unique shares admitted to candidate storage |
| `bloc_decryption_shares_rejected_total{cluster_id,node_id,reason}` | counter | bounded share rejection reasons |
| `bloc_decryption_share_subset_attempts_total{cluster_id,node_id}` | counter | cryptographic recovery attempts |
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

Optionally test chart compatibility from the distributed output if the charting
schema changed. Do not treat Compose charts as thesis evidence:

```sh
cd latency-charts
python -m bloc_latency_charts ../bloc-node/results/distributed/compose-smoke
```

### Mock Placeholder Mempool Smoke

Use this when validating realistic transaction-source behavior. Public mempool
transactions are treated as target payloads, not as already-valid BLOC
placeholders. `mempool-il` encrypts each corpus target once, signs a mock
placeholder transaction, parses that placeholder transaction's calldata, and
sidecars consume the derived `encrypted_payload_hex` from the inclusion-list
API.
This is a realism smoke path, not the central milestone unless the task is
specifically about transaction-source realism.

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
- evaluator artifacts remain compatible with the later distributed reporting
  pipeline when those results are intentionally promoted for presentation.

### VM/EC2-Per-Sidecar Distributed Evidence

Use this as the primary distributed thesis evaluation shape. Run one
`bloc-node` sidecar per independent VM/EC2 instance and run the evaluator from a
separate controller machine.

The first supported EC2 recipe uses host-local Docker Compose:

- each operator EC2 runs exactly one `bloc-node` sidecar container from
  `deploy/ec2/operator-compose.yaml`;
- the controller EC2 runs Prometheus/Grafana from
  `deploy/ec2/controller-compose.yaml` and runs `eval-remote`;
- `deploy/ec2/terraform/` can create the operator/controller instances and
  emit an inventory JSON;
- Terraform creates an ECR repository for the sidecar image and an EC2 instance
  profile with ECR read-only pull access, so the deploy IAM principal needs
  ECR repository-management permissions and scoped IAM role/profile permissions;
- ECR-backed experiment ids must begin with `bloc-ec2-`, because the scoped IAM
  policy allows only `bloc-ec2-*` roles and instance profiles;
- `bloc-node gen-ec2-config` converts that inventory into a shared
  `cluster.ec2.json` plus the controller's `remote-eval.ec2.json`;
- Prometheus on the controller scrapes every operator's `/metrics` directly.

Generate configs from the Terraform inventory:

```sh
cd bloc-node
go run ./cmd/bloc-node gen-ec2-config \
  --inventory ../deploy/ec2/inventory.json \
  --cluster-out ../deploy/ec2/cluster.ec2.json \
  --remote-eval-out ../deploy/ec2/remote-eval.ec2.json \
  --cluster-id bloc-ec2 \
  --nodes 4 \
  --bmax 128
```

The generated `cluster.ec2.json` and `cluster.ec2.crs` are public prototype
material. `secrets.ec2/operator-<id>.json` contains only that operator's
trusted-dealer share and libp2p private key. Copy each operator only its own
secret file, store it as mode `0600`, do not commit it, and exclude all operator
secret files from collected experiment artifacts. The CRS removes the shared
setup seed but retains inherited insecure diagonal elements, so this remains a
prototype rather than production-secure setup.

Acceptance criteria for a first VM-distributed smoke:

- all sidecars report healthy `/healthz`;
- all sidecars expose bounded-label `/metrics`;
- `eval-remote` succeeds and reports cross-node consistency;
- result manifests record environment, image or binary version, git commit,
  node count, threshold, batch size, region/zone labels where applicable, and
  endpoint mode.

For the A1 same-AZ pilot readiness campaign, run the Bash EC2 runner:

```sh
bash deploy/ec2/run-a1-pilot.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --experiment-id bloc-ec2-a1-pilot-same-az-n4-step1
```

The A1 pilot must:

- plan only the expected low-cost EC2, VPC, ECR, and scoped IAM resources,
  with generated role/profile names under the `bloc-ec2-*` IAM policy scope;
- deploy 4 operators plus 1 controller in one availability zone;
- run batch sizes `8`, `32`, and `128` with 1 warmup and 3 measured repetitions;
- collect `manifest.json`, generated configs, inventory, network pre/post CSVs,
  Prometheus target snapshots, logs, and merged evaluator CSV/JSON outputs;
- generate charts when the latency chart virtual environment is available;
- destroy Terraform resources and verify no experiment EC2 instances, EBS
  volumes, VPC, ECR repository, temporary key pair, IAM role, or instance
  profile remain.

When debugging the runner itself, it is acceptable to keep a failed environment
alive with `--keep-resources-on-failure`, then use
`deploy/ec2/rerun-a1-pilot-existing.sh` to rebuild and redeploy only the
container image and rerun evaluator scenarios against the existing EC2
instances. Use fresh slot ranges for every rerun and destroy the Terraform
workdir as soon as the debugging window ends.

Network pre/post files should report controller-to-operator HTTP timing for
`/healthz`; do not interpret ICMP ping loss as BLOC network loss unless ICMP is
explicitly allowed in the pilot security groups.

For the first thesis-grade M3 same-AZ synthetic campaign, run:

```sh
bash deploy/ec2/run-m3-same-az.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

The wrapper runs `n=4`, `n=7`, and `n=10` as separate EC2 phases with batch
sizes `8`, `32`, and `128`, 5 warmups, and 30 measured repetitions per batch.
It pauses between node counts unless `--auto-approve-phases` is supplied.

For the same-region cross-AZ synthetic comparison, run:

```sh
bash deploy/ec2/run-m3-cross-az.sh \
  --admin-cidr "<your-ip>/32" \
  --aws-profile bloc \
  --auto-approve-plan
```

The cross-AZ wrapper defaults to `n=4` and `n=7`, spreading generated public
subnets across `us-east-1a`, `us-east-1b`, and `us-east-1c`. It uses the same
batch sizes, warmups, repetitions, instance sizing, cleanup verification, and
chart-generation expectations as the same-AZ M3 path. Do not run `n=10` with
`t3.small` operators until the account vCPU quota can cover 10 operators plus
the controller.

Acceptance criteria:

- every phase manifest is `status=complete`;
- every measured row in `run_measurements.csv` is `success=true` and
  `consistent=true`;
- each node-count phase has exactly 30 measured rows per batch size;
- Prometheus target snapshots show every operator target as `up`;
- `resource-samples.csv` exists and contains pre-campaign, before-batch,
  during-batch, and after-batch operator `docker stats` rows;
- combined campaign charts generate under `results/charts/<campaign-id>/`;
- each phase and the top-level campaign write cleanup verification showing no
  leftover EC2 instances, EBS volumes, VPC, ECR repository, temporary key pair,
  IAM role, or instance profile.

### Three-Region Latency Campaign

The active cross-region campaign uses `t3.small` throughout, with one VPC in
`us-east-1`, `eu-west-1`, and `eu-central-1`. All three VPC pairs are directly
peered because VPC peering is non-transitive. The controller remains in the US;
operators use `node_id % 3`, producing `2/1/1` for `n=4` and `3/2/2` for
`n=7`. Run a plan-only check first:

```sh
bash deploy/ec2/run-m3-three-region.sh \
  --admin-cidr "127.0.0.1/32" \
  --aws-profile bloc \
  --plan-only \
  --unattended
```

Plan-only mode may record an unavailable quota check when the deploy identity
lacks `servicequotas:GetServiceQuota`; it still validates the resource plans,
but the runner refuses a real apply until quotas are positively verified in all
three regions. The required `n=7` ceilings are 8 vCPUs in `us-east-1` and 4 in
each EU region.

Before the full matrix, run an `n=4`, batches `8,128` probe with one warmup and
three measurements. The accepted campaign uses `n=4,7`, batches `8,32,128`, five
warmups, thirty measurements, and a 60-second timeout. Thirty samples support
Type-7 p50/p95, not p99.

Both real commands require a committed source tree and
`--confirm-credit-coverage`. The confirmation is an operator attestation that
an administrator checked the current plan/credit balance before allocation; it
is not inferred from EC2's instance-type `FreeTierEligible` flag.

The four-stage report must add to `total_slot_us` within 20 microseconds:

1. Proposal: `proposal_preparation_us`.
2. ACS: `acs_us`.
3. Merge + Plan: `merge_plan_us`.
4. Decryption + Materialization: `threshold_wait_us + combine_us + materialization_us`.

Acceptance also requires exactly 180 measured slots and 990 finalized node
rows for the canonical matrix; all slots successful and consistent; requested
selected-ciphertext counts; all Prometheus targets up before and after; every
one of five health attempts for every ordered node pair successful before and
after; correct region/AZ placement; one image digest; no restart/OOM evidence;
and authenticated empty cleanup of instances, volumes, three VPCs, all three
peering connections, ECR, regional keys, IAM role, and instance profile.
Operator secrets and temporary SSH private keys are excluded from artifacts.
Generate the report with `python -m bloc_latency_charts.three_region`; it emits
protocol p50/p95, four-stage, pairwise-network, and critical-node-region
summaries. Inter-region transfer and T3 Unlimited surplus credits are
potentially billable even when an instance type is Free Tier eligible.

### EC2 Merge/Plan Attribution

Run `deploy/ec2/run-merge-plan-attribution.sh` only after the relevant
protocol, chart, deployment, and canonical-document changes are committed. The
runner deliberately blocks before AWS preflight when those sources are dirty.
It requires at least 16 Standard On-Demand vCPUs, uses one image digest for all
three phases, limits every phase to 90 minutes, and applies a conservative
`$5` campaign ceiling.

Each phase is valid only when every batch has exactly 30 successful and
consistent measured runs, every node finalized metrics, selected ciphertext
counts equal the batch, all five Merge + Plan substages are present and additive
within 20 microseconds, Prometheus reports all operators up, and cleanup is
empty. The analyzer writes `merge-plan-measurements.csv`,
`merge-plan-summary.csv`, `comparison.csv`, `REPORT.md`, and both PNG and
SVG charts. Use p50 and p95; 30 observations are not enough for a p99 claim.

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

### Campaign runners

Run `bash scripts/test-campaign-runners.sh` on macOS Bash 3.2 and Linux Bash 5.
The gate covers syntax, structured-artifact unit/fixture tests, CLI exit-code
behavior, paths containing spaces, UTF-8-without-BOM output, and all eight
side-effect-free validation paths. Terraform formatting and validation for
`deploy/ec2/terraform` and `deploy/ec2/terraform-three-region` remain separate
required gates. A real EC2 pilot is intentionally not part of this migration's
local acceptance and requires separate approval.

### bloc-node

- deterministic inclusion-list merge behavior,
- accepted inclusion lists and ACS outputs are bound to the active slot and proposer,
- wire/protobuf round-trips,
- deterministic signed Ethereum transaction generation for the evaluator,
- wrong-batch share filtering before threshold combination.
- bounded proposal/envelope handling, authenticated per-operator share
  retention/pruning, and bounded-label rejection metrics.

### BTE

- ciphertext verification rejects mutations,
- scope-bound decoding rejects foreign cluster/slot ciphertexts while generic APIs remain compatible,
- malformed AEAD envelopes return errors without panicking during combine,
- decoded batch identity and exported ciphertext ownership resist caller mutation,
- hybrid encryption/decryption round-trip works,
- deterministic `BatchPlan` behavior, including collision-free fallback for interleaved repeated indices,
- BEAT-MEV-style `2*sqrt(B)` sub-batching is the default cluster planning mode,
- threshold enforcement and duplicate-share handling,
- operator/share-index enforcement and deterministic capped subset recovery,
- full-path benchmark coverage for several batch sizes.

### hbbft

- RBC, BBA, and ACS behavior are covered by module tests,
- slot-scoped BLOC adapter behavior is covered separately from the original HoneyBadger driver.

## Research-Question-Oriented Validation

The current prototype can support:

- local slot-level latency experiments for clean protocol baselines,
- coordination and message-overhead measurement,
- fault-injection scenarios such as omitted proposals or withheld shares,
- blockspace-cap experiments and related materialization metrics.

The current prototype does not yet support stronger deployment claims involving:

- DKG-generated shares,
- public share-verifiability,
- real proposer signing,
- execution-client validation of decrypted transactions,
- Builder API compatibility,
- realistic network-loss or WAN deployment evidence until VM/EC2-per-sidecar
  distributed runs have actually been collected.

## Milestone Evidence Map

| Milestone | Primary evidence |
|---|---|
| `M0. Current Prototype Baseline` | module tests, `bloc-node` demo smoke flow, documented command paths |
| `M1. Slot Timing and Baseline Latency Evidence` | Clean local protocol baseline from `eval-suite`, useful for interpreting distributed overhead |
| `M2. Distributed Deployment-Ready BLOC Sidecar` | Docker/Compose deployment rehearsal, Prometheus `/metrics`, Grafana dashboard, and `eval-remote` outputs |
| `M3. Distributed Sidecar Metrics Collection` | repeated VM/EC2-per-sidecar remote-evaluator campaigns, distributed manifests/CSVs, Prometheus/Grafana or OpenTelemetry observations, and plot-ready outputs |
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
