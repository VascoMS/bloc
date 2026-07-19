# Roadmap

This roadmap is now ordered around the thesis path from local protocol baseline evidence to VM/EC2-per-sidecar distributed evidence, then to later Builder API and DVT integration work.

Milestone state is maintained in [`STATUS.md`](STATUS.md). M3 is the latest
completed milestone, and no next active milestone has been selected.

Builder API compatibility, production mev-boost behavior, real proposer signing, and PBS prefix enforcement are explicitly deferred until the sidecar can first be deployed, observed, and evaluated across independently hosted operator machines.

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
- Objective: make `bloc-node` deployable as a DVT-adjacent sidecar cluster with first-class visibility.
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

## M4. Coordination, Cryptographic, and Resource Overhead Characterization

- RQs advanced: `RQ2`, `RQ4`
- Objective: isolate the cost of ACS coordination, BTE cryptography, and sidecar resource usage.
- Deliverables:
  - ACS/share message and byte counts,
  - BTE share-generation, combine, and optimization sweeps,
  - CPU/memory/bandwidth characterization for sidecar deployments.
- Done criteria:
  - overhead evidence is separated from orchestration/setup overhead,
  - results can support comparison tables in the thesis.

## M5. Fault and Adversarial Robustness Validation

- RQs advanced: `RQ3`
- Objective: validate safety and liveness behavior under omission, withholding, malformed data, and near-threshold faults.
- Deliverables:
  - documented fault scenarios,
  - targeted correctness tests,
  - remote or local evaluator runs showing expected success/failure behavior.
- Done criteria:
  - correct operators agree on the same accepted encrypted set under tested faults,
  - liveness failure conditions are explicit.

## M6. Builder API Boundary

- RQs advanced: future `RQ1`, `RQ4`
- Objective: expose BLOC-agreed transaction sets through a Builder-facing development boundary.
- Deliverables:
  - stable BLOC candidate artifact,
  - Builder-API-shaped development adapter,
  - clear labeling that the adapter is not yet production Ethereum block building.
- Done criteria:
  - the adapter serves the real BLOC-agreed ordered transaction set,
  - real execution payload construction remains clearly separated unless implemented later.

## M7. SSV/DVT Signing Integration

- RQs advanced: future `RQ1`, `RQ3`, `RQ4`
- Objective: integrate the BLOC sidecar with a real DVT workflow after deployment and Builder-boundary evidence exists.
- Deliverables:
  - pre-sign verification design,
  - signing-boundary tests,
  - explicit SSV integration notes or adapter implementation.
- Done criteria:
  - integration claims are backed by a real signing or pre-sign verification path, not by architectural intent alone.

## Deferred Target: PBS Prefix Enforcement

- Objective: extend the architecture so the materialized plaintext prefix is enforced through PBS builder constraints or proofs.
- Deliverables:
  - prefix-enforcement mechanism design,
  - builder-side constraint or proof validation path,
  - robustness/economic validation for prefix-preserving bids.
- Done criteria:
  - the feature is implemented and validated as a real extension, not implied by the current prototype.
