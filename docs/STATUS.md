# Status

## Current Prototype State

The current implemented prototype supports a local BLOC pipeline where operators propose encrypted inclusion lists, reach slot-scoped ACS agreement, deterministically merge the accepted encrypted set, threshold-decrypt the selected batch, and materialize the same plaintext transaction set. The repo already supports local evaluation, fault injection, deterministic merge validation, and cluster-facing BTE benchmarking.

The integrated BTE path already uses deterministic BEAT-MEV `Opt-2`
sub-batching (`alpha = ceil(2*sqrt(B))`) during batch planning. M1 therefore
measures the optimized integrated path, not the naive BEAT-MEV combine path or a
comparison across optimization variants.

The current implemented prototype does not yet include DKG-generated shares, public decryption-share verifiability, real DVT threshold signing, execution-client validation of decrypted transactions, Builder API compatibility, or PBS prefix enforcement. Builder/PBS integration remains deferred until after distributed sidecar deployment evidence exists.

## Active Milestone

- Milestone: `M2. Distributed Deployment-Ready BLOC Sidecar`
- Status: `in progress`
- Success criteria:
  - `bloc-node` can run as a containerized sidecar from mounted cluster config and `NODE_ID`,
  - generated configs support local, container, and Kubernetes listen/advertise addresses without breaking old local configs,
  - each sidecar exposes Prometheus-compatible `/metrics` using counters, gauges, seconds-based histograms, and bounded labels for slot phase, latency stages, message/byte volume, selected tx/gas, HTTP traffic, and result availability,
  - Docker Compose can run a local 4-node sidecar cluster with Prometheus and Grafana visibility,
  - a remote evaluator can drive already-running sidecars and write chart-compatible latency outputs,
  - Kubernetes manifests provide a repeatable thesis deployment shape using generated prototype config.

## Immediate Next Actions

1. Validate the mock-placeholder mempool path with `cd mempool-il && go test ./...` and `cd bloc-node && go test ./...`.
2. Run Docker Compose with the mock-placeholder override and confirm sidecars recover the target transaction hashes.
3. Generate charts from the mock-placeholder remote evaluator output.
4. If Kubernetes is available, repeat the distributed sidecar smoke with a mounted corpus and generated cluster config.

## Current Blockers / Risks

- The previous 315-run libp2p-only campaign remains invalid historical evidence: result timeouts were concentrated in 7/10-node scenarios.
- The diagnosed cause was BBA/ACS liveness, not BTE combination: lagging nodes had all RBC outputs and peer decryption shares but were waiting for one or more BBA instances to terminate.
- A corrected 30-run 7/10-node stress matrix passed; the complete 315-sample M1 campaign is still required before reporting final baseline figures.
- Local-host scheduling noise remains a risk for Compose smoke runs; cloud/Kubernetes runs are still needed for distributed evidence.
- The Kubernetes manifests intentionally rely on generated trusted-dealer config supplied out of band, because prototype key material should not be committed.
- Prometheus `/metrics` now uses native collectors and histogram-safe PromQL is required for Grafana p50/p95 panels; evaluator CSV/JSON remains the offline chart artifact format.
- Realistic transaction-source evidence now requires the mock-placeholder path: public mempool transactions are target payloads, not native BLOC placeholders, so they must be encrypted once by a mock external submitter before sidecars include them.
- Builder API compatibility, SSV signing enforcement, and PBS-specific validation are intentionally out of scope for this milestone.

## Last Known Good State

- Date: `2026-06-28`
- Meaning: the local BLOC path remains stable after the deployment-readiness changes. `bloc-node` now has backward-compatible listen/advertise config fields, `NODE_ID` sidecar startup, a collector-backed Prometheus `/metrics` endpoint with counters/gauges/histograms, Docker/Compose/Kubernetes deployment artifacts, and an `eval-remote` command for already-running sidecar clusters. The local M1 full baseline is still pending, but the active engineering milestone has moved to distributed deployment readiness.
- Data-realism addendum: `mempool-il` now has a corpus-backed `replay-placeholder` mode that validates real signed Ethereum target transactions, encrypts them once using BLOC public cluster material, and exposes mock placeholder candidates through the existing inclusion-list API. `bloc-node` can consume these encrypted payloads via the mempool provider without changing synthetic evaluator defaults.
- Baseline commands:
  - `cd bloc-node && go test ./...`
  - `cd sbc/hbbft && go test ./...`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 3 --out-dir results/acs-bba-self-vote-matrix`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 5 --out-dir results/acs-all-rbc-stress`
- Evidence location:
  - `bloc-node/results/m1-local/libp2p-baseline/` (315 runs, invalid diagnostic dataset)
  - `bloc-node/results/m1-local/baseline-meeting-20260625/` (invalid diagnostic dataset; exposed remaining 7/10 liveness stalls)
  - `bloc-node/results/acs-bba-self-vote-matrix/` (27/27 successful across 4/7/10 nodes and 8/32/128 batches)
  - `bloc-node/results/acs-all-rbc-stress/` (30/30 successful across 7/10 nodes and 8/32/128 batches)

## Next Milestone

- `M3. Distributed Sidecar Metrics Collection`
  - Run repeated remote-evaluator campaigns against Compose and cloud/Kubernetes deployments, then generate thesis-ready latency/performance charts.

## Deferred Later Milestones

- `M4. Coordination, Cryptographic, and Resource Overhead Characterization`
- `M5. Fault and Adversarial Robustness Validation`
- `M6. Builder API Boundary`
- `M7. SSV/DVT Signing Integration`
- `Deferred Target: PBS Prefix Enforcement`
