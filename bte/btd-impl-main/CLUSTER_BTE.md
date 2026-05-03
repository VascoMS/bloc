# Cluster BTE Integration

This document explains how this repository now exposes the BEAT-MEV batched threshold encryption primitive as a Go library that a DVT-style proposer cluster can use after consensus has fixed an encrypted transaction batch.

The implementation is still prototype-safe, not production hardened. It removes the most important benchmarking shortcuts from the original proof of concept and adds a cluster-facing API, but it does not yet implement DKG, public share-verifiability, verifiable aggregation, slashing, or Ethereum transaction semantic validation.

## Protocol Overview

The cluster protocol has two separate phases:

1. Ordering while encrypted.
2. Decryption after consensus has fixed the batch.

Users encrypt raw Ethereum transaction bytes under the public key of a target proposer cluster. The encrypted transaction is placed inside a normal placeholder transaction. Cluster operators run their normal consensus or DVT coordination protocol over those placeholder transactions, without seeing the hidden transaction contents.

After consensus agrees on the final ordered batch, every operator computes the same deterministic `BatchPlan`. Each operator then releases one BTE decryption share per sub-batch. Once at least `t` valid shares exist for every sub-batch, anyone can combine the shares and recover the raw transaction bytes in the exact order fixed by consensus.

Transactions not included in the agreed batch are not decrypted because the committee only releases shares for the finalized batch/sub-batches.

## Hybrid Encryption

The BEAT-MEV construction encrypts messages in the pairing target group `GT`, while Ethereum transactions are byte strings. The library bridges that mismatch with hybrid encryption:

1. Sample a random `GT` element called the capsule secret.
2. Derive an AEAD key from that `GT` element using HKDF-SHA256.
3. Encrypt the raw transaction bytes with AES-GCM.
4. BTE-encrypt only the `GT` capsule secret.
5. Store the BTE capsule, AEAD nonce, encrypted transaction bytes, and `SHA256(rawTx)` in the public `Ciphertext`.

During combine, BTE recovers the `GT` capsule secret. The library derives the same AEAD key, decrypts the transaction bytes, and checks the plaintext hash.

This keeps BTE responsible for the timing of key release while allowing arbitrary transaction bytes as payloads.

## Main Types

The cluster-facing API is in `be/cluster.go`.

- `ClusterBTE`: main library object for encryption, batch planning, share generation, and share combination.
- `PublicParams`: version, suite ID, maximum batch size, and PRF domain size.
- `PublicKey`: threshold ElGamal public key.
- `SecretShare`: one operator's threshold ElGamal secret share and operator ID.
- `Ciphertext`: encrypted raw transaction payload plus BTE capsule and metadata.
- `BatchPlan`: deterministic sub-batch layout for a finalized ordered batch.
- `DecryptionShare`: one operator's share for one sub-batch.
- `PlaintextResult`: recovered raw transaction bytes, original batch position, hash status, and any error.

The lower-level BTE API remains in `be/btd.go`. The important cluster-usable additions are:

- `EncWithContext`: BTE encryption where cluster/slot/index metadata is bound into the NIZK transcript.
- `VerifyCT`: now returns `(bool, error)` instead of panicking.
- `BatchDecWithShare`: computes a share from an explicit secret share.
- `BatchCombineMessages`: returns decrypted `GT` messages instead of checking hidden plaintexts.

## Cluster Lifecycle

### 1. Setup

For experiments, the trusted-dealer path is still used:

```go
suite := curves.NewSuite(kilic.NewBLS12381Suite())
btd := be.NewBTD(suite, bMax)
shares, pk := btd.KeyGen(n, t)
cluster := be.NewClusterBTE(btd, pk, shares)
```

For a future DKG integration, each operator should load its own DKG-generated `SecretShare` and construct a node-local instance:

```go
node := be.NewNode(btd, pk, mySecretShare, n, t)
```

### 2. User Encryption

The user encrypts complete raw transaction bytes:

```go
ct, err := cluster.EncryptTx(rawTx, index, clusterID, slot)
encoded, err := ct.MarshalBinary()
```

The encoded `Ciphertext` is what should be carried by the placeholder transaction calldata or whatever envelope the prototype uses.

The metadata `(clusterID, slot, index)` is part of the AEAD associated data and is also bound into the BTE proof context. If this metadata is changed later, planning or verification fails.

### 3. Consensus Over Placeholders

Cluster operators run the DVT/multi-proposer protocol over encrypted placeholders. The BTE library does not decide inclusion or ordering.

The output of consensus must be an ordered list of encrypted `Ciphertext` values. That ordered list is the binding input for decryption.

### 4. Deterministic Batch Planning

After consensus finalizes the ordered ciphertext batch:

```go
plan, err := cluster.PlanBatch(ciphertexts)
```

`PlanBatch`:

- Rejects empty batches and batches larger than `BMax`.
- Checks ciphertext version and index domain.
- Checks that outer metadata matches the inner BTE capsule context.
- Computes `BatchID = H(version, ordered ciphertext encodings)`.
- Uses default `alpha = ceil(2*sqrt(B))`.
- Raises `alpha` if repeated indices require more sub-batches.
- Sorts by descending index frequency and stable original position.
- Assigns ciphertexts round-robin across sub-batches.
- Rejects any sub-batch with duplicate indices.

This handles the BEAT-MEV index-collision constraint by ensuring each sub-batch has distinct puncture indices.

### 5. Operator Share Generation

Each operator publishes one share per sub-batch after the batch is committed:

```go
share, err := cluster.MakeShare(mySecretShare, plan, subBatchID)
```

`MakeShare` verifies every ciphertext proof in the sub-batch, aggregates the ElGamal ciphertexts, and computes a threshold ElGamal partial decryption share scoped to `(BatchID, SubBatchID)`.

The prototype does not yet attach a public proof to the share, so callers should treat share validity as partially checked only by successful threshold reconstruction. Public share-verifiability is a future hardening step.

### 6. Combining Shares

Once at least `t` shares are available for every sub-batch:

```go
results, err := cluster.CombineShares(plan, shares)
```

`CombineShares`:

- Rejects shares for the wrong `BatchID`.
- Rejects out-of-range sub-batch IDs.
- Rejects duplicate shares from the same operator for the same sub-batch.
- Requires at least `t` shares per sub-batch.
- Reconstructs each BTE capsule secret.
- Decrypts the AEAD transaction payload.
- Checks `SHA256(rawTx)` against the committed plaintext hash.
- Returns results in original consensus order.

The cluster block builder can then apply the PIC materialization rule:

1. If decryption succeeds, the hash matches, and the raw transaction is syntactically valid, replace the placeholder with the raw transaction.
2. Otherwise retain the placeholder or apply the deterministic fallback rule defined by the larger protocol.

## Implementation Changes Made

The original proof of concept was benchmark-oriented. The following changes make it usable as a library boundary:

- Removed stored plaintext fields from BTE and ElGamal ciphertext structs.
- Removed correctness checks that depended on hidden plaintext inside ciphertexts.
- Added `BatchCombineMessages` so combine returns decrypted messages.
- Changed proof verification to return `(bool, error)` instead of panicking.
- Added context binding to BTE encryption/proof hashing.
- Added explicit duplicate-index checks for batch/sub-batch decryption.
- Added explicit secret-share based partial decryption for node-local operation.
- Added hybrid transaction encryption for raw bytes.
- Added deterministic batch planning and sub-batching.
- Added deterministic binary encoding for ciphertexts and BTE capsules.
- Added decoding helpers that use the initialized suite to unmarshal group elements.
- Added tests for round-trip encryption, proof mutation rejection, serialization, duplicate-index handling, deterministic planning, threshold enforcement, and share combination.

## Current Limitations

This is not production-ready cryptography or a complete DVT integration.

Known missing pieces:

- No DKG implementation. `KeyGen` is still trusted-dealer Shamir sharing.
- No public share-verifiability. Malformed shares are not individually attributable yet.
- No verifiable aggregation proof for outsourced combine.
- No slashing or accountability layer.
- No Ethereum transaction semantic validation.
- No complete placeholder transaction format beyond `Ciphertext.MarshalBinary`.
- No stable cross-language wire format commitment beyond this Go binary encoding.
- No concurrency or networking layer for share gossip/collection.

The next hardening step should be public share-verifiability, because the PIC protocol needs to tolerate operators that withhold or publish malformed decryption shares.

## Running Tests And Benchmarks

Run all tests:

```sh
GOCACHE=/private/tmp/bte-go-cache go test ./...
```

Run the new full-path hybrid benchmark for one batch size:

```sh
GOCACHE=/private/tmp/bte-go-cache go test ./be -run '^$' -bench '^BenchmarkHybridFullPath8$' -benchtime=1x
```

Available full-path benchmark names:

- `BenchmarkHybridFullPath8`
- `BenchmarkHybridFullPath32`
- `BenchmarkHybridFullPath128`
- `BenchmarkHybridFullPath512`

The original BEAT-MEV benchmarks are still available through `./bench.sh`.
