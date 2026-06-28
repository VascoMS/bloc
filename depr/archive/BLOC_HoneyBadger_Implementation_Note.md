# BLOC HoneyBadger Adaptation Note

Historical note. The active architectural summary now lives in [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md) and the durable rationale is summarized in [docs/DECISIONS.md](/bloc/docs/DECISIONS.md).

This note documents two things:

1. how the current open-source `hbbft` implementation in this workspace works,
2. what was changed to adapt it into a slot-scoped block-building primitive for BLOC.

The implementation analyzed here lives under `hbbft/`.

## 1. How the original implementation works

### 1.1 Consensus layers in the original codebase

The repository already has a clean layering:

- `hbbft/rbc.go`
  implements Reliable Broadcast (RBC) for one proposer.
- `hbbft/bba.go`
  implements Binary Byzantine Agreement (BBA) for one proposer.
- `hbbft/acs.go`
  combines one RBC instance and one BBA instance per proposer into an Asynchronous Common Subset (ACS).
- `hbbft/honey_badger.go`
  provides a top-level HoneyBadger driver that manages a transaction buffer and repeatedly runs ACS across epochs.

The BLOC adaptation keeps the first three layers as the consensus core and bypasses the fourth layer.

### 1.2 The original top-level HoneyBadger flow

The old entry point is `NewHoneyBadger` in `hbbft/honey_badger.go`.

That object owns:

- a `txBuffer` mempool,
- an `acsInstances` map keyed by epoch,
- an `outputs` map keyed by epoch,
- a current `epoch`,
- and a message queue used to emit network traffic.

The original execution flow is:

1. application code adds individual transactions through `AddTransaction`,
2. `Start()` calls `propose()`,
3. `propose()` samples a batch from the local mempool,
4. the sampled `[]Transaction` batch is gob-encoded into a `[]byte`,
5. that encoded byte slice is submitted to the local ACS instance for the current epoch,
6. ACS emits RBC and BBA messages,
7. when ACS produces a common subset, `maybeProcessOutput()` decodes each accepted payload back into `[]Transaction`,
8. the driver deduplicates transactions by hash,
9. the committed transactions are removed from the local buffer,
10. the epoch is incremented,
11. the driver recursively starts the next epoch.

So the original engine is not just ACS. It is ACS plus:

- local mempool management,
- batch sampling,
- transaction-level deduplication,
- epoch chaining,
- and output bookkeeping across epochs.

### 1.3 How proposals enter the original protocol

In the original implementation, proposals do not come from an external block-builder or inclusion-list service.

Instead:

- the application pushes individual `Transaction` objects into HoneyBadger,
- HoneyBadger samples a local batch,
- HoneyBadger serializes that batch,
- ACS only sees the serialized bytes.

The `Transaction` abstraction itself is intentionally minimal:

```go
type Transaction interface {
    Hash() []byte
}
```

There is no built-in Ethereum transaction type in this repository. The concrete types used in tests and demos are simple structs such as a `Nonce uint64` wrapper.

### 1.4 How RBC works in the current code

`RBC` in `hbbft/rbc.go` disseminates one proposer payload.

At a high level:

1. the proposer gives RBC a `[]byte`,
2. RBC Reed-Solomon encodes it into data and parity shards,
3. Merkle proofs are built for the shards,
4. the proposer distributes one proof/shard package per participant,
5. each participant validates the received proof and broadcasts `Echo`,
6. after enough `Echo` or `Ready` messages, participants broadcast `Ready`,
7. once enough shards are available, the payload is reconstructed,
8. RBC outputs the reconstructed byte slice.

Important detail:

- RBC fundamentally operates on byte slices, not typed application objects.
- Reconstruction is based on shard layout.
- Because of shard sizing, reconstructed bytes can include padding if the application relies on raw byte length without an explicit length wrapper.

### 1.5 How BBA works in the current code

`BBA` in `hbbft/bba.go` decides one boolean value per proposer.

At a high level:

1. once a node believes a proposer's RBC completed, it inputs `true` to that proposer's BBA instance,
2. nodes exchange `Bval` and `Aux` messages,
3. BBA eventually outputs a boolean decision for that proposer.

ACS uses BBA to decide whether a proposer's RBC value is part of the common subset.

### 1.6 How ACS works in the current code

`ACS` in `hbbft/acs.go` creates:

- one `RBC` instance per proposer,
- one `BBA` instance per proposer.

The control flow is:

1. each participant inputs exactly one local proposal into its own RBC instance,
2. whenever an RBC instance outputs a payload for proposer `P`, ACS inputs `true` into proposer `P`'s BBA instance,
3. once `N - f` BBA instances have decided `true`, ACS inputs `false` into any remaining undecided BBA instances,
4. once all BBA instances have terminated and at least `N - f` are `true`, ACS outputs the set of RBC payloads whose corresponding BBA decision is `true`.

This is the core common-subset behavior we wanted to preserve for BLOC.

### 1.7 Encryption and decryption in the current codebase

The prompt asked to remove initial threshold encryption and bypass post-ACS decryption.

In this local `hbbft` codebase:

- there is no implemented threshold encryption stage,
- there is no decryption-share collection stage,
- there is no post-ACS decryption pipeline.

The README mentions threshold encryption as TODO material, but the actual code does not implement it.

So for this repository, "removing encryption/decryption from the ACS path" mostly means:

- do not introduce any new encryption logic,
- do not add decryption dependencies into ACS,
- keep post-agreement processing abstract.

## 2. Why the original top-level path did not match BLOC

The original `HoneyBadger` driver is an epoch-based transaction batching engine. BLOC needs a different boundary.

BLOC needs the primitive to behave like:

- one committee,
- one slot,
- one local candidate batch per participant,
- one ACS execution,
- one common subset output,
- one deterministic block-building step,
- optional post-agreement processing outside ACS.

The original `HoneyBadger` driver does not match that shape because it:

- owns a mempool,
- samples transactions itself,
- chains epochs indefinitely,
- returns transaction batches per epoch instead of a slot result,
- and does not expose a clean slot-scoped integration point for an external candidate-batch provider.

## 3. Changes made for the BLOC adaptation

### 3.1 Kept the ACS core unchanged

The following files were intentionally left unchanged in behavior:

- `hbbft/rbc.go`
- `hbbft/bba.go`
- `hbbft/acs.go`

This preserves:

- per-proposer reliable dissemination,
- per-proposer binary agreement,
- delayed completion behavior in ACS,
- deterministic agreement on the accepted proposer set.

### 3.2 Bypassed the old HoneyBadger driver

The new BLOC path does not use `hbbft/HoneyBadger` as its execution driver.

Specifically, the BLOC path bypasses:

- `txBuffer`,
- transaction sampling in `propose()`,
- epoch recursion,
- transaction deduplication in `maybeProcessOutput()`,
- output bookkeeping by epoch.

These behaviors still exist in the repository for the original engine, but they are not on the new BLOC execution path.

### 3.3 Added a new slot-scoped adapter

The new entry point is `NewSlotACS` in `hbbft/bloc_slot.go`.

The new adapter introduces these main types:

- `SlotConfig`
  configures a slot-scoped ACS run.
- `SlotACS`
  wraps one ACS instance for one slot.
- `SlotMessage`
  wraps ACS network traffic with a slot number.
- `SlotOutput`
  represents the final slot result returned to the surrounding BLOC pipeline.
- `AcceptedBatch`
  represents an accepted candidate batch tagged with its proposer id.
- `SlotBlockBody`
  is the deterministic block-body representation built from the common subset.

This changes the top-level model from:

- "start a recurring HoneyBadger epoch engine"

to:

- "run ACS once for one slot and return one slot result."

### 3.4 Added a clean external batch boundary

The slot adapter accepts candidate batches in two ways:

- `InputBatch(batch []byte)`
- `InputFromProvider(provider CandidateBatchProvider)`

`CandidateBatchProvider` is intentionally abstract:

```go
type CandidateBatchProvider interface {
    CandidateBatch(slot uint64) ([]byte, error)
}
```

This matches the BLOC requirement that the candidate batch may come from an external HTTP, gRPC, or JSON-RPC service.

The ACS layer itself does not call those protocols directly. It just accepts the bytes supplied by the surrounding integration layer.

### 3.5 Bound messages to a single slot

The original top-level HoneyBadger message wrapper was:

```go
type HBMessage struct {
    Epoch   uint64
    Payload interface{}
}
```

For the BLOC path, this was not the right boundary because we are no longer driving recurring epochs.

So the adapter introduces:

```go
type SlotMessage struct {
    Slot    uint64
    Payload *ACSMessage
}
```

This ensures:

- each adapter instance handles one slot,
- incoming traffic is checked against that slot,
- the slot-scoped path is explicit and isolated from the old epoch driver.

### 3.6 Added deterministic block-body construction

ACS outputs a map keyed by proposer id:

- `map[uint64][]byte`

Maps do not preserve iteration order, so that is not a sufficient block-building boundary for BLOC.

The adapter therefore:

1. converts the subset into a list of `(proposerID, batch)` pairs,
2. sorts the list by proposer id,
3. passes the ordered list to a block builder.

If the caller does not provide a custom builder, the default builder is `EncodeSlotBlockBody`, which gob-encodes:

- the slot id,
- the accepted batches in proposer-id order.

This gives all honest nodes the same deterministic block-body bytes once ACS has decided the common subset.

### 3.7 Kept decryption out of ACS and made post-agreement processing abstract

The adapter adds:

```go
type PostAgreementHook func(*SlotOutput) (interface{}, error)
```

This hook is called only after:

1. ACS has produced the common subset,
2. the accepted batches have been ordered deterministically,
3. the block body has been built.

This is where a surrounding BLOC implementation can later attach:

- decryption,
- materialization,
- signing preparation,
- or any other post-agreement logic.

The ACS core remains independent from all of those concerns.

### 3.8 Added explicit length-delimited batch encoding

This was the main subtle integration fix.

The original `HoneyBadger` path already gob-encoded `[]Transaction` before sending them into ACS. That mattered because gob embeds enough structure to recover the original logical value after RBC reconstruction.

For the BLOC adapter, candidate batches were initially modeled as raw `[]byte`. That exposed a problem:

- RBC reconstructs shard data as bytes,
- the reconstructed byte slice can contain trailing padding,
- returning those bytes directly would not preserve an opaque candidate batch exactly.

Example:

- input batch: `[]byte("batch-b")`
- reconstructed raw bytes: `[]byte("batch-b\x00")`

For BLOC, that is a correctness issue because ACS should preserve the candidate batch exactly.

The fix was to add an explicit wrapper:

- before ACS: `encodeCandidateBatch(batch []byte)`,
- after ACS: `decodeCandidateBatch(batch []byte)`.

The adapter currently uses gob for that wrapper so the batch length is encoded as part of the payload. This ensures the exact original byte slice is recovered after ACS even if RBC internally reconstructs padded shard buffers.

This does not make ACS application-specific. It only makes the payload self-delimiting before it enters a byte-oriented broadcast layer.

### 3.9 Kept the ACS boundary generic instead of Ethereum-specific

The old repository used a generic `Transaction` interface and did not define Ethereum transaction semantics internally.

For the BLOC slot path, I kept the new boundary byte-oriented for the same reason:

- ACS only needs to disseminate and agree on payloads,
- it does not need to understand Ethereum transaction structure,
- keeping the boundary generic avoids coupling the consensus layer to Ethereum-specific types or serialization choices.

If BLOC candidate batches are collections of Ethereum transaction objects, that is still fully compatible with this design:

1. the surrounding layer builds the batch from Ethereum transaction objects,
2. the surrounding layer serializes the batch,
3. ACS agrees on the serialized batch bytes,
4. the surrounding layer deserializes them after agreement.

## 4. What the new slot-scoped path looks like now

The adapted BLOC path is now:

1. create a `SlotACS` instance for one slot,
2. obtain one local candidate batch for that slot,
3. call `InputBatch` directly or use `InputFromProvider`,
4. exchange `SlotMessage` traffic between participants,
5. let the underlying `ACS` instance run RBC + BBA as before,
6. once ACS outputs a common subset, decode the length-delimited candidate batches,
7. sort accepted batches by proposer id,
8. build the deterministic block body,
9. run the optional post-agreement hook,
10. consume the final `SlotOutput`.

This is the new BLOC integration boundary.

## 5. Where the new integration path begins and ends

The new slot-scoped BLOC path begins at:

- `hbbft.NewSlotACS`
- `(*SlotACS).InputBatch`
- `(*SlotACS).InputFromProvider`

It processes network traffic through:

- `(*SlotACS).HandleMessage`
- `(*SlotACS).Messages`

It ends at:

- `(*SlotACS).Output`

which returns a `SlotOutput` containing:

- `Slot`
- `CommonSubset`
- `OrderedBatches`
- `BlockBody`
- `DecryptionResult`

## 6. Summary of reused vs changed pieces

### Reused as-is

- RBC logic in `hbbft/rbc.go`
- BBA logic in `hbbft/bba.go`
- ACS coordination logic in `hbbft/acs.go`

### Bypassed from the BLOC path

- transaction mempool ownership in `hbbft/honey_badger.go`
- transaction sampling in `propose()`
- epoch recursion and infinite chaining
- transaction deduplication/output bookkeeping in the old driver

### Added for BLOC

- `hbbft/bloc_slot.go`
  slot-scoped adapter and integration boundary
- `SlotMessage`
  slot-bound wrapper for ACS traffic
- `SlotOutput`
  slot result object
- `CandidateBatchProvider`
  abstract hook for external batch sourcing
- deterministic block-body building
- abstract post-agreement hook
- explicit batch length-delimiting around ACS

## 7. Verification

The adaptation was verified with tests in `hbbft/bloc_slot_test.go`.

Those tests cover:

- slot-scoped ACS agreement across multiple nodes,
- deterministic block-body output,
- slot mismatch rejection,
- and execution of the post-agreement hook.
