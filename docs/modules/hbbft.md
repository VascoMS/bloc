# `hbbft` And Slot-Scoped ACS Implementation Architecture

## Responsibility And Non-Goals

`sbc/hbbft` contains an inherited HoneyBadger implementation and the
BLOC-specific consensus path. The active BLOC path uses the Reliable Broadcast
(RBC), Binary Byzantine Agreement (BBA), and Asynchronous Common Subset (ACS)
building blocks, wrapped by one `SlotACS` per slot. It deliberately bypasses the
inherited recurring `HoneyBadger` epoch/mempool driver.

The module decides a common subset of opaque proposer byte strings. It does not
parse inclusion lists, merge transactions, run BTE, release decryption shares,
or materialize a block. Those operations belong to `bloc-node` after ACS.

## Source Map

| Layer | Principal source and symbols |
| --- | --- |
| Reliable broadcast | [`rbc.go`](../../sbc/hbbft/rbc.go): `RBC`, `ProofRequest`, `EchoRequest`, `ReadyRequest`, `tryDecodeValue` |
| Binary agreement | [`bba.go`](../../sbc/hbbft/bba.go): `BBA`, `BvalRequest`, `AuxRequest`, `tryOutputAgreement` |
| Common subset | [`acs.go`](../../sbc/hbbft/acs.go): `ACS`, `processBroadcast`, `processAgreement`, `tryCompleteAgreement` |
| BLOC slot adapter | [`bloc_slot.go`](../../sbc/hbbft/bloc_slot.go): `SlotACS`, `SlotMessage`, `SlotOutput`, `AcceptedBatch` |
| Outbound queue | [`message_que.go`](../../sbc/hbbft/message_que.go): atomic queue drain used by ACS |
| Generic transport types | [`transport.go`](../../sbc/hbbft/transport.go): `Transport`, `RPC`, `MessageTuple` |
| Inherited top-level driver | [`honey_badger.go`](../../sbc/hbbft/honey_badger.go): inactive BLOC path |
| Local harness transport | [`local_transport.go`](../../sbc/hbbft/local_transport.go): tests, simulation, and benchmark only |

## Inputs, Outputs, State, And Message Identities

### Configuration

All three consensus layers embed `Config`, which supplies local operator ID,
the ordered membership list, `N`, `F`, and an inherited batch-size field. When
`F` is zero the constructors derive `F = floor((N-1)/3)`. The active node path
passes an explicit `F` and uses the same configuration to create one RBC and one
BBA for every configured proposer ID.

The package assumes operator IDs are unique and that `N`, `Nodes`, and `F` are
consistent. Constructors do not comprehensively validate those relationships.

### Message hierarchy

The in-memory hierarchy is:

```text
SlotMessage(slot)
  ACSMessage(proposerID)
    BroadcastMessage
      ProofRequest | EchoRequest | ReadyRequest
    AgreementMessage(epoch)
      BvalRequest | AuxRequest
```

`SlotMessage.Slot` identifies one `SlotACS`. `ACSMessage.ProposerID` identifies
which proposer's RBC/BBA pair receives the nested message. The transport sender
ID is separate: it identifies the participant that sent an ECHO, READY, BVAL,
or AUX message.

`bloc-node` converts this hierarchy to versioned protobuf. The `hbbft` package
itself has no authentication or serialization policy; it trusts its caller to
provide an authenticated sender ID and route messages to the correct slot.

### Outputs

- `RBC.Output` returns one reconstructed byte string and consumes the stored
  output.
- `BBA.Output` returns one Boolean decision and consumes the notification while
  retaining the internal decision.
- `ACS.Output` returns one proposer-ID-to-bytes map and consumes it.
- `SlotACS.Output` returns one `SlotOutput` containing the slot, decoded common
  subset, proposer-sorted batches, a deterministic default block-body encoding,
  and an optional post-agreement hook result.

The active `bloc-node` integration consumes `OrderedBatches`; it does not use
the default gob `BlockBody` as a production cross-language wire format.

## Stage-By-Stage Algorithm

### 1. Reliable Broadcast setup

`NewRBC` creates an `(N-2F, 2F)` Reed-Solomon encoder. The proposer calls
`InputValue`, which:

1. splits and encodes its opaque proposal into `N` shards;
2. builds a SHA-256 Merkle tree over those shards;
3. creates one proof containing a shard, branch, leaf index/count, and root for
   each participant;
4. handles its own proof locally; and
5. returns the other `N-1` proofs for addressed delivery.

The implementation rebuilds the Merkle tree for each proof. This is
computationally redundant but deterministic.

### 2. RBC PROOF, ECHO, and READY transitions

Only the configured proposer may send a `ProofRequest`. A recipient validates
the Merkle proof, broadcasts one `EchoRequest`, and processes its own ECHO.
Each sender can contribute at most one stored ECHO and one stored READY.

For a matching root:

- `N-F` ECHOs cause a node that has not sent READY to broadcast READY.
- `F+1` READYs cause a node that has not sent READY to relay READY.
- `2F+1` READYs plus `F+1` ECHOs make the current implementation attempt
  reconstruction.

`tryDecodeValue` reconstructs only from ECHOs whose root matches the selected
READY/ECHO threshold root. It leaves decoding retryable when the matching
ECHOs do not yet provide enough distinct shards, recomputes the Merkle root
after erasure decoding, and stores output only when that root equals the
selected commitment.

### 3. BBA BV-broadcast

Each BBA begins at epoch zero. Its first input becomes the estimate and sends
`BVAL(value)`. For each Boolean value independently:

- after `F+1` distinct BVAL senders, a node relays that BVAL once;
- after `2F+1` distinct senders, the value enters `binValues`; and
- when the first value enters `binValues`, the node broadcasts one AUX carrying
  that value.

A sender may validly relay both BVAL values, so BVAL state is indexed by value
and sender. AUX state is indexed only by sender. The current handler overwrites
an earlier AUX from the same sender rather than rejecting an equivocation,
which makes conflicting Byzantine AUX delivery order observable.

### 4. BBA epoch advancement and decision

`tryOutputAgreement` counts only AUX messages whose values have already entered
local `binValues`. It waits for `N-F` such messages. This admission rule is
required so reordered delivery cannot count an unvalidated AUX value.

The current common-coin value is public and deterministic:

```text
coin(epoch) = true when epoch is even, false when epoch is odd
```

If valid AUX values contain both bits, the next estimate is the coin. If they
contain one bit, that bit becomes the next estimate and is emitted as a
decision when it equals the coin. The state machine continues until a later
matching coin marks it done. This parity value is a prototype placeholder, not
the unpredictable threshold common coin required by the HoneyBadger liveness
argument.

Messages from older epochs are ignored. Messages from future epochs are queued.
On advancement the current implementation processes the queue and then clears
it; a message still more than one epoch ahead can be requeued during that pass
and subsequently discarded. This is a confirmed liveness gap.

### 5. ACS orchestration

`NewACS` creates `N` RBC instances and `N` BBA instances. Local proposal input
starts the local proposer RBC. When any RBC yields bytes, ACS stores those bytes
and inputs `true` to the corresponding BBA if it has not already received an
estimate.

When exactly `N-F` BBA instances have decided `true`, ACS inputs `false` to
every remaining BBA that has not accepted an input. Each produced BBA result is
stored by proposer ID.

ACS completes only when all of the following are true:

1. at least `N-F` BBA decisions are true;
2. all `N` BBA decisions have been received; and
3. an RBC payload exists for every proposer whose BBA decision is true.

The output contains exactly those truthy proposer IDs and their RBC bytes. RBC
availability alone never selects a proposal. This completion rule is the
corrected boundary recorded in Decision 0011.

### 6. Message emission and diagnostics

RBC and BBA append messages to local queues. ACS wraps each message with its
proposer ID and creates one addressed `MessageTuple` for every other member.
`SlotACS.Messages` drains the ACS queue and adds the active slot.

`ACS.Progress` is obtained through the ACS event loop and exposes sorted RBC
output IDs, all known BBA results, truthy proposer IDs, per-instance compact
state, queued-message count, and one waiting reason. `SlotProgress` adds the
slot. These snapshots are diagnostic and do not drive protocol transitions.

`SlotConfig.Trace.Enabled` opts one slot into the bounded
`bloc-acs-trace/v1` recorder. Its process-local monotonic offsets share the
proposal-ready origin used by the node's legacy ACS timer. The recorder stores
fixed aggregate milestones, one RBC and BBA entry per configured proposer, and
one message counter entry for each of PROOF, ECHO, READY, BVAL, and AUX. A
disabled recorder returns an empty trace and avoids its clock, mutex, map, and
snapshot work. Trace snapshots are detached copies and never feed a protocol
transition.

RBC and BBA instances run concurrently. Per-proposer points are therefore
diagnostic offsets, not durations that can be summed. Only the mutually
exclusive completion waits (`N-F` true BBAs, all BBAs, and truthy RBC
availability) are additive within the core wait interval. Adapter points split
the core decision from common-subset decoding, block-body construction, and
node receipt.

### 7. Slot adapter

`SlotACS.InputBatch` gob-encodes the candidate `[]byte` before giving it to RBC.
The length-bearing gob value lets `decodeCandidateBatch` recover the original
proposal despite Reed-Solomon shard padding. This internal gob wrapper is
separate from the protobuf inclusion-list payload and protobuf network
envelopes.

`HandleMessage` rejects nil messages and any slot other than the instance slot.
Once ACS returns a subset, the adapter:

1. gob-decodes every proposer value;
2. sorts proposer IDs ascending;
3. deep-copies each accepted batch into `OrderedBatches`;
4. calls `EncodeSlotBlockBody` unless a custom builder is configured; and
5. invokes an optional post-agreement hook outside the ACS core.

`bloc-node` uses neither a custom builder nor a hook; it processes
`OrderedBatches` itself.

## Determinism And Invariants

- Each proposer ID selects exactly one RBC/BBA pair.
- RBC thresholds are based on distinct transport sender IDs.
- BBA BVAL counts are per value and distinct sender.
- AUX counts advance only for values already admitted to `binValues`.
- ACS membership is determined only by BBA results and requires every truthy
  RBC payload to be present.
- The common subset map is converted to ascending proposer order before it
  crosses into `bloc-node`.
- Every `SlotMessage` must match the `SlotACS` instance.
- `Output` methods are consumptive; callers must capture output within the same
  serialized transition that drains emitted messages.

## Validation And Failure Semantics

- Invalid Merkle proofs, nonproposer PROOF messages, duplicate ECHOs/READYs,
  unknown nested message types, and unknown proposer IDs return errors.
- `bloc-node` treats duplicate ECHO/READY errors as benign retransmission noise;
  other ACS handler errors are logged but do not currently create a terminal
  failed slot result.
- The asynchronous core has no timeout. Missing proposals or messages can leave
  an instance pending indefinitely.
- ACS does not validate application proposal bytes. Invalid inclusion-list
  payloads fail later at the `bloc-node` boundary after agreement.

## Concurrency, Lifecycle, And Ownership

RBC, BBA, and ACS each own a goroutine and process input through channels. ACS
calls child state machines while running its own event loop. `bloc-node` adds an
outer `acsMu` because libp2p stream handlers can call the slot adapter
concurrently and methods such as `Messages` and `Output` are consumptive.

`SlotACS.Close` closes ACS once; ACS cascades idempotent shutdown to all RBC and
BBA children. The node stops routing the old slot before closing it. The
inherited `HoneyBadger` driver owns separate epoch and buffer lifecycle and is
not part of this active path.

## Paper Correspondence And Deviations

The design follows the high-level construction in
[The Honey Badger of BFT Protocols](../../papers/honeybadger.pdf): `N` parallel
RBC instances disseminate proposals, `N` binary agreements decide inclusion,
and ACS outputs at least `N-F` proposals under the stated assumptions. The RBC
uses Reed-Solomon shards and Merkle proofs with the paper's ECHO/READY threshold
shape.

The repository's [ACS improvement reference](../../papers/ACS_Improvement.pdf)
and BLOC work motivate the slot-specific integration and diagnostics. The
project adaptation is the `SlotACS` boundary, not a new ACS selection rule.

Important deviations from the paper model are:

- a deterministic epoch-parity value replaces the cryptographic common coin;
- transport authentication and sender-ID binding are delegated to the caller;
- Byzantine AUX equivocation is overwritten rather than rejected; and
- the default block-body and RBC length wrapper use Go gob and are not stable
  cross-language protocol formats.

## Test Evidence

Existing tests cover:

- correct-sender and one-fault RBC delivery, proof construction, shard
  reconstruction, mixed-root rejection, post-decode commitment verification,
  retry after an incomplete distinct-shard set, and consumptive output;
- BBA all-good/faulty inputs, per-value BVAL tracking, validated AUX admission,
  and epoch advancement;
- ACS common-subset agreement, required truthy RBC outputs, all-BBA completion,
  progress diagnostics, and fixed reordered-delivery schedules;
- atomic message-queue draining;
- slot common-subset output, wrong-slot rejection, close idempotence, progress,
  and post-agreement hooks; and
- the inherited HoneyBadger driver and local transport separately.

The current tests do not cover authenticated peer-to-sender binding, conflicting
AUX messages from one Byzantine sender, or future messages more than one epoch
ahead. These gaps are recorded in the
[implementation review](../../docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).

Run `go test ./...` from `sbc/hbbft`.

## Known Limitations

- The common coin is not cryptographic and does not satisfy the paper's
  asynchronous liveness assumption against an adaptive scheduler.
- The current BBA equivocation/future-queue behaviors require correctness
  patches before broader adversarial deployment.
- Constructors assume valid, unique membership configuration.
- The package contains inherited spelling, error-handling, and API issues;
  several state accessors are safe only under the active caller discipline.
- There are no signatures inside `hbbft`; authenticated sender identity must be
  enforced by the integration layer.
- The BLOC path is one-shot per slot and intentionally does not use the
  inherited HoneyBadger transaction buffer, epoch batching, or encryption path.
