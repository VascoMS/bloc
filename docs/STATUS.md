# Status

- Last reviewed: `2026-08-02`
- Active milestone: `M5. Performance, Scaling, And Resource Evidence`
- Latest completed milestone: `M4. Evaluation Readiness And Prototype Hardening`
- Last known good source: `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`

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
operator-local secrets, durable bounded terminal slot failures, node-owned
bounded mempool HTTP requests, non-contaminating host resource sampling and
validation, p99-capable Type-7/order-statistic reporting, balanced seeded
long-campaign scheduling, explicit terminal-attempt accounting, a deterministic
500-transaction balanced client corpus with one plaintext/encrypted measurement
per target, a separate 100-transaction representative full-protocol workload,
Prometheus metrics, local evaluators, and VM/EC2 remote evaluation.
The source-led protocol review and current module boundaries are documented in
[ARCHITECTURE.md](ARCHITECTURE.md), the module deep dives, and the [PIR evidence
register](archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).

This remains a research prototype. It does not provide production DKG or key
custody, public decryption-share verification, a cryptographic common coin,
execution-client validation, Builder API compatibility, proposer signing,
slashing protection, or PBS prefix enforcement.

## Latest Completed Milestone

M4 is complete. Its now-superseded evaluation candidate was source
`2bc8efc9269798a7f7ab58021f8b9bda1012ae5d` and local immutable image
`bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`.
That historical image contract was `linux/amd64`, user `10001:10001`, entrypoint
`["bloc-node"]`, and default command
`["run","--config","/config/cluster.json"]`.

Validation logs are retained under the ignored root
`results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/`.
All four Go module suites, chart tests, normal and targeted race suites, both
BTE decoder fuzz targets, Terraform validation, runner portability, local
evaluation, full-path BTE benchmarks, and standard/mock-placeholder Compose
rehearsals passed. The exact-source ACS campaign
`results/local/acs-common-subset-safety/rc-2bc8efc/` passed 1,000 delivery
schedules, the 100-slot sustained gate, the 180-slot compatibility matrix,
identity checks, and Merge/Plan attribution.

Issue #8's accepted validation-only distributed preflight is retained at
`results/local/distributed-preflight-2bc8efc/`. It exercised all primary and
extension configurations from the frozen source with complete, consistent
artifacts and chart-loader compatibility. It collected no resource evidence and
supports no local performance claim.

The accepted M3 three-region evidence remains at
`results/ec2/m3-three-region-synthetic-accepted-20260718-1/`. Issue #13's
balanced 500-target client-overhead artifact remains separate from the
`28/50/12/8/2` 100-target full-protocol workload. Their scoped claims and the
release-candidate configuration contract are defined in
[VALIDATION.md](VALIDATION.md).

## Open Blockers And Risks

- **Replacement campaign freeze:** the epochless hybrid-ciphertext wire,
  deterministic 512-target master corpus, immutable cluster-specific encrypted
  prefixes, and exact-count provider path are implemented. Network-independent
  n4/n7 identity generation, primary corpus binding, frozen-bundle verification,
  and topology materialization are now implemented locally on issue #15's task
  branch. The replacement source, two private-ECR image digests, real n4/n7
  bundles, and complete replacement validation evidence are not frozen yet.
- **Live-run plumbing:** live AWS execution remains blocked until the shared
  final-campaign runner, pull-only ECR IAM, encrypted-corpus and secret staging,
  health gates, separate latency/resource execution, failure retention,
  artifact recovery, and authenticated cleanup are implemented and pass the
  side-effect-free runner contract without a synthetic fallback.
- **Evidence completeness:** the final evidence contract requires p99-capable
  same-region and three-region VM performance campaigns, complete per-operator
  VM resource measurements, RQ3 fault campaigns, and operator cost synthesis.
  The accepted M3 campaign remains valid historical p50/p95 evidence but is not
  the final release-candidate campaign. Local issue #8 output is
  validation-only preflight evidence, not local performance or resource
  evidence.

## Bounded Prototype Limitations

- BBA uses a deterministic rather than cryptographic common coin; RQ3 liveness
  claims will be restricted to the exercised schedules and fault model.
- The CRS retains inherited insecure diagonal elements and a trusted generator
  creates all shares; secure setup and DKG are future work.
- Shares have no public correctness proof. Admission and subset recovery remain
  bounded, and invalid shares are detected through reconstruction failure.
- The current prototype assigns BTE puncture indexes from local/corpus
  position. This coordinated model is retained for the thesis metrics;
  paper-aligned independent index sampling and collision-distribution evidence
  are deferred to issue #22.
- Builder API integration, DVT/SSV signing, execution payload construction, and
  block publication are future integration work and outside the measured path.

## Immediate Next Actions

1. Complete and locally validate the shared final-campaign lifecycle,
   pull-only ECR infrastructure contract, failure retention, artifact recovery,
   and authenticated cleanup for both matched topologies.
2. Freeze one clean replacement source, two private-ECR image digests, and the
   n4/n7 campaign bundles; then rerun the affected local distributed-campaign
   preflight against only those identities.
3. After the replacement freeze and affected preflight pass, run issue `#15` as
   the first M5 thesis-performance campaign.
4. Run issue `#16` only after issue #15 is accepted, using matched manifests
   and configurations.
5. Do not combine measurements from different source, image, corpus,
   configuration, or schema revisions into one final campaign.
6. Track granular work in the [BLOC Thesis Prototype GitHub
   Project](https://github.com/users/VascoMS/projects/1) while keeping this file
   limited to milestone state, major blockers, accepted evidence, and next
   actions.

## Last Known Good Baseline

- Date: `2026-07-26`
- Source: `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`
- Image: `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`
- Release-candidate validation:
  `results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/`
- Local safety evidence:
  `results/local/acs-common-subset-safety/rc-2bc8efc/`
- Accepted distributed-campaign preflight:
  `results/local/distributed-preflight-2bc8efc/`
- Linux RBC/ACS/BBA race evidence:
  `results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/logs/hbbft-linux-amd64-race.log`
- Accepted distributed evidence:
  `results/ec2/m3-three-region-synthetic-accepted-20260718-1/`
- Canonical validation commands and evidence semantics:
  [VALIDATION.md](VALIDATION.md)
- Reproduction and deployment procedures: [WORKFLOWS.md](WORKFLOWS.md) and the
  module-local deployment runbooks.

## Planned And Deferred Work

- Active: `M5. Performance, Scaling, And Resource Evidence`
- Planned after M5: `M6. Fault And Adversarial Robustness Evidence`
- Planned after M6: `M7. Cost Analysis And Thesis Evidence Synthesis`
- Deferred: Builder API boundary, SSV/DVT signing integration, secure CRS/DKG,
  public share proofs, cryptographic common coin, and PBS prefix enforcement.

See [ROADMAP.md](ROADMAP.md) for milestone objectives and done criteria.
