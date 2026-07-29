# Epochless Hybrid Ciphertext And Static Campaign Corpus Implementation Plan

> **Required execution skill:** Use `superpowers:test-driven-development` for
> every behavior change. Execute this plan inline with
> `superpowers:executing-plans`; do not delegate unless the user separately asks
> for subagents.

**Goal:** Replace the slot-bound hybrid ciphertext and runtime mock encryption
with an epochless, capsule-authenticated ciphertext and immutable
cluster-specific encrypted corpora suitable for the matched issue #15 metric
campaigns.

**Architecture:** The BTE capsule binds only its puncture index, cryptographic
components, setup, and cluster public key. AES-256-GCM authenticates the
transaction payload against the canonical capsule. A deterministic 512-entry
plaintext master corpus is encrypted offline for each cluster configuration,
verified before publication, and served by `mempool-il` as a static prefix
selected by the evaluator's proposal limit. Slot identity remains in the
surrounding protocol but never determines ciphertext validity.

**Tech Stack:** Go 1.24, Kyber pairing and ElGamal primitives, AES-256-GCM,
HKDF-SHA256, Ethereum signed transactions, JSON/JSONL artifacts, Docker
Compose, Bash campaign-contract tests, Markdown canonical documentation.

## Global Constraints

- [ ] Keep all implementation and documentation on
  `codex/issue-15-same-region-campaign`.
- [ ] Do not call AWS APIs, run Terraform plan/apply, push an image, push Git
  commits, or create billable resources.
- [ ] Treat source
  `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d` and image
  `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`
  as superseded historical evidence, never as executable inputs.
- [ ] Do not preserve `bte-tx-v1`, `bloc-cluster-v2`, slot-bound ciphertexts, or
  synthetic final-campaign fallback through compatibility decoding.
- [ ] Keep coordinated `index = corpus_position % BMax` allocation explicit
  and retain collision-aware batch planning.
- [ ] Keep corpus generation and verification outside timed latency attempts.
- [ ] Keep latency and resource-measured phases separate.
- [ ] Keep `n=10` and batch 512 outside the primary launch; enable only the
  documented 30-observation extension pilot.
- [ ] Use `apply_patch` for hand-written edits, preserve unrelated work, and
  stage only task-owned files.
- [ ] At every checkpoint, run the smallest focused test first, then the
  affected module suite.

## File Structure

### BTE library

- Modify `bte/btd-impl-main/be/btd.go` for context-free capsule proof
  construction and verification.
- Modify `bte/btd-impl-main/be/cluster.go` for the minimal ciphertext, v2 wire
  encoding, capsule-bound HKDF/GCM, public configuration identity, and
  GCM-authenticated share recovery.
- Modify `bte/btd-impl-main/be/cluster_test.go` for v2 round trips, mutation
  rejection, legacy rejection, cross-slot reuse semantics, and batch identity.
- Modify `bte/btd-impl-main/be/btd_test.go` for proof/index/setup binding.
- Modify BTE benchmarks that call removed scope-aware APIs.

### Corpus construction and static serving

- Modify `mempool-il/internal/mempool/corpus.go` and its tests to define and
  validate the 512-entry nested master corpus.
- Add `mempool-il/internal/mempool/encrypted_corpus.go` and
  `encrypted_corpus_test.go` for encrypted artifact schemas, identities,
  atomic generation, loading, and self-checks.
- Add `mempool-il/cmd/encrypt-corpus/main.go` as the offline generator.
- Replace runtime encryption in
  `mempool-il/internal/mempool/replay_placeholder.go` and its tests with an
  immutable encrypted-corpus source.
- Modify `mempool-il/internal/api/server.go` and API tests for bounded
  `slot`/`limit` prefix retrieval.
- Modify `mempool-il/cmd/service/main.go` for encrypted-artifact startup.
- Replace `deploy/docker-compose/corpus/mock-targets.jsonl` with the canonical
  512-entry master corpus; retain the separate client-overhead corpus.

### Node and evaluator

- Modify `bloc-node/internal/app/types.go`, `config.go`, and config tests for
  `bloc-cluster-v3` plus expected encrypted-corpus provenance.
- Modify `bloc-node/internal/app/provider.go` and tests to send proposal limits,
  parse provenance metadata, and enforce response bounds.
- Modify `bloc-node/internal/app/node.go` and slot tests to retain proposal
  limits and use GCM authentication as the decryption success condition.
- Modify `bloc-node/internal/app/eval_persistent.go`,
  `eval_remote.go`, `tx_source.go`, evaluator tests, and manifest tests to
  request exact scenario sizes and reject synthetic final-campaign fallback.
- Update direct-submission and benchmark callers of the removed ciphertext
  scope APIs.

### Deployment, campaign validation, and documentation

- Modify `deploy/docker-compose/compose.yaml`,
  `compose.mock-placeholder.yaml`, `.env.example`, and its README to mount and
  serve prebuilt encrypted corpora.
- Modify the applicable `deploy/ec2` templates, same-AZ runner, campaign runner
  contract tests, and README to require source/corpus/config identities.
- Modify `docs/ARCHITECTURE.md`, `docs/modules/bte.md`,
  `docs/modules/mempool-il.md`, `docs/modules/bloc-node.md`,
  `docs/VALIDATION.md`, `docs/DECISIONS.md`, `docs/CHANGELOG.md`,
  `docs/STATUS.md`, and affected module READMEs.
- Update `docs/superpowers/plans/2026-07-28-issue-15-same-region-campaign.md`
  so its launch commands refer to the replacement candidate and static
  encrypted corpus only after the new source and image identities exist.

## Task 1: Make The Lower-Level BTE Capsule Context-Free

**Files:**

- Modify: `bte/btd-impl-main/be/btd.go`
- Modify: `bte/btd-impl-main/be/btd_test.go`
- Modify: `bte/btd-impl-main/be/cluster.go`
- Test: `bte/btd-impl-main/be/cluster_test.go`

- [ ] **Step 1: Write proof-binding tests that do not supply application
  context**

Add tests that call the intended paper-aligned interface:

```go
ct, err := Enc(pk, index, message)
require.NoError(t, err)
require.NoError(t, ct.Verify(crs, pk))

mutated := cloneCT(ct)
mutated.I++
require.Error(t, mutated.Verify(crs, pk))
```

Also verify that changing `Gamma`, `Kp`, either ElGamal point, any proof point,
the public key, or the CRS rejects the proof.

- [ ] **Step 2: Run the focused tests and verify the interface is missing**

Run:

```sh
cd bte/btd-impl-main
go test ./be -run 'TestCTProofBinds(Index|Capsule|Setup|PublicKey)$'
```

Expected: FAIL because the current interface requires context and `CT` carries
`Context`.

- [ ] **Step 3: Remove application context from the capsule**

Implement the exact lower-level shape:

```go
type CT struct {
	I     int
	Gamma kyber.Point
	Kp    kyber.Point
	C     elgamal.CT
	Pi    Proof
}

func Enc(pk kyber.Point, index int, message kyber.Point) (*CT, error)
```

Remove `EncWithContext`, remove `CT.Context`, and remove context bytes from the
Fiat-Shamir transcript. The transcript must still cover suite/domain
separation, setup, public key, index, `Gamma`, `Kp`, `C`, and proof commitments.
Temporarily adapt the hybrid caller to the context-free `Enc` so the BTE module
continues to compile.

- [ ] **Step 4: Run BTE unit tests**

Run:

```sh
cd bte/btd-impl-main
go test ./be
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit the lower-level capsule change**

```sh
git add bte/btd-impl-main/be
git commit -m "refactor: remove application context from BTE capsule"
```

## Task 2: Introduce The Minimal `bte-tx-v2` Hybrid Ciphertext

**Execution note:** Tasks 2 and 3 form one atomic BTE compatibility checkpoint.
Removing the legacy object fields necessarily removes the scope APIs and
plaintext-hash recovery contract that consume those fields. Run both tasks'
focused tests and the complete BTE suite before creating the checkpoint commit;
do not preserve a transient API that accepts a scope and silently ignores it.

**Files:**

- Modify: `bte/btd-impl-main/be/cluster.go`
- Modify: `bte/btd-impl-main/be/cluster_test.go`

- [ ] **Step 1: Write v2 structure and cryptographic mutation tests**

Test the intended API and invariants:

```go
ct, err := cluster.EncryptTx(rawTx, index)
require.NoError(t, err)
require.Equal(t, index, ct.Capsule.I)
require.Greater(t, len(ct.EncryptedTx), 12+16)

wire, err := ct.MarshalBinary()
require.NoError(t, err)
require.Equal(t, []byte("bte-tx-v2"), wire[:len("bte-tx-v2")])
```

Add table-driven rejection tests for:

- one-byte changes to every canonical capsule component;
- swapping `EncryptedTx` between two independently valid capsules;
- truncated nonce, ciphertext, and tag;
- trailing wire bytes;
- `bte-tx-v1` input; and
- a v2 magic string placed around a v1 payload.

- [ ] **Step 2: Run the focused tests and verify failure**

```sh
cd bte/btd-impl-main
go test ./be -run 'TestHybridCiphertextV2|TestHybridRejects'
```

Expected: FAIL because the current object and wire format expose version,
cluster, slot, index, nonce, and plaintext hash fields.

- [ ] **Step 3: Implement the v2 object and canonical capsule binding**

Use:

```go
type Ciphertext struct {
	Capsule     CT
	EncryptedTx []byte
}

func (b *ClusterBTE) EncryptTx(rawTx []byte, index int) (*Ciphertext, error)
```

Implement:

```text
capsuleDigest = SHA256(canonicalCapsule)
key = HKDF-SHA256(
    ikm=canonicalGTSecret,
    salt=nil,
    info="bloc-hybrid-key-v2" || capsuleDigest,
)
aad = "bloc-hybrid-aad-v2" || capsuleDigest
EncryptedTx = random12ByteNonce || AESGCM.Seal(nil, nonce, rawTx, aad)
```

Keep the wire magic constant private and immutable. Canonical capsule encoding
must reject non-canonical point encodings, negative/out-of-domain indexes,
length overflow, trailing data, and omitted proof components.

- [ ] **Step 4: Run focused and complete BTE tests**

```sh
cd bte/btd-impl-main
go test ./be -run 'TestHybridCiphertextV2|TestHybridRejects'
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit the v2 construction**

```sh
git add bte/btd-impl-main/be
git commit -m "feat: add capsule-authenticated hybrid ciphertext v2"
```

## Task 3: Use AEAD Authentication For Share Recovery And Remove Scope APIs

**Files:**

- Modify: `bte/btd-impl-main/be/cluster.go`
- Modify: `bte/btd-impl-main/be/cluster_test.go`
- Modify: BTE benchmark files returned by
  `rg 'PlanBatchFor|DecodeBatchFor|DecodeAndPlanBatchFor|HashOK|CiphertextScope' bte/btd-impl-main`

- [ ] **Step 1: Write decryption-oracle and epochless-reuse tests**

The result API is:

```go
type PlaintextResult struct {
	Plaintext []byte
	Err       error
}
```

Tests must prove:

- valid threshold shares open GCM and return the original signed bytes;
- an incorrect threshold subset returns an authentication error and no bytes;
- bounded subset recovery finds a valid subset when bad shares are present;
- no plaintext is returned when the bounded search is exhausted;
- the same serialized ciphertext can be decoded and proposed in different
  outer slot envelopes; and
- `BatchID` is independent of outer slot identity and uses the v2 domain.

- [ ] **Step 2: Run focused tests and verify failure**

```sh
cd bte/btd-impl-main
go test ./be -run 'TestCombineUsesGCMAuthentication|TestCiphertextReusableAcrossSlots|TestBatchIDV2'
```

Expected: FAIL because `HashOK` and scope-aware APIs still exist.

- [ ] **Step 3: Remove redundant correctness and scope state**

Delete:

```text
CiphertextScope
PlanBatchFor
DecodeBatchFor
DecodeAndPlanBatchFor
PlaintextResult.HashOK
```

Make failed `cipher.Open` the only hybrid-decryption failure signal. Compute:

```text
BatchID = SHA256(
    "bloc-batch-v2" ||
    lengthPrefixed(canonicalCiphertext[0]) ||
    ...,
)
```

Retain deterministic collision separation and `alpha >= maximum index
multiplicity`.

- [ ] **Step 4: Adapt BTE tests and benchmarks, then validate**

```sh
cd bte/btd-impl-main
go test ./...
go test ./... -run '^$' -bench 'Batch|Combine' -benchtime=1x
```

Expected: PASS without any occurrence of removed APIs.

- [ ] **Step 5: Commit the API cleanup**

```sh
git add bte/btd-impl-main
git commit -m "refactor: authenticate BTE recovery with AEAD"
```

## Task 4: Adapt Node Ciphertext Consumers And Bump The Config Contract

**Files:**

- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: direct-submission and benchmark callers returned by
  `rg 'EncryptTx|DecodeBatchFor|PlanBatchFor|HashOK|bloc-cluster-v2' bloc-node`
- Test: corresponding `*_test.go` files

- [ ] **Step 1: Write config and decryption-consumer tests**

Cover:

```go
require.Equal(t, "bloc-cluster-v3", cfg.Version)
require.Error(t, LoadClusterConfig(v2Path))

results := node.combineBatchShares(...)
require.NoError(t, results[0].Err)
require.Equal(t, signedTx, results[0].Plaintext)
```

Assert that v2 configs and `bte-tx-v1` ciphertexts fail closed, and that the
node never tests a removed plaintext hash.

- [ ] **Step 2: Run focused node tests and verify failure**

```sh
cd bloc-node
go test ./internal/app -run 'TestClusterConfigV3|TestNodeCombineUsesAEAD'
```

- [ ] **Step 3: Adapt node and evaluator compilation surfaces**

Set the sole accepted configuration version to `bloc-cluster-v3`. Update
direct encryption to:

```go
ciphertext, err := cluster.EncryptTx(rawSignedTx, index)
```

Use `result.Err == nil` as authenticated decryption success. Keep recovered
transaction hash comparison only as an evaluator/corpus diagnostic after
successful Ethereum decoding.

- [ ] **Step 4: Run the node module suite**

```sh
cd bloc-node
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit the consumer migration**

```sh
git add bloc-node
git commit -m "refactor: migrate node to epochless ciphertext v2"
```

## Task 5: Define The Nested 512-Transaction Master Corpus

**Files:**

- Modify: `mempool-il/internal/mempool/corpus.go`
- Modify: `mempool-il/internal/mempool/corpus_test.go`
- Modify: `deploy/docker-compose/corpus/mock-targets.jsonl`
- Add or modify: deterministic corpus-generation helper under
  `mempool-il/cmd/` if the current generator cannot express the nested contract

- [ ] **Step 1: Write exact nested-prefix validation tests**

Encode the accepted class counts:

```go
var campaignPrefixCounts = map[int]map[PayloadClass]int{
	8:   {Transfer: 2, Bytes128: 4, Bytes256: 1, KiB1: 1, KiB4: 0},
	32:  {Transfer: 9, Bytes128: 16, Bytes256: 4, KiB1: 2, KiB4: 1},
	128: {Transfer: 36, Bytes128: 64, Bytes256: 15, KiB1: 10, KiB4: 3},
	512: {Transfer: 143, Bytes128: 256, Bytes256: 62, KiB1: 41, KiB4: 10},
}
```

For each prefix, require unique signed transaction hashes, chain ID 1337,
recoverable senders, valid signatures, and exact class counts. Verify that
prefix 8 is byte-for-byte contained in prefix 32, then 128, then 512.

- [ ] **Step 2: Run corpus tests and verify failure**

```sh
cd mempool-il
go test ./internal/mempool -run 'TestCampaignMasterCorpus'
```

- [ ] **Step 3: Generate and check in the deterministic master corpus**

Generate exactly 512 signed transactions with stable keys, nonces, recipients,
payload bytes, and ordering. Keep
`deploy/docker-compose/corpus/client-overhead-targets.jsonl` unchanged because
it owns the separate issue #13 500-observation client-overhead workload.

- [ ] **Step 4: Validate the artifact and module**

```sh
cd mempool-il
go test ./internal/mempool -run 'TestCampaignMasterCorpus|TestClientOverheadCorpus'
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit the plaintext fixture**

```sh
git add mempool-il deploy/docker-compose/corpus/mock-targets.jsonl
git commit -m "feat: add nested 512-transaction campaign corpus"
```

## Task 6: Build And Verify Immutable Encrypted Corpus Artifacts

**Files:**

- Add: `mempool-il/internal/mempool/encrypted_corpus.go`
- Add: `mempool-il/internal/mempool/encrypted_corpus_test.go`
- Add: `mempool-il/cmd/encrypt-corpus/main.go`
- Modify: `mempool-il/README.md`

- [ ] **Step 1: Write manifest identity and atomic-generation tests**

Define the artifact contract:

```go
type EncryptedCorpusManifest struct {
	SchemaVersion           string         `json:"schema_version"`
	CiphertextWireVersion   string         `json:"ciphertext_wire_version"`
	PublicConfigID          string         `json:"public_config_id"`
	PlaintextMasterID       string         `json:"plaintext_master_id"`
	PlaintextPrefixIDs      map[int]string `json:"plaintext_prefix_ids"`
	EncryptedCorpusID       string         `json:"encrypted_corpus_id"`
	EncryptedPrefixIDs      map[int]string `json:"encrypted_prefix_ids"`
	BMax                    int            `json:"bmax"`
	Count                   int            `json:"count"`
	IndexAssignment         string         `json:"index_assignment"`
	ClassCounts             map[int]map[string]int `json:"class_counts"`
	Candidates              []EncryptedCandidate  `json:"candidates"`
}
```

Test deterministic identities over canonical content, non-deterministic
ciphertext bytes across independent generations, `candidate.Index ==
position % BMax`, proof verification, threshold self-decryption, signed
transaction equality, atomic rename, output-file non-overwrite, and rejection
of one-byte manifest/ciphertext/config mutations.

- [ ] **Step 2: Run the new tests and verify failure**

```sh
cd mempool-il
go test ./internal/mempool -run 'TestEncryptedCorpus'
```

- [ ] **Step 3: Implement the generator and loader**

Expose:

```go
func GenerateEncryptedCorpus(
	plaintextPath string,
	clusterConfigPath string,
	limit int,
	outputPath string,
) (*EncryptedCorpusManifest, error)

func LoadEncryptedCorpus(path string) (*EncryptedCorpusManifest, error)
```

The CLI must require explicit input, cluster config, limit, and output paths.
It must encrypt outside campaign timing, validate the entire artifact in
memory, write to a sibling temporary file, `fsync`, and atomically rename only
after all proof/decryption/transaction checks succeed.

Compute:

```text
PublicConfigID = SHA256(
  suiteID || BMax || CRSSHA256 || canonicalClusterPublicKey
)
```

Use separate artifacts for `n=4,t=3,BMax=128` and
`n=7,t=5,BMax=128`; generate a BMax 512 artifact only when the extension is
authorized.

- [ ] **Step 4: Validate the generator locally**

```sh
cd mempool-il
go test ./...
go run ./cmd/encrypt-corpus -h
```

Expected: tests PASS and help exits without writing an artifact.

- [ ] **Step 5: Commit artifact tooling**

```sh
git add mempool-il
git commit -m "feat: generate verified encrypted campaign corpora"
```

## Task 7: Serve Static Ciphertext Prefixes From `mempool-il`

**Files:**

- Modify: `mempool-il/internal/mempool/replay_placeholder.go`
- Modify: `mempool-il/internal/mempool/replay_placeholder_test.go`
- Modify: `mempool-il/internal/api/server.go`
- Modify: `mempool-il/internal/api/server_test.go`
- Modify: `mempool-il/cmd/service/main.go`

- [ ] **Step 1: Write static-source and HTTP-contract tests**

The source contract becomes:

```go
type SlotSource interface {
	FetchSlot(ctx context.Context, slot uint64, limit int) (InclusionList, error)
}
```

Test:

- `limit=8/32/128` returns the corresponding immutable prefix;
- repeated and concurrent calls for different slots are byte-identical for the
  same limit;
- slot is echoed only as protocol correlation;
- `limit <= 0`, malformed, or greater than artifact count rejects;
- responses expose manifest provenance but no raw transaction;
- no encryption or file mutation occurs at request time.

- [ ] **Step 2: Run focused tests and verify failure**

```sh
cd mempool-il
go test ./internal/mempool ./internal/api -run 'TestEncryptedCorpusSource|TestInclusionListLimit'
```

- [ ] **Step 3: Replace runtime replay encryption**

Load and fully validate the artifact during service startup. Store canonical
ciphertext bytes in immutable memory. Implement:

```http
GET /inclusion-list?slot=<uint64>&limit=<1..artifact_count>
```

Remove measured-path requirements for plaintext corpus, private BTE shares,
cluster encryption, fixed replay slot, cache fill, and loop/consumption state.
Keep any development-only raw source isolated behind a distinct command/config
that final-campaign validation rejects.

- [ ] **Step 4: Run the service module suite**

```sh
cd mempool-il
go test ./...
```

Expected: PASS under the race detector for the new source tests:

```sh
go test -race ./internal/mempool ./internal/api
```

- [ ] **Step 5: Commit static serving**

```sh
git add mempool-il
git commit -m "feat: serve immutable encrypted corpus prefixes"
```

## Task 8: Propagate Proposal Limits And Verify Corpus Provenance In Nodes

**Files:**

- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/provider.go`
- Modify: `bloc-node/internal/app/provider_test.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/node_slot_test.go`
- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/config_test.go`

- [ ] **Step 1: Write request, bound, and provenance tests**

Introduce:

```go
type prepareSlotRequest struct {
	Slot          uint64
	ProposalLimit int
}
```

Test that the provider emits `slot` and `limit`, accepts at most the requested
count for generic use, and in campaign-strict mode requires exactly the
requested count. Reject mismatched:

```text
schema_version
ciphertext_wire_version
public_config_id
encrypted_corpus_id
encrypted_prefix_id
limit
```

Also test concurrent slots with different limits and prove no slot overwrites
another slot's retained `ProposalLimit`.

- [ ] **Step 2: Run focused tests and verify failure**

```sh
cd bloc-node
go test ./internal/app -run 'TestProviderProposalLimit|TestProviderProvenance|TestPrepareSlotRetainsLimit'
```

- [ ] **Step 3: Implement the bounded provider contract**

Extend cluster/provider config with expected immutable identities. Have the
provider request:

```go
q.Set("slot", strconv.FormatUint(slot, 10))
q.Set("limit", strconv.Itoa(proposalLimit))
```

Decode and plan only after provenance and count checks pass. Store the limit
in `slotState`; do not derive it from BMax or from the number of available
transactions.

- [ ] **Step 4: Run node tests**

```sh
cd bloc-node
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit proposal-limit propagation**

```sh
git add bloc-node
git commit -m "feat: bind proposals to encrypted corpus prefixes"
```

## Task 9: Make Evaluator Workloads Exact And Final-Campaign Safe

**Files:**

- Modify: `bloc-node/internal/app/eval_persistent.go`
- Modify: `bloc-node/internal/app/eval_remote.go`
- Modify: `bloc-node/internal/app/tx_source.go`
- Modify: evaluator and manifest tests under `bloc-node/internal/app/`
- Modify: report schema documentation/tests if provenance fields are added

- [ ] **Step 1: Write evaluator contract tests**

For each primary batch size, assert:

```go
scenario.BatchSize == prepare.ProposalLimit
manifest.TxSource == "mock-encrypted-corpus"
manifest.TxCount == scenario.BatchSize
manifest.EncryptedPrefixID != ""
manifest.PublicConfigID == cluster.ExpectedPublicConfigID
```

Test that final-campaign mode fails before warmups when the provider returns too
few/many ciphertexts, a wrong prefix, v1 wire data, a mismatched public config,
or any `synthetic` source/fallback.

- [ ] **Step 2: Run focused evaluator tests and verify failure**

```sh
cd bloc-node
go test ./internal/app -run 'TestPersistentEvaluatorUsesExactCorpusPrefix|TestFinalCampaignRejectsSynthetic'
```

- [ ] **Step 3: Set proposal size per scenario**

Pass the scenario batch size directly to every `prepareSlotRequest` in local
persistent and remote evaluation. Record plaintext master/prefix IDs,
encrypted corpus/prefix IDs, public config ID, wire version, requested count,
and received count in the run manifest. Preserve the existing attempt
classification and 12-second completion boundary.

- [ ] **Step 4: Run evaluator and full node validation**

```sh
cd bloc-node
go test ./internal/app
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit evaluator enforcement**

```sh
git add bloc-node
git commit -m "feat: enforce fixed encrypted workloads in evaluators"
```

## Task 10: Wire Local Deployment And Campaign Validation Without AWS

**Files:**

- Modify: `deploy/docker-compose/compose.yaml`
- Modify: `deploy/docker-compose/compose.mock-placeholder.yaml`
- Modify: `deploy/docker-compose/.env.example`
- Modify: `deploy/docker-compose/README.md`
- Modify: relevant `deploy/ec2` config templates and bootstrap scripts
- Modify: `deploy/ec2/run-same-az-campaign.sh`
- Modify: `deploy/ec2/run-final-campaign.sh`
- Modify: `deploy/ec2/scripts/test-campaign-runners.sh`
- Modify: `deploy/ec2/README.md`

- [ ] **Step 1: Extend shell contract tests before runner changes**

Assert that validate-only requires:

- source SHA and image digest for the replacement candidate;
- `bloc-cluster-v3` and `bte-tx-v2`;
- exact public config and encrypted corpus/prefix IDs;
- `tx_source=mock-encrypted-corpus`;
- same-AZ `us-east-1a`, `t3.small`, `n=4,t=3` and `n=7,t=5`;
- batches `8/32/128`, 10 warmups, 1,000 measured attempts, 10 balanced
  blocks, seed `20260621`, and 12-second completion boundary;
- latency/resource phase separation; and
- exclusion of n=10 and batch 512 from primary mode.

- [ ] **Step 2: Run the contract tests and verify failure**

```sh
bash deploy/ec2/scripts/test-campaign-runners.sh
```

- [ ] **Step 3: Mount immutable artifacts and fail closed**

Compose must mount one read-only encrypted artifact into `mempool-il`.
Deployment preflight must compare on-disk SHA256, manifest IDs, node config,
and node image labels before a service starts. Runners must never generate,
encrypt, rebuild, pull a mutable tag, or substitute an artifact during a timed
phase.

- [ ] **Step 4: Run side-effect-free deployment checks**

Run only:

```sh
docker compose -f deploy/docker-compose/compose.yaml config
bash deploy/ec2/scripts/test-campaign-runners.sh
bash deploy/ec2/run-same-az-campaign.sh --validate-only
bash deploy/ec2/run-final-campaign.sh --validate-only
```

Expected: PASS without AWS, Terraform plan/apply, registry access, or container
creation. If a validate-only command attempts any external mutation, stop and
fix the validation path before continuing.

- [ ] **Step 5: Commit local/deployment wiring**

```sh
git add deploy
git commit -m "feat: validate static corpus campaign deployment"
```

## Task 11: Update Canonical Architecture, Validation, And Issue-15 Plan

**Files:**

- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/modules/bte.md`
- Modify: `docs/modules/mempool-il.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`
- Modify: `bloc-node/README.md`
- Modify: `mempool-il/README.md`
- Modify: `bte/btd-impl-main/README.md`
- Modify: `bte/btd-impl-main/TESTING.md`
- Modify: `docs/superpowers/plans/2026-07-28-issue-15-same-region-campaign.md`

- [ ] **Step 1: Write the decision record and replace obsolete semantics**

Supersede the slot-binding decision without erasing history. Record:

- epochless key/CRS lifetime;
- capsule-bound AEAD;
- fixed v2/v3 compatibility break;
- coordinated indexes as a prototype limitation tracked by issue #22;
- nested corpus identities;
- offline per-cluster encryption;
- static prefix serving; and
- the explicit invalidation of the old source/image.

- [ ] **Step 2: Update architecture and module ownership documents**

Remove every normative claim that ciphertext validity depends on slot or
cluster label. Keep slot scoping for lists, ACS, shares, and node state. State
that AEAD authenticates decryption while Ethereum signature/hash checks are
post-decryption diagnostics.

- [ ] **Step 3: Update validation and runbook contracts**

Document exact artifact-generation, verification, local startup,
validate-only, primary latency, resource, extension pilot, retention, and
authenticated cleanup checkpoints. Do not insert a replacement source SHA,
image digest, or live command until those identities have actually been
created and inspected.

- [ ] **Step 4: Run documentation checks**

Run the repository's documented link/ownership checks. At minimum:

```sh
rg -n 'bte-tx-v1|bloc-cluster-v2|CiphertextScope|HashOK|slot-bound|replay-slot' \
  README.md docs bloc-node mempool-il bte deploy
```

Review every hit; historical decision text may remain only when explicitly
marked superseded.

- [ ] **Step 5: Commit canonical documentation**

```sh
git add README.md docs bloc-node/README.md mempool-il/README.md \
  bte/btd-impl-main/README.md bte/btd-impl-main/TESTING.md deploy
git commit -m "docs: define epochless encrypted campaign workload"
```

## Task 12: Validate, Freeze The Replacement Candidate, And Stop Before AWS

**Files:**

- Modify only if validation exposes a task-owned defect.
- Create ignored artifacts only under existing module `results/` directories.

- [ ] **Step 1: Run all affected module suites**

```sh
cd bte/btd-impl-main && go test ./...
cd ../../../mempool-il && go test ./...
cd ../bloc-node && go test ./...
cd ../latency-charts && python -m pytest
```

- [ ] **Step 2: Run race and cryptographic focused checks**

```sh
cd bte/btd-impl-main
go test -race ./be
go test ./... -run 'Ciphertext|Combine|Batch|Proof'

cd ../../mempool-il
go test -race ./internal/mempool ./internal/api

cd ../bloc-node
go test -race ./internal/app
```

- [ ] **Step 3: Generate and verify local n=4 and n=7 artifacts**

Using checked-in local prototype cluster configs, generate BMax 128 artifacts
for n=4/t=3 and n=7/t=5 into ignored result paths. Verify both with the loader,
prove that their plaintext prefix IDs match and their public/encrypted IDs
differ, and confirm that generation is outside evaluator timing.

Do not generate BMax 512 unless separately entering the extension checkpoint.

- [ ] **Step 4: Run complete no-AWS readiness validation**

```sh
docker compose -f deploy/docker-compose/compose.yaml config
bash deploy/ec2/scripts/test-campaign-runners.sh
bash deploy/ec2/run-same-az-campaign.sh --validate-only
bash deploy/ec2/run-final-campaign.sh --validate-only
git diff --check
```

Confirm the checks make no AWS, Terraform plan/apply, registry push, or
billable-resource call.

- [ ] **Step 5: Self-review against the approved specification**

Check each section of
`docs/superpowers/specs/2026-07-29-epochless-hybrid-ciphertext-design.md`
against implementation and tests. Search for unfinished markers and forbidden
fallbacks:

```sh
rg -n 'TODO|TBD|FIXME|synthetic|bte-tx-v1|bloc-cluster-v2|CiphertextScope|HashOK' \
  bte bloc-node mempool-il deploy docs
```

Classify every intentional historical/test fixture occurrence.

- [ ] **Step 6: Establish replacement identities**

After tests pass, record:

```sh
git rev-parse HEAD
git status --short --branch
git rev-list --left-right --count origin/main...HEAD
docker image inspect <local-replacement-image> --format '{{json .RepoDigests}}'
```

If the locally built image has no immutable distributable digest, report that
as a live-run blocker. Do not push it. Update the issue #15 execution plan and
GitHub issue only with identities actually established.

- [ ] **Step 7: Stop at the live authorization checkpoint**

Report:

- replacement source SHA;
- local image ID and whether a distributable digest exists;
- plaintext/public-config/encrypted-corpus/prefix identities;
- exact validated live commands, but do not run them;
- duration, cost, quota, distribution, artifact, retention, and authenticated
  cleanup bounds;
- branch, worktree, divergence, commits, and validation;
- `STATUS.md` review outcome;
- unresolved blockers; and
- confirmation of no push and no AWS resources.

Do not begin the issue #15 n=4 pilot until the user gives separate explicit AWS
authorization against the replacement identities.
