# Bounded Parallel BTE Ciphertext Decode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add deterministic bounded parallel ciphertext decoding, propagate its worker configuration and provenance through BLOC, and retain a 30-sample local n=4/7/10 × workers=1/2/4/8 B=512 scaling campaign before any AWS decision.

**Architecture:** `ClusterBTE.DecodeBatch` remains the serial compatibility entry point and delegates to a new `DecodeBatchBounded` API. A decode-specific rolling worker window preserves ordered output and lowest-index errors while limiting speculative work; `bloc-node` independently configures `max_decode_workers` and propagates configured/effective counts through metrics and campaign artifacts.

**Tech Stack:** Go 1.24, Kyber/kilic BTE code, Go race detector and benchmarks, Bash 3.2-compatible campaign scripts, Python 3 artifact validation, JSON/CSV campaign artifacts, Terraform validation.

**Spec:** `docs/superpowers/specs/2026-09-25-parallel-bte-decode-campaign-design.md`

## Global Constraints

- Preserve ciphertext encoding, encryption, `BatchID`, Opt-2 planning, combine, threshold-share exchange, proof, AEAD, materialization, ACS, RBC, BBA, and stream semantics.
- Keep `DecodeBatch` serial-compatible; library `MaxWorkers == 0` defaults to one and negative values fail.
- Node `limits.max_decode_workers` defaults to one when omitted; explicit zero, negative values, and values above 64 fail.
- For nonempty input, effective workers equal `min(configured, len(encoded), GOMAXPROCS)`; empty input reports zero effective workers.
- Reject `len(encoded) > BMax` before worker creation or ciphertext parsing.
- Preserve ordered results, serial `BatchID`, and the lowest failing input index independently of completion order.
- Join all started workers before returning; never use a global crypto mutex as a performance fallback.
- Historical artifacts without `max_decode_workers` normalize to one; new artifacts fail closed on requested/materialized/reported mismatches.
- Treat more than 10% growth in either bytes/op or allocs/op versus worker one as material allocation growth.
- Do not allocate AWS resources in this plan; deployment requires a later explicit authorization.

## Review Focus

- Oversized input containing malformed bytes must return only the batch-size error; Task 1 proves parsing never reaches an indexed ciphertext error.
- Multiple malformed ciphertexts completing out of order must report the lowest input index and leave no worker running; Task 1 controls release order and checks the active counter reaches zero.
- Empty input must report configured workers but zero effective workers and retain existing empty-batch behavior; Tasks 1 and 3 cover both paths.
- Library zero means the compatibility default, while explicit JSON zero is invalid; Tasks 1 and 3 test both sides.
- Historical manifests may omit the new field, but any new manifest/identity/config/result disagreement must fail; Tasks 4 and 5 add omission and mutation fixtures.

---

### Task 0: Create the campaign tracker and update live status

**Files:**
- Modify: `docs/STATUS.md`
- External: one repository issue, M5 milestone, and BLOC Thesis Prototype project item

**Interfaces:**
- Consumes: the approved specification and this implementation plan.
- Produces: the task-level owner and current-status pointer required before Task 1.

- [ ] **Step 1: Verify branch and tracker state**

Run `git status --short --branch`, `git rev-list --left-right --count main...HEAD`,
`gh auth status`, and search open issues for the exact title `Parallelize BTE
ciphertext decoding`. Expected: clean task branch, authenticated CLI, and no
duplicate issue.

- [ ] **Step 2: Create and classify the issue**

Create the issue with that exact title. Its body must link the approved spec
and plan, name every canonical document from Task 6, list Tasks 1–6 as scope,
copy the validation gates, state that AWS is not authorized, and make local
evidence review a deployment prerequisite. Add it to GitHub milestone M5 and
Project 1; set Status `In progress`, Priority `High`, Area `BTE`, and Roadmap
target `M5` using field/item IDs resolved from `gh project field-list` and
`gh project item-list` rather than hard-coded IDs.

- [ ] **Step 3: Update and verify `STATUS.md`**

Add the new issue as the active parallel-decode development campaign, retain
issue #33/#34 evidence as its motivation, and set the immediate next action to
Task 1's regression-first BTE executor. Do not claim implementation or local
decode evidence yet. Run `git diff --check` and inspect the focused status diff.

- [ ] **Step 4: Commit the campaign start**

```text
git add docs/STATUS.md
git commit -m "docs: start parallel decode campaign"
```

Post the commit SHA and branch name to the issue.

---

### Task 1: BTE bounded decode API and deterministic executor

**Files:**
- Create: `bte/btd-impl-main/be/decode_parallel.go`
- Create: `bte/btd-impl-main/be/decode_parallel_test.go`
- Modify: `bte/btd-impl-main/be/cluster.go`
- Modify: `bte/btd-impl-main/be/cluster_test.go`

**Interfaces:**
- Consumes: `ClusterBTE.UnmarshalCiphertext`, `computeBatchIDFromEncoded`, and `DecodedBatch`.
- Produces: `DecodeOptions`, `DecodeStats`, `DecodeBatchBounded`, `effectiveDecodeWorkers`, and `runDecodeJobs`.

- [ ] **Step 1: Write failing normalization tests**

Add `TestEffectiveDecodeWorkers` under `runtime.GOMAXPROCS(2)` with this exact matrix:

```go
tests := []struct {
	name, wantErr string
	configured, ciphertexts, wantConfigured, wantEffective int
}{
	{"omitted", "", 0, 512, 1, 1},
	{"negative", "max decode workers must be non-negative", -1, 512, 0, 0},
	{"input-bound", "", 8, 1, 8, 1},
	{"cpu-bound", "", 8, 512, 8, 2},
	{"empty", "", 2, 0, 2, 0},
}
```

- [ ] **Step 2: Verify the test is red**

Run: `cd bte/btd-impl-main && go test ./be -run '^TestEffectiveDecodeWorkers$' -count=1`

Expected: build failure because `effectiveDecodeWorkers` does not exist.

- [ ] **Step 3: Define options, statistics, and normalization**

Add beside `DecodedBatch`:

```go
type DecodeOptions struct { MaxWorkers int }
type DecodeStats struct {
	ConfiguredWorkers int
	EffectiveWorkers int
}
```

Add to `decode_parallel.go`:

```go
const defaultDecodeWorkers = 1

func effectiveDecodeWorkers(configured, ciphertexts int) (int, int, error) {
	if configured < 0 { return 0, 0, fmt.Errorf("max decode workers must be non-negative") }
	if configured == 0 { configured = defaultDecodeWorkers }
	if ciphertexts == 0 { return configured, 0, nil }
	return configured, max(1, min(configured, ciphertexts, runtime.GOMAXPROCS(0))), nil
}
```

Run the Step 2 command. Expected: PASS.

- [ ] **Step 4: Write failing executor tests**

Add these exact tests using controlled channels and `atomic.Int32` counters:

- `TestRunDecodeJobsBoundsConcurrencyAndIndexesOutcomes`: four inputs, two workers, peak exactly two, and `outcomes[i].index == i`.
- `TestRunDecodeJobsStopsAdmissionAndDrainsAfterError`: release failing index 1 before successful index 0; no index above 1 starts and active returns to zero.
- `TestRunDecodeJobsSelectsLowestStartedError`: index 2 fails first and index 0 last; scanning indexed outcomes selects zero.
- `TestRunDecodeJobsRejectsInvalidArguments`: nonempty input rejects zero/negative workers and a nil decoder before work starts.

Use this seam:

```go
type decodeFunc func(index int, raw []byte) (Ciphertext, error)
func runDecodeJobs(workerCount int, encoded [][]byte, decode decodeFunc) ([]decodeOutcome, error)
```

- [ ] **Step 5: Verify executor tests are red**

Run: `cd bte/btd-impl-main && go test ./be -run '^TestRunDecodeJobs' -count=1`

Expected: build failure because the executor is absent.

- [ ] **Step 6: Implement the rolling worker window**

Define:

```go
type decodeJob struct { index int; raw []byte }
type decodeOutcome struct { index int; ciphertext Ciphertext; err error }
```

`runDecodeJobs` must validate its arguments, launch exactly `workerCount`
workers, initially admit `min(workerCount, len(encoded))` ascending jobs, and
admit one new ascending job after each success. On the first observed error it
stops admission, drains all started outcomes, closes the job channel, waits for
all workers, and returns the indexed prefix of started outcomes. One coordinator
owns all sends, collection, and channel closure.

Run the Step 5 command. Expected: PASS.

- [ ] **Step 7: Write failing public API tests**

Add:

- `TestDecodeBatchBoundedMatchesSerialOrderAndBatchID` for eight distinct inputs and workers 1/4.
- `TestDecodeBatchBoundedReportsLowestMalformedIndex` with failures at indices 1 and 6.
- `TestDecodeBatchBoundedRejectsOversizedBeforeDecode` using BMax two and three malformed values.
- `TestDecodeBatchBoundedEmptyStats` requiring configured two/effective zero.
- `TestDecodeBatchRemainsSerialCompatibilityWrapper` comparing worker one byte-for-byte.
- `TestDecodeBatchBoundedSameClusterStress` with 20 concurrent calls against one cluster and a serial oracle.

- [ ] **Step 8: Verify public API tests are red**

Run: `cd bte/btd-impl-main && go test ./be -run '^TestDecodeBatch(Bounded|Remains)' -count=1`

Expected: build failure because `DecodeBatchBounded` is absent.

- [ ] **Step 9: Implement the API**

Implement this contract in `cluster.go`:

```go
func (c *ClusterBTE) DecodeBatch(encoded [][]byte) (DecodedBatch, error) {
	decoded, _, err := c.DecodeBatchBounded(encoded, DecodeOptions{MaxWorkers: 1})
	return decoded, err
}

func (c *ClusterBTE) DecodeBatchBounded(encoded [][]byte, options DecodeOptions) (DecodedBatch, DecodeStats, error)
```

Normalize workers, reject BMax before calling `runDecodeJobs`, decode through
`c.UnmarshalCiphertext`, scan the started outcomes in index order for the first
error, populate a fixed-length ciphertext slice by index, and compute
`batchID` from the original encodings only after complete success. Document
input immutability during the call.

- [ ] **Step 10: Verify focused, race, and complete BTE behavior**

Run:

```text
cd bte/btd-impl-main
go test ./be -run '^Test(EffectiveDecodeWorkers|RunDecodeJobs|DecodeBatch)' -count=1
go test -race ./be -run '^TestDecodeBatchBoundedSameClusterStress$' -count=20
go test ./...
git diff --check
```

Expected: PASS. A backend race stops the task; do not add a global mutex.

- [ ] **Step 11: Commit**

```text
git add bte/btd-impl-main/be/cluster.go bte/btd-impl-main/be/cluster_test.go bte/btd-impl-main/be/decode_parallel.go bte/btd-impl-main/be/decode_parallel_test.go
git commit -m "feat(bte): add bounded parallel ciphertext decoding"
```

---

### Task 2: B=512 decode scaling benchmark

**Files:**
- Create: `bte/btd-impl-main/be/decode_benchmark_test.go`

**Interfaces:**
- Consumes: `DecodeBatchBounded`, `DecodeOptions`, and `DecodeStats` from Task 1.
- Produces: `BenchmarkDecodeBatchBoundedB512` and its exact 12-leaf matrix for Task 6.

- [ ] **Step 1: Write the failing benchmark-matrix test**

Define `decodeBenchmarkCase`, `decodeBenchmarkCommittees`, and
`decodeBenchmarkCases`. Add `TestDecodeBenchmarkCasesCoverWorkerScalingMatrix`
requiring n4/t3, n7/t5, and n10/t7, each with workers 1, 2, 4, and 8 in that
order. Assert all fields for all 12 leaves, matching the existing combine
benchmark's matrix test style.

- [ ] **Step 2: Verify the matrix test is red**

Run: `cd bte/btd-impl-main && go test ./be -run '^TestDecodeBenchmarkCasesCoverWorkerScalingMatrix$' -count=1`

Expected: build failure because the helpers are absent.

- [ ] **Step 3: Implement the fixture and benchmark**

Build one fixture per committee outside the timer. Encrypt and canonically
encode 512 distinct transactions, obtain the serial decoded batch and planned
`BatchID`, and retain both as the oracle. Implement:

```go
func BenchmarkDecodeBatchBoundedB512(b *testing.B) {
	for _, committee := range decodeBenchmarkCommittees() {
		b.Run(committee.name, func(b *testing.B) {
			fixture := newDecodeBenchmarkFixture(b, 512, committee.n, committee.threshold)
			for _, workers := range []int{1, 2, 4, 8} {
				b.Run(fmt.Sprintf("workers-%d", workers), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						decoded, stats, err := fixture.cluster.DecodeBatchBounded(fixture.encoded, DecodeOptions{MaxWorkers: workers})
						b.StopTimer()
						fixture.validate(b, workers, decoded, stats, err)
						b.ReportMetric(float64(stats.ConfiguredWorkers), "configured_workers")
						b.ReportMetric(float64(stats.EffectiveWorkers), "effective_workers")
						b.ReportMetric(512, "ciphertexts")
						if i+1 < b.N { b.StartTimer() }
					}
				})
			}
		})
	}
}
```

`validate` must compare all 512 re-encodings in order, compare the planned
batch ID with the serial oracle, and require exact configured/effective counts.

- [ ] **Step 4: Verify the matrix and one-sample benchmark**

Run:

```text
cd bte/btd-impl-main
go test ./be -run '^TestDecodeBenchmarkCasesCoverWorkerScalingMatrix$' -count=1
GOMAXPROCS=8 go test ./be -run '^$' -bench '^BenchmarkDecodeBatchBoundedB512$' -benchtime=1x -count=1 -benchmem
```

Expected: PASS and exactly 12 result leaves, each reporting 512 ciphertexts.

- [ ] **Step 5: Commit**

```text
git add bte/btd-impl-main/be/decode_benchmark_test.go
git commit -m "test(bte): add ciphertext decode scaling matrix"
```

---

### Task 3: Node configuration, runtime wiring, and metrics

**Files:**
- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/ec2_config.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/resource_safety_test.go`
- Modify: `bloc-node/internal/app/config_security_test.go`
- Create: `bloc-node/internal/app/node_decode_test.go`

**Interfaces:**
- Consumes: `be.DecodeBatchBounded` and `be.DecodeOptions` from Task 1.
- Produces: `ResourceLimits.MaxDecodeWorkers`, `Metrics.DecodeWorkersConfigured`, and `Metrics.DecodeWorkersEffective` for Task 4.

- [ ] **Step 1: Write failing configuration tests**

Extend `resource_safety_test.go` so omitted JSON normalizes to one and these
documents fail with `limits.max_decode_workers`:

```go
[]string{
	`{"max_decode_workers":0}`,
	`{"max_decode_workers":-1}`,
	`{"max_decode_workers":65}`,
}
```

Extend `config_security_test.go` to assert generated default one and explicit
`--max-decode-workers 2` value two.

- [ ] **Step 2: Verify configuration tests are red**

Run: `cd bloc-node && go test ./internal/app -run '^Test(ResourceLimits|Generated|EC2Config).*Decode' -count=1`

Expected: failures for the missing field and flag.

- [ ] **Step 3: Add strict defaults, validation, and generator support**

Follow the combine-worker presence pattern exactly:

```go
const defaultMaxDecodeWorkers = 1
const absoluteMaxDecodeWorkers = 64

type ResourceLimits struct {
	// existing fields remain unchanged
	MaxDecodeWorkers int `json:"max_decode_workers,omitempty"`
	explicitZeroDecodeWorkers bool
}
```

Add `*int` presence tracking in `UnmarshalJSON`, omitted normalization,
`[1,64]` validation, and `--max-decode-workers` in normal and EC2 config
generation. Run the Step 2 command. Expected: PASS.

- [ ] **Step 4: Write a failing runtime metrics test**

In `node_decode_test.go`, construct a small node fixture with decode workers two
and pass a successful two-ciphertext ACS result through `handleACSOutput`.
Require:

```go
if node.metrics.DecodeWorkersConfigured != 2 { t.Fatal("configured decode workers not recorded") }
if node.metrics.DecodeWorkersEffective != 2 { t.Fatal("effective decode workers not recorded") }
```

Add an empty merged-set case requiring configured two and effective zero.

- [ ] **Step 5: Verify runtime tests are red**

Run: `cd bloc-node && go test ./internal/app -run '^TestNodeRecordsDecodeWorkers' -count=1`

Expected: build failure because the metrics fields are absent.

- [ ] **Step 6: Wire bounded decoding and record stats**

Add:

```go
DecodeWorkersConfigured int `json:"decode_workers_configured"`
DecodeWorkersEffective int `json:"decode_workers_effective"`
```

Replace the node decode call with:

```go
decodedBatch, decodeStats, err := n.cluster.DecodeBatchBounded(
	encodedCiphertexts,
	be.DecodeOptions{MaxWorkers: n.cfg.Limits.MaxDecodeWorkers},
)
n.mu.Lock()
n.metrics.DecodeWorkersConfigured = decodeStats.ConfiguredWorkers
n.metrics.DecodeWorkersEffective = decodeStats.EffectiveWorkers
n.mu.Unlock()
```

Record stats before the error and empty-batch branches. Keep the existing
`ciphertext_decode_us` boundary unchanged.

- [ ] **Step 7: Verify focused and complete node behavior**

Run:

```text
cd bloc-node
go test ./internal/app -run '^Test(NodeRecordsDecodeWorkers|ResourceLimits|Generated|EC2Config)' -count=1
go test ./...
git diff --check
```

Expected: PASS.

- [ ] **Step 8: Commit**

```text
git add bloc-node/internal/app/types.go bloc-node/internal/app/config.go bloc-node/internal/app/ec2_config.go bloc-node/internal/app/node.go bloc-node/internal/app/resource_safety_test.go bloc-node/internal/app/config_security_test.go bloc-node/internal/app/node_decode_test.go
git commit -m "feat(node): configure parallel ciphertext decoding"
```

---

### Task 4: Evaluator and Go artifact provenance

**Files:**
- Modify: `bloc-node/internal/app/eval.go`
- Modify: `bloc-node/internal/app/eval_suite.go`
- Modify: `bloc-node/internal/app/eval_persistent.go`
- Modify: `bloc-node/internal/app/eval_remote.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`
- Modify: `bloc-node/internal/app/deployment_test.go`
- Modify: `bloc-node/internal/app/campaign_identity.go`
- Modify: `bloc-node/internal/app/campaign_identity_test.go`
- Modify: `bloc-node/internal/app/campaign_bundle.go`
- Modify: `bloc-node/internal/app/campaign_bundle_test.go`
- Modify: `bloc-node/internal/app/campaign_materialize.go`
- Modify: `bloc-node/internal/app/campaign_materialize_test.go`

**Interfaces:**
- Consumes: node config and decode metrics from Task 3.
- Produces: `max_decode_workers` in evaluator/campaign inputs and `effective_decode_workers` in node/run CSV rows.

- [ ] **Step 1: Write failing propagation tests**

Extend evaluator tests to require `EvalRun.MaxDecodeWorkers == 2`, generated
cluster config value two, and result metrics effective value two. Extend CSV
header assertions to place `max_decode_workers` after `max_combine_workers` and
`effective_decode_workers` after `effective_combine_workers`.

Extend campaign tests with these exact cases:

- identity omission normalizes decode workers to one;
- manifest omission normalizes decode workers to one;
- manifest two versus identity one returns `campaign decode workers mismatch`;
- materialized cluster and remote configs both carry two.

- [ ] **Step 2: Verify focused tests are red**

Run: `cd bloc-node && go test ./internal/app -run '^Test(Eval|Campaign|Materialize|Remote).*Workers' -count=1`

Expected: build or assertion failures for missing fields.

- [ ] **Step 3: Propagate evaluator options and result rows**

Add `MaxDecodeWorkers int` with JSON name `max_decode_workers` to `EvalRun`,
`suiteManifest`, and `suiteOptions`. Add `--max-decode-workers` to `eval-local`,
`eval-suite`, persistent config generation, and remote invocation. Extend
`runLocalExperiment` with `maxDecodeWorkers` immediately after
`maxCombineWorkers` and add:

```go
"--max-decode-workers", strconv.Itoa(maxDecodeWorkers),
```

Validate `[1,64]`. Make persistent cluster validation compare both worker
limits. Add configured/effective decode fields to run/node CSV headers and rows
without changing existing columns.

- [ ] **Step 4: Propagate identity, bundle, and materialization**

Use `ResourceLimits.MaxDecodeWorkers` in campaign identity and add:

```go
MaxDecodeWorkers int `json:"max_decode_workers,omitempty"`
```

to the bundle manifest. Decode manifest presence through `*int`, normalizing
omission to one; validate `[1,64]`; compare identity and manifest; materialize
the value into cluster and remote evaluator configs.

- [ ] **Step 5: Verify focused and complete node suites**

Run:

```text
cd bloc-node
go test ./internal/app -run '^Test(Eval|Campaign|Materialize|Remote).*Workers' -count=1
go test ./...
git diff --check
```

Expected: PASS.

- [ ] **Step 6: Commit**

```text
git add bloc-node/internal/app
git commit -m "feat(eval): bind ciphertext decode worker provenance"
```

---

### Task 5: Strict mempool reader and deployment contracts

**Files:**
- Modify: `mempool-il/internal/mempool/encrypted_corpus.go`
- Modify: `mempool-il/internal/mempool/encrypted_corpus_test.go`
- Modify: `scripts/lib/final-campaign-contract.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `scripts/lib/campaign_artifacts.py`
- Modify: `scripts/tests/test-final-campaign-contract.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test_campaign_artifacts.py`
- Modify: `scripts/test-campaign-runners.sh`

**Interfaces:**
- Consumes: decode-worker manifest, identity, config, and result fields from Tasks 3 and 4.
- Produces: backward-compatible strict decoding and fail-closed validation for a later authorized AWS stage.

- [ ] **Step 1: Write failing strict-reader and mutation tests**

Add `"max_decode_workers": 2` to the mempool campaign identity fixture and
assert acceptance. Add a historical omission fixture that normalizes to one;
keep unknown-field rejection strict.

Add Python/Bash rejection fixtures for:

- invocation two versus manifest one;
- manifest two versus identity one;
- identity two versus materialized cluster one;
- expected two versus run row one;
- expected two versus node effective one for a nonempty B=512 run.

- [ ] **Step 2: Verify contract tests are red**

Run:

```text
cd mempool-il
go test ./internal/mempool -run '^Test.*CampaignIdentity' -count=1
cd ..
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
python3 scripts/tests/test_campaign_artifacts.py
```

Expected: failures for the unrecognized or unbound field.

- [ ] **Step 3: Extend the strict mempool identity**

Add beside the combine field:

```go
MaxDecodeWorkers int `json:"max_decode_workers,omitempty"`
```

Normalize omission to one and validate `[1,64]`. Preserve
`DisallowUnknownFields`.

- [ ] **Step 4: Extend campaign arguments and lifecycle provenance**

Add `--max-decode-workers 1..64` with default one:

```text
FINAL_MAX_DECODE_WORKERS=1
--max-decode-workers) final_take_value "$1" "${2-}" || return; FINAL_MAX_DECODE_WORKERS="$2"; shift 2 ;;
```

Validate the range, bind invocation to manifest and identity using omission as
one, pass the value to materialization/evaluator commands, and write it into
the lifecycle manifest. Do not force historical B=512 artifacts to worker two;
only an explicit new invocation and matching bundle select two.

- [ ] **Step 5: Extend Python artifact checks**

Validate `max_decode_workers` across expected campaign identity, run rows, and
node rows. For nonempty successful `t3.small` runs, require effective workers
to equal `min(configured, selected_ciphertexts, 2)`; derive the vCPU count from
the deployment contract, never from the controller host.

- [ ] **Step 6: Verify complete module and contract behavior**

Run:

```text
cd mempool-il
go test ./...
cd ..
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
python3 scripts/tests/test_campaign_artifacts.py
bash scripts/test-campaign-runners.sh
git diff --check
```

Expected: PASS.

- [ ] **Step 7: Commit**

```text
git add mempool-il/internal/mempool scripts/lib scripts/tests scripts/test-campaign-runners.sh
git commit -m "feat(campaign): validate decode worker provenance"
```

---

### Task 6: Repeatable local campaign, full validation, and canonical docs

**Files:**
- Create: `bloc-node/scripts/run-parallel-decode-campaign.sh`
- Modify: `scripts/test-campaign-runners.sh`
- Modify: `bte/btd-impl-main/README.md`
- Modify: `bte/btd-impl-main/TESTING.md`
- Modify: `bloc-node/README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/modules/bte.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`
- Modify: `deploy/ec2/README.md`
- Generated but ignored: per-campaign directories under `results/local/parallel-decode/`

**Interfaces:**
- Consumes: benchmark, evaluator flags, and artifact validators from Tasks 1–5.
- Produces: reproducible retained local evidence and a deployment-candidate decision; no AWS resources.

- [ ] **Step 1: Write a failing runner-contract check**

Extend `scripts/test-campaign-runners.sh` to require the new runner and execute:

```text
bash bloc-node/scripts/run-parallel-decode-campaign.sh --campaign-id validate-only --validate-only
```

Require invalid IDs and unknown arguments to exit two. Snapshot the matching
results path before and after `--validate-only` and assert it is not created.

- [ ] **Step 2: Verify the runner check is red**

Run: `bash scripts/test-campaign-runners.sh`

Expected: failure because the runner is absent.

- [ ] **Step 3: Implement the Bash 3.2-compatible runner**

Follow `run-merge-plan-campaign.sh` and `campaign-common.sh`. Accept only
`--campaign-id`, `--resume`, `--report-only`, and `--validate-only`; write one
validated ID directory under `results/local/parallel-decode/`; record every command.

The benchmark stage is exactly:

```text
env GOMAXPROCS=8 go test ./be -run '^$' -bench '^BenchmarkDecodeBatchBoundedB512$' -benchtime=1x -count=30 -benchmem -timeout=90m
```

The integrated smoke stage runs twice from `bloc-node`, first with
`--max-decode-workers 1` as the serial-compatible control and then with
`--max-decode-workers 2` as the candidate:

```text
env GOMAXPROCS=2 go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7,10 --batch-sizes 512 --bmax 512 --max-combine-workers 2 --max-decode-workers 1 --warmups 1 --repetitions 3 --repetition-blocks 1 --stream-mode persistent-lanes --timeout 30s --deadline 12s --seed 20260621 --experiment-id parallel-decode-control --out-dir "$campaign_root/smoke/control"
env GOMAXPROCS=2 go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7,10 --batch-sizes 512 --bmax 512 --max-combine-workers 2 --max-decode-workers 2 --warmups 1 --repetitions 3 --repetition-blocks 1 --stream-mode persistent-lanes --timeout 30s --deadline 12s --seed 20260621 --experiment-id parallel-decode-candidate --out-dir "$campaign_root/smoke/candidate"
```

Here `campaign_root` is the runner's validated absolute result root. The
manifest records source SHA, clean/dirty status, Go version, OS/architecture,
both `GOMAXPROCS` values, the exact matrix, and all commands.

`--report-only` must validate 30 samples for every one of 12 leaves, calculate
p50/p95 and allocation summaries, validate all six control/candidate scenario
groups, and write
`decode-scaling.csv` and `decision.json`. Worker two qualifies only if decode
p50 improves for every committee, neither bytes/op nor allocs/op grows by more
than 10% relative to that committee's worker-one median, and integrated
total-slot p50 regression is
at most 5%; otherwise the decision records no deployment candidate and lists
the failed gates.

- [ ] **Step 4: Verify runner portability and side-effect-free validation**

Run:

```text
bash bloc-node/scripts/run-parallel-decode-campaign.sh --campaign-id validate-only --validate-only
bash scripts/test-campaign-runners.sh
```

Expected: PASS and no validation-only output path.

- [ ] **Step 5: Run complete pre-campaign validation**

Run:

```text
cd bte/btd-impl-main
go test ./...
go test -race ./be
go test -race ./be -run '^TestDecodeBatchBoundedSameClusterStress$' -count=20
cd ../../bloc-node
go test ./...
cd ../mempool-il
go test ./...
cd ..
bash scripts/tests/run-final-campaign-race-gate.sh
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
python3 scripts/tests/test_campaign_artifacts.py
bash scripts/tests/test-final-campaign-terraform.sh
bash scripts/test-campaign-runners.sh
git diff --check
```

Expected: every command PASS. Retain any timeout output and request an explicit
decision; never represent a timeout as a pass.

- [ ] **Step 6: Run and retain the local campaign**

From the repository root:

```text
bash bloc-node/scripts/run-parallel-decode-campaign.sh --campaign-id b512-workers-1-2-4-8-20260925
```

Expected: 360 benchmark observations, successful and consistent control and
candidate smokes for n=4/7/10, manifest/checksums, and a generated candidate
decision under the ignored result root.

- [ ] **Step 7: Update canonical documentation from verified evidence**

Record only conclusions present in retained artifacts:

- BTE README/TESTING and deep dive: API, defaults, errors, race result,
  benchmark command, and scaling.
- Node README/deep dive and architecture: config, cap, metrics, unchanged phase
  timing, and data flow.
- Validation and EC2 runbook: provenance, local gate, and separately authorized
  AWS procedure.
- Decisions and changelog: selected architecture and implementation history.
- Status: active issue, accepted/rejected local evidence, baseline, blockers,
  and immediate action; do not claim AWS evidence.

- [ ] **Step 8: Verify docs and commit campaign support**

Run:

```text
rg -n 'max_decode_workers|DecodeBatchBounded|parallel decode' README.md docs bloc-node/README.md bte/btd-impl-main/README.md bte/btd-impl-main/TESTING.md deploy/ec2/README.md
git diff --check
git status --short
```

Expected: new behavior has a canonical owner and generated results remain
ignored. Commit:

```text
git add bloc-node/scripts/run-parallel-decode-campaign.sh scripts/test-campaign-runners.sh bte/btd-impl-main/README.md bte/btd-impl-main/TESTING.md bloc-node/README.md docs deploy/ec2/README.md
git commit -m "docs: record parallel decode campaign evidence"
```

- [ ] **Step 9: Final review and handoff**

Fetch remote refs, prove ancestry/divergence, inspect the whole branch diff,
and post focused validation/evidence summaries to the campaign issue. Report
branch, commits, validation, retained ignored evidence, publication state, and
the `STATUS.md` outcome. Do not push, merge, build images, or start AWS without
the corresponding user authorization.
