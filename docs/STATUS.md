# Status

- Last reviewed: `2026-07-23`
- Active milestone: `M4. Evaluation Readiness And Prototype Hardening`
- Latest completed milestone: `M3. Distributed Sidecar Metrics Collection`
- Last known good source: `f1580c8cb46a1099093f94b4b5b3a6fae14519c5`

## Current Prototype State

The implemented prototype supports the complete local BLOC path: operators
construct encrypted inclusion-list proposals, reach slot-scoped ACS agreement,
deterministically merge and plan the accepted ciphertext set, exchange bounded
threshold-decryption shares, and materialize the same ordered plaintext
transactions.

The active integration uses libp2p-authenticated operator messaging, scoped BTE
decoding, deterministic BEAT-MEV `Opt-2` sub-batching, bounded proposal and
envelope sizes, root-bound RBC reconstruction with post-decode commitment
verification, bounded share retention/recovery, split public configuration and
operator-local secrets, durable bounded terminal slot failures, Prometheus
metrics, local evaluators, and VM/EC2 remote evaluation. The source-led protocol
review and current module boundaries are documented in
[ARCHITECTURE.md](ARCHITECTURE.md), the module deep dives, and the [PIR evidence
register](archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).

This remains a research prototype. It does not provide production DKG or key
custody, public decryption-share verification, a cryptographic common coin,
execution-client validation, Builder API compatibility, proposer signing,
slashing protection, or PBS prefix enforcement.

## Latest Completed Milestone

M3 is complete for the accepted honest-path three-region latency scope. Source
`8de4af179465f9cd77920eacdcca163ca5cef01d` ran the canonical `n=4/n=7`, batch
`8/32/128` matrix on `t3.small` instances across `us-east-1`, `eu-west-1`, and
`eu-central-1`.

The campaign retained:

- `180/180` successful and cross-node-consistent measured slots;
- `990/990` finalized measured node rows;
- one image digest across both phases;
- complete placement, stage-additivity, Prometheus, pairwise-health, and
  restart/OOM checks; and
- authenticated empty cleanup after Terraform destroyed 40 then 43 resources.

Accepted evidence is stored under the ignored artifact root
`results/ec2/m3-three-region-synthetic-accepted-20260718-1/`. It supports scoped
p50/p95 honest-path latency, stage, pairwise-network, and critical-node-region
reporting. It is not Byzantine-safety, production-confidentiality, or causal
same-region-versus-cross-region evidence.

## Open Blockers And Risks

- **Mempool provider timeout:** `mempool-http` uses the default HTTP client without
  an explicit request timeout.
- **Evidence completeness:** the final evidence contract requires p99-capable
  local, same-region, and three-region campaigns, complete operator resource
  measurements, RQ3 fault campaigns, and user/operator cost analysis. The
  accepted M3 campaign remains valid historical p50/p95 evidence but is not the
  final release-candidate campaign.

## Bounded Prototype Limitations

- BBA uses a deterministic rather than cryptographic common coin; RQ3 liveness
  claims will be restricted to the exercised schedules and fault model.
- The CRS retains inherited insecure diagonal elements and a trusted generator
  creates all shares; secure setup and DKG are future work.
- Shares have no public correctness proof. Admission and subset recovery remain
  bounded, and invalid shares are detected through reconstruction failure.
- Builder API integration, DVT/SSV signing, execution payload construction, and
  block publication are future integration work and outside the measured path.

## Immediate Next Actions

1. Complete the remaining M4 mempool-timeout issue.
2. Add the p99, resource, scale-extension, and fault-evidence support required by
   the accepted research-question contract.
3. Freeze and validate one release-candidate source SHA and image digest before
   running the final local and VM campaigns.
4. Track granular work in the [BLOC Thesis Prototype GitHub
   Project](https://github.com/users/VascoMS/projects/1) while keeping this file
   limited to milestone state, major blockers, accepted evidence, and next
   actions.

## Last Known Good Baseline

- Date: `2026-07-23`
- Source: `f1580c8cb46a1099093f94b4b5b3a6fae14519c5`
- Terminal-failure validation: full bloc-node normal and race suites, including
  pending/success/failure/wrong-slot lifecycle, failure-before-start ordering,
  synchronous start failure, and both evaluator artifact formats.
- Local safety evidence:
  `results/local/acs-common-subset-safety/acs-safety-issue9-ebb69c5/`
- Linux RBC/ACS/BBA race evidence:
  `results/local/rbc-mixed-root/ebb69c5/`
- Accepted distributed evidence:
  `results/ec2/m3-three-region-synthetic-accepted-20260718-1/`
- Canonical validation commands and evidence semantics:
  [VALIDATION.md](VALIDATION.md)
- Reproduction and deployment procedures: [WORKFLOWS.md](WORKFLOWS.md) and the
  module-local deployment runbooks.

## Planned And Deferred Work

- Planned after M4: `M5. Performance, Scaling, And Resource Evidence`
- Planned after M5: `M6. Fault And Adversarial Robustness Evidence`
- Planned after M6: `M7. Cost Analysis And Thesis Evidence Synthesis`
- Deferred: Builder API boundary, SSV/DVT signing integration, secure CRS/DKG,
  public share proofs, cryptographic common coin, and PBS prefix enforcement.

See [ROADMAP.md](ROADMAP.md) for milestone objectives and done criteria.
