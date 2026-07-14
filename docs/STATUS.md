# Status

## Current Prototype State

The current implemented prototype supports a local BLOC pipeline where operators propose encrypted inclusion lists, reach slot-scoped ACS agreement, deterministically merge the accepted encrypted set, threshold-decrypt the selected batch, and materialize the same plaintext transaction set. The repo already supports local evaluation, fault injection, deterministic merge validation, and cluster-facing BTE benchmarking.

The integrated BTE path already uses deterministic BEAT-MEV `Opt-2`
sub-batching (`alpha = ceil(2*sqrt(B))`) during batch planning. M1 therefore
measures the optimized integrated path, not the naive BEAT-MEV combine path or a
comparison across optimization variants.

Merge/plan latency is now attributed to ACS-output decoding, agreed-set
construction, deterministic merge, ciphertext decoding, and batch planning.
The local optimization campaign removed repeated inclusion-list hashing,
repeated fee parsing during sort, exact-placeholder duplicate validation, and
BTE batch-ID reserialization while preserving protocol identities.

The current implemented prototype does not yet include DKG-generated shares, public decryption-share verifiability, real DVT threshold signing, execution-client validation of decrypted transactions, Builder API compatibility, or PBS prefix enforcement. Builder/PBS integration remains deferred until after distributed sidecar deployment evidence exists.

## Active Milestone

- Milestone: `M3. Distributed Sidecar Metrics Collection`
- Status: `in progress`
- Evidence posture: local `eval-local`/`eval-suite` runs are the clean protocol
  baseline for ACS/BTE behavior under controlled conditions. Docker Compose is
  a local deployment-mechanics rehearsal. The primary distributed thesis
  evidence target is one VM/EC2 instance per BLOC operator, driven by
  `eval-remote` from a separate controller instance.
- Success criteria:
  - `bloc-node` can run as a containerized sidecar from mounted cluster config and `NODE_ID`,
  - generated configs support local and container listen/advertise addresses without breaking old local configs,
  - each sidecar exposes Prometheus-compatible `/metrics` using counters, gauges, seconds-based histograms, and bounded labels for slot phase, latency stages, message/byte volume, selected tx/gas, HTTP traffic, and result availability,
  - Docker Compose can rehearse a local 4-node sidecar cluster with Prometheus/Grafana or direct `/metrics` visibility,
  - a remote evaluator can drive already-running sidecars and write chart-compatible latency outputs,
  - EC2 inventory can be converted into sidecar cluster config and remote-evaluator config for one-sidecar-per-EC2 deployments,
  - a first 4-operator EC2 smoke can be launched, observed through Prometheus, driven by `eval-remote`, and destroyed after artifact collection,
  - VM/EC2-per-sidecar deployment can run repeated distributed metric-gathering campaigns.

## Immediate Next Actions

1. Run the committed Merge/Plan attribution PowerShell campaign using Free-plan-eligible `c7i-flex.large` `n=4/n=7` and `t3.small` `n=7` phases; its source guard refuses to build or allocate AWS resources from relevant uncommitted files.
2. Use the resulting Compute Flex and T3 report to decide whether the roughly 457 ms local batch-128 decode result is primarily cryptographic work or host-class contention. Treat C7i Flex as baseline-plus-burst capacity, not fixed-performance hardware, and inspect the three measurement blocks for temporal drift.
3. Treat Docker Compose as a local deployment-mechanics rehearsal only.
4. Inspect and compare the completed EC2 M3 synthetic `n=4/n=7` same-AZ and cross-AZ charts/tables before adding mock-placeholder realism, p99, or fault campaigns.
5. Decide whether to request an AWS vCPU quota increase for comparable `t3.small` `n=10` EC2 phases, or document `n=10` as deferred until the account quota is raised.
6. Keep the bash runner as an optional Linux/WSL path once the distro/tooling issue is resolved.

## Current Blockers / Risks

- The previous 315-run libp2p-only campaign remains invalid historical evidence: result timeouts were concentrated in 7/10-node scenarios.
- The diagnosed cause was BBA/ACS liveness, not BTE combination: lagging nodes had all RBC outputs and peer decryption shares but were waiting for one or more BBA instances to terminate.
- A corrected 30-run 7/10-node stress matrix passed; the complete 315-sample M1 campaign is still required before reporting final baseline figures.
- Local-host scheduling noise means Compose timing output is diagnostic only. Distributed thesis metrics should come from the VM/EC2-per-sidecar deployment, where each operator has an independent machine and network identity.
- Prometheus `/metrics` now uses native collectors and histogram-safe PromQL is required for Grafana p50/p95 panels; evaluator CSV/JSON remains the offline chart artifact format.
- Realistic transaction-source evidence now requires the mock-placeholder path: public mempool transactions are target payloads, not native BLOC placeholders, so they must be encrypted once by a mock external submitter before sidecars include them.
- Builder API compatibility, SSV signing enforcement, and PBS-specific validation are intentionally out of scope for this milestone.

## Last Known Good State

- Date: `2026-07-13`
- Meaning: the local BLOC path remains stable after merge/plan attribution and optimization. A matched local 4/7-node, batch-8/32/128 campaign completed 60/60 measured runs successfully and consistently in both baseline and optimized phases. Batch-32/128 pipeline benchmark medians improved by 6.1% to 17.5%, with no retained-scenario regression; evaluator merge/plan medians improved by 11.0% to 14.3%. Existing same-AZ and cross-AZ EC2 evidence remains valid for the earlier image but must be labeled separately from future optimized-image campaigns. `n=10` with `t3.small` remains blocked by the current 16-vCPU account quota.
- Data-realism addendum: `mempool-il` now has a corpus-backed `replay-placeholder` mode that validates real signed Ethereum target transactions, encrypts them once using BLOC public cluster material, and exposes mock placeholder candidates through the existing inclusion-list API. `bloc-node` can consume these encrypted payloads via the mempool provider without changing synthetic evaluator defaults.
- Baseline commands:
  - `cd bloc-node && go test ./...`
  - `cd sbc/hbbft && go test ./...`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 3 --out-dir results/acs-bba-self-vote-matrix`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 5 --out-dir results/acs-all-rbc-stress`
- Evidence location:
  - `results/local/merge-plan-optimization/merge-plan-opt-20260713/` (ignored local baseline/optimized benchmarks, profiles, evaluator outputs, charts, comparison CSVs, and report; 60/60 measured runs succeeded and were consistent in each phase)
  - `results/ec2/m3-cross-az-synthetic-20260706t122922z/` and `results/charts/m3-cross-az-synthetic-20260706t122922z/` (ignored local artifact collection and generated charts from the M3 cross-AZ synthetic campaign: `n=4` and `n=7` `t3.small` operators plus one `t3.small` controller per phase in `us-east-1` across `us-east-1a/b/c`; batches 8/32/128; 5 warmups and 30 measured repetitions per batch; 180/180 measured runs had `success=true` and `consistent=true`; Prometheus saw 4/4 and 7/7 targets up; Terraform destroy completed for both phases; cleanup verification and follow-up AWS checks found no tagged EC2 instances, volumes, VPC, ECR repository, temporary key pair, IAM role, or instance profile)
  - `results/ec2/m3-same-az-synthetic-20260706t105535z/` (ignored local artifact collection from the M3 same-AZ synthetic campaign: `n=4` and `n=7` phases completed cleanly with 180/180 measured runs successful and consistent; the `n=10` phase was not collected because AWS rejected the `t3.small` plan under the current 16-vCPU account quota; cleanup checks found no leftover resources)
  - `results/ec2/bloc-ec2-a1-pilot-same-az-n4-20260705-192544/` and `results/charts/bloc-ec2-a1-pilot-same-az-n4-20260705-192544/` (ignored local artifact collection and generated charts from the automated Windows A1 pilot: 4 `t3.small` operators plus 1 `t3.small` controller in `us-east-1a`; batches 8/32/128; 1 warmup and 3 measured repetitions per batch; all measured runs `success=true` and `consistent=true`; Prometheus saw 4/4 targets up; controller-to-operator HTTP `/healthz` timing succeeded before and after; Terraform destroy completed; follow-up AWS checks found no tagged EC2 instances, volumes, VPC, ECR repository, temporary key pair, IAM role, or instance profile)
  - `deploy/ec2/artifacts/ec2-smoke-20260705-1149/` (ignored local artifact collection from 4 `t3.micro` operators plus 1 `t3.small` controller in `us-east-1`; sidecars healthy, Prometheus saw all 4 targets, `eval-remote` batch-8 smoke succeeded 1/1, node outputs consistent, Terraform destroy completed)
  - `bloc-node/results/m1-local/libp2p-baseline/` (315 runs, invalid diagnostic dataset)
  - `bloc-node/results/m1-local/baseline-meeting-20260625/` (invalid diagnostic dataset; exposed remaining 7/10 liveness stalls)
  - `bloc-node/results/acs-bba-self-vote-matrix/` (27/27 successful across 4/7/10 nodes and 8/32/128 batches)
  - `bloc-node/results/acs-all-rbc-stress/` (30/30 successful across 7/10 nodes and 8/32/128 batches)

## Current M3 Target

- Run repeated remote-evaluator campaigns against VM/EC2-per-sidecar deployments, using Compose only as a local deployment rehearsal, then produce thesis-ready distributed latency/performance artifacts.
- Current target campaign: analyze EC2 same-AZ versus cross-AZ synthetic baselines for `n=4` and `n=7`, then decide the quota/cost path for comparable `n=10` evidence.

## Deferred Later Milestones

- `M4. Coordination, Cryptographic, and Resource Overhead Characterization`
- `M5. Fault and Adversarial Robustness Validation`
- `M6. Builder API Boundary`
- `M7. SSV/DVT Signing Integration`
- `Deferred Target: PBS Prefix Enforcement`
