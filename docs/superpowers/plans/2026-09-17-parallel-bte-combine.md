# Bounded Parallel BTE Combine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement deterministic, bounded two-worker Opt-2 sub-batch combine, carry its configuration and observed worker counts through every evaluator and campaign artifact, and pass all local gates required before publishing images for the three B=512 AWS pilots.

**Architecture:** `CombineSharesBounded` keeps serial preflight and serial subset enumeration within each sub-batch, but dispatches independently prepared sub-batches to a fixed worker pool. Indexed outcomes are committed in ascending sub-batch order so workers 1 and 2 have identical plaintext ordering, lowest-error selection, and visible attempt statistics. `bloc-node` defaults omitted configuration to one worker, strictly validates explicit values, records configured/effective counts, and freezes the same count into every campaign layer.

**Tech Stack:** Go 1.24, Kyber/BN256 BTE, `bloc-node`, shell campaign contracts, Python `unittest`, Terraform, Docker/ECR only after the local review checkpoint.

**Spec:** [2026-09-17-parallel-bte-combine-campaign-design.md](../specs/2026-09-17-parallel-bte-combine-campaign-design.md)

## Global Constraints

- Preserve the existing node-level `combineInFlight` single-flight boundary.
- Parallelize planned sub-batches only. Do not parallelize subset enumeration or alter Opt-2 planning, RBC/ACS, stream mode, ECHO behavior, ciphertexts, shares, or transaction materialization.
- Keep `MaxWorkers == 0` as a library-level compatibility default of one; reject negative library values. At the JSON/configuration boundary, distinguish omission from explicit zero and accept only `1..64`.
- Define effective workers for a nonempty plan as `max(1, min(configured, len(plan.SubBatches), runtime.GOMAXPROCS(0)))`.
- Select the lowest numbered failing sub-batch after all workers finish. Commit attempts through that failure and report zero attempts for later sub-batches, even if speculative work completed.
- Do not use a global mutex around cryptographic combine. If race testing finds shared mutable backend state, build worker-local reconstruction state from the same immutable public parameters.
- Do not rewrite or relabel accepted BMax-128 evidence. Only new B=512 bundles carry two workers.
- Generated benchmark, smoke, and evaluator output belongs under ignored `results/` directories.
- No image publication, AWS allocation, or live campaign execution occurs in this plan. Stop at the review checkpoint.

---

### Task 1: Add and prove the bounded worker executor

**Files:**
- Create: `bte/btd-impl-main/be/combine_parallel.go`
- Create: `bte/btd-impl-main/be/combine_parallel_test.go`

- [ ] **Step 1: Write failing worker-count normalization tests**

Cover omitted/default, negative, sub-batch-count, and `GOMAXPROCS` bounds without invoking cryptography:

```go
func TestEffectiveCombineWorkers(t *testing.T) {
    previous := runtime.GOMAXPROCS(2)
    t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

    tests := []struct {
        name       string
        configured int
        subBatches int
        wantConfig int
        wantActive int
        wantErr    bool
    }{
        {"omitted-defaults-to-one", 0, 46, 1, 1, false},
        {"negative-rejected", -1, 46, 0, 0, true},
        {"bounded-by-plan", 8, 1, 8, 1, false},
        {"bounded-by-gomaxprocs", 8, 46, 8, 2, false},
        {"empty-plan", 2, 0, 2, 0, false},
    }
    // Assert configured/effective values and errors for every row.
}
```

- [ ] **Step 2: Run the focused tests and prove they fail**

Run from `bte/btd-impl-main`:

```sh
go test ./be -run TestEffectiveCombineWorkers -count=1
```

Expected: compile failure because `effectiveCombineWorkers` does not exist.

- [ ] **Step 3: Implement worker normalization**

Use these stable internal contracts:

```go
const defaultCombineWorkers = 1

func effectiveCombineWorkers(configured, subBatches int) (configuredWorkers, effectiveWorkers int, err error) {
    if configured < 0 {
        return 0, 0, fmt.Errorf("max combine workers must be non-negative")
    }
    if configured == 0 {
        configured = defaultCombineWorkers
    }
    if subBatches == 0 {
        return configured, 0, nil
    }
    return configured, max(1, min(configured, subBatches, runtime.GOMAXPROCS(0))), nil
}
```

- [ ] **Step 4: Write a failing executor concurrency test**

Define the executor around prepared jobs and indexed outcomes:

```go
type preparedSubBatch struct {
    id           int
    items        []BatchItem
    shares       []*share.PubShare
    attemptLimit int
}

type subBatchOutcome struct {
    id       int
    results  []positionedPlaintextResult
    attempts int
    err      error
}

func runSubBatchJobs(
    workerCount int,
    jobs []preparedSubBatch,
    combine func(preparedSubBatch) subBatchOutcome,
) []subBatchOutcome
```

The test must submit at least four jobs, use `atomic.Int32` to measure current and peak concurrency, block jobs on a release channel, and assert peak concurrency is exactly two when `workerCount == 2`. Also assert the returned slice remains indexed by ascending job ID even when completion order is reversed.

- [ ] **Step 5: Run the executor test and prove it fails**

```sh
go test ./be -run TestRunSubBatchJobsBoundsConcurrencyAndIndexesOutcomes -count=1
```

Expected: compile failure because `runSubBatchJobs` does not exist.

- [ ] **Step 6: Implement the fixed worker pool**

Use one jobs channel, exactly `workerCount` goroutines, one outcome slot per job ID, and a `sync.WaitGroup`. Validate that IDs are unique and in range before launching workers. Do not launch one goroutine per sub-batch.

- [ ] **Step 7: Run focused tests and commit**

```sh
go test ./be -run 'TestEffectiveCombineWorkers|TestRunSubBatchJobs' -count=1
git add bte/btd-impl-main/be/combine_parallel.go bte/btd-impl-main/be/combine_parallel_test.go
git commit -m "feat(bte): add bounded combine worker executor"
```

---

### Task 2: Integrate deterministic parallel sub-batch reconstruction

**Files:**
- Modify: `bte/btd-impl-main/be/cluster.go`
- Modify: `bte/btd-impl-main/be/cluster_test.go`
- Modify: `bte/btd-impl-main/be/combine_parallel_test.go`

- [ ] **Step 1: Add the public option and statistics fields**

Extend the existing structs without changing existing field meanings:

```go
type CombineOptions struct {
    MaxAttemptsPerSubBatch  int
    AttemptLimitsBySubBatch []int
    MaxWorkers              int
}

type CombineStats struct {
    AttemptsBySubBatch []int
    ConfiguredWorkers  int
    EffectiveWorkers   int
}
```

Keep `CombineShares` explicitly serial by passing `MaxWorkers: defaultCombineWorkers`.

- [ ] **Step 2: Write failing worker=1/2 equivalence tests**

Build one deterministic multi-sub-batch fixture and call `CombineSharesBounded` twice. Assert equality of:

- plaintext result count, bytes, sub-batch IDs, and restored original positions;
- `AttemptsBySubBatch`;
- returned error text; and
- configured/effective statistics (`1/1` versus `2/2`).

The plan must contain at least two planned sub-batches so worker=2 is observable.

- [ ] **Step 3: Write failing deterministic failure tests**

Create invalid exact-threshold candidates in both sub-batch 0 and sub-batch 1. Compare workers 1 and 2 and require:

```text
returned error = sub-batch 0 error
attempts[0]     = serial oracle attempts[0]
attempts[1:]    = zero
```

Add a preflight case (missing threshold candidates or impossible attempt budget) and require the existing error text plus an all-zero attempt vector.

- [ ] **Step 4: Run the focused tests and prove behavioral failure**

```sh
go test ./be -run 'TestCombineSharesBounded.*Workers|TestCombineSharesBounded.*LowestFailure|TestCombineSharesBounded.*Preflight' -count=1
```

Expected: new assertions fail because the combine loop is still serial and worker statistics are absent.

- [ ] **Step 5: Split `CombineSharesBounded` into prepare, execute, and commit phases**

Normalize and validate the worker option at function entry, populate the configured/effective statistics, and do not launch workers yet. Keep all remaining plan/share/ownership/budget checks in the serial prepare phase. For each sub-batch, copy immutable-use items and the already deterministically sorted candidates into `preparedSubBatch`. Then:

```go
configured, effective, err := effectiveCombineWorkers(options.MaxWorkers, len(plan.SubBatches))
stats.ConfiguredWorkers = configured
stats.EffectiveWorkers = effective
if err != nil { return nil, stats, err }

// Run only after every sub-batch has passed serial preflight.
outcomes := runSubBatchJobs(effective, jobs, func(job preparedSubBatch) subBatchOutcome {
    results, attempts, err := c.combineSubBatch(job.items, job.shares, job.attemptLimit)
    return subBatchOutcome{id: job.id, results: results, attempts: attempts, err: err}
})
```

Commit outcomes only in ascending sub-batch order. Copy attempts/results through the first failure, return that failure, and leave later visible attempts at zero. Restore plaintexts to their original positions only after all committed sub-batches succeed.

- [ ] **Step 6: Add repeated real-crypto stress coverage**

Run the real workers=2 path repeatedly with distinct deterministic fixtures or slots, checking plaintext equivalence and worker statistics every time. The test name must be `TestCombineSharesBoundedParallelStress` so the race gate can target it.

- [ ] **Step 7: Run normal and race gates**

```sh
go test ./be -run 'TestCombineSharesBounded.*Workers|TestCombineSharesBounded.*LowestFailure|TestCombineSharesBounded.*Preflight|TestCombineSharesBoundedParallelStress' -count=10
go test -race ./be -run TestCombineSharesBoundedParallelStress -count=20
go test ./...
go test -race ./...
```

If the race detector reports shared backend mutation, stop and add worker-local BTD reconstruction state. Do not serialize the crypto body with a global lock.

- [ ] **Step 8: Commit the BTE behavior**

```sh
git add bte/btd-impl-main/be/cluster.go bte/btd-impl-main/be/cluster_test.go bte/btd-impl-main/be/combine_parallel.go bte/btd-impl-main/be/combine_parallel_test.go
git commit -m "feat(bte): combine sub-batches in bounded parallel workers"
```

---

### Task 3: Add strict node configuration, runtime wiring, and metrics

**Files:**
- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/commands.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/config_security_test.go`
- Modify: `bloc-node/internal/app/resource_safety_test.go`
- Modify: `bloc-node/internal/app/node_combine_test.go`

- [ ] **Step 1: Write failing configuration compatibility tests**

Add table-driven tests proving:

- a missing JSON field normalizes to one worker;
- generated config contains `"max_combine_workers": 1` by default;
- `--max-combine-workers 2` reaches the written public config; and
- explicit `0`, negative values, and values above `64` fail validation.

Use a pointer in the wire-only JSON struct to distinguish omission from explicit zero:

```go
type wireLimits struct {
    MaxCombineWorkers *int `json:"max_combine_workers"`
    // existing fields remain unchanged
}
```

- [ ] **Step 2: Run the focused config tests and prove they fail**

Run from `bloc-node`:

```sh
go test ./internal/app -run 'Test.*CombineWorkers.*Config|Test.*ResourceLimits' -count=1
```

- [ ] **Step 3: Implement configuration and generator validation**

Add:

```go
const (
    defaultMaxCombineWorkers  = 1
    absoluteMaxCombineWorkers = 64
)

type ResourceLimits struct {
    // existing fields
    MaxCombineWorkers        int `json:"max_combine_workers,omitempty"`
    explicitZeroCombineWorkers bool
}
```

`defaultResourceLimits` sets one. Normalization fills one only when omitted. Validation accepts `1..64` and returns a field-specific error otherwise. Add `--max-combine-workers` to `gen-config` with default one.

- [ ] **Step 4: Write failing runtime and metrics tests**

Extend `node_combine_test.go` so a node configured with two workers passes `MaxWorkers: 2` into BTE and `recordCombineStats` retains:

```go
CombineWorkersConfigured int `json:"combine_workers_configured"`
CombineWorkersEffective  int `json:"combine_workers_effective"`
```

Verify attempt-budget subtraction remains based on committed `AttemptsBySubBatch` only.

- [ ] **Step 5: Wire the node combine call and metrics**

In `tryCombine`:

```go
be.CombineOptions{
    MaxAttemptsPerSubBatch:  n.cfg.Limits.MaxCombineAttemptsPerSubBatch,
    AttemptLimitsBySubBatch: attemptLimits,
    MaxWorkers:              n.cfg.Limits.MaxCombineWorkers,
}
```

Update `recordCombineStats` under the existing metrics lock; do not add another combine admission mechanism.

- [ ] **Step 6: Run focused and complete node tests**

```sh
go test ./internal/app -run 'Test.*CombineWorkers|TestRecordCombineStats|Test.*ResourceLimits' -count=1
go test ./...
go test -race ./...
```

- [ ] **Step 7: Commit node configuration and runtime behavior**

```sh
git add bloc-node/internal/app/types.go bloc-node/internal/app/config.go bloc-node/internal/app/commands.go bloc-node/internal/app/node.go bloc-node/internal/app/config_security_test.go bloc-node/internal/app/resource_safety_test.go bloc-node/internal/app/node_combine_test.go
git commit -m "feat(node): configure and report combine workers"
```

---

### Task 4: Propagate worker configuration through local, suite, and remote evaluators

**Files:**
- Modify: `bloc-node/internal/app/eval.go`
- Modify: `bloc-node/internal/app/eval_suite.go`
- Modify: `bloc-node/internal/app/eval_persistent.go`
- Modify: `bloc-node/internal/app/eval_remote.go`
- Modify: `bloc-node/internal/app/ec2_config.go`
- Modify: `bloc-node/internal/app/campaign_identity.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`
- Modify: `bloc-node/internal/app/campaign_identity_test.go`
- Modify: matching EC2/deployment tests under `bloc-node/internal/app/`

- [ ] **Step 1: Write failing propagation tests**

Require two workers to survive every evaluator boundary:

- `eval-local --max-combine-workers 2` into generated node config and `EvalRun`;
- `eval-suite --max-combine-workers 2` into `suiteManifest`, persistent cluster generation, and immutable-config validation;
- campaign identity and EC2 config generation into public `limits`; and
- remote evaluator JSON into the recorded run.

- [ ] **Step 2: Add explicit artifact fields**

Use exact field names:

```go
// EvalRun
MaxCombineWorkers int `json:"max_combine_workers"`

// suiteManifest and suiteOptions
MaxCombineWorkers int `json:"max_combine_workers"`

// remoteEvalConfig
MaxCombineWorkers int `json:"max_combine_workers,omitempty"`
```

Remote omission normalizes to one for compatibility. All newly generated campaign inputs write the field explicitly.

- [ ] **Step 3: Add CLI flags and pass the value without hidden defaults**

Add `--max-combine-workers` with default one to local evaluation, suite evaluation, campaign identity, and EC2 config generation. Update `runLocalExperiment` and persistent execution signatures so the value is passed as an argument, not recovered from process-global state.

- [ ] **Step 4: Extend CSV output and tests**

Add these columns to both `run_measurements.csv` and `node_measurements.csv`:

```text
max_combine_workers
effective_combine_workers
```

The configured value comes from `EvalRun`, so failed/pre-combine rows still retain it. The effective value comes from node result metrics and may remain zero only when combine was never reached.

- [ ] **Step 5: Run focused evaluator tests and complete suites**

```sh
go test ./internal/app -run 'Test.*Eval.*CombineWorkers|Test.*CampaignIdentity.*CombineWorkers|Test.*EC2.*CombineWorkers|Test.*Measurement.*CombineWorkers' -count=1
go test ./...
go test -race ./...
```

- [ ] **Step 6: Commit evaluator propagation**

```sh
git add bloc-node/internal/app/eval.go bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_persistent.go bloc-node/internal/app/eval_remote.go bloc-node/internal/app/ec2_config.go bloc-node/internal/app/campaign_identity.go bloc-node/internal/app/*_test.go
git commit -m "feat(eval): preserve combine worker provenance"
```

---

### Task 5: Freeze the worker count into bundles and materialized deployment inputs

**Files:**
- Modify: `bloc-node/internal/app/campaign_bundle.go`
- Modify: `bloc-node/internal/app/campaign_materialize.go`
- Modify: `bloc-node/internal/app/campaign_bundle_test.go`
- Modify: `bloc-node/internal/app/campaign_materialize_test.go`

- [ ] **Step 1: Write failing bundle contract tests**

Build a B=512 identity with `limits.max_combine_workers=2`, generate a manifest, reload it, and assert all of:

- manifest worker count is two;
- manifest/identity mismatch fails closed;
- public materialized cluster config contains two; and
- remote evaluator config contains two.

Add a historical compatibility fixture whose old manifest omits the field and whose identity defaults to one. It must load as one, but a new B=512 issue-33 bundle must never pass a required-two validation with omission.

- [ ] **Step 2: Add the manifest field and validation**

```go
type campaignBundleManifest struct {
    // existing fields
    MaxCombineWorkers int `json:"max_combine_workers,omitempty"`
}
```

Set it from `bundle.Identity.Limits.MaxCombineWorkers`. Normalize an omitted v1 field to one only for historical reads, then compare it with the normalized identity. Materialization must copy the normalized value into public and remote configs.

- [ ] **Step 3: Run focused tests and complete Go suites**

```sh
go test ./internal/app -run 'TestCampaignBundle.*CombineWorkers|TestCampaignMaterialize.*CombineWorkers' -count=1
go test ./...
go test -race ./...
```

- [ ] **Step 4: Commit bundle provenance**

```sh
git add bloc-node/internal/app/campaign_bundle.go bloc-node/internal/app/campaign_materialize.go bloc-node/internal/app/campaign_bundle_test.go bloc-node/internal/app/campaign_materialize_test.go
git commit -m "feat(campaign): freeze combine workers into bundles"
```

---

### Task 6: Enforce fail-closed runner and artifact provenance

**Files:**
- Modify: `deploy/ec2/run-final-campaign.sh`
- Modify: `scripts/lib/final-campaign-contract.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `scripts/lib/campaign_artifacts.py`
- Modify: `scripts/tests/test-final-campaign-contract.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test-final-campaign-race-gate-contract.sh`
- Modify: `scripts/tests/test_campaign_artifacts.py`

- [ ] **Step 1: Add failing shell contract fixtures**

Extend B=512 fixtures with `max_combine_workers: 2`, then add mismatch cases for:

- bundle manifest versus identity;
- frozen inputs versus bundle;
- generated public config versus bundle; and
- requested runner value versus bundle.

Each mismatch must make the contract or lifecycle test nonzero. Preserve the accepted historical BMax-128 path at one worker.

- [ ] **Step 2: Add failing Python artifact fixtures**

For issue-33 B=512 final-phase validation, assert configured value two in:

- final manifest;
- frozen inputs;
- bundle manifest;
- `generated-public/cluster.json`;
- remote evaluator config;
- scenario manifest;
- every selected run row; and
- every node row.

Successful nonempty node rows must report effective value two. Failed rows must still report configured two, but may report effective zero only when combine was not reached.

- [ ] **Step 3: Run tests and prove the new mismatch fixtures fail**

From the repository root:

```sh
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
python3 -m unittest scripts.tests.test_campaign_artifacts
```

- [ ] **Step 4: Implement the shell provenance chain**

Introduce `FINAL_MAX_COMBINE_WORKERS`, export it from the runner, freeze it in lifecycle inputs and final manifests, and require exactly two for the new B=512 extension. Keep historical/default one support only where existing accepted artifacts are intentionally read.

- [ ] **Step 5: Implement Python cross-artifact checks**

Use one normalized expected value and emit field-specific mismatch errors. Do not silently coerce missing issue-33 B=512 fields to two.

- [ ] **Step 6: Run all campaign contract gates**

```sh
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-race-gate-contract.sh
bash scripts/tests/test-final-campaign-terraform.sh
python3 -m unittest scripts.tests.test_campaign_artifacts
```

- [ ] **Step 7: Commit campaign enforcement**

```sh
git add deploy/ec2/run-final-campaign.sh scripts/lib/final-campaign-contract.sh scripts/lib/final-campaign-lifecycle.sh scripts/lib/campaign_artifacts.py scripts/tests/test-final-campaign-contract.sh scripts/tests/test-final-campaign-lifecycle.sh scripts/tests/test-final-campaign-race-gate-contract.sh scripts/tests/test_campaign_artifacts.py
git commit -m "feat(campaign): enforce parallel combine provenance"
```

---

### Task 7: Add decision-grade B=512 combine benchmarks

**Files:**
- Create: `bte/btd-impl-main/be/combine_benchmark_test.go`
- Modify: `bte/btd-impl-main/TESTING.md`

- [ ] **Step 1: Add a benchmark fixture outside the timed region**

Generate the B=512 plan, ciphertexts, and valid shares once before `ResetTimer`. Benchmark only `CombineSharesBounded` and validate its result after each iteration.

```go
func BenchmarkCombineSharesBoundedB512(b *testing.B) {
    fixture := newCombineBenchmarkFixture(b, 512)
    for _, workers := range []int{1, 2, runtime.GOMAXPROCS(0)} {
        b.Run(fmt.Sprintf("workers-%d", workers), func(b *testing.B) {
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                results, stats, err := fixture.cluster.CombineSharesBounded(
                    fixture.plan,
                    fixture.shares,
                    CombineOptions{MaxAttemptsPerSubBatch: 256, MaxWorkers: workers},
                )
                // Stop timing before result/stat validation.
            }
        })
    }
}
```

Keep the existing full-path benchmark intact; this new benchmark isolates combine.

- [ ] **Step 2: Compile and run deployment-relevant samples**

Run from `bte/btd-impl-main` and save ignored raw output:

```sh
mkdir -p results/issue-33
GOMAXPROCS=2 go test ./be -run '^$' -bench BenchmarkCombineSharesBoundedB512 -benchtime=1x -count=10 | tee results/issue-33/combine-b512.txt
```

Confirm the output contains workers 1 and 2 and is consumable by `benchstat`. Do not treat local timings as AWS latency evidence.

- [ ] **Step 3: Document the benchmark contract and commit**

Document what is excluded from timing, the `GOMAXPROCS=2` deployment analogue, and the ignored result path.

```sh
git add bte/btd-impl-main/be/combine_benchmark_test.go bte/btd-impl-main/TESTING.md
git commit -m "test(bte): benchmark B512 combine worker counts"
```

---

### Task 8: Run local B=512 smoke and complete canonical documentation

**Files:**
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/modules/bte.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Review and modify if required: `docs/STATUS.md`
- Modify: `bloc-node/README.md`
- Modify: `bte/btd-impl-main/README.md`
- Modify: `deploy/ec2/README.md`
- Modify: `graphify-out/`

- [ ] **Step 1: Run local two-worker B=512 correctness smoke**

Use non-overlapping port ranges and separate ignored output directories:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --threshold 3 --bmax 512 --batch-sizes 512 --max-combine-workers 2 --base-port 31000 --timeout 60s --out-dir results/issue-33/n4-b512 --print summary
go run ./cmd/bloc-node eval-local --nodes 7 --threshold 5 --bmax 512 --batch-sizes 512 --max-combine-workers 2 --base-port 41000 --timeout 60s --out-dir results/issue-33/n7-b512 --print summary
go run ./cmd/bloc-node eval-local --nodes 10 --threshold 7 --bmax 512 --batch-sizes 512 --max-combine-workers 2 --base-port 51000 --timeout 60s --out-dir results/issue-33/n10-b512 --print summary
```

For each result, require success, consistency, batch size 512, configured workers two, and effective workers two. Record in issue #33 that these are correctness/artifact-shape evidence, not latency evidence.

- [ ] **Step 2: Run direct infrastructure validation**

```sh
terraform -chdir=deploy/ec2/terraform init -backend=false
terraform -chdir=deploy/ec2/terraform validate
terraform -chdir=deploy/ec2/terraform-three-region init -backend=false
terraform -chdir=deploy/ec2/terraform-three-region validate
```

These commands validate configuration only and must not plan or allocate AWS resources.

- [ ] **Step 3: Update canonical behavior and validation documentation**

Document:

- bounded sub-batch parallelism and serial subset enumeration;
- deterministic lowest-failure and committed-attempt semantics;
- omitted-one versus explicit-invalid configuration;
- configured/effective metrics and unchanged `combine_us` wall-clock meaning;
- B=512 two-worker benchmark/smoke commands;
- persistent-lanes, broadcast ECHO, selective/hash-only ECHO disabled;
- the three independent n=4/7/10, B=512, 30-run AWS cells; and
- the review-before-image-publication boundary.

Record the architecture choice in `docs/DECISIONS.md` and the implementation in `docs/CHANGELOG.md`. Do not duplicate an operational procedure outside `deploy/ec2/README.md`.

- [ ] **Step 4: Review `STATUS.md` under its maintenance contract**

If local gates are complete, replace the implementation next action with the pre-publication review gate and cite the accepted local evidence. Do not mark the AWS cells complete and do not select a new milestone.

- [ ] **Step 5: Refresh and inspect the repository graph**

```sh
graphify update .
git status --short
```

Inspect graph changes and stage only generated graph files attributable to this feature.

- [ ] **Step 6: Validate documentation ownership and commit**

Inspect the staged document list against the ownership table in `docs/DEVELOPMENT.md`, confirm every new relative Markdown link resolves in the worktree, and run:

```sh
git diff --check
git diff --name-only origin/main...HEAD
git add docs/ARCHITECTURE.md docs/modules/bte.md docs/modules/bloc-node.md docs/VALIDATION.md docs/DECISIONS.md docs/CHANGELOG.md docs/STATUS.md bloc-node/README.md bte/btd-impl-main/README.md deploy/ec2/README.md graphify-out
git commit -m "docs: define parallel combine validation campaign"
```

Omit `docs/STATUS.md` from the staged set if the maintenance review found no status fact to change.

---

### Task 9: Execute the complete pre-publication review gate

**Files:**
- Modify only files required to fix failures found by this gate.
- Update: GitHub issue `#33` with evidence links and command outcomes.

- [ ] **Step 1: Re-run both complete affected-module suites**

```sh
cd bte/btd-impl-main
go test ./...
go test -race ./...
cd ../../bloc-node
go test ./...
go test -race ./...
```

- [ ] **Step 2: Re-run focused concurrency and campaign contracts**

```sh
cd bte/btd-impl-main
go test ./be -run 'TestRunSubBatchJobs|TestCombineSharesBounded.*Workers|TestCombineSharesBounded.*LowestFailure|TestCombineSharesBoundedParallelStress' -count=20
go test -race ./be -run TestCombineSharesBoundedParallelStress -count=20
cd ../..
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-race-gate-contract.sh
bash scripts/tests/test-final-campaign-terraform.sh
python3 -m unittest scripts.tests.test_campaign_artifacts
```

- [ ] **Step 3: Verify branch and diff hygiene**

```sh
git fetch origin
git merge-base --is-ancestor origin/main HEAD
git status --short
git diff --check origin/main...HEAD
git log --oneline origin/main..HEAD
```

Require a clean worktree, no untracked secrets or result artifacts, and no unrelated protocol/config changes.

- [ ] **Step 4: Perform the explicit design review checklist**

Confirm with code and test references:

- concurrency never exceeds the effective worker count;
- workers 1 and 2 return identical ordered plaintexts;
- the lowest failing sub-batch and visible attempt vector match serial behavior;
- preflight failures launch no workers and consume no attempts;
- race tests demonstrate safe shared backend reads or worker-local reconstruction;
- omitted config remains one and explicit invalid values fail;
- every required JSON/CSV/campaign layer agrees on configured two;
- successful nonempty B=512 node rows report effective two; and
- stream/RBC/ACS behavior is unchanged.

- [ ] **Step 5: Request code review and resolve findings**

Use the `superpowers:requesting-code-review` skill against `origin/main...HEAD`. Fix findings test-first, repeat affected gates, and make focused commits.

- [ ] **Step 6: Publish implementation evidence to issue #33**

Post the commit SHA, normal/race results, benchmark artifact location and summary, n=4/7/10 local smoke outcomes, campaign contract results, Terraform validations, and `STATUS.md` review outcome. Move Project Status from `In progress` to the project's review-ready state if one exists; otherwise leave it `In progress` and state that image publication is the next gated action.

- [ ] **Step 7: Stop before external mutation**

Do not build/push ECR images and do not run Terraform plan/apply or any live campaign. Hand off the clean reviewed branch for the separately executed freeze, quota/cost check, immutable image publication, exact `--validate-only`, AWS pilot, cleanup, and evidence-ingestion phase already defined by the approved specification.
