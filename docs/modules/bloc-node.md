# `bloc-node` Implementation Architecture

## Responsibility And Non-Goals

`bloc-node` is the integration boundary for the implemented BLOC protocol. A
process represents one operator. It owns long-lived cluster/key material,
libp2p identity and connections, HTTP control state, observability collectors,
and exactly one active slot. For that slot it constructs a proposal, drives
ACS, validates and merges accepted lists, plans BTE decryption, exchanges
shares, combines plaintexts, and publishes the result.

The process is not a beacon node, execution client, Builder API server, DVT
signer, or production key-management service. Its HTTP API and evaluators are a
prototype control and evidence surface.

## Source Map

| Area | Principal source and symbols |
| --- | --- |
| CLI and trusted setup | [`internal/app/app.go`](../../bloc-node/internal/app/app.go), [`commands.go`](../../bloc-node/internal/app/commands.go): `genConfig`, `runNode` |
| Configuration | [`internal/app/config.go`](../../bloc-node/internal/app/config.go), [`types.go`](../../bloc-node/internal/app/types.go): `ConfigFile`, `Node`, `slotState` |
| Node state machine | [`internal/app/node.go`](../../bloc-node/internal/app/node.go): slot lifecycle, ACS, share, combine, result paths |
| Proposal providers | [`internal/app/provider.go`](../../bloc-node/internal/app/provider.go): direct and `mempool-http` proposals |
| Inclusion-list schema | [`internal/app/inclusion/types.go`](../../bloc-node/internal/app/inclusion/types.go), [`proto.go`](../../bloc-node/internal/app/inclusion/proto.go) |
| Canonical merge | [`internal/app/inclusion/merge.go`](../../bloc-node/internal/app/inclusion/merge.go): list/agreed/merged hashes and bounds |
| Network schema | [`proto/bloc/v1/messages.proto`](../../bloc-node/proto/bloc/v1/messages.proto), [`internal/app/codec.go`](../../bloc-node/internal/app/codec.go) |
| Operator transport | [`internal/app/transport.go`](../../bloc-node/internal/app/transport.go), [`transport_libp2p.go`](../../bloc-node/internal/app/transport_libp2p.go) |
| BTE encoding helpers | [`internal/app/crypto.go`](../../bloc-node/internal/app/crypto.go) |
| Ethereum parsing | [`internal/app/ethdemo/tx.go`](../../bloc-node/internal/app/ethdemo/tx.go): deterministic test generation and signed-byte parsing |
| HTTP/metrics | [`internal/app/node.go`](../../bloc-node/internal/app/node.go), [`metrics.go`](../../bloc-node/internal/app/metrics.go) |
| Evaluation support | `eval*.go`, `report.go`, and `tx_source.go`; operational rather than protocol-defining |

## Configuration, State, And Trust Material

### Cluster configuration

`ConfigFile` contains:

- cluster ID, slot, `N`, BTE threshold, and `BMax`;
- public CRS path and SHA-256 plus the BTE public key;
- every node's HTTP/libp2p listen and advertise addresses and peer ID;
- blockspace limits; and
- provider/network modes.

`gen-config` requires at least four nodes, derives `F = floor((N-1)/3)`, and
defaults BTE threshold to `2F+1`. It creates a versioned `cluster.crs`, public
`cluster.json`, one trusted-dealer threshold key, static Ed25519 libp2p
identities, and `secrets/operator-<id>.json` files. Each secret file contains
only that operator's share and private identity. `run` requires `--secrets` and
rejects legacy combined config, cluster/operator mismatches, and a private key
that does not derive the configured peer ID.

`normalizeConfig` supplies compatibility defaults for old address fields,
direct provider mode, libp2p, and default transaction gas. It does not fully
validate `BMax`, threshold, membership uniqueness, peer-ID correspondence, or
all cross-field relationships before constructing cryptographic state.

### Long-lived versus slot-scoped state

`Node` owns configuration, membership, the node-local `ClusterBTE`, BTE secret
share, curve suite, transport, fault controls, lifecycle counters, and
Prometheus collectors.

`slotState` owns everything that must be fresh for one protocol execution:

- slot ID and `prepared/running/completed/failed` phase;
- one `SlotACS` and its serialization lock;
- pending encrypted candidates and deduplication set;
- immutable-use batch plan and materialization prefix;
- accepted shares and deduplication/version state;
- share-generation and combine single-flight flags;
- mutually exclusive success result or bounded terminal `SlotFailure`;
- stage timestamps and per-slot metrics.

## HTTP And Process Lifecycle

The active process exposes:

- `/healthz`: ready only after libp2p is connected to every configured peer;
- `/tx`: accept and encrypt direct candidates while the slot is prepared;
- `/slot/prepare`: replace a completed or failed slot with a strictly greater ID;
- `/slot/status`: phase, pending/plan/share state, and diagnostic ACS progress;
- `/start`: build and input the local proposal once; a synchronous terminal
  failure returns a bounded HTTP 200 failure notice so controllers proceed to
  the authoritative `/result` response;
- `/result`: HTTP 202 while pending, 200 with the stable success `Result`, or
  422 with the stable terminal failure; and
- `/metrics`: Prometheus collectors.

Requests may include `?slot=`. When present it must equal the active slot;
omitting it preserves compatibility with older evaluator clients.

`prepareSlot` holds the lifecycle write lock, requires the current phase to be
`completed` or `failed`, closes the old ACS tree, installs a fresh `slotState`,
and retains the process, HTTP server, BTE object, and libp2p mesh. The lifecycle
lock also waits for in-flight sends/handlers before replacement. A pending slot
cannot be replaced through this API.

## Proposal Construction

### Direct submissions

`handleSubmitTx` serializes submission/encryption with `inputMu` and accepts
requests only in `prepared` phase. JSON requests provide raw hex plus gas, fee,
sender, nonce, and kind metadata. A non-JSON body is treated directly as raw
bytes. Fee must be a nonnegative decimal; zero gas uses the configured default.

The puncture index is `len(pending) % BMax`. The node calls:

```text
EncryptTx(raw, index, clusterID, activeSlot)
```

It serializes the ciphertext, assigns SHA-256 of those bytes as placeholder
hash, and stores ordering metadata. Ciphertext encryption is randomized, so
resubmitting identical raw bytes normally creates a distinct ciphertext hash.

Direct input is a prototype path: raw non-JSON bytes are not parsed as Ethereum
transactions before encryption. Ethereum syntax and sender recovery happen
only after decryption.

### Mempool HTTP provider

`fetchMempoolInclusionList` requests:

```text
<mempool_url>/inclusion-list?slot=<activeSlot>
```

The response body is capped at 8 MiB. Records explicitly marked non-placeholder
are ignored. Payload precedence is `encrypted_payload_hex`, then legacy
`ciphertext_hex`, then `placeholder_envelope_hex`. The first two are strict hex;
the legacy field permits the older raw fallback. The sidecar recomputes
SHA-256 of extracted ciphertext bytes and does not trust the service's
transaction or list hash as a BLOC protocol identity.

The provider currently uses the default global HTTP client without an explicit
timeout, so an unresponsive service can block proposal preparation.

### Inclusion-list proposal

Both providers produce:

```text
InclusionList {
  slot = active slot
  operator_id = local operator
  items = encrypted placeholders
}
```

The local diagnostic hash is canonical JSON over slot, operator, and items with
the hash field excluded. `EncodeList` transmits only the structured protobuf
fields; it does not transmit this hash. After ACS, every node recomputes hashes
from accepted content.

`startConsensus` is guarded by `startOnce`. It sets phase to `running`, builds
the proposal, optionally replaces its items with an empty list for the
`omit-proposal` test fault, protobuf-encodes it, records the proposal boundary,
and calls `SlotACS.InputBatch` through `stepACS`.

## Operator Messaging

### Protobuf envelope

Every network message is protobuf `Envelope` version 1 with application `from`,
`to`, `direct`, `kind`, `slot`, and exactly one ACS or share payload. ACS payloads
preserve slot, proposer ID, RBC message type and proof fields, or BBA epoch and
Boolean value. Share payloads carry application operator ID, hex `BatchID`,
sub-batch ID, and a serialized `G1` point.

`ProtoEnvelopeCodec` rejects unsupported versions and unknown concrete payload
types. The transport separately requires the string `kind` and protobuf oneof
to identify the same non-nil ACS or share payload before routing.

### libp2p transport

The transport creates a host from the configured static Ed25519 private key and
listens on protocol `/bloc/envelope/1.0.0`. Lower-ID peers repeatedly connect to
higher-ID peers; health becomes ready once every configured peer is connected.
Each envelope uses a fresh logical stream over multiplexed persistent
connections. `writeAll` handles short writes before `CloseWrite`.

Before reading an inbound stream, the handler maps
`stream.Conn().RemotePeer()` through the unique configured peer-ID membership
map and rejects unknown peers. After decoding, it requires `Envelope.From` to
match that authenticated operator, requires a direct envelope addressed to the
local operator, and requires a share's operator ID to match the same identity.
Outbound routing overwrites `From`, `To`, and `Direct` from local transport
state rather than trusting caller-populated fields. Both directions enforce the
shared `limits.max_envelope_bytes`; inbound reads stop at one byte beyond the
limit and reset oversized streams before protobuf decoding. Rejections use the
bounded reasons `oversize`, `decode`, `authentication`, and `payload`.

Encoded inclusion-list proposals are independently bounded by
`limits.max_proposal_bytes` and `BMax`. The defaults are 8 MiB and 128 items;
the envelope default is 16 MiB. The separate proposal bound prevents a local
operator from creating RBC messages that configured peers cannot accept.

### Node-level routing and serialization

`handleEnvelope` drops wrong-recipient direct messages and any envelope whose
outer slot differs from the active slot before recording active-slot metrics.
ACS messages are processed under `acsMu` by `stepACS`, which performs one state
transition, atomically drains emitted messages, and consumes output before
unlocking. Network sends happen after the ACS lock is released.

Sends run in goroutines so consensus mutation is not blocked by network I/O.
Each send holds the lifecycle read lock, rechecks the active slot, applies an
optional fault delay, fills local routing fields, and records bytes only after
successful transport delivery.

## Post-ACS Processing

### 1. Accepted-list boundary

`handleACSOutput` first requires `SlotOutput.Slot == activeSlot`.
`decodeAcceptedLists` protobuf-decodes each proposer-tagged batch and requires:

- nonzero encoded list slot;
- list slot equal to the active slot; and
- `list.OperatorID == AcceptedBatch.ProposerID`.

Any failure records the existing `decode` slot-failure reason. No malformed
proposal is filtered and no replacement proposal is selected.

### 2. Agreed-set canonicalization

`NewAgreedSet` recomputes each inclusion-list hash once, sorts lists by hash
ascending and operator ID ascending, counts all proposed items, and hashes
canonical JSON over the active slot and sorted full lists. This ordering, not
ACS map/proposer order, feeds merge and resolves the first-winner rule for
conflicting duplicate placeholders.

### 3. Deterministic merge

`Merge` walks the canonical list order and validates each candidate:

- ciphertext is nonempty;
- gas is nonzero;
- effective fee is a nonnegative decimal;
- an absent claimed hash is filled from SHA-256(ciphertext); otherwise it must
  equal that digest after lowercase/`0x` normalization;
- sender is lowercased and empty kind/fee receive defaults.

An exactly repeated placeholder with the same claimed hash, ciphertext, and
metadata uses the prior-validation fast path. Conflicting duplicates continue
through validation; the first valid candidate for one normalized ciphertext
hash wins.

Unique candidates sort by fee descending, sender ascending, nonce ascending,
and ciphertext hash ascending. The scan stops at the effective transaction cap
(`min(configured cap, BMax)`) and skips, rather than terminates on, a candidate
that does not fit the remaining gas. The selected order, gas, skipped count,
and canonical `MergedSetHash` are protocol outputs.

### 4. Scoped decode and deterministic plan

The node passes selected canonical ciphertext bytes to:

```text
DecodeBatchFor(encoded, {ClusterID: config.ClusterID, Slot: activeSlot})
```

Any selected structural, version, index, context, AEAD-shape, curve, or trailing
byte error records `decode` failure for the whole slot. A zero-length selected
batch completes successfully through the explicit empty-result path without a
`BatchPlan` or decryption shares.

Nonempty decoded batches call `PlanDecodedBatch`, which revalidates the retained
scope and returns the immutable `BatchID` plus deterministic Opt-2 sub-batches.
Planning failure records reason `planning`.

## Share Exchange And Threshold Combine

### Local shares

After committing the plan and measurement boundaries, the node calls
`MakeShare` once per sub-batch unless the `withhold-share` fault is active. It
adds its own share locally before serializing and sending it to every other
operator. `corrupt-share` mutates only the transmitted point, not the local
copy.

`shareGenerationDone` is set only after the sub-batch loop completes. Combine
cannot start earlier even if remote threshold candidates arrive during local
proof verification/share generation.

### Inbound shares

Before decoding a curve point, `addWireShare` requires a configured operator,
canonical 32-byte batch-ID hex, the exact encoded `G1` length, and a sub-batch
ID in `[0,BMax)`. Authenticated envelope validation independently binds the
share operator to `Envelope.From` and the configured libp2p peer. `addShare`
then requires the Kyber public-share index to equal that operator ID.

Before planning, each configured operator may retain one batch identity and
one immutable candidate per sub-batch, bounding storage by `N*BMax`. After the
local plan exists, other batch identities and sub-batches outside the exact
plan range are pruned, reducing the bound to `N*alpha`. Byte-identical repeats
are idempotent; conflicting replacements are rejected and the first candidate
is retained. Accepted and fixed-category rejected counters never include
attacker-controlled metric labels.

### Combine single-flight

`claimCombine` starts at most one reconstruction when:

- a plan exists;
- local share generation is finished;
- no result or combine attempt exists; and
- distinct configured operator IDs reach threshold in every sub-batch; and
- every sub-batch still has recovery-attempt budget.

It snapshots the plan, materialization prefix, canonical candidate set, share
version, and remaining per-sub-batch budgets. The BTE library sorts by operator
ID and searches deterministic threshold subsets. Attempt statistics are
charged back to slot state, so a new share can trigger one retry but cannot
reset work already consumed. A sub-batch that exhausts its configured budget
permanently fails closed for that slot. Candidates are still not publicly
verifiable; the bounded search is the prototype fallback rather than proof of
production-grade share validity.

## Materialization And Results

Successful BTE combine returns raw bytes in selected consensus order. For each
item the node records raw hex and SHA-256, then parses a signed Ethereum
transaction with go-ethereum, requires a nonzero chain ID, and recovers the
sender.

If Ethereum parsing fails, the current code replaces that entry's displayed
plaintext with an `ERROR:invalid ethereum transaction` string but still
publishes a completed slot result. It does not retain a placeholder, refill the
batch, fail the slot, or ask an execution client to validate stateful semantics.
This is a protocol-completeness limitation, distinct from fail-closed selected
ciphertext decoding.

The final `MaterializedTransactionSet` records slot, agreed/merged identities,
selected gas, encrypted hashes, plaintext hashes/hex, and parsed Ethereum
summaries. `Result` adds node ID, `BatchID`, ciphertext count, total latency,
and the finalized metrics snapshot.

## Fail-Closed And Empty-Slot Behavior

Failures during proposal construction, ACS input, accepted-list decoding,
selected ciphertext decoding/planning, or share generation call
`markSlotFailed`. The first call atomically fixes phase `failed` and stores
`SlotFailure{slot, reason, failed_at_unix_nano}`; later calls and late success
paths cannot replace that outcome. Reasons are normalized to the bounded
`proposal`, `acs`, `decode`, `planning`, `share`, `combine`, or `unknown`
labels. `/result` returns the same HTTP 422 failure on repeated reads, while
`/slot/status` includes it for diagnostics. Local, persistent, and remote
evaluators stop polling on 422, retain the bounded reason in JSON/JSONL and both
CSV formats, emit a run-level row even when no node succeeded, and exclude the
failed observation from latency summaries.

Some inbound ACS/share errors are only logged and do not call
`markSlotFailed`, because another valid message may still allow progress.

An empty selected merge is a successful slot. It preserves agreed/merged hashes
and gas, records zero ciphertext/share/combine work, publishes empty plaintext
arrays and an empty `BatchID`, marks metrics finalized, and transitions to
`completed`.

## Determinism And Cross-Module Invariants

- One active slot owns all mutable protocol state.
- Accepted list slot/operator metadata must agree with the ACS wrapper.
- Canonical list hashing and sorting precede merge.
- Merge order and bounds determine the only ciphertext order passed to BTE.
- Production BTE decoding binds cluster ID and slot before share release.
- `BatchID`, sub-batch memberships, and original positions must match at every
  correct operator.
- Only matching-batch, in-range sub-batch shares reach BTE combine.
- Share candidates are keyed by authenticated operator. Before planning, each
  operator may establish one batch identity and one candidate for each of at
  most `BMax` sub-batches; after planning, mismatched candidates are pruned and
  the bound becomes one candidate per operator for each of `alpha` sub-batches.
- Identical share retransmissions are idempotent. A conflicting second point
  for the same operator/sub-batch is rejected without replacing the first.
- Subset recovery is deterministic and consumes a cumulative configured budget
  per sub-batch across retries; exhaustion prevents further combine work.
- Results preserve selected order even though cryptography executes by
  sub-batch.
- Old-slot envelopes are discarded before metrics and state mutation.

## Concurrency And Ownership

- `lifecycleMu` prevents slot replacement while handlers and sends use the
  current embedded `slotState`.
- `inputMu` serializes direct submissions and proposal freeze.
- `acsMu` serializes the non-thread-safe, consumptive ACS transition boundary.
- `slotState.mu` protects phase, pending data, plan, shares, result, and metrics.
- `startOnce` prevents duplicate proposal input.
- `combineInFlight` plus `shareVersion` provides single-flight combine with
  useful retry semantics.
- The node treats a returned BTE plan as immutable after publication to slot
  state.

## Observability Boundary

The protocol stage timestamps are monotonic `time.Time` values; Unix timestamps
are evidence fields. Exported microsecond intervals are proposal preparation,
ACS, ACS-output decode, agreed-set construction, merge, ciphertext decode,
batch planning, share generation, threshold wait, combine, materialization,
commit-to-plaintext, and total slot.

Share generation and threshold wait can overlap, so all stage values are event
intervals rather than a universally additive decomposition. Exact measurement
and experiment acceptance semantics are in
[VALIDATION.md](../../docs/VALIDATION.md). Generic evaluator workflow is in
[WORKFLOWS.md](../../docs/WORKFLOWS.md); environment-specific deployment is in
the matching `deploy/*/README.md`.

## Paper Correspondence And Deviations

The repository's [BLOC paper](../../papers/BLOC_Final.pdf) supplies the
top-level encrypted-proposal/agreement/decryption concept. The node implements
that concept as an external sidecar and local materialization surface rather
than integrating with a beacon/execution client or DVT signing protocol.

Consensus details and deviations from HoneyBadger are in
[hbbft.md](../../docs/modules/hbbft.md). BEAT-MEV mapping, setup shortcuts, and
cryptographic limitations are in [bte.md](../../docs/modules/bte.md). The
sidecar-specific additions are the slot lifecycle, application wire envelopes,
canonical merge identities, scoped BTE boundary, share gossip, and evaluator
metrics.

## Test Evidence

Current `bloc-node` tests cover:

- v2 public/secret config separation, legacy rejection, CRS loading, operator
  binding, peer-key derivation, and libp2p-only enforcement;
- protobuf round trips for every ACS/share payload and inclusion lists;
- deterministic merge, bounds, invalid candidates, duplicates, conflicting
  first winner, and empty selection;
- accepted-list slot/proposer checks and wrong `SlotOutput` slot rejection;
- mempool encrypted-payload adaptation and malformed payload rejection;
- strictly increasing completed-slot replacement, stale-envelope metrics,
  in-flight-send drain, and serialized ACS stepping;
- slot-status ACS diagnostics;
- share filtering, combine threshold admission, single-flight, retry on new
  shares, per-operator retention bounds, conflicting-duplicate rejection,
  cumulative subset-attempt budgets, and successful-result finality;
- deterministic signed Ethereum test transactions;
- authenticated envelope/share sender validation, complete transport writes,
  and HTTP connection reuse; and
- evaluator scheduling, consistency, metrics boundaries, and artifact policy.

The suite tests sender binding, exact/oversized envelope reads, outbound size
rejection, proposal bounds, share retention/pruning, and bounded n=10 recovery.
It also covers pending/success/failure/wrong-slot result reads, repeated failure
reads, failed-slot replacement, late-success rejection, evaluator 422 handling,
failure-before-start ordering, synchronous start failure routing, legacy
`eval-local` failure rows, and failure exclusion from latency summaries. It does
not yet exercise an oversized malicious remote stream end-to-end or cover a
deterministic invalid-Ethereum fallback.

Run `go test ./...` from `bloc-node`.

## Known Limitations

- Setup and operator secrets still originate at one trusted generator; this is
  isolation for the prototype deployment, not DKG or hardened key custody.
- The public CRS still includes inherited insecure diagonal elements.
- Envelope/proposal bytes, retained share candidates, and recovery attempts are
  bounded by shared v2 configuration. Public share correctness proofs are still
  unavailable, so bounded subset recovery remains a prototype fallback.
- `mempool-http` proposal fetch has no explicit timeout.
- Ethereum validation is syntactic only and invalid raw bytes still produce a
  completed result with per-item errors.
- There is no execution payload, Builder API, DVT signing, slashing,
  execution-state validation, public share proof, DKG, or secure key store.
- The `omit-proposal` fault proposes a valid empty list; it does not simulate a
  network-silent proposer.
- Confirmed review findings and priorities are in
  [PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md](../../docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).
