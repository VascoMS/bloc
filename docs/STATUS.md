# Status

## Current Prototype State

The current implemented prototype supports a local BLOC pipeline where operators propose encrypted inclusion lists, reach slot-scoped ACS agreement, deterministically merge the accepted encrypted set, threshold-decrypt the selected batch, and materialize the same plaintext transaction set. The repo already supports local evaluation, fault injection, deterministic merge validation, and cluster-facing BTE benchmarking.

The integrated BTE path already uses deterministic BEAT-MEV `Opt-2`
sub-batching (`alpha = ceil(2*sqrt(B))`) during batch planning. M1 therefore
measures the optimized integrated path, not the naive BEAT-MEV combine path or a
comparison across optimization variants.

The current implemented prototype does not yet include DKG-generated shares, public decryption-share verifiability, real DVT threshold signing, execution-client validation of decrypted transactions, or PBS prefix enforcement. PBS remains a deferred target architecture item, not part of the active roadmap.

## Active Milestone

- Milestone: `M1. Slot Timing and Baseline Latency Evidence`
- Status: `in progress`
- Success criteria:
  - end-to-end slot latency is measured for the current local prototype,
  - per-stage timing is broken out for ACS, merge/planning, share generation, and commit-to-plaintext,
  - output structure is ready for p50/p95/p99 aggregation in later runs,
  - the baseline validation path is reproducible from documented commands.

## Immediate Next Actions

1. Run the complete 315-sample `m1-baseline` profile using the corrected ACS/BBA liveness path.
2. Generate and inspect the three documented latency charts only from a fully valid dataset.
3. Record final p50/p95 results and update the roadmap only if the baseline changes milestone sequencing.

## Current Blockers / Risks

- The previous 315-run libp2p-only campaign remains invalid historical evidence: result timeouts were concentrated in 7/10-node scenarios.
- The diagnosed cause was BBA/ACS liveness, not BTE combination: lagging nodes had all RBC outputs and peer decryption shares but were waiting for one or more BBA instances to terminate.
- A corrected 30-run 7/10-node stress matrix passed; the complete 315-sample M1 campaign is still required before reporting final baseline figures.
- Local-host scheduling noise remains a risk, and these measurements still do not represent an integrated DVT environment.
- Distributed evidence and PBS-specific validation remain intentionally out of scope for the active roadmap.

## Last Known Good State

- Date: `2026-06-25`
- Meaning: raw TCP has been removed; protobuf-over-libp2p is the sole operator transport; nodes execute sequential clean slots while reusing BTE state, processes, HTTP clients, and their libp2p mesh. The 7/10-node liveness stalls were traced to BBA accounting and ACS completion behavior, then corrected with per-slot ACS driver serialization, BBA dual-BVAL tracking, later-epoch self-BVAL handling, and an all-RBC ACS completion path. A 27-run 4/7/10 matrix and a 30-run 7/10 stress matrix both passed; the full 315-run M1 baseline is still pending.
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

- `M2. Coordination and Cryptographic Overhead Characterization`
  - Include a BEAT-MEV optimization sweep if the claim requires comparing normal, `sqrt(B)`, `2*sqrt(B)`, parallel combine, or batch sizes beyond the M1 `BMax=128` profile.

## Deferred Later Milestones

- `M3. Fault and Adversarial Robustness Validation`
- `M4. Economic and Resource Cost Characterization`
- `M5. Distributed Evaluation and Dissertation-Ready Evidence`
- `Deferred Target: PBS Prefix Enforcement`
