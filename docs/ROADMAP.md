# Roadmap

This roadmap is ordered around answering the thesis research questions for BLOC
as an independently operating distributed sidecar. The evaluated path ends when
correct operators produce the same deterministic plaintext transaction set.
Builder API and DVT integration are future integration strategies rather than
requirements of the current evaluation.

Milestone state is maintained only in [`STATUS.md`](STATUS.md); this roadmap
defines objectives and done criteria without selecting the active milestone.
Granular execution is tracked in the [BLOC Thesis Prototype GitHub
Project](https://github.com/users/VascoMS/projects/1).

Builder API compatibility, production mev-boost behavior, proposer signing, and
PBS prefix enforcement are explicitly outside the two-week evidence programme.

## M0. Current Prototype Baseline

- RQs advanced: `RQ1`, `RQ2`, `RQ3`
- Objective: define exactly what the repo proves today.
- Deliverables:
  - documented local path from encrypted inclusion lists to deterministic materialized plaintexts,
  - documented validation commands for the current local baseline,
  - explicit statements for deferred DKG, share verification, Builder API, PBS, and DVT signing capabilities.
- Done criteria:
  - baseline behavior is summarized consistently in `docs/STATUS.md`, `docs/ARCHITECTURE.md`, and `docs/VALIDATION.md`,
  - reproducible local validation commands exist.

## M1. Slot Timing and Baseline Latency Evidence

- RQs advanced: `RQ1`
- Objective: produce reproducible local timing evidence for the current latency-critical BLOC path.
- Deliverables:
  - end-to-end slot latency measurements,
  - per-stage timing for ACS, merge/planning, share generation, threshold wait, combine, and materialization,
  - chart-compatible p50/p95 output structure.
- Done criteria:
  - the corrected local M1 campaign completes without failed or inconsistent measured runs,
  - generated charts and scenario summaries are suitable for professor/thesis review.

## M2. Distributed Deployment-Ready BLOC Sidecar

- RQs advanced: `RQ1`, `RQ2`
- Objective: make `bloc-node` deployable as an independently operating
  distributed sidecar cluster with first-class visibility.
- Deliverables:
  - multi-stage `bloc-node` Docker image,
  - local/container listen-vs-advertise config support,
  - Docker Compose 4-node cluster with Prometheus and Grafana,
  - Prometheus-native `/metrics` with counters, gauges, seconds-based histograms, and bounded labels,
  - remote evaluator for already-running sidecar clusters.
- Done criteria:
  - Compose can run a 4-node BLOC sidecar cluster,
  - Prometheus can scrape every sidecar,
  - Grafana can display latency, phase, message/byte, and selected tx/gas panels,
  - `eval-remote` writes manifest/CSV outputs compatible with the existing chart module.

## M3. Distributed Sidecar Metrics Collection

- Status: complete for the accepted honest-path three-region latency campaign.
- RQs advanced: `RQ1`, `RQ2`, `RQ4`
- Objective: collect repeated distributed latency/performance evidence from the deployment-ready sidecar using one VM/EC2 instance per operator, with both synthetic transaction submissions and mock-placeholder mempool inputs where useful.
- Deliverables:
  - clean local `eval-suite` baseline for protocol comparison,
  - Compose smoke evidence as local deployment rehearsal,
  - corpus-backed mock placeholder mempool evidence,
  - VM/EC2-per-sidecar deployment evidence,
  - remote-evaluator campaigns over 4/7/10-node VM clusters where infrastructure allows,
  - charts/tables for p50/p95 latency, per-stage timing, message volume, and resource observations.
- Done criteria:
  - sidecar VM runs are reproducible from documented deployment commands,
  - distributed results are clearly separated from local M1 results,
  - thesis figures identify environment, node count, threshold, batch size, endpoint mode, and region/zone labels when available,

## Out-of-Scope Deployment Artifacts

The repository still contains Kubernetes manifests from an earlier deployment
rehearsal under `deploy/k8s/`. They are retained as optional historical
artifacts in case they are useful later, but Kubernetes is not part of the
current roadmap, validation path, or thesis metric collection plan.

## M4. Evaluation Readiness And Prototype Hardening

- Status: complete.
- RQs advanced: prerequisite for `RQ1`–`RQ4`.
- Objective: freeze an admissible release candidate whose known defects cannot
  invalidate correctness or experimental interpretation.
- Deliverables:
  - mixed-root RBC reconstruction rejection and adversarial regression tests;
  - durable terminal slot failures distinguishable from pending results;
  - a bounded mempool-provider HTTP timeout;
  - p99/confidence-interval reporting, balanced long-campaign scheduling, and
    non-contaminating operator-resource collection support;
  - the deterministic transaction corpus and client-overhead benchmark needed
    by the final evidence campaigns;
  - final RQ definitions, experiment matrix, artifact contract, and GitHub task
    hierarchy;
  - a complete release-candidate validation gate and frozen source/image ID.
- Done criteria:
  - all four Go module suites, targeted race/fuzz checks, campaign-runner tests,
    and the local ACS safety gate pass;
  - measurement-threatening blockers are resolved or explicitly shown not to
    affect the selected campaigns;
  - deterministic common coin, trusted setup/DKG, and absent public share
    proofs are documented as bounded prototype limitations rather than claimed
    production properties;
  - the frozen source SHA and image digest are recorded in `STATUS.md`.

## M5. Performance, Scaling, And Resource Evidence

- Status: active.
- RQs advanced: `RQ1`, `RQ2`, `RQ4`.
- Objective: collect the final honest-path timing, coordination, cryptographic,
  resource, and scaling evidence from the frozen release candidate.
- Primary matrix:
  - `n=4,t=3` and `n=7,t=5`;
  - batches `8/32/128`;
  - matched same-region VM and three-region VM environments;
  - 10 warmups and 1,000 measured observations per scenario for p99.
- Scale extension:
  - `n=10,t=7` at batches `8/32/128`;
  - batch `512` at `n=4/7/10`;
  - a separate 30-observation pilot, followed by 1,000 measurements when the
    scenario remains viable or 100 boundary observations when it clearly
    exceeds the slot envelope.
- Deliverables:
  - validation-only issue #8 local preflight: `n=4/7`, batches `8/32/128`,
    with 1 warmup and 1 measured observation per cell; `n=10`, batches
    `8/32/128`, plus batch `512` at `n=4/7/10`, with 1 warmup and 3 measured
    observations per unique extension cell;
  - p50/p95/p99, maximum, deadline completion, confidence intervals, and stage
    attribution from VM campaigns;
  - VM CPU, peak memory, allocations, messages, bytes, and throughput;
  - accepted results from the deterministic transaction corpus and client
    encryption/expansion benchmark;
  - share generation, reconstruction, and BTE optimization benchmarks;
  - exact same-region/three-region VM artifact bundles and the separate
    validation-only local preflight artifact.
- Done criteria:
  - planned observations are retained without unexplained filtering;
  - failures and timeouts remain visible outside successful-run quantiles;
  - matched VM campaigns use the same source, image, instance class, corpus,
    configuration, and balanced execution blocks;
  - the local preflight is retained only as configuration, correctness, and
    artifact-contract validation, with no local performance or resource claim;
  - accepted artifacts pass the evidence contract in `VALIDATION.md`.

## M6. Fault And Adversarial Robustness Evidence

- RQs advanced: `RQ3`.
- Objective: characterize agreement, liveness, rejection, and bounded failure
  under the explicit `n=3f+1`, `t=2f+1` prototype fault model.
- Deliverables:
  - deterministic mixed-root, equivocation, malformed-input, wrong-scope,
    commitment-mismatch, and conflicting-share tests;
  - 30-observation operational campaigns for proposal omission, target
    censorship, share withholding/corruption, and bounded delay at `n=4/7`;
  - selected `n=10,f=3` threshold-withholding confirmation;
  - a matrix separating demonstrated behavior, tested invariants, inherited
    cryptographic assumptions, and unresolved limitations.
- Done criteria:
  - no tested scenario produces divergent successful outputs;
  - within-bound liveness scenarios complete consistently;
  - threshold-breaking and malformed selected-input scenarios publish bounded,
    durable failure rather than hanging or accepting inconsistent output;
  - claims are restricted to exercised schedules because the prototype common
    coin is not cryptographic.

## M7. Cost Analysis And Thesis Evidence Synthesis

- RQs advanced: `RQ1`–`RQ4`.
- Objective: translate accepted technical evidence into thesis-ready answers,
  cost models, figures, tables, and limitations.
- Deliverables:
  - user byte-expansion and clearly qualified carrier-gas estimates derived
    from the accepted M5 corpus and client benchmarks;
  - operator CPU/memory/network demand and dated dedicated-infrastructure cost
    estimates per slot and transaction;
  - an RQ-to-evidence matrix, fault outcome table, figures, confidence
    intervals, limitations, and a final evidence archive with checksums.
- Done criteria:
  - every RQ has a scoped answer or an explicit negative/inconclusive result;
  - raw measurements and financial assumptions are traceable;
  - no claims are made about paid on-chain placeholder gas, proposer rewards,
    lost MEV, Builder/PBS economics, or complete block-publication latency;
  - the final evidence bundle is reproducible and integrity-verifiable.

## Future Integration Target: Builder API Boundary

- Expose the agreed ordered transaction set through a Builder-facing adapter.
- Treat execution payload construction and real mev-boost behavior as new work,
  not implied by the sidecar evaluation.

## Future Integration Target: SSV/DVT Signing

- Integrate the Builder-facing boundary with a real DVT workflow only after the
  independent sidecar evaluation is complete.
- Require real pre-sign verification and signing evidence before making DVT
  integration claims.

## Deferred Target: PBS Prefix Enforcement

- Objective: extend the architecture so the materialized plaintext prefix is enforced through PBS builder constraints or proofs.
- Deliverables:
  - prefix-enforcement mechanism design,
  - builder-side constraint or proof validation path,
  - robustness/economic validation for prefix-preserving bids.
- Done criteria:
  - the feature is implemented and validated as a real extension, not implied by the current prototype.
