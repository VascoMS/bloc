# Issue 13 Corpus Separation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Separate the balanced client-overhead input set from the realistically weighted full-protocol workload, with 100 distinct client measurements per payload class and no weighted client summary.

**Architecture:** Add a 500-row `client-overhead-targets.jsonl` corpus with 100 unique signed targets per class while retaining the existing 100-row `mock-targets.jsonl` protocol workload. Give both files explicit validation contracts, make the client report consume every balanced target exactly once, and keep the protocol distribution methodology outside the client-result interpretation.

**Tech Stack:** Go 1.24, go-ethereum transaction types and signing, existing BTE implementation, JSONL fixtures, standard-library CSV and testing packages.

## Global Constraints

- The client corpus contains exactly 500 valid signed EIP-1559 transactions on chain ID 1337.
- The client corpus contains exactly 100 unique targets in each transfer, 128, 256, 1,024, and 4,096-byte class.
- The protocol workload remains exactly 100 unique targets distributed `28/50/12/8/2`.
- The client report emits exactly 100 measurements per class and measures each client target once.
- The client report does not cycle targets or calculate a weighted or pooled summary.
- The mainnet distribution justifies only the full-protocol workload.
- Generated CSV and cluster material remain below ignored `results/` paths.
- No full-protocol batch-128 or batch-512 scheduling policy is added in this amendment.

---

## File Structure

- Create `deploy/docker-compose/corpus/client-overhead-targets.jsonl`: balanced 500-target client corpus.
- Keep `deploy/docker-compose/corpus/mock-targets.jsonl`: weighted 100-target protocol workload.
- Modify `mempool-il/internal/mempool/corpus.go`: named client and protocol corpus contracts plus shared strict validation.
- Modify `mempool-il/internal/mempool/corpus_test.go`: deterministic generation and committed-contract tests for both files.
- Modify `mempool-il/internal/mempool/corpus_report.go`: exact non-cycling client sampling.
- Modify `mempool-il/internal/mempool/corpus_report_test.go`: unique-target and exact-count behavior.
- Modify `mempool-il/cmd/corpus-report/main_test.go`: reject sample counts other than 100.
- Modify `mempool-il/README.md`: separate client and protocol workload instructions.
- Modify `docs/modules/mempool-il.md`: describe the two corpus roles.
- Modify `docs/VALIDATION.md`: update RQ4 acceptance and evidence command.
- Modify `docs/CHANGELOG.md`: record the methodology correction.
- Modify `docs/STATUS.md`: bind the regenerated artifact to its reviewed source and checksum.

---

### Task 1: Add explicit balanced-client and weighted-protocol corpus contracts

**Files:**
- Create: `deploy/docker-compose/corpus/client-overhead-targets.jsonl`
- Modify: `mempool-il/internal/mempool/corpus.go`
- Modify: `mempool-il/internal/mempool/corpus_test.go`

**Interfaces:**
- Consumes: `readTargetCorpus(path string) ([]parsedTargetTx, error)`.
- Produces:
  - `var clientOverheadCorpusClasses []corpusClassSpec`
  - `var protocolWorkloadClasses []corpusClassSpec`
  - `func readClientOverheadCorpus(path string) ([]parsedTargetTx, error)`
  - `func readProtocolWorkloadCorpus(path string) ([]parsedTargetTx, error)`
  - shared private strict validation used by both readers.

- [ ] **Step 1: Write failing committed-corpus contract tests**

Change the existing client corpus test to use the new file and exact balanced
contract:

```go
func TestCommittedClientOverheadCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "client-overhead-targets.jsonl")
	targets, err := readClientOverheadCorpus(path)
	if err != nil {
		t.Fatalf("read client corpus: %v", err)
	}
	if got, want := len(targets), 500; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	assertCorpusCounts(t, targets, clientOverheadCorpusClasses)
}
```

Add a separate protocol-workload contract:

```go
func TestCommittedProtocolWorkloadCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	targets, err := readProtocolWorkloadCorpus(path)
	if err != nil {
		t.Fatalf("read protocol workload: %v", err)
	}
	if got, want := len(targets), 100; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	assertCorpusCounts(t, targets, protocolWorkloadClasses)
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run 'TestCommitted(ClientOverhead|ProtocolWorkload)Corpus' -count=1
```

Expected: FAIL because the new file and named readers/contracts do not exist.

- [ ] **Step 3: Implement the two strict contracts**

Replace the single weighted `evidenceCorpusClasses` definition with:

```go
var clientOverheadCorpusClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 100},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 100},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 100},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 100},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 100},
}

var protocolWorkloadClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 28},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 50},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 12},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 8},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 2},
}
```

Refactor the existing validation body into:

```go
func readClientOverheadCorpus(path string) ([]parsedTargetTx, error) {
	return readStrictCorpus(path, "client overhead corpus", clientOverheadCorpusClasses)
}

func readProtocolWorkloadCorpus(path string) ([]parsedTargetTx, error) {
	return readStrictCorpus(path, "protocol workload corpus", protocolWorkloadClasses)
}
```

`readStrictCorpus` must retain all current chain ID, EIP-1559 type, sender,
hash uniqueness, class-label, calldata-size, and EIP-7623 gas-floor checks.

- [ ] **Step 4: Generate deterministic balanced client fixture content**

Parameterize `generateEvidenceCorpus` to accept class specifications. Preserve
the current deterministic signer, nonzero payload generation, and monotonically
unique nonces. Make `-update-corpus` rewrite only
`client-overhead-targets.jsonl`; the protocol workload remains committed and
read-only in this amendment.

Run:

```sh
cd mempool-il
go test ./internal/mempool -run TestCommittedClientOverheadCorpus -count=1 -args -update-corpus
```

- [ ] **Step 5: Verify both corpus contracts are GREEN**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run 'TestCommitted(ClientOverhead|ProtocolWorkload)Corpus|TestRead.*CorpusRejectsInvalidContracts' -count=1
```

Expected: PASS with 500 unique balanced client targets and the unchanged
100-target weighted protocol workload.

- [ ] **Step 6: Commit the corpus separation**

```sh
git add deploy/docker-compose/corpus/client-overhead-targets.jsonl \
  mempool-il/internal/mempool/corpus.go \
  mempool-il/internal/mempool/corpus_test.go
git commit -m "feat: separate client and protocol corpora"
```

---

### Task 2: Remove cycling from client-overhead measurement

**Files:**
- Modify: `mempool-il/internal/mempool/corpus_report.go`
- Modify: `mempool-il/internal/mempool/corpus_report_test.go`
- Modify: `mempool-il/cmd/corpus-report/main_test.go`

**Interfaces:**
- Consumes: `readClientOverheadCorpus` and
  `clientOverheadCorpusClasses`.
- Produces: exactly 500 stable report rows, one per distinct client target.

- [ ] **Step 1: Write a failing unique-target measurement test**

Point the report test at `client-overhead-targets.jsonl`, retain the exact
500-row and class/sample ordering checks, and assert that every measured target
hash appears once:

```go
seen := make(map[string]bool, len(rows))
for _, row := range rows {
	if seen[row.TargetHash] {
		t.Fatalf("target %s measured more than once", row.TargetHash)
	}
	seen[row.TargetHash] = true
}
if got, want := len(seen), 500; got != want {
	t.Fatalf("distinct targets = %d, want %d", got, want)
}
```

Add table cases showing both `99` and `101` samples per class are rejected with
an `exactly 100` error.

- [ ] **Step 2: Run report and CLI tests and verify RED**

Run:

```sh
cd mempool-il
go test ./internal/mempool ./cmd/corpus-report -run 'ClientOverheadRows|RunValidatesRequiredReportArguments' -count=1
```

Expected: FAIL because the report still accepts values above 100 and cycles
through the weighted source targets.

- [ ] **Step 3: Implement exact, non-cycling selection**

Change `WriteClientOverheadReport` to call `readClientOverheadCorpus`. Require
`SamplesPerClass == 100`. In `buildClientOverheadRows`, require every class to
contain exactly 100 targets and select directly:

```go
for sampleIndex := 0; sampleIndex < samplesPerClass; sampleIndex++ {
	target := classTargets[sampleIndex]
	row, err := measure(target, globalIndex)
	// preserve existing row metadata and error wrapping
}
```

Remove the modulo operation. Do not add any weighted aggregation.

- [ ] **Step 4: Verify focused report behavior is GREEN**

Run:

```sh
cd mempool-il
go test ./internal/mempool ./cmd/corpus-report -run 'ClientOverhead|RunValidatesRequiredReportArguments' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit non-cycling client sampling**

```sh
git add mempool-il/internal/mempool/corpus_report.go \
  mempool-il/internal/mempool/corpus_report_test.go \
  mempool-il/cmd/corpus-report/main_test.go
git commit -m "fix: measure distinct client targets once"
```

---

### Task 3: Update evidence documentation and regenerate the artifact

**Files:**
- Modify: `mempool-il/README.md`
- Modify: `docs/modules/mempool-il.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`
- Generate, do not commit: `results/issue-13-client-overhead/client_overhead.csv`

**Interfaces:**
- Consumes: the balanced client corpus, retained protocol workload, and existing
  `corpus-report` command.
- Produces: one documented interpretation and a regenerated ignored artifact
  bound to the implementation source.

- [ ] **Step 1: Write a failing documentation ownership check**

Run:

```sh
rg -n 'client-overhead-targets.jsonl|exactly 100 distinct|full-protocol workload|28/50/12/8/2' \
  mempool-il/README.md docs/modules/mempool-il.md docs/VALIDATION.md
```

Expected: the new client corpus path and role separation are missing.

- [ ] **Step 2: Update canonical documentation**

Document:

- `client-overhead-targets.jsonl` has 500 rows and 100 unique targets per class;
- `corpus-report` requires exactly 100 measurements per class and does not
  cycle targets;
- client results remain class-specific with no weighted summary;
- `mock-targets.jsonl` retains the `28/50/12/8/2` distribution only for the
  full-protocol mock-placeholder workload; and
- the mainnet observation justifies only that protocol workload.

Update the evidence-generation command to pass:

```sh
-corpus ../deploy/docker-compose/corpus/client-overhead-targets.jsonl
```

- [ ] **Step 3: Run the complete source validation**

Run:

```sh
cd mempool-il && go test ./... -count=1
cd ../bloc-node && go test ./... -count=1
cd ../mempool-il && go test -race ./internal/mempool \
  -run 'ClientOverhead|ReplayPlaceholder|Committed.*Corpus|SignedMockPlaceholder' \
  -count=1
cd .. && git diff --check
```

Expected: all commands exit zero.

- [ ] **Step 4: Commit the reviewed implementation and documentation source**

```sh
git add mempool-il/README.md docs/modules/mempool-il.md docs/VALIDATION.md \
  docs/CHANGELOG.md
git commit -m "docs: separate client sampling from protocol workload"
```

- [ ] **Step 5: Regenerate the ignored client artifact**

Generate fresh public development cluster material and the report from the
committed implementation source:

```sh
mkdir -p results/issue-13-client-overhead
cd bloc-node
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 128 \
  --out ../results/issue-13-client-overhead/cluster.json
cd ../mempool-il
go run ./cmd/corpus-report \
  -corpus ../deploy/docker-compose/corpus/client-overhead-targets.jsonl \
  -cluster-config ../results/issue-13-client-overhead/cluster.json \
  -out ../results/issue-13-client-overhead/client_overhead.csv \
  -slot 1 \
  -samples-per-class 100
```

Record the source SHA before generation, then verify:

```sh
wc -l results/issue-13-client-overhead/client_overhead.csv
awk -F, 'NR>1 {count[$1]++; hash[$3]++} END {
  for (class in count) print class, count[class]
  for (value in hash) if (hash[value] != 1) exit 1
}' results/issue-13-client-overhead/client_overhead.csv
shasum -a 256 results/issue-13-client-overhead/client_overhead.csv
```

Expected: 501 lines, 100 rows per class, 500 distinct target hashes, and one
recorded SHA-256.

- [ ] **Step 6: Bind live status to the regenerated artifact**

Update `docs/STATUS.md` with the new implementation source SHA, artifact
checksum, 500-distinct-target contract, and the unchanged immediate action for
issue #14. This update is required because the accepted evidence artifact
changed.

- [ ] **Step 7: Run final verification and commit status binding**

Run:

```sh
cd mempool-il && go test ./... -count=1
cd ../bloc-node && go test ./... -count=1
cd .. && git diff --check
git status --short --branch
git rev-list --left-right --count main...HEAD
```

Commit only the status binding:

```sh
git add docs/STATUS.md
git commit -m "docs: bind balanced client evidence artifact"
```

