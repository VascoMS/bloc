# Roadmap

This roadmap is organized as a linear sequence of milestones tagged to the thesis research questions. It distinguishes between:

- the current implemented prototype baseline,
- the near-term evaluation roadmap,
- the deferred target architecture.

PBS prefix enforcement remains a deferred target architecture item and is not part of the current active milestone sequence.

## M0. Current Prototype Baseline

- RQs advanced: `RQ1`, `RQ2`, `RQ3`
- Objective: define exactly what the current repo already proves today.
- Why it matters: every later milestone depends on an honest baseline for implemented capabilities versus deferred target capabilities.
- Deliverables:
  - documented local prototype path from encrypted inclusion lists to deterministic materialized plaintexts,
  - documented validation commands for the current local baseline,
  - explicit statement that PBS and full DVT signing are not yet in scope.
- Done criteria:
  - the current implemented prototype is summarized consistently in `docs/STATUS.md`, `docs/ARCHITECTURE.md`, and `docs/VALIDATION.md`,
  - the baseline validation commands are documented and reproducible,
  - deferred capabilities are clearly labeled as deferred.
- Validation evidence expected:
  - module tests,
  - `bloc-node` demo smoke flow,
  - current evaluator command paths described in `docs/VALIDATION.md`.
- Dependencies: none.

## M1. Slot Timing and Baseline Latency Evidence

- RQs advanced: `RQ1`
- Objective: produce reproducible local evidence that the current latency-critical BLOC path can be measured stage by stage within the current prototype boundary.
- Why it matters: slot timing is the first thesis-critical question and anchors the rest of the evaluation sequence.
- Deliverables:
  - end-to-end slot latency measurements,
  - per-stage latency measurements for ACS, deterministic merge/planning, share generation, and commit-to-plaintext,
  - output structure ready for later p50/p95/p99 aggregation.
- Done criteria:
  - a documented local experiment produces reproducible timing output,
  - per-stage timing coverage is explicit,
  - the baseline can later be aggregated across repeated runs.
- Validation evidence expected:
  - `bloc-node` `eval-local` runs,
  - demo smoke path where relevant,
  - documented timing fields referenced from `docs/VALIDATION.md`.
- Dependencies: `M0`.

## M2. Coordination and Cryptographic Overhead Characterization

- RQs advanced: `RQ2`
- Objective: isolate the cost of ACS coordination and BTE cryptography in the current prototype.
- Why it matters: the project needs evidence about the cost of multi-proposer coordination and threshold decryption relative to a simpler path.
- Deliverables:
  - ACS and dissemination message/byte counts,
  - deterministic merge/planning cost,
  - BTE share-generation, aggregation, and reconstruction cost,
  - user-side encryption or submission overhead where feasible.
- Done criteria:
  - coordination and cryptographic overhead are broken out instead of reported only as one end-to-end number,
  - at least one repeatable benchmark path exists for each major cost category,
  - output format is suitable for later comparison tables.
- Validation evidence expected:
  - `bloc-node` evaluator metrics,
  - BTE full-path benchmarks,
  - any added timing or resource instrumentation documented in `docs/VALIDATION.md`.
- Dependencies: `M1`.

## M3. Fault and Adversarial Robustness Validation

- RQs advanced: `RQ3`
- Objective: validate safety and liveness properties under omission, withholding, malformed data, and near-threshold faulty behavior.
- Why it matters: the protocol claims Byzantine resilience, so the thesis needs explicit evidence of correct behavior under faulty operators.
- Deliverables:
  - formalized omission and withholding scenarios,
  - malformed-share or invalid-input scenarios,
  - explicit ACS-property checks in the BLOC setting,
  - documented failure and rejection behavior.
- Done criteria:
  - all correct operators still agree on the same accepted encrypted set under the tested fault scenarios,
  - liveness failure conditions are identified rather than left implicit,
  - robustness evidence is mapped to metrics such as agreement time, decrypt time, and failure rate.
- Validation evidence expected:
  - `bloc-node` fault-injection runs,
  - targeted tests for malformed share handling and wrong-batch rejection,
  - documented result expectations in `docs/VALIDATION.md`.
- Dependencies: `M1`, `M2`.

## M4. Economic and Resource Cost Characterization

- RQs advanced: `RQ2`, `RQ4`
- Objective: estimate the user-side and operator-side cost of the current prototype path.
- Why it matters: the protocol must be not only correct but also economically and operationally credible.
- Deliverables:
  - ciphertext and proof overhead estimates,
  - operator CPU, memory, and bandwidth cost characterization per slot,
  - cost scaling notes by batch size and cluster size,
  - a clear statement of what cannot yet be economically validated because PBS and full proposer signing remain deferred.
- Done criteria:
  - user-side and operator-side cost framing is documented,
  - cost outputs can be tied back to measurable prototype artifacts,
  - remaining blind spots are explicit instead of implied away.
- Validation evidence expected:
  - evaluator byte and message counters,
  - BTE benchmark outputs,
  - any added resource measurement methodology captured in `docs/VALIDATION.md`.
- Dependencies: `M2`.

## M5. Distributed Evaluation and Dissertation-Ready Evidence

- RQs advanced: `RQ1`, `RQ2`, `RQ3`, `RQ4`
- Objective: turn the local evidence into a more dissertation-ready evaluation package with repeated runs, distributions, and reproducible outputs.
- Why it matters: thesis claims need more than one-off local runs.
- Deliverables:
  - local plus geo-distributed or latency-emulated runs,
  - metrics tables and dissertation-ready plots,
  - reproducible output structure for repeated experiments,
  - explicit p50/p95/p99 reporting where relevant.
- Done criteria:
  - experiments are repeatable,
  - output organization supports plotting and comparison,
  - thesis-ready evidence spans latency, overhead, robustness, and cost dimensions within the active scope.
- Validation evidence expected:
  - repeated `eval-local` runs or distributed equivalents,
  - aggregated metrics outputs,
  - plotting or report-generation artifacts tracked outside canonical docs.
- Dependencies: `M1`, `M2`, `M3`, `M4`.

## Deferred Target: PBS Prefix Enforcement

- RQs advanced: future `RQ1`, `RQ3`, `RQ4`
- Objective: extend the architecture so the materialized plaintext prefix is enforced through PBS builder constraints or proofs.
- Why it matters: this is the longer-term target architecture described by the broader BLOC thesis framing.
- Deliverables:
  - prefix-enforcement mechanism design,
  - builder-side constraint or proof validation path,
  - robustness and economic validation for prefix-preserving bids.
- Done criteria:
  - the feature is designed and implemented as a real extension, not implied by the current prototype,
  - validation paths cover invalid bids, missing prefixes, reordered prefixes, and proof failure modes where applicable.
- Validation evidence expected:
  - future PBS-specific integration tests and evaluation runs.
- Dependencies: not part of the active milestone ladder.
