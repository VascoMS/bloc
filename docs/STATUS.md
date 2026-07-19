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

The post-ACS correctness boundary now binds production BTE decoding to the
active cluster and slot, rejects malformed AEAD envelopes without panicking,
freezes decoded batch identity and ownership, verifies accepted-list
slot/proposer metadata, and deterministically repairs the rare repeated-index
layouts that the original round-robin assignment rejected. Existing wire
formats, hashes, `BatchID`, `alpha`, and previously successful plans are
unchanged.

The source-led protocol architecture review is complete. The top-down system
boundary now lives in `docs/ARCHITECTURE.md`, with canonical implementation deep
dives under `docs/modules/` and the reviewed findings in
`docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md`. The review identified
correctness and security gaps below the previously exercised test surface,
including mixed-root RBC reconstruction, the deterministic BBA placeholder
coin, BBA equivocation/future-message handling, insecure diagonal CRS elements,
and the then-unbounded/unverified share admission path. Existing local campaign results remain
useful honest-path prototype measurements, but they are not evidence against
these adversarial findings.

The BLOC-owned critical transport and key-distribution boundaries are now
patched. Production consumers load a versioned, hash-checked public CRS instead
of a shared setup seed; public cluster configuration is separate from one
operator-local BTE share/libp2p key file; and inbound envelope/share identities
must match the authenticated configured libp2p peer. PIR-002 and PIR-004 are
remediated for the active prototype workflows. PIR-001 is only partially
remediated because the inherited diagonal CRS elements remain intentionally in
scope; no secure-CRS claim is made.

The BLOC-owned resource-admission boundary is also bounded. Shared v2 cluster
configuration caps encoded proposals, inbound/outbound libp2p envelopes, and
per-sub-batch recovery attempts. Share admission is membership/index checked,
retains at most one batch identity and one candidate per operator/sub-batch,
and contracts from an `N*BMax` pre-plan bound to `N*alpha` after planning.
These controls remediate the active prototype resource-exhaustion portions of
PIR-008 and PIR-009; public share verifiability remains deferred.

Campaign tooling is now maintained as one Bash 3.2-compatible interface for
macOS and Linux. All eight active local/EC2 runners support side-effect-free
`--validate-only`, EC2 image paths enforce `linux/amd64`, and structured
artifact work is shared through a Python-standard-library helper. The complete
ACS safety campaign and one current-code Merge/Plan baseline phase have passed
through the Bash runners on macOS. The three-region runner also completed its
real `n=4` probe and accepted `n=4/n=7` campaign, then destroyed and
authenticated the absence of every scoped AWS resource.

The current implemented prototype does not yet include DKG-generated shares, public decryption-share verifiability, real DVT threshold signing, execution-client validation of decrypted transactions, Builder API compatibility, or PBS prefix enforcement. Builder/PBS integration remains deferred to the later integration milestones now that the M3 distributed evidence exists.

## Latest Milestone

- Milestone: `M3. Distributed Sidecar Metrics Collection`
- Status: `complete` for the accepted honest-path three-region latency scope
- Evidence posture: local `eval-local`/`eval-suite` runs are the clean protocol
  baseline for ACS/BTE behavior under controlled conditions. Docker Compose is
  a local deployment-mechanics rehearsal. The primary distributed thesis
  evidence target is one VM/EC2 instance per BLOC operator, driven by
  `eval-remote` from a separate controller instance.
- Success criteria:
  - `bloc-node` can run as a containerized sidecar from mounted cluster config and `NODE_ID`,
  - generated configs support local and container listen/advertise addresses
    with a clean v2 public-config/per-operator-secret boundary,
  - each sidecar exposes Prometheus-compatible `/metrics` using counters, gauges, seconds-based histograms, and bounded labels for slot phase, latency stages, message/byte volume, selected tx/gas, HTTP traffic, and result availability,
  - Docker Compose can rehearse a local 4-node sidecar cluster with Prometheus/Grafana or direct `/metrics` visibility,
  - a remote evaluator can drive already-running sidecars and write chart-compatible latency outputs,
  - EC2 inventory can be converted into sidecar cluster config and remote-evaluator config for one-sidecar-per-EC2 deployments,
  - a first 4-operator EC2 smoke can be launched, observed through Prometheus, driven by `eval-remote`, and destroyed after artifact collection,
  - VM/EC2-per-sidecar deployment can run repeated distributed metric-gathering campaigns.
  - a dedicated three-region path can plan and collect standalone `t3.small`
    evidence across fully meshed, privately peered `us-east-1`, `eu-west-1`,
    and `eu-central-1` VPCs.

## Immediate Next Actions

1. Archive the accepted three-region artifact root with checksums and keep the
   source SHA, image digest, raw CSV/JSON, report, and cleanup evidence together.
2. Decide whether the thesis needs one independent confirmation campaign and a
   current-build same-region control before making causal cross-region-overhead
   claims; the accepted standalone campaign already supports scoped p50/p95
   characterization.
3. Plan M4 coordination, cryptographic, and resource-overhead characterization
   without mixing it into the frozen M3 evidence.
4. Continue the mixed-root RBC, conflicting BBA AUX, delayed-message, and
   secure-CRS/DKG work without describing latency evidence as Byzantine-safety
   or production-confidentiality evidence.

## Current Blockers / Risks

- The inherited critical RBC gap remains outside the existing honest/reordered
  test corpus: reconstruction uses ECHO shards from all roots without
  recomputing the selected Merkle root. Do not treat current distributed
  results as Byzantine-safety evidence until that finding and its adversarial
  tests are addressed. The BLOC-owned libp2p sender-binding finding is fixed.
- The shared setup seed and all-secrets config are removed from active local,
  Compose, and EC2 workflows. The public artifact still includes diagonal
  elements marked insecure by the inherited implementation, and one trusted
  generator still creates all shares. These remain prototype shortcuts, not a
  secure BTE setup/DKG design. Kubernetes manifests are intentionally deferred
  and incompatible with the clean v2 config boundary until separately migrated.

- The Merge + Plan and resource-safety patches pass both Go suites, bounded
  decoder tests, local n4 BMax-128 batches 8/32/128, and the complete Bash ACS
  campaign on macOS. The campaign tooling also passes its portability suite on
  macOS Bash 3.2 and Linux Bash 5.2. A separately approved real EC2 pilot is
  still required before the migrated cloud runners produce new evidence.

- The optimized 2026-07-14 cross-AZ campaign remains invalid historical evidence: after 7 clean
  batch-128 measurements, three `n=4` operators decided 3 inclusion lists/96
  transactions while the fourth decided 4 lists/128 transactions. A clean
  restart probe reproduced the divergence on its first slot with a different
  operator as the outlier. The local ACS/BBA correction passed its full safety
  campaign, but the optimized image has not yet been revalidated on EC2. The
  `n=7` phase was not launched, and all AWS resources were destroyed and
  verified absent.
- The previous 315-run libp2p-only campaign remains invalid historical evidence: result timeouts were concentrated in 7/10-node scenarios.
- The disagreement was not caused by BTE or Merge + Plan. Its two safety defects were a project-added all-RBC ACS completion shortcut and imported BBA logic that counted AUX values before BV-broadcast admitted them locally. Both are corrected; EC2 confirmation remains pending.
- A corrected 30-run 7/10-node stress matrix passed; the complete 315-sample M1 campaign is still required before reporting final baseline figures.
- Local-host scheduling noise means Compose timing output is diagnostic only. Distributed thesis metrics should come from the VM/EC2-per-sidecar deployment, where each operator has an independent machine and network identity.
- Prometheus `/metrics` now uses native collectors and histogram-safe PromQL is required for Grafana p50/p95 panels; evaluator CSV/JSON remains the offline chart artifact format.
- Realistic transaction-source evidence now requires the mock-placeholder path: public mempool transactions are target payloads, not native BLOC placeholders, so they must be encrypted once by a mock external submitter before sidecars include them.
- Resource limits default to 8 MiB proposals, 16 MiB envelopes, and 256
  cumulative recovery attempts per sub-batch. Oversized inputs and conflicting
  or out-of-scope shares fail closed and emit bounded-label rejection metrics.
  Terminal failed-slot publication and mempool HTTP timeouts remain separate
  operational follow-ups.
- Builder API compatibility, SSV signing enforcement, and PBS-specific validation are intentionally out of scope for this milestone.
- The selected three-region `n=7` placement needs 8 Standard On-Demand vCPUs
  in `us-east-1` and 4 in each EU region. The currently verified limits are
  16/5/5 respectively, so no quota increase is required. Campaign authorization
  accepts that inter-region transfer and T3 Unlimited surplus credits may be
  billable; mandatory authenticated teardown is the operational constraint.

## Last Known Good State

- Date: `2026-07-18`
- Meaning: source `8de4af179465f9cd77920eacdcca163ca5cef01d`
  completed the three-region `t3.small` campaign in `us-east-1`, `eu-west-1`,
  and `eu-central-1`. The `n=4/n=7`, batch `8/32/128` matrix retained 180/180
  successful consistent measured slots and 990/990 finalized measured node
  rows under one image digest. Pre/post Prometheus and five-attempt ordered-pair
  health gates passed, every sampled operator remained running with zero
  restarts/OOMs, Terraform destroyed 40 then 43 resources, and authenticated
  cleanup found no remaining scoped AWS resources.
- Data-realism addendum: `mempool-il` now has a corpus-backed `replay-placeholder` mode that validates real signed Ethereum target transactions, encrypts them once using BLOC public cluster material, and exposes mock placeholder candidates through the existing inclusion-list API. `bloc-node` can consume these encrypted payloads via the mempool provider without changing synthetic evaluator defaults.
- Baseline commands:
  - `bash bloc-node/scripts/run-acs-safety-campaign.sh`
  - `cd bloc-node && go test ./...`
  - `cd sbc/hbbft && go test ./...`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 3 --out-dir results/acs-bba-self-vote-matrix`
  - `cd bloc-node && go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 7,10 --batch-sizes 8,32,128 --warmups 0 --repetitions 5 --out-dir results/acs-all-rbc-stress`
- Evidence location:
  - `results/ec2/m3-three-region-synthetic-accepted-20260718-1/` (ignored accepted standalone three-region evidence: raw per-slot/per-node JSON, merged CSVs, manifests, placement, network/resource/Prometheus checks, analysis tables/charts/report, one image digest, and authenticated empty teardown for both phases)
  - `results/local/acs-common-subset-safety/acs-common-subset-20260715/` (ignored local safety campaign: repeated 1,000-seed reordered-delivery tests and Linux race validation passed; persistent n4/batch-128 gate passed 100/100 measured slots; n4/n7 batches 8/32/128 matrix passed 180/180 measured slots; bloc-node/BTE identity suites and focused Merge + Plan benchmarks passed; no AWS resources were allocated)
  - `results/ec2/m3-cross-az-synthetic-optimized-20260714-v2/` (ignored invalid optimized cross-AZ attempt, clean reproduction probe, operator logs, run report, and authenticated empty cleanup verification; batches 8/32 passed 30/30 but batch 128 exposed divergent ACS decisions, so no thesis-grade before/after campaign was accepted)
  - `results/ec2/merge-plan-attribution-free-20260714/` (ignored accepted Compute Flex `n=4/n=7` measurements, analysis tables/charts/report, and separately labeled invalid T3 diagnostic artifacts)
  - `results/local/merge-plan-optimization/merge-plan-opt-20260713/` (ignored local baseline/optimized benchmarks, profiles, evaluator outputs, charts, comparison CSVs, and report; 60/60 measured runs succeeded and were consistent in each phase)
  - `results/ec2/m3-cross-az-synthetic-20260706t122922z/` and `results/charts/m3-cross-az-synthetic-20260706t122922z/` (ignored local artifact collection and generated charts from the M3 cross-AZ synthetic campaign: `n=4` and `n=7` `t3.small` operators plus one `t3.small` controller per phase in `us-east-1` across `us-east-1a/b/c`; batches 8/32/128; 5 warmups and 30 measured repetitions per batch; 180/180 measured runs had `success=true` and `consistent=true`; Prometheus saw 4/4 and 7/7 targets up; Terraform destroy completed for both phases; cleanup verification and follow-up AWS checks found no tagged EC2 instances, volumes, VPC, ECR repository, temporary key pair, IAM role, or instance profile)
  - `results/ec2/m3-same-az-synthetic-20260706t105535z/` (ignored local artifact collection from the M3 same-AZ synthetic campaign: `n=4` and `n=7` phases completed cleanly with 180/180 measured runs successful and consistent; the `n=10` phase was not collected because AWS rejected the `t3.small` plan under the current 16-vCPU account quota; cleanup checks found no leftover resources)
  - `results/ec2/bloc-ec2-a1-pilot-same-az-n4-20260705-192544/` and `results/charts/bloc-ec2-a1-pilot-same-az-n4-20260705-192544/` (ignored local artifact collection and generated charts from the automated Windows A1 pilot: 4 `t3.small` operators plus 1 `t3.small` controller in `us-east-1a`; batches 8/32/128; 1 warmup and 3 measured repetitions per batch; all measured runs `success=true` and `consistent=true`; Prometheus saw 4/4 targets up; controller-to-operator HTTP `/healthz` timing succeeded before and after; Terraform destroy completed; follow-up AWS checks found no tagged EC2 instances, volumes, VPC, ECR repository, temporary key pair, IAM role, or instance profile)
  - `deploy/ec2/artifacts/ec2-smoke-20260705-1149/` (ignored local artifact collection from 4 `t3.micro` operators plus 1 `t3.small` controller in `us-east-1`; sidecars healthy, Prometheus saw all 4 targets, `eval-remote` batch-8 smoke succeeded 1/1, node outputs consistent, Terraform destroy completed)
  - `bloc-node/results/m1-local/libp2p-baseline/` (315 runs, invalid diagnostic dataset)
  - `bloc-node/results/m1-local/baseline-meeting-20260625/` (invalid diagnostic dataset; exposed remaining 7/10 liveness stalls)
  - `bloc-node/results/acs-bba-self-vote-matrix/` (27/27 successful across 4/7/10 nodes and 8/32/128 batches)
  - `bloc-node/results/acs-all-rbc-stress/` (30/30 successful across 7/10 nodes and 8/32/128 batches)

## Current M3 Outcome

- The full-mesh three-region target is complete: standalone `n=4/n=7`, batch
  `8/32/128`, 30-sample protocol latency on `t3.small`, with `node_id % 3`
  placement across US/Ireland/Frankfurt and the controller in the US.
- The dataset is accepted honest-path prototype evidence for p50/p95, stage,
  pairwise-network, and critical-node-region reporting. Older same-AZ/cross-AZ
  and two-region data remains historical context because it predates this
  source and image.

## Deferred Later Milestones

- `M4. Coordination, Cryptographic, and Resource Overhead Characterization`
- `M5. Fault and Adversarial Robustness Validation`
- `M6. Builder API Boundary`
- `M7. SSV/DVT Signing Integration`
- `Deferred Target: PBS Prefix Enforcement`
