# Thesis Research-Question Evidence Programme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a frozen, admissible BLOC sidecar prototype and the timing, overhead, fault, and cost evidence needed to answer the four revised thesis research questions.

**Architecture:** The evaluated boundary runs from mempool inclusion-list collection through ACS, deterministic merge/planning, BTE threshold decryption, and common plaintext transaction-set materialization. M4 removes defects that can invalidate results, M5 collects honest-path and scaling evidence, M6 collects fault evidence, and M7 converts accepted artifacts into thesis answers. GitHub issues track task execution while canonical repository docs retain milestone state, evidence semantics, and durable conclusions.

**Tech Stack:** Go 1.24 multi-module repository, HoneyBadger ACS, BEAT-MEV-derived BTE, libp2p, Bash 3.2-compatible campaign runners, Python 3.11/pandas/matplotlib, Docker, Terraform, AWS EC2, Prometheus, GitHub Issues and Projects.

## Global Constraints

- Complete the programme in approximately two weeks without implementing Builder API, DVT/SSV signing, execution payload construction, or block publication.
- Fix mixed-root RBC reconstruction, durable terminal failure publication, and the mempool HTTP timeout before freezing the release candidate.
- Treat deterministic common coin, trusted setup/DKG, and absent public share proofs as explicit prototype limitations.
- Run the primary p99 matrix at `n=4,t=3` and `n=7,t=5`, batches `8/32/128`, in local, same-region, and three-region environments, with 10 warmups and 1,000 measured observations per scenario.
- Attempt `n=10,t=7` at batches `8/32/128` and batch `512` at `n=4/7/10` through a separate 30-observation pilot and the predeclared 1,000-observation or 100-boundary-observation continuation rule.
- Retain warmups, failures, timeouts, and inconsistent observations. Never filter failed observations into successful-run quantiles.
- Perform no AWS operation without explicit plan/apply authorization and mandatory authenticated teardown.
- Update `docs/STATUS.md` whenever milestone, blocker, accepted evidence, baseline, or immediate-action state changes.
- Track every implementation/evidence task in `VascoMS/bloc` and the BLOC Thesis Prototype GitHub Project.

---

## Ownership And Schedule

| Surface | Responsibility |
|---|---|
| `docs/ROADMAP.md` | M4–M7 objectives, ordering, deliverables, and done criteria |
| `docs/STATUS.md` | active milestone, blockers, accepted evidence, baseline, and immediate actions |
| `docs/VALIDATION.md` | RQ definitions, experiment matrix, statistics, and evidence acceptance |
| GitHub milestones and Project | delivery buckets, operational status, area, priority, and roadmap target |
| GitHub issues | task scope, dependencies, progress, acceptance, validation, and documentation impact |
| `docs/CHANGELOG.md`, module docs, runbooks | durable conclusions after implementation |
| ignored `results/` trees | raw measurements, reports, figures, logs, and manifests |

| Window | Milestone | Exit gate |
|---|---|---|
| Days 1–3 | M4 | measurement-threatening defects and evidence-support gaps resolved |
| Day 4 | M4 | full validation passes; release-candidate SHA/image frozen |
| Days 5–9 | M5 | primary p99, scale, BTE, client, and resource artifacts accepted |
| Days 9–11 | M6 | deterministic adversarial tests and operational fault campaigns accepted |
| Days 12–14 | M7 | cost model, figures, RQ answers, limitations, and archive complete |

## Dependency Order

1. Tasks 1–4 establish governance and remove M4 blockers.
2. Tasks 5–7 add resource, p99, and client/corpus evidence support.
3. Task 8 freezes the release candidate after Tasks 2–7 pass.
4. Tasks 9–12 collect M5 evidence only from the Task 8 source/image.
5. Tasks 13–14 collect M6 evidence using durable failures from Task 3.
6. Tasks 15–16 synthesize only accepted M5/M6 artifacts and close M7.

### Task 1: Establish the RQ plan and GitHub tracking hierarchy

**GitHub issue:** [#10 Establish the thesis RQ evidence plan and GitHub tracking hierarchy](https://github.com/VascoMS/bloc/issues/10)

**Files:** modify `AGENTS.md`, `docs/DEVELOPMENT.md`, `docs/ROADMAP.md`, `docs/STATUS.md`, `docs/VALIDATION.md`, `docs/DECISIONS.md`, `docs/CHANGELOG.md`; create this plan.

**Produces:** canonical M4–M7 definitions, RQ evidence contract, four GitHub milestones, and a project-backed issue backlog.

- [ ] Update canonical docs with the approved sidecar boundary, p99 matrix, scale rule, fault model, cost boundary, and GitHub/local ownership split.
- [ ] Create M4–M7 GitHub milestones and add every issue to Project 1 with matching `Roadmap target`, `Area`, `Priority`, and initial `Status`.
- [ ] Run `rg -n 'M4|M5|M6|M7|Builder API|DVT' AGENTS.md docs` and `git diff --check`; verify M3 is latest complete, M4 active, and integration deferred.
- [ ] Commit with `git commit -m "docs: plan thesis research evidence programme"`.

### Task 2: Reject mixed-root RBC reconstruction

**GitHub issue:** [#9 Harden RBC reconstruction against mixed-root ECHO shards](https://github.com/VascoMS/bloc/issues/9)

**Files:** modify `sbc/hbbft/rbc.go`, `sbc/hbbft/rbc_test.go`, `docs/modules/hbbft.md`, the PIR register, `STATUS.md`, and `CHANGELOG.md`.

**Interface:** `RBC.tryDecodeValue(hash []byte)` must use only ECHOs whose `RootHash` equals `hash`, reconstruct shards, recompute the Merkle root with `makeProofRequests`, compare it to `hash`, and set `outputDecoded` only after successful verification.

- [ ] Add `TestRBCRejectsMixedRootReconstruction`, using two payload roots and reordered mixed ECHOs; verify no mixed output and later valid root-A completion.
- [ ] Run `cd sbc/hbbft && go test ./... -run TestRBCRejectsMixedRootReconstruction -count=1` and confirm it fails before the fix.
- [ ] Implement root-filtered reconstruction and post-reconstruction commitment validation.
- [ ] Run `cd sbc/hbbft && go test ./...`, `cd bloc-node && go test ./...`, and `bash bloc-node/scripts/run-acs-safety-campaign.sh`.
- [ ] Update docs, remove/rewrite the status blocker only after acceptance, and commit `fix: bind RBC reconstruction to selected root`.

### Task 3: Publish durable terminal slot failures

**GitHub issue:** [#2 Publish durable terminal slot failures to controllers](https://github.com/VascoMS/bloc/issues/2)

**Files:** modify `bloc-node/internal/app/types.go`, `node.go`, `eval_persistent.go`, `eval_suite.go`, lifecycle/evaluator tests, the bloc-node deep dive, validation, status, and changelog.

**Interface:** add `slotFailed` plus bounded `SlotFailure{Slot uint64, Reason string, FailedAtUnixNano int64}`. `/result` remains 202 for pending and 200 with `Result` for success; terminal failure returns 422 with status, slot, and bounded reason. Evaluators stop polling and retain that reason as a failed attempt.

- [x] Add failing tests for pending, success, terminal failure, repeated reads, wrong-slot reads, and evaluator 422 handling.
- [x] Implement idempotent failure storage/reset at the slot lifecycle boundary and expose it from `/result`.
- [x] Update JSONL/CSV writers so expected and unexpected terminal failures remain visible and never enter successful latency summaries.
- [x] Run `cd bloc-node && go test ./...`, update docs/status, and commit `feat: publish durable terminal slot failures`.

### Task 4: Bound mempool provider requests

**GitHub issue:** [#4 Add an explicit timeout to the mempool HTTP provider](https://github.com/VascoMS/bloc/issues/4)

**Files:** modify `bloc-node/internal/app/types.go`, `config.go`, `provider.go`, `provider_test.go`, `commands.go`, README, deep dive, status, and changelog.

**Interface:** add `ProviderConfig.MempoolTimeoutMS int64`, normalize zero to 2,000 ms, reject negatives, and use a node-owned `http.Client{Timeout: duration}` instead of `http.Get`.

- [x] Add `httptest.Server` coverage for success, HTTP error, cancellation, blocking timeout, invalid configuration, and old-config defaulting.
- [x] Run the timeout test and verify the unbounded implementation fails.
- [x] Implement the configuration and client boundary without retries.
- [x] Run `cd bloc-node && go test ./...`, update docs/status, and commit `fix: bound mempool provider requests`.

### Task 5: Collect non-contaminating operator resource metrics

**GitHub issue:** [#11 Collect per-operator CPU, memory, and network resource evidence](https://github.com/VascoMS/bloc/issues/11)

**Files:** create `deploy/ec2/sample-container-resources.sh`; modify active EC2 runners, `scripts/lib/campaign_artifacts.py`, artifact tests, three-region analysis/tests, EC2 runbook, validation, and changelog.

**Interface:** produce `resource_timeseries.csv` with timestamp, sample index, node, region, scenario, phase, CPU usage microseconds, memory current/peak bytes, network receive/transmit bytes, restart count, and OOM state. Run it only in separate resource phases so p99 latency campaigns remain minimally perturbed.

- [x] Add failing fixtures for missing samples, counter resets, incomplete nodes, restarts/OOM, CPU deltas, memory peaks, and network deltas.
- [x] Implement a 250 ms host-local cgroup-v2 sampler with Docker fallback, a bounded stop-file lifecycle, and no credential/environment capture.
- [x] Integrate sampler start/stop/collection before mandatory teardown in same-region and three-region runners.
- [x] Generate per-node/configuration and cluster-total summaries while keeping protocol-message bytes separate from host network counters.
- [x] Run `bash scripts/test-campaign-runners.sh` and `cd latency-charts && python -m pytest`; commit `feat: collect sidecar resource evidence`.

### Task 6: Add p99 statistics and balanced long-campaign scheduling

**GitHub issue:** [#12 Add p99 statistics, confidence intervals, and long-campaign scheduling](https://github.com/VascoMS/bloc/issues/12)

**Files:** modify evaluator suite/tests, same/three-region runner helpers, chart data/reports/tests; create `latency-charts/src/bloc_latency_charts/statistics.py` and `tests/test_statistics.py`.

**Interface:** add Type-7 p50/p95/p99 plus non-parametric 95% order-statistic intervals without SciPy; report attempted/completed/consistent-within-12-seconds/failed/timed-out counts; add `--repetition-blocks`; accept nodes `4,7,10` and batches `8,32,128,512` only when `BMax >= max(batch)`.

- [x] Add deterministic tests using 1,000 values, duplicates, insufficient samples, and failures/timeouts; refuse a p99 claim below the contracted sample count.
- [x] Implement `QuantileEstimate` and `estimate_quantile(values, quantile, confidence=0.95)` using pandas Type-7 values and a `math.comb` binomial CDF for interval ranks.
- [x] Derive outcome rates from every measured attempt while latency quantiles use only successful consistent rows and list every excluded reason.
- [x] Interleave scenarios in stable seeded blocks and persist block IDs/counts in manifests/CSV.
- [x] Run all bloc-node tests, chart tests, and campaign-runner validation; commit `feat: support p99 evidence campaigns`.

### Task 7: Build the user corpus and encrypted/plaintext benchmark

**GitHub issue:** [#13 Build the RQ2/RQ4 transaction corpus and client overhead benchmark](https://github.com/VascoMS/bloc/issues/13)

**Files:** expand `deploy/docker-compose/corpus/mock-targets.jsonl`; modify replay-placeholder code/tests and mempool docs; create a benchmark test and `mempool-il/cmd/corpus-report/main.go`; update validation/changelog.

**Interface:** at least 100 deterministic valid signed transactions across simple transfer and 128/256/1,024/4,096-byte calldata classes. Produce `client_overhead.csv` with class, raw/ciphertext/placeholder/calldata bytes, `carrier_gas_estimate`, encryption microseconds, and submission-serialization microseconds.

- [ ] Add failing corpus tests for validity, unique hashes, exact size-class coverage, and report schema.
- [ ] Generate the corpus with fixed development keys, chain ID 1337, documented payloads, and no live-chain material.
- [ ] Benchmark raw serialization/submission preparation and encrypted placeholder construction from identical target bytes with at least 100 samples per class.
- [ ] Run mempool and bloc-node suites; commit `feat: add client overhead evidence corpus`.

### Task 8: Freeze the evaluation release candidate

**GitHub issue:** [#14 Validate and freeze the thesis evaluation release candidate](https://github.com/VascoMS/bloc/issues/14)

**Files:** update status, validation, and changelog; write ignored logs below `results/release-candidate/<sha>/validation/`.

**Produces:** one source SHA, one `linux/amd64` image digest, complete validation logs, and a no-mixed-source rule for final evidence.

- [ ] Run all four Go module suites and latency-chart tests.
- [ ] Run targeted race/fuzz commands from Tasks 2–7, the ACS safety campaign, runner portability tests, Terraform format/validate, and Compose rehearsal.
- [ ] Build and inspect the immutable amd64 image and record digest, user, architecture, and entrypoint.
- [ ] Update status with SHA, digest, validation root, resolved blockers, and M5 actions; commit `docs: freeze thesis evaluation release candidate`.

### Task 9: Run the distributed-campaign contract preflight

**GitHub issue:** [#8 Run the distributed-campaign contract preflight](https://github.com/VascoMS/bloc/issues/8)

**Files:** produce ignored `results/local/distributed-preflight-2bc8efc/`; modify evaluator only if a preflight rejects its contract; update status/validation/changelog after acceptance.

- [ ] Run `n=4/7`, batches `8/32/128`, with 1 warmup and 1 measured observation per cell.
- [ ] Run `n=10`, batches `8/32/128`, plus batch `512` at `n=4/7/10`, with 1 warmup and 3 measured observations per unique extension cell.
- [ ] Validate startup/teardown, counts, outcome retention, consistency, primary 12-second completion, additivity, provenance, schema completeness, and chart loading; retain `classification=validation-only` and `performance_claims_allowed=false` without manual repair.
- [ ] Do not collect local resource evidence or report local p99, throughput, scaling, topology, or local-versus-VM performance claims.

### Task 10: Collect the first M5 matched same-region p99 and resource campaigns

**GitHub issue:** [#15 Collect matched same-region p99 and resource campaigns](https://github.com/VascoMS/bloc/issues/15)

**Files:** operate through `deploy/ec2/run-m3-same-az.sh` and the EC2 runbook; produce ignored `results/ec2/final-same-region-<sha>/`; update canonical evidence docs after acceptance.

- [ ] Run validation, quota, offering, maximum-cost, and exact Terraform-plan preflights before authorization.
- [ ] After successful Task 9 preflight, run the primary p99 matrix with the Task 9-accepted source/image/corpus/configuration/seed/schema contract and balanced schedule.
- [ ] Run independent scale pilots/continuations and a separate high-frequency resource campaign.
- [ ] Require authenticated empty teardown and complete artifact checks before promotion.

### Task 11: Collect matched three-region p99 and resource campaigns after accepted Task 10 manifests

**GitHub issue:** [#16 Collect matched three-region p99 and resource campaigns](https://github.com/VascoMS/bloc/issues/16)

**Files:** operate through `deploy/ec2/run-m3-three-region.sh`; produce ignored `results/ec2/final-three-region-<sha>/`; update canonical evidence docs after acceptance.

- [ ] Verify three-region quota/offering/plan preflights, especially `n=10`; unavailable quota documents an uncollected extension without changing the primary matrix.
- [ ] Run primary p99, extension pilots/continuations, and separate resource phases from the exact frozen image.
- [ ] Authenticate teardown after every phase and reject any phase with residual scoped resources.
- [ ] Require accepted Task 10 manifests, then compare same/three-region manifests before causal topology analysis and reject mismatched comparisons.

### Task 12: Collect final BTE optimization and cryptographic benchmarks

**GitHub issue:** [#17 Collect final BTE optimization and cryptographic overhead benchmarks](https://github.com/VascoMS/bloc/issues/17)

**Files:** create `bte/btd-impl-main/scripts/run-thesis-benchmarks.sh`; modify benchmark code only for missing contracted cases; update BTE testing/deep-dive/validation/changelog; produce ignored `results/local/bte-final-<sha>/`.

**Interface:** raw Go output and normalized CSV for encryption, partial decryption, combine, hybrid full path, `ns/op`, `B/op`, `allocs/op`, and normal/`sqrt(B)`/`2*sqrt(B)`/parallel variants at batches `8/32/128/512`.

- [ ] Add runner validation for exact benchmark/configuration coverage.
- [ ] Record Go/OS/CPU/source/command/count/benchtime and use independent `-count` samples rather than internal iterations as samples.
- [ ] Run on the frozen source, retain all samples, and report medians/ranges without mixing earlier hardware.
- [ ] Run BTE tests and commit `feat: add thesis BTE benchmark campaign` if tooling changes.

### Task 13: Implement the bounded RQ3 scenario matrix

**GitHub issue:** [#18 Implement the RQ3 bounded fault and adversarial scenario matrix](https://github.com/VascoMS/bloc/issues/18)

**Files:** modify fault config, commands, node, evaluator/tests, HBBFT tests; create `bloc-node/scripts/run-rq3-fault-campaign.sh`; update module docs/validation/changelog.

**Interface:** retain current proposal omission/share withholding/share corruption/delay; add fixed-enum targeted transaction omission and malformed selected-input modes. Manifest fields: `fault_count`, `faulty_nodes`, `expected_outcome`, `target_hash`, and `fault_model`.

- [ ] Add failing outcome tests for universally correct target inclusion, within-bound omission, threshold-breaking withholding, corrupt shares, delay, malformed ciphertext, wrong scope, commitment mismatch, and repeated failure reads.
- [ ] Implement only fixed-enum deterministic faults; prohibit arbitrary commands and unbounded mutation.
- [ ] Add a 30-repetition `n=4/7` runner with selected batch-128 stress and `n=10` withholding confirmation; expected terminal failures count as correct outcomes, never latency successes.
- [ ] Run HBBFT/bloc-node tests and runner validation; commit `feat: add bounded RQ3 fault scenarios`.

### Task 14: Collect the final RQ3 fault evidence

**GitHub issue:** [#19 Collect the final RQ3 fault and adversarial evidence](https://github.com/VascoMS/bloc/issues/19)

**Files:** produce ignored `results/local/rq3-faults-<sha>/`; update status, validation, changelog, and PIR register after acceptance.

- [ ] Pass deterministic mixed-root, equivocation, malformed proof/encoding, wrong-scope, commitment, conflicting-share, and future/conflicting-BBA tests.
- [ ] Run within-bound and threshold-breaking operational scenarios separately and preserve expected terminal failures.
- [ ] Reject divergent success, loss of a universally correct target, unexpected hang, or unclassified failure.
- [ ] Produce a matrix separating demonstrated behavior, deterministic tests, cryptographic assumptions, and deferred limitations.

### Task 15: Produce RQ1/RQ2 tables and the RQ4 cost model

**GitHub issue:** [#20 Produce user and operator cost analysis from accepted evidence](https://github.com/VascoMS/bloc/issues/20)

**Files:** create `latency-charts/src/bloc_latency_charts/thesis_analysis.py` and its tests; modify chart CLI/README and validation/changelog; produce ignored `results/thesis-analysis/<sha>/`.

**Interface:** consume accepted Tasks 7 and 9–12 plus explicit dated provider pricing JSON. Produce timing, stage/resource, user overhead, and operator cost CSV/tables with formulas and assumptions.

- [ ] Test `cluster_hourly_cost / 300` dedicated cost per 12-second slot, per-transaction amortization, transfer inputs, missing prices, and decimal currency handling.
- [ ] Require provider, source URL, retrieval date, currency, instance rates, and transfer rates; never fetch prices implicitly during reproduction.
- [ ] Reject mixed source/image/corpus artifacts and separate dedicated provisioning cost from measured resource demand.
- [ ] Run chart tests and commit `feat: generate thesis cost and overhead analysis`.

### Task 16: Synthesize RQ answers and archive final evidence

**GitHub issues:** [#21 Synthesize the final RQ answers and thesis figures](https://github.com/VascoMS/bloc/issues/21) and [#3 Archive accepted M3 and final thesis evidence with checksums](https://github.com/VascoMS/bloc/issues/3).

**Files:** update status, validation, roadmap, changelog; create `docs/archive/THESIS_EVIDENCE_REGISTER_2026.md`; produce the ignored checksummed archive and generated figures/tables.

- [ ] For every claim record metric, environment, configuration, sample count, source SHA, image digest, artifact, figure/table, result, and limitation.
- [ ] Write positive, negative, or inconclusive answers: RQ1 ends at sidecar output; RQ2 excludes DVT signing; RQ3 is bounded; RQ4 excludes paid gas/proposer/PBS claims.
- [ ] Exclude secrets, SSH keys, credential-bearing state, and ephemeral configs; generate and verify checksums from a clean extraction while preserving M3 historical evidence.
- [ ] Close milestones only when done criteria pass; synchronize Project state, issues, status, roadmap, validation, and the latest baseline.
- [ ] Run `git diff --check` and stale-reference/link scans; commit `docs: register final thesis evidence`.

## Programme Completion Gate

- [ ] M4 blockers are resolved and the release candidate is frozen.
- [ ] M5 primary p99 and scale attempts are accepted or explicitly rejected with evidence.
- [ ] M6 deterministic and operational fault evidence satisfies the bounded claim contract.
- [ ] M7 cost analysis and all four RQ answers are traceable to accepted artifacts.
- [ ] GitHub milestones, Project fields, issues, `STATUS.md`, `ROADMAP.md`, and `VALIDATION.md` agree.
- [ ] Only the independent sidecar path is claimed; Builder API and DVT integration remain future work.
