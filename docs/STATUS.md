# Status

- Last reviewed: `2026-07-19`
- Active milestone: `not selected`
- Latest completed milestone: `M3. Distributed Sidecar Metrics Collection`
- Last known good source: `8de4af179465f9cd77920eacdcca163ca5cef01d`

## Current Prototype State

The implemented prototype supports the complete local BLOC path: operators
construct encrypted inclusion-list proposals, reach slot-scoped ACS agreement,
deterministically merge and plan the accepted ciphertext set, exchange bounded
threshold-decryption shares, and materialize the same ordered plaintext
transactions.

The active integration uses libp2p-authenticated operator messaging, scoped BTE
decoding, deterministic BEAT-MEV `Opt-2` sub-batching, bounded proposal and
envelope sizes, bounded share retention/recovery, split public configuration and
operator-local secrets, Prometheus metrics, local evaluators, and VM/EC2 remote
evaluation. The source-led protocol review and current module boundaries are
documented in [ARCHITECTURE.md](ARCHITECTURE.md), the module deep dives, and the
[PIR evidence register](archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).

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

- **RBC reconstruction:** inherited reconstruction can combine ECHO shards from
  different roots without recomputing the selected Merkle root. Current results
  must not be presented as Byzantine-safety evidence until this is corrected and
  covered by adversarial tests.
- **BBA liveness and equivocation:** the common coin is deterministic rather than
  cryptographic, and conflicting/future-message behavior still needs hardening.
- **Setup and key custody:** the CRS retains inherited insecure diagonal
  elements, and one trusted generator still creates all operator shares. A
  secure setup ceremony and DKG remain unimplemented.
- **Share verification:** admission and recovery work are bounded, but shares do
  not carry public correctness proofs. Invalid candidates are discovered through
  bounded reconstruction attempts.
- **Terminal failure publication:** failed slots do not yet expose a durable
  terminal result that controllers can distinguish from a pending timeout.
- **Mempool provider timeout:** `mempool-http` uses the default HTTP client without
  an explicit request timeout.
- **Evidence completeness:** the accepted M3 campaign characterizes one
  three-region topology. An independent confirmation and matched current-build
  same-region control are still required before stronger repeatability or causal
  topology-overhead claims. The full corrected 315-sample M1 baseline also
  remains outstanding.

## Immediate Next Actions

1. Archive the accepted three-region artifact root with checksums while keeping
   its source SHA, image digest, raw CSV/JSON, report, and cleanup evidence
   together.
2. Decide whether to collect an independent M3 confirmation and a matched
   same-region control before making stronger repeatability or causal claims.
3. Select the next active milestone in a separately authorized planning task.
4. Continue the RBC, BBA/common-coin, secure-CRS/DKG, public-share-verification,
   terminal-failure, and mempool-timeout work without overstating existing
   latency evidence.

## Last Known Good Baseline

- Date: `2026-07-18`
- Source: `8de4af179465f9cd77920eacdcca163ca5cef01d`
- Local safety evidence:
  `results/local/acs-common-subset-safety/acs-common-subset-20260715/`
- Accepted distributed evidence:
  `results/ec2/m3-three-region-synthetic-accepted-20260718-1/`
- Canonical validation commands and evidence semantics:
  [VALIDATION.md](VALIDATION.md)
- Reproduction and deployment procedures: [WORKFLOWS.md](WORKFLOWS.md) and the
  module-local deployment runbooks.

## Deferred Work

- `M4. Coordination, Cryptographic, and Resource Overhead Characterization`
- `M5. Fault and Adversarial Robustness Validation`
- `M6. Builder API Boundary`
- `M7. SSV/DVT Signing Integration`
- `Deferred Target: PBS Prefix Enforcement`

These roadmap items remain deferred until a separate task explicitly selects an
active milestone. See [ROADMAP.md](ROADMAP.md) for their objectives and done
criteria.
