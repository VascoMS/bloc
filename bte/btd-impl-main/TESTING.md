# Testing Coverage

This document explains what the current test and benchmark suite checks after the cluster-facing BTE integration.

Run all tests from the module root:

```sh
GOCACHE=/private/tmp/bte-go-cache go test ./...
```

The explicit `GOCACHE` avoids macOS sandbox/cache permission issues in this workspace.

## Cluster BTE Tests

These tests live in `be/cluster_test.go`. They cover the new library API intended for DVT/cluster use.

### `TestBatchCombineMessagesReturnsPlaintexts`

Checks the lower-level BTE refactor.

What it does:

- Creates a BTE instance with `B=8`, `n=10`, `t=5`.
- Samples four random `GT` messages.
- Encrypts them with `BTD.Enc`.
- Generates exactly `t` partial decryption shares.
- Calls `BatchCombineMessages`.
- Verifies that the returned `GT` messages equal the original messages.

Why it matters:

- Confirms combine now returns plaintext messages instead of relying on hidden plaintext fields inside ciphertext structs.
- Confirms the original BTE math still works after removing benchmark-only plaintext storage.

### `TestVerifyCTRejectsMutations`

Checks CCA-style ciphertext binding at the API level.

What it does:

- Creates one valid BTE capsule with context metadata.
- Confirms `VerifyCT` accepts the original capsule.
- Mutates each of these fields independently:
  - `Gamma`
  - punctured key `Kp`
  - ElGamal component `C.A`
  - index `I`
  - proof scalar `Pi.KHat`
  - proof `Context`
- Confirms each mutation makes `VerifyCT` return `false`.

Why it matters:

- Confirms proof verification is non-panicking and rejects malformed capsules.
- Confirms the proof transcript is bound to ciphertext contents and context metadata.

### `TestHybridEncryptionRoundTrip`

Checks the full cluster-facing encryption/decryption path for raw transaction bytes.

What it does:

- Encrypts four raw byte payloads with `EncryptTx`.
- Computes a deterministic `BatchPlan`.
- Generates one share per sub-batch from the first `t` operators.
- Combines all shares with `CombineShares`.
- Verifies that each result:
  - has no error,
  - has `HashOK = true`,
  - returns the original raw bytes in consensus order.

Why it matters:

- Confirms the hybrid encryption strategy works end to end.
- Confirms BTE decrypts the `GT` capsule secret and AES-GCM decrypts the raw transaction bytes.
- Confirms plaintext hash checking works.

### `TestCiphertextSerializationRoundTrip`

Checks deterministic binary encoding for placeholder transport.

What it does:

- Encrypts one raw byte payload.
- Calls `Ciphertext.MarshalBinary`.
- Decodes it with `ClusterBTE.UnmarshalCiphertext`.
- Re-encodes the decoded value.
- Confirms both encodings are identical.
- Verifies the decoded BTE capsule proof.

Why it matters:

- Confirms encrypted placeholder payloads can be serialized and recovered by cluster nodes.
- Confirms group elements/scalars survive binary round-trip.

### `TestPlanBatchSeparatesDuplicateIndices`

Checks deterministic sub-batching for repeated puncture indices.

What it does:

- Creates six ciphertexts.
- Forces the first three ciphertexts to use the same index.
- Calls `PlanBatch`.
- Verifies no sub-batch contains duplicate indices.

Why it matters:

- Confirms the BEAT-MEV index collision constraint is handled by planning.
- Confirms repeated indices can be distributed across sub-batches rather than immediately failing the entire batch.

### `TestBatchDecRejectsDuplicateIndices`

Checks the low-level batch decryption invariant.

What it does:

- Creates two valid BTE capsules with the same index.
- Calls `BatchDec` directly on both in the same batch.
- Expects an error.

Why it matters:

- Confirms the implementation explicitly rejects invalid same-index batches/sub-batches.
- Prevents silent incorrect decryption when callers bypass `PlanBatch`.

### `TestPlanBatchRejectsMetadataMismatch`

Checks metadata binding between the outer cluster ciphertext and the inner BTE capsule.

What it does:

- Encrypts one transaction for `cluster-a`.
- Mutates the outer `ClusterID` to `cluster-b`.
- Calls `PlanBatch`.
- Expects an error.

Why it matters:

- Confirms cluster/slot/index metadata cannot be changed after encryption without detection.
- Confirms `PlanBatch` checks that outer metadata matches the proof-bound capsule context.

### `TestCombineSharesRequiresThreshold`

Checks threshold enforcement.

What it does:

- Encrypts one transaction.
- Computes a `BatchPlan`.
- Generates only `t-1` shares.
- Calls `CombineShares`.
- Expects an error.

Why it matters:

- Confirms the cluster API does not reconstruct plaintexts without the threshold number of shares.

### `TestBatchPlanDeterministic`

Checks deterministic planning.

What it does:

- Creates eight ciphertexts with repeated indices.
- Calls `PlanBatch` twice on the same ordered input.
- Confirms both plans have the same:
  - `BatchID`,
  - `Alpha`,
  - number of sub-batches,
  - original positions per sub-batch,
  - ciphertext encodings.

Why it matters:

- Confirms all cluster operators will compute the same sub-batches from the same consensus-ordered batch.
- This is required before operators can independently generate compatible decryption shares.

## Core ElGamal Test

This test lives in `elgamal/elgamal_test.go`.

### `TestElGamal`

Checks threshold ElGamal correctness.

What it does:

- Creates an ElGamal instance over `G1`.
- Generates trusted-dealer Shamir shares for `n=10`, `t=5`.
- Encrypts one random `G1` point.
- Computes five partial decryptions.
- Combines the shares.
- Expects successful recovery.

Why it matters:

- Confirms the threshold ElGamal building block still works after removing plaintext assertions from the ciphertext struct.

## Original BEAT-MEV Benchmarks

These benchmarks live in `main_test.go`. They are inherited from the original proof of concept and are performance-oriented rather than correctness-oriented.

### `BenchmarkEnc`

Measures BTE encryption time for one index with `B=16`, `n=10`, `t=5`.

### `BenchmarkPDec8`, `BenchmarkPDec32`, `BenchmarkPDec128`, `BenchmarkPDec512`

Measure partial decryption/share generation for batch sizes:

- `B=8`
- `B=32`
- `B=128`
- `B=512`

Each benchmark includes:

- normal full-batch partial decryption,
- `alpha = sqrt(B)` sub-batching,
- `alpha = 2*sqrt(B)` sub-batching.

### `BenchmarkBatchCombine8`, `BenchmarkBatchCombine32`, `BenchmarkBatchCombine128`

Measure combine/reconstruction cost for batch sizes:

- `B=8`
- `B=32`
- `B=128`

Each benchmark includes normal combine plus sub-batched variants.

### `BenchmarkBatchCombine512Slow`

Measures normal combine for `B=512`. This is expected to be slow because normal combine has quadratic pairing cost.

### `BenchmarkBatchCombine512Fast`

Measures sub-batched combine variants for `B=512`.

### `BenchmarkBatchCombineParSqrt`

Measures parallel sub-batch combine behavior for `B=128` and `B=512`, with:

- `alpha = sqrt(B)`
- `alpha = 2*sqrt(B)`

## Cluster Full-Path Benchmarks

These benchmarks live in `be/cluster_test.go`. They measure the new cluster-facing full path.

Available benchmarks:

- `BenchmarkHybridFullPath8`
- `BenchmarkHybridFullPath32`
- `BenchmarkHybridFullPath128`
- `BenchmarkHybridFullPath512`

Each benchmark performs the full prototype path:

1. Encrypt raw byte payloads with `EncryptTx`.
2. Compute `PlanBatch`.
3. Generate threshold shares for every sub-batch.
4. Combine shares with `CombineShares`.
5. Check the number of returned plaintext results.

Run one benchmark:

```sh
GOCACHE=/private/tmp/bte-go-cache go test ./be -run '^$' -bench '^BenchmarkHybridFullPath8$' -benchtime=1x
```

Run all cluster full-path benchmarks:

```sh
GOCACHE=/private/tmp/bte-go-cache go test ./be -run '^$' -bench '^BenchmarkHybridFullPath' -benchtime=1x
```

## What Is Not Tested Yet

The current suite does not test:

- DKG-generated shares, because DKG is not implemented.
- Public share-verification proofs, because share proofs are not implemented.
- Malformed share attribution; invalid shares may fail reconstruction but are not individually proven bad.
- Verifiable aggregation or proofs that an external combiner returned the correct plaintexts.
- Ethereum transaction syntax, signature, nonce, gas, or execution validity.
- Network behavior, share gossip, timeouts, retries, or operator liveness under realistic delays.
- Cross-language serialization stability.
- Backward compatibility of serialized ciphertexts across future versions.
- Security against adaptive adversaries beyond the local mutation/rejection tests.
- Fuzzing of decoders, malformed binary encodings, or group element edge cases.

## Recommended Next Tests

Before integrating with a real cluster, add:

- Decoder fuzz tests for `UnmarshalCiphertext` and `BTD.UnmarshalCT`.
- Tests for duplicate operator shares in `CombineShares`.
- Tests for wrong `BatchID` and wrong `SubBatchID` shares.
- Tests for tampered AEAD ciphertext and nonce.
- Tests for plaintext hash mismatch.
- A deterministic test vector for `Ciphertext.MarshalBinary`.
- A simulation where one or more operators withhold shares.
- A simulation where a malformed share is included, then future share-verifiability rejects it.
- Ethereum raw transaction parsing/validation tests once the materialization layer is added.
