# `mempool-il` Implementation Architecture

## Responsibility And Non-Goals

`mempool-il` is a standalone candidate-data service. It reads Ethereum-shaped
pending transactions, normalizes them into one internal model, maintains a
snapshot, and builds a deterministic bounded inclusion list. In
`replay-placeholder` mode it also acts as the prototype's mock external
submitter: it encrypts signed target transactions once and embeds the encrypted
payload in a signed placeholder transaction.

The service is not a consensus participant. Its list hash is a service-local
snapshot artifact, not an ACS decision or a `bloc-node` agreed-set identity.
It does not decide the final transaction set, release BTE shares, validate an
execution payload, or implement a production Ethereum inclusion-list API.

System-level context is in [ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Source Map

| Stage | Principal source and symbols |
| --- | --- |
| Process wiring | [`cmd/service/main.go`](../../mempool-il/cmd/service/main.go): source selection, polling, list bounds, HTTP lifecycle |
| Source contracts | [`internal/mempool/reader.go`](../../mempool-il/internal/mempool/reader.go): `Source`, `SlotSource`, `Reader` |
| RPC normalization | [`internal/mempool/rpc.go`](../../mempool-il/internal/mempool/rpc.go): `RPCClient`, `flatten`, `normalizeTx` |
| Public pending block | [`internal/mempool/public_rpc.go`](../../mempool-il/internal/mempool/public_rpc.go): `PublicRPCClient.Fetch` |
| Alchemy filter cache | [`internal/mempool/alchemy_pending.go`](../../mempool-il/internal/mempool/alchemy_pending.go): `AlchemyPendingClient`, `upsert`, `expire` |
| Placeholder parsing | [`internal/mempool/classifier.go`](../../mempool-il/internal/mempool/classifier.go): `ClassifyAndParse`, `ParsePlaceholderCalldata` |
| Replay source | [`internal/mempool/replay_placeholder.go`](../../mempool-il/internal/mempool/replay_placeholder.go): corpus parsing, encryption, placeholder signing |
| Snapshot ownership | [`internal/mempool/store.go`](../../mempool-il/internal/mempool/store.go): `Store.ReplaceAll`, `Store.Snapshot` |
| List construction | [`internal/inclusion/builder.go`](../../mempool-il/internal/inclusion/builder.go): `Builder.Build` |
| HTTP boundary | [`internal/api/server.go`](../../mempool-il/internal/api/server.go): `/healthz`, `/snapshot`, `/inclusion-list` |

## Inputs, Outputs, State, And Identities

### Internal transaction model

`mempool.Transaction` is the normalized source record. The fields used by
snapshot and list ordering are hash, sender, nonce, gas, kind, effective fee,
and queued status. Placeholder and replay records additionally carry target
metadata, the encrypted BTE payload, the signed placeholder transaction, and
calldata/gas measurements.

`EffectiveFeePerGas` selects the explicit replay override first, then
`maxFeePerGas`, then legacy `gasPrice`, and otherwise zero. The prototype does
not calculate EIP-1559 effective price against a current base fee; for dynamic
fee transactions it treats the fee cap as the ordering value.

### Snapshot identity

`Store.Snapshot` copies records under a read lock, then sorts them by sender,
nonce, and hash. Its SHA-256 identity is computed from newline-separated
records containing:

```text
hash|from|nonce|gas|kind|effective_fee|is_queued
```

The snapshot hash deliberately does not include every replay or placeholder
metadata field. For genuine signed Ethereum transactions the transaction hash
is expected to bind the transaction content. No downstream consensus identity
uses this snapshot hash.

### Inclusion-list identity

`Builder.Build` returns `items`, `count`, `total_gas`, and `hash`. The hash is
SHA-256 over newline-separated selected items containing:

```text
hash|from|nonce|gas|kind|effective_fee
```

The HTTP list hash is not forwarded into the `bloc-node` proposal. The sidecar
adapts selected encrypted payloads into its own `EncryptedPlaceholder` values,
recomputes their ciphertext hashes, and then computes the protocol inclusion-
list identity defined by `bloc-node`.

## Stage-By-Stage Data Flow

### 1. Source selection and polling

`cmd/service` selects exactly one source:

- `txpool` calls `txpool_content` and reads both `pending` and `queued` maps.
- `public-pending` calls `eth_getBlockByNumber("pending", true)` and represents
  only the provider's pending-block candidate set.
- `alchemy-pending` creates an `eth_newPendingTransactionFilter`, consumes
  `eth_getFilterChanges`, and resolves each hash through
  `eth_getTransactionByHash`.
- `replay-placeholder` loads a static signed-transaction corpus and cluster
  material, then implements both `Source` and slot-aware `SlotSource`.

For ordinary sources, `Reader.Run` polls immediately and then on a ticker. A
failed poll is logged and the previous store remains intact. A successful poll
atomically replaces the entire store. Replay mode does not run the polling
goroutine; the HTTP endpoint asks the slot-aware source for the requested slot.

### 2. RPC normalization and classification

`normalizeTx` lowercases address/hash/calldata strings and parses nonce, gas,
and fee fields from hexadecimal or decimal RPC values. Invalid nonce or gas is
an error for `txpool` and public-pending fetches. The Alchemy source skips an
individual transaction whose lookup or normalization fails so one transient
hash does not discard its retained cache.

`ClassifyAndParse` defaults every record to `plaintext`. It recognizes the
prototype placeholder format only when calldata is:

```text
"phld" selector (4 bytes)
target commitment (32 bytes)
requested gas (32-byte big-endian integer)
encrypted BTE ciphertext bytes (one or more bytes)
```

Successful parsing changes the record kind to `placeholder`, sets the target
commitment, derives SHA-256 of the encrypted payload, and replaces transaction
gas with the requested gas when it is nonzero. This is a custom prototype
encoding, not standard Ethereum ABI encoding or a finalized on-chain contract
interface.

### 3. Source-specific replacement behavior

`txpool` and public-pending results are whole-source snapshots and are keyed in
`Store` by transaction hash. `alchemy-pending` must reconstruct a view from
incremental filter events, so it owns two caches:

- `byHash` retains fetched transactions with their last update time.
- `bySenderNonce` retains one winner per sender/nonce.

On replacement, higher effective fee wins. Equal-fee ties choose the
lexicographically smaller hash. Entries older than the configured TTL are
removed. If an Alchemy filter expires, the client creates a new filter and
continues with its retained cache; it does not backfill a full historical
mempool.

### 4. Deterministic snapshot and list construction

`Store.ReplaceAll` builds a new hash-indexed map under an exclusive lock.
`Store.Snapshot` releases its read lock before sorting and hashing the copied
slice, so HTTP encoding and list construction do not block writers.

`Builder.Build`:

1. drops records with empty hash or zero gas;
2. deduplicates by transaction hash;
3. sorts by effective fee descending, sender ascending, nonce ascending, and
   hash ascending;
4. walks the sorted candidates once;
5. skips an item that would exceed the gas cap rather than terminating the
   scan;
6. stops when `MaxTransactions` is reached; and
7. calculates the service-local list hash over selected ordering fields.

The CLI default list cap is 128. A zero CLI `max-gas` becomes twice
`max-block-gas`; the builder's independent library default is 15,000,000 gas.

### 5. Replay-placeholder construction

The replay source reads JSONL entries containing `raw_tx`, optionally with a
class label, or a raw hex line. For every entry it:

1. decodes a signed Ethereum transaction;
2. requires a nonzero chain ID and recovers the sender;
3. verifies the configured CRS SHA-256 and reconstructs the BTE public object
   from the versioned public CRS artifact, `BMax`, and public key;
4. calls `EncryptTx(raw, index % BMax, clusterID, slot)`;
5. encodes calldata using the custom placeholder format;
6. derives a deterministic mock private key from the corpus index;
7. signs an EIP-1559 placeholder transaction on development chain 1337;
8. parses the resulting calldata through the normal classifier; and
9. exposes the parsed encrypted payload and metadata without exposing raw
   target bytes in the inclusion-list response.

The first request for a slot encrypts the complete corpus and caches the
result. Later requests return a shallow copy of the cached transaction slice.
Encryption is randomized, so the cached value is stable within a process but a
fresh service process produces different ciphertext bytes for the same corpus
and slot.

### 6. Client-overhead evidence

Issue #13 separates two strict corpus contracts from ordinary replay input.
`readClientOverheadCorpus` requires exactly 500 EIP-1559 transactions on
development chain 1337, with 100 unique targets in each
transfer/128/256/1,024/4,096-byte class. `readProtocolWorkloadCorpus` validates
the separate 100-row `28/50/12/8/2` mock-placeholder workload. Both require
recoverable signatures, unique hashes, sufficient EIP-7623 data-floor gas
limits, exact calldata sizes, and matching class labels. Permissive replay
loading remains separate so small local fixtures and raw hex lines continue to
work.

`corpus-report` reuses replay construction without timing the whole function.
The shared encryption boundary returns the encoded BTE ciphertext; placeholder
calldata construction and signing happen after the encryption timer stops.
Plaintext submission preparation times hex encoding and JSON serialization of
the same signed target bytes. It does not submit either path to a network.

The report consumes every balanced client target exactly once and writes
exactly 100 rows per class in stable class/sample order. It rejects an
incomplete class instead of cycling transactions. Corpus membership, schema,
sizes, and ordering are deterministic. Ciphertext contents and timings are not
because BTE encryption is randomized. Results remain per class; no weighted or
pooled client summary is produced.

The carrier figure applies the post-Pectra EIP-7623 data-only floor to
placeholder calldata: `21,000 + 10 * (zero bytes + 4 * nonzero bytes)`. It is a
gas-equivalent estimate, not paid gas. The protocol-workload weighting
provenance and exact one-day mainnet sample are owned by the
[mempool README](../../mempool-il/README.md#full-protocol-workload-share-methodology); the
acceptance rules and generation commands are in
[VALIDATION.md](../VALIDATION.md#rq4-client-overhead-corpus).

### 7. HTTP boundary

- `GET /healthz` returns a static service-health response.
- `GET /snapshot` returns the current ordinary-source snapshot.
- `GET /inclusion-list` builds a list from the current snapshot.
- In replay mode, `/inclusion-list?slot=<uint64>` fetches the slot-specific
  encrypted candidates directly and builds from them. An omitted or zero slot
  resolves to the replay source's configured default.

Invalid slot syntax returns HTTP 400. Slot-source failures return HTTP 502.
The handlers do not currently enforce HTTP methods, authentication, request
rate limits, or a production API schema.

## Determinism And Cross-Module Invariants

- Ordinary-source snapshots and lists are deterministic for an identical set
  of normalized records, independent of Go map iteration.
- Alchemy replacement ties are deterministic.
- Replay keys, placeholder sender/nonce, and corpus traversal are deterministic;
  BTE encryption randomness means ciphertext bytes are stable only after the
  per-process slot cache is populated.
- Target raw bytes are encrypted once by replay mode. `bloc-node` consumes the
  parsed encrypted payload and must not encrypt the target again.
- The sidecar requests the active slot and BTE later rejects any ciphertext
  whose embedded cluster or slot differs.
- `mempool-il` ordering is proposal-local. ACS and `bloc-node` merge determine
  the final selected ordering.

## Validation And Failure Semantics

- Whole-snapshot sources return errors for transport, HTTP status, JSON-RPC,
  decoding, nonce, or gas failures; the reader retains its previous snapshot.
- Alchemy skips failed individual lookups and normalizations, retains cached
  entries until TTL, and recreates a missing filter.
- Placeholder parsing is fail-open to plaintext classification: malformed or
  nonmatching calldata remains a plaintext transaction rather than failing the
  source fetch.
- The builder silently drops empty-hash/zero-gas records and skips candidates
  that do not fit its gas cap.
- `bloc-node` ignores non-placeholder records from the HTTP provider and fails
  proposal construction when a selected encrypted payload is malformed hex.

## Concurrency, Lifecycle, And Ownership

- `Reader` is the only ordinary polling writer to `Store`; `Store` supports
  concurrent snapshot readers through `RWMutex`.
- `AlchemyPendingClient` serializes filter state and holds its mutex across the
  associated JSON-RPC calls.
- `ReplayPlaceholderClient` serializes first-time per-slot encryption and owns
  a slot-indexed cache.
- Returned snapshots own their transaction slice. Transaction structs still
  contain pointer-valued `big.Int` fields, but normal service paths do not
  mutate them after normalization.

## Paper Correspondence And Deviations

The repository's [BLOC design paper](../../papers/BLOC_Final.pdf) motivates an
external source of encrypted candidate transactions and inclusion lists. This
module implements that boundary as a deterministic HTTP service and mock
placeholder producer. It does not implement a standardized Ethereum
inclusion-list proposal, execution-client mempool integration, or a production
searcher/submitter identity.

BEAT-MEV applies only to the encryption operation invoked by replay mode. The
BTE construction and its implementation deviations are documented in
[the BTE deep dive](../../docs/modules/bte.md).

## Test Evidence

The current module tests cover:

- deterministic bounded list construction in `builder_test.go`;
- exclusion of raw target bytes from mock list items in
  `mock_placeholder_test.go`;
- deterministic snapshot ordering in `store_test.go`;
- plaintext and placeholder classification in `classifier_test.go`;
- public-pending RPC normalization and nil pending blocks;
- Alchemy replacement, tie-breaking, filter recovery, and TTL behavior; and
- replay encryption, signed placeholder parsing, and ciphertext scope;
- strict committed-corpus validity, distribution, size, and uniqueness; and
- client-overhead sampling, serialization, gas estimation, CSV schema, and
  real encryption-boundary integration.

Run the complete module suite with `go test ./...` from `mempool-il`.

## Known Limitations

- Public and provider-backed sources are approximations of an execution
  client's complete mempool view.
- `Loop` exists in replay configuration but is not used by the current source.
- The replay service consumes only the public cluster JSON and CRS artifact; it
  has no access to operator shares or libp2p private keys.
- The issue #13 class distribution is based on one dated, approximately
  24-hour mainnet sample and is not a longitudinal workload model.
- The service list hash does not include every placeholder metadata field and
  is not a cross-module protocol identity.
- There is no authentication, pagination, response-size contract, persistence,
  chain reorganization handling, base-fee-aware ordering, or production
  inclusion-list standard.
- Confirmed security and correctness concerns are tracked in the
  [implementation review](../../docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md).
