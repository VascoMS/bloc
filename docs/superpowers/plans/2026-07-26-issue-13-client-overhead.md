# Issue 13 Client-Overhead Corpus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a 100-transaction representative Ethereum corpus and a local report that records at least 100 plaintext/encrypted client-overhead measurements per transaction class.

**Architecture:** Keep the existing replay corpus loader permissive for ordinary fixtures, and add a strict evidence-corpus validator for issue #13. Split replay encryption from placeholder assembly so the benchmark can time encryption without duplicating protocol logic. A thin `cmd/corpus-report` wrapper will validate the corpus, run stable class-ordered samples through the shared path, and atomically write CSV output.

**Tech Stack:** Go 1.24, go-ethereum transaction types and signing, existing `btd/be` BTE implementation, standard-library JSON/CSV/flag/time packages.

## Global Constraints

- The committed corpus contains exactly 100 valid signed EIP-1559 transactions on chain ID 1337.
- Corpus distribution is exactly `28/50/12/8/2` for transfer, 128, 256, 1,024, and 4,096-byte calldata classes.
- Every corpus hash is unique and every declared class matches decoded calldata length.
- Fixed key derivation is development-only and must be documented as unsafe for live-chain funds.
- The report produces at least 100 raw measurements per class from identical target bytes on plaintext and encrypted paths.
- CSV columns are `class,sample_index,target_hash,raw_bytes,ciphertext_bytes,placeholder_bytes,calldata_bytes,carrier_gas_estimate,encryption_us,submission_serialization_us`.
- Encryption timing excludes placeholder signing and encoding; submission serialization timing excludes network I/O.
- Carrier gas is an EIP-7623 data-only estimate, not paid gas.
- Generated CSV and generated cluster material remain below ignored `results/` paths.
- No mainnet sampler, live submission, cloud operation, or production-readiness claim is added.

---

## File Structure

- Create `mempool-il/internal/mempool/corpus.go`: evidence class definitions and strict corpus validation.
- Create `mempool-il/internal/mempool/corpus_test.go`: corpus contract, invalid-corpus cases, and deterministic development fixture generation.
- Modify `mempool-il/internal/mempool/replay_placeholder.go`: retain declared class metadata and split encryption from placeholder construction.
- Modify `mempool-il/internal/mempool/replay_placeholder_test.go`: cover the split encryption/assembly boundary.
- Create `mempool-il/internal/mempool/corpus_report.go`: measurement, sampling, gas estimation, and atomic CSV writing.
- Create `mempool-il/internal/mempool/corpus_report_test.go`: schema, sample-count, ordering, measurement-definition, and error tests.
- Create `mempool-il/cmd/corpus-report/main.go`: local CLI flag parsing and report invocation.
- Create `mempool-il/cmd/corpus-report/main_test.go`: required-flag and minimum-sample validation.
- Replace `deploy/docker-compose/corpus/mock-targets.jsonl`: the labelled 100-row corpus.
- Modify `mempool-il/README.md`: usage, corpus contract, methodology, and result semantics.
- Modify `docs/modules/mempool-il.md`: internal data flow and limitations.
- Modify `docs/VALIDATION.md`: evidence-generation command and acceptance checks.
- Modify `docs/CHANGELOG.md`: issue #13 implementation/evidence entry.
- Modify `docs/STATUS.md`: accepted artifact and issue #14 as the immediate next action.

---

### Task 1: Define and populate the strict evidence corpus

**Files:**
- Create: `mempool-il/internal/mempool/corpus.go`
- Create: `mempool-il/internal/mempool/corpus_test.go`
- Modify: `mempool-il/internal/mempool/replay_placeholder.go`
- Replace: `deploy/docker-compose/corpus/mock-targets.jsonl`

**Interfaces:**
- Consumes: existing `readTargetCorpus(path string) ([]parsedTargetTx, error)` and `parseTargetRawTx(rawHex string) (parsedTargetTx, error)`.
- Produces:
  - `type corpusClass string`
  - `var evidenceCorpusClasses []corpusClassSpec`
  - `func readEvidenceCorpus(path string) ([]parsedTargetTx, error)`
  - `parsedTargetTx.DeclaredClass corpusClass`
  - `parsedTargetTx.EvidenceClass corpusClass`

- [ ] **Step 1: Add the failing committed-corpus contract test**

Create a test that reads the repository corpus and asserts the exact contract:

```go
func TestCommittedEvidenceCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	targets, err := readEvidenceCorpus(path)
	if err != nil {
		t.Fatalf("read evidence corpus: %v", err)
	}
	if got, want := len(targets), 100; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	counts := map[corpusClass]int{}
	hashes := map[string]bool{}
	for _, target := range targets {
		counts[target.EvidenceClass]++
		if hashes[target.Summary.Hash] {
			t.Fatalf("duplicate hash %s", target.Summary.Hash)
		}
		hashes[target.Summary.Hash] = true
	}
	for _, spec := range evidenceCorpusClasses {
		if got := counts[spec.Name]; got != spec.Rows {
			t.Fatalf("%s rows = %d, want %d", spec.Name, got, spec.Rows)
		}
	}
}
```

- [ ] **Step 2: Run the test and confirm the current four-row fixture fails**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run TestCommittedEvidenceCorpus -count=1
```

Expected: FAIL because `readEvidenceCorpus` is undefined, followed by a
four-versus-100 contract failure once the validator exists.

- [ ] **Step 3: Implement class definitions and strict validation**

Add these definitions in `corpus.go`:

```go
type corpusClass string

const (
	corpusClassTransfer corpusClass = "transfer"
	corpusClass128      corpusClass = "calldata_128"
	corpusClass256      corpusClass = "calldata_256"
	corpusClass1024     corpusClass = "calldata_1024"
	corpusClass4096     corpusClass = "calldata_4096"
)

type corpusClassSpec struct {
	Name          corpusClass
	CalldataBytes int
	Rows          int
}

var evidenceCorpusClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 28},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 50},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 12},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 8},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 2},
}
```

Extend the corpus JSON shape and parsed target without making ordinary replay
fixtures strict:

```go
type targetCorpusEntry struct {
	Class corpusClass `json:"class,omitempty"`
	RawTx string      `json:"raw_tx"`
}

type parsedTargetTx struct {
	DeclaredClass corpusClass
	EvidenceClass corpusClass
	Raw           []byte
	Tx            types.Transaction
	Summary       txSummary
}
```

`readEvidenceCorpus` must call the existing loader and then reject:

- any count other than 100;
- non-EIP-1559 transactions;
- a chain ID other than 1337;
- unknown, absent, or mismatched class labels;
- duplicate transaction hashes;
- calldata lengths other than the class's exact length; and
- class counts that differ from `28/50/12/8/2`.

- [ ] **Step 4: Add failing validation tests for malformed evidence corpora**

Use table-driven tests covering duplicate hashes, wrong chain ID, wrong
calldata length, wrong label, missing label, and wrong distribution. Each case
must assert a stable error fragment such as `duplicate transaction hash`,
`chain id`, `declared class`, or `class distribution`.

- [ ] **Step 5: Add deterministic development-corpus generation to the test**

In `corpus_test.go`, derive a test-only signer from a public label plus counter,
create EIP-1559 transactions on chain 1337, and generate nonzero deterministic
payload bytes:

```go
seed := sha256.Sum256([]byte("bloc-issue-13-development-corpus-signer"))
key, err := ethcrypto.ToECDSA(seed[:])

data := make([]byte, spec.CalldataBytes)
for byteIndex := range data {
	data[byteIndex] = byte((globalIndex+byteIndex)%255 + 1)
}

gas := uint64(21_000 + 40*len(data))
tx := types.NewTx(&types.DynamicFeeTx{
	ChainID:   big.NewInt(1337),
	Nonce:     uint64(globalIndex),
	GasTipCap: big.NewInt(1),
	GasFeeCap: big.NewInt(100),
	Gas:       gas,
	To:        &target,
	Value:     big.NewInt(0),
	Data:      data,
})
```

Add an explicit `-update-corpus` test flag. Without that flag the test is
read-only and compares the committed lines with deterministic expected lines.
With it, the test rewrites only
`deploy/docker-compose/corpus/mock-targets.jsonl`.

Document in the test that the publicly derivable signer is unsafe for live
funds and is used only on development chain 1337.

- [ ] **Step 6: Generate the corpus and rerun corpus tests**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run TestCommittedEvidenceCorpus -count=1 \
  -args -update-corpus
go test ./internal/mempool -run 'TestCommittedEvidenceCorpus|TestReadEvidenceCorpus' -count=1
```

Expected: PASS, with exactly 100 labelled JSONL rows and no duplicate hashes.

- [ ] **Step 7: Commit the corpus contract**

```sh
git add mempool-il/internal/mempool/corpus.go \
  mempool-il/internal/mempool/corpus_test.go \
  mempool-il/internal/mempool/replay_placeholder.go \
  deploy/docker-compose/corpus/mock-targets.jsonl
git commit -m "feat: add client overhead evidence corpus"
```

---

### Task 2: Split replay encryption from placeholder assembly

**Files:**
- Modify: `mempool-il/internal/mempool/replay_placeholder.go`
- Modify: `mempool-il/internal/mempool/replay_placeholder_test.go`

**Interfaces:**
- Consumes: `parsedTargetTx`, `replayCluster`, `*be.ClusterBTE`,
  `buildPlaceholderCalldata`, and `signMockPlaceholderTx`.
- Produces:
  - `func encryptReplayTarget(target parsedTargetTx, index int, slot uint64, cluster replayCluster, encryptor *be.ClusterBTE) ([]byte, error)`
  - `func buildMockPlaceholderFromCiphertext(target parsedTargetTx, index int, encoded []byte) (Transaction, error)`
  - Existing `buildMockPlaceholder(...)` remains behavior-compatible.

- [ ] **Step 1: Write a failing boundary-preservation test**

Extend the replay fixture test to call `encryptReplayTarget`, pass its bytes to
`buildMockPlaceholderFromCiphertext`, decrypt the parsed payload, and assert
that it equals the original signed target bytes.

- [ ] **Step 2: Run the focused replay test and verify it fails**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run TestReplayPlaceholderSplitEncryptionBoundary -count=1
```

Expected: FAIL because the two boundary functions do not exist.

- [ ] **Step 3: Extract the two focused functions**

Move only the `EncryptTx` and `MarshalBinary` operations into
`encryptReplayTarget`. Move calldata construction, placeholder signing,
normalization, and classifier validation into
`buildMockPlaceholderFromCiphertext`. Keep `buildMockPlaceholder` as:

```go
func buildMockPlaceholder(target parsedTargetTx, index int, slot uint64, cluster replayCluster, encryptor *be.ClusterBTE) (Transaction, error) {
	encoded, err := encryptReplayTarget(target, index, slot, cluster, encryptor)
	if err != nil {
		return Transaction{}, err
	}
	return buildMockPlaceholderFromCiphertext(target, index, encoded)
}
```

- [ ] **Step 4: Run replay tests**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run ReplayPlaceholder -count=1
```

Expected: PASS, including existing cache and decryption behavior.

- [ ] **Step 5: Commit the refactor**

```sh
git add mempool-il/internal/mempool/replay_placeholder.go \
  mempool-il/internal/mempool/replay_placeholder_test.go
git commit -m "refactor: expose replay encryption boundary"
```

---

### Task 3: Implement client-overhead measurement and CSV reporting

**Files:**
- Create: `mempool-il/internal/mempool/corpus_report.go`
- Create: `mempool-il/internal/mempool/corpus_report_test.go`

**Interfaces:**
- Consumes: `readEvidenceCorpus`, `readReplayCluster`,
  `newReplayEncryptor`, `encryptReplayTarget`, and
  `buildMockPlaceholderFromCiphertext`.
- Produces:

```go
type ClientOverheadConfig struct {
	CorpusPath      string
	ClusterPath     string
	OutputPath      string
	Slot            uint64
	SamplesPerClass int
}

type ClientOverheadRow struct {
	Class                     string
	SampleIndex               int
	TargetHash                string
	RawBytes                  int
	CiphertextBytes           int
	PlaceholderBytes          int
	CalldataBytes             int
	CarrierGasEstimate        uint64
	EncryptionUS              float64
	SubmissionSerializationUS float64
}

func WriteClientOverheadReport(cfg ClientOverheadConfig) error
```

- [ ] **Step 1: Write failing tests for schema, count, and stable ordering**

Test a private report runner with a fake measurer so it produces 100 cheap rows
for each class. Assert:

```go
wantHeader := []string{
	"class", "sample_index", "target_hash", "raw_bytes",
	"ciphertext_bytes", "placeholder_bytes", "calldata_bytes",
	"carrier_gas_estimate", "encryption_us",
	"submission_serialization_us",
}
```

Assert 501 total CSV records including the header, 100 rows per class, class
order matching `evidenceCorpusClasses`, and sample indices `0..99` within each
class.

- [ ] **Step 2: Run the report tests and verify they fail**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run ClientOverhead -count=1
```

Expected: FAIL because report types and functions do not exist.

- [ ] **Step 3: Implement plaintext serialization and carrier-gas helpers**

Use the exact plaintext request boundary:

```go
type rawSubmissionRequest struct {
	RawTx string `json:"raw_tx"`
}

func serializeRawSubmission(raw []byte) ([]byte, error) {
	return json.Marshal(rawSubmissionRequest{
		RawTx: "0x" + hex.EncodeToString(raw),
	})
}
```

Implement the EIP-7623 data-only estimate:

```go
func estimateCarrierGas(calldata []byte) uint64 {
	var tokens uint64
	for _, value := range calldata {
		if value == 0 {
			tokens++
		} else {
			tokens += 4
		}
	}
	return 21_000 + 10*tokens
}
```

Add exact tests for empty, all-zero, all-nonzero, and mixed calldata.

- [ ] **Step 4: Implement one real measurement**

`measureClientOverhead` must:

1. time `serializeRawSubmission(target.Raw)`;
2. time only `encryptReplayTarget`;
3. construct the placeholder after the encryption timer stops;
4. hex-decode placeholder raw bytes and calldata to calculate their lengths;
5. calculate gas from placeholder calldata; and
6. return microseconds as `float64(duration.Nanoseconds()) / 1000`.

Do not include JSON serialization, encryption, signing, or network I/O in the
wrong timer.

- [ ] **Step 5: Implement deterministic per-class sampling**

Group targets by `EvidenceClass`, iterate `evidenceCorpusClasses` in declared
order, and select targets with:

```go
target := classTargets[sampleIndex%len(classTargets)]
globalIndex := classOffset + sampleIndex
```

Reject `SamplesPerClass < 100`. Populate `Class` and `SampleIndex` in the runner
rather than trusting the measurer.

- [ ] **Step 6: Implement atomic CSV output**

Create the output directory, write through `os.CreateTemp` in that directory,
flush and check `csv.Writer.Error`, close the file, then rename the temporary
file to `OutputPath`. Remove the temporary file on every error path so no
partial `client_overhead.csv` is left behind.

- [ ] **Step 7: Add a real one-sample integration test**

Reuse `writeReplayFixture` and call the real measurement function once. Assert:

- raw bytes equal the original target length;
- ciphertext bytes are nonzero;
- placeholder bytes exceed raw bytes for the small fixture;
- calldata bytes exceed ciphertext bytes by exactly 68 bytes;
- carrier gas equals `estimateCarrierGas` on decoded placeholder calldata;
- encryption and serialization timings are nonnegative; and
- decrypting the ciphertext recovers the original target bytes.

- [ ] **Step 8: Run focused and package tests**

Run:

```sh
cd mempool-il
go test ./internal/mempool -run 'ClientOverhead|CarrierGas|ReplayPlaceholder' -count=1
go test ./internal/mempool -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the report library**

```sh
git add mempool-il/internal/mempool/corpus_report.go \
  mempool-il/internal/mempool/corpus_report_test.go
git commit -m "feat: measure plaintext and encrypted client overhead"
```

---

### Task 4: Add the local report command and produce raw evidence

**Files:**
- Create: `mempool-il/cmd/corpus-report/main.go`
- Create: `mempool-il/cmd/corpus-report/main_test.go`
- Generate, do not commit: `results/issue-13-client-overhead/client_overhead.csv`

**Interfaces:**
- Consumes: `mempool.WriteClientOverheadReport`.
- Produces: a `corpus-report` CLI with `-corpus`, `-cluster-config`, `-out`,
  `-slot`, and `-samples-per-class`.

- [ ] **Step 1: Write failing CLI validation tests**

Factor parsing into:

```go
func run(args []string) error
```

Tests must assert errors for missing corpus, missing cluster config, empty
output, slot zero, and fewer than 100 samples per class without invoking BTE.

- [ ] **Step 2: Run CLI tests and verify they fail**

Run:

```sh
cd mempool-il
go test ./cmd/corpus-report -count=1
```

Expected: FAIL because the command does not exist.

- [ ] **Step 3: Implement the thin CLI**

Defaults:

```text
-out results/client-overhead/client_overhead.csv
-slot 1
-samples-per-class 100
```

Require explicit `-corpus` and `-cluster-config`. `main` logs a fatal error only
after `run(os.Args[1:])` returns.

- [ ] **Step 4: Run command and module tests**

Run:

```sh
cd mempool-il
go test ./cmd/corpus-report ./internal/mempool -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the command**

```sh
git add mempool-il/cmd/corpus-report/main.go \
  mempool-il/cmd/corpus-report/main_test.go
git commit -m "feat: add client overhead report command"
```

- [ ] **Step 6: Generate temporary local cluster material**

Run from the repository root:

```sh
mkdir -p results/issue-13-client-overhead
cd bloc-node
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 128 \
  --out ../results/issue-13-client-overhead/cluster.json
```

Confirm all generated configuration, CRS, and operator secrets are below the
ignored result root and remain untracked.

- [ ] **Step 7: Generate the 500-row raw CSV**

Run:

```sh
cd mempool-il
go run ./cmd/corpus-report \
  -corpus ../deploy/docker-compose/corpus/mock-targets.jsonl \
  -cluster-config ../results/issue-13-client-overhead/cluster.json \
  -out ../results/issue-13-client-overhead/client_overhead.csv \
  -slot 1 \
  -samples-per-class 100
```

Expected: a header plus 500 raw measurement rows.

- [ ] **Step 8: Check the generated artifact**

Run:

```sh
awk -F, 'NR>1 {count[$1]++} END {for (class in count) print class, count[class]}' \
  results/issue-13-client-overhead/client_overhead.csv
git status --short
```

Expected: 100 rows for each of five classes and no generated result or secret
appears in `git status`.

---

### Task 5: Document methodology, evidence semantics, and current status

**Files:**
- Modify: `mempool-il/README.md`
- Modify: `docs/modules/mempool-il.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Consumes: the accepted corpus/report contract and generated artifact.
- Produces: canonical operational commands, thesis-justification methodology,
  limitations, validation evidence, changelog entry, and updated next action.

- [ ] **Step 1: Update the mempool README**

Document:

- labelled JSONL shape `{"class":"calldata_128","raw_tx":"0x..."}`;
- exact `28/50/12/8/2` corpus counts;
- development-only signer and chain ID 1337 warning;
- the `corpus-report` command;
- each CSV column and timer boundary;
- the EIP-7623 estimate formula; and
- generated results staying ignored.

Add the sampling methodology exactly as recorded in the design spec: timestamp,
endpoint, block range, stride, finality offset, transaction count, input-length
calculation, bin table, range-to-class mapping, rounding, and one-day-sample
caveat.

- [ ] **Step 2: Update the module deep dive**

Explain the strict evidence validator separately from permissive replay loading,
the shared encryption boundary, randomized ciphertext behavior, deterministic
row order, and why the report measures local preparation rather than network or
mining latency.

- [ ] **Step 3: Add the validation contract**

In `docs/VALIDATION.md`, include the exact config-generation and report commands,
required 500 data rows, per-class counts, schema, corpus invariants, ignored
artifact path, and the rule that `carrier_gas_estimate` is not paid gas.

- [ ] **Step 4: Update changelog and status**

Add a dated issue #13 changelog row naming corpus, report, docs, both Go suites,
and the ignored raw artifact. In `docs/STATUS.md`, record the accepted local
client-overhead artifact and make issue #14 release-candidate validation the
immediate next action. Do not change the last-known-good release baseline until
issue #14 freezes it.

- [ ] **Step 5: Validate documentation and diff**

Run:

```sh
git diff --check
rg -n 'client_overhead|28/50/12/8/2|EIP-7623|74,383|issue #14' \
  mempool-il/README.md docs/modules/mempool-il.md docs/VALIDATION.md \
  docs/CHANGELOG.md docs/STATUS.md
```

Expected: no whitespace errors and every canonical owner contains its required
portion of the contract.

- [ ] **Step 6: Commit documentation**

```sh
git add mempool-il/README.md docs/modules/mempool-il.md docs/VALIDATION.md \
  docs/CHANGELOG.md docs/STATUS.md
git commit -m "docs: record client overhead evidence contract"
```

---

### Task 6: Run final validation and update issue tracking

**Files:**
- Verify: all task files
- Update externally: GitHub issue #13 and project fields

**Interfaces:**
- Consumes: the complete implementation and raw local artifact.
- Produces: verified branch state and issue evidence suitable for closure.

- [ ] **Step 1: Run both required Go suites**

Run:

```sh
cd mempool-il
go test ./... -count=1
cd ../bloc-node
go test ./... -count=1
```

Expected: both suites PASS.

- [ ] **Step 2: Run targeted race coverage**

Run:

```sh
cd mempool-il
go test -race ./internal/mempool -run 'ClientOverhead|ReplayPlaceholder|CommittedEvidenceCorpus' -count=1
```

Expected: PASS with no race reports.

- [ ] **Step 3: Inspect repository and artifact state**

Run:

```sh
git status -sb
git diff main...HEAD --check
git diff --stat main...HEAD
git log --oneline --decorate main..HEAD
wc -l results/issue-13-client-overhead/client_overhead.csv
```

Expected: clean tracked worktree, no ignored artifacts in the diff, and 501 CSV
lines.

- [ ] **Step 4: Review implementation against issue acceptance criteria**

Confirm:

- 100 valid unique corpus transactions with exact class counts;
- 500 raw report rows, 100 per class;
- contracted schema and timer definitions;
- no live-chain key or generated secret tracked;
- required docs updated;
- both module suites and targeted race test pass; and
- `docs/STATUS.md` was reviewed and updated only for accepted evidence and next
  actions.

- [ ] **Step 5: Post evidence and close issue #13**

Post the branch commits, validation commands, result path, schema/count summary,
and gas-estimate caveat to issue #13. Set Project status to Done and close the
issue only after all acceptance checks above pass.
