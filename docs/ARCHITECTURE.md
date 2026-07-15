# Architecture

## Purpose

BLOC is a thesis prototype that explores a pipeline where operators agree on encrypted transaction placeholders, then deterministically decrypt and materialize a shared transaction set after agreement.

## Module Boundaries

- `bloc-node/`
  - Integrates slot-scoped ACS, inclusion-list handling, batched threshold decryption, local evaluation, and transport.
  - Owns the main CLI, local cluster config generation, node runtime, evaluator, and report generation.
- `mempool-il/`
  - Builds deterministic bounded inclusion lists from a live Ethereum mempool view.
  - Stays independent from consensus and threshold cryptography.
- `bte/btd-impl-main/`
  - Provides the cluster-facing batched threshold encryption library.
  - Owns ciphertext encoding, batch planning, share generation, and share combination.
- `sbc/hbbft/`
  - Provides Reliable Broadcast, Binary Byzantine Agreement, and Asynchronous Common Subset.
  - Includes the BLOC-specific slot adapter that bypasses the original recurring HoneyBadger driver.

## End-to-End Flow

1. A transaction source provides raw transaction bytes and scheduling metadata.
2. `bloc-node` encrypts payloads into placeholder ciphertexts using the BTE library.
3. Each operator proposes one slot-scoped inclusion list into ACS.
4. ACS outputs a common subset of proposer batches.
5. `bloc-node` deterministically merges the accepted lists and computes a shared `BatchPlan`.
6. Operators publish one decryption share per sub-batch.
7. Once threshold shares exist for every sub-batch, the BTE library combines them and recovers the agreed plaintexts in consensus order.
8. `bloc-node` emits a materialized transaction set and evaluation artifacts.

For synthetic/local evaluator runs, the evaluator submits raw signed Ethereum
transactions directly to each sidecar and the sidecar performs step 2. For
realistic mock-mempool runs, the transaction source is split into two layers:
real signed Ethereum transactions are treated as target payloads, and
`mempool-il` acts as a mock external submitter that encrypts those targets once
into BLOC encrypted payloads and exposes mock placeholder candidates. Sidecars
then include the encrypted payloads; they do not independently re-encrypt the
same public transaction.

## Post-ACS Merge And Batch Planning

The Merge and Batch Planning phase is the deterministic bridge between an ACS
decision and share generation. Every correct operator executes the same five
stages locally over the same ordered ACS output. No network exchange occurs
inside this phase.

1. **ACS output decoding.** ACS returns accepted
   proposer payloads as protobuf bytes because consensus treats proposals as
   opaque application data. `DecodeList` parses each payload into an inclusion
   list and validates its basic structure. It does not hash the list.
2. **Agreed-set construction.** `NewAgreedSet` computes each
   accepted inclusion-list hash exactly once using the existing canonical JSON
   definition. It sorts lists by hash and then operator ID, counts their items,
   and hashes that canonical list sequence into the slot's agreed-set identity.
3. **Deterministic merge.** The merge validates
   ciphertext presence, gas, decimal effective fee, and the claimed SHA-256
   ciphertext hash. It parses each effective fee once and carries the parsed
   integer through sorting. An exact repeated placeholder with the same claimed
   hash, ciphertext, and metadata uses a fast path after the first validation;
   conflicting duplicates still take the full validation path. Unique
   candidates are ordered by effective fee descending, sender, nonce, and hash,
   then bounded by `BMax`, transaction-count, and gas limits. The selected order,
   gas total, skipped count, and merged-set hash are protocol outputs.
4. **Ciphertext decoding.** `DecodeBatch`
   serially parses each selected outer ciphertext and its BTE capsule. The
   capsule contains seven encoded curve points and two encoded scalars, which
   are reconstructed through the Kyber/Kilic suite. The result is a
   `DecodedBatch` pairing decoded objects with the accepted canonical wire
   encodings. Structural, length, and trailing-byte errors fail the slot; no
   partially decoded batch is planned.
5. **Batch planning.** `PlanDecodedBatch` validates the
   decoded metadata, chooses deterministic Opt-2 sub-batches, and separates
   repeated puncture indices. It derives `BatchID` directly from the already
   accepted ordered encodings, including each encoding's length, instead of
   serializing every curve object a second time. The resulting `BatchID`,
   `alpha`, original positions, and sub-batch membership must match at every
   operator before share generation begins.

The retained implementation deliberately preserves the original wire formats,
JSON hash definitions, first-winner merge behavior, ordering rules, gas limits,
and BTE plan identities. The optimizations remove repeated work within those
boundaries; they do not change the protocol algorithm. Ciphertext decoding is
currently serial, with no worker pool, object pooling, unsafe byte conversion,
or decoded-ciphertext cache. Empty selected sets complete the list and merge
boundaries, record zero ciphertext-planning duration, and materialize an empty
result without constructing a `BatchPlan`.

For observability, these stages map respectively to the exported
`acs_output_decode_us`, `agreed_set_us`, `merge_us`, `ciphertext_decode_us`, and
`batch_plan_us` fields. Those integer fields record microseconds in evaluator
artifacts; Prometheus exposes the same bounded stage concepts using seconds.
Their durations must add up to the enclosing Merge and Batch Planning duration
within the measurement tolerance documented in `docs/VALIDATION.md`.

## Cross-Module Invariants

- The accepted encrypted set must be deterministic across honest operators.
- Batch planning must be deterministic for a fixed ordered ciphertext list.
- Decryption shares must be scoped to the agreed `BatchID` and sub-batch.
- Module-local artifacts such as demo outputs and benchmark results are not canonical documentation.
- `mempool-il` is a source of candidate data, not a consensus participant.

## ACS Adaptation

The BLOC path intentionally does not use the original `HoneyBadger` epoch driver as the top-level integration boundary. Instead, `sbc/hbbft` exposes a slot-scoped adapter that:

- runs one ACS instance per slot,
- accepts externally prepared candidate batch bytes,
- wraps traffic in slot-bound messages,
- orders accepted proposer batches deterministically,
- leaves post-agreement decryption/materialization outside the ACS core.

RBC establishes availability and consistency for each proposer payload; it does
not decide common-subset membership. Each proposer has a BBA instance, and ACS
completes only after at least `N-F` instances decided true, all `N` BBA results
are present, and every true decision has its RBC payload. The output contains
exactly those true proposers. Within BBA, only AUX messages whose values have
already entered the local BV-broadcast `binValues` set count toward the `N-F`
advance threshold. These rules make reordered delivery change timing, not the
subset selected by correct operators.

The slot status endpoint exposes sorted RBC output IDs, completed BBA decisions,
truthy BBA proposer IDs, and an explicit waiting reason. These fields are
diagnostic only and never drive protocol decisions. This keeps consensus,
mempool logic, deterministic ordering, and decryption concerns separated.

## Repeated Slot Lifecycle

An operator process owns long-lived membership, BTE key material, HTTP state,
and its libp2p mesh. Its active `slotState` owns the ACS instance, pending
transactions, decryption shares, result, and measurements for exactly one slot.
Only one slot may be active at a time. Replacing a completed slot drains current
handlers and sends, closes ACS/RBC/BBA goroutines, and installs a clean state
with a strictly increasing slot identifier. Envelopes for older slots are
discarded before they can affect active-slot metrics.

The M1 evaluator exploits this boundary to measure many steady-state slots per
cluster without rebuilding cryptographic and network setup around every sample.
The isolated evaluator remains available for lifecycle smoke testing.

## Operator Transport

Operators exchange addressed ACS and decryption-share protobuf envelopes over
direct libp2p streams. Local configurations use TCP multiaddresses underneath,
while libp2p supplies peer identity, authenticated connections, and stream
multiplexing. gRPC and the former raw socket-per-envelope TCP transport are not
part of the active prototype.

## Distributed Sidecar Deployment

The deployment-ready path treats `bloc-node` as the BLOC sidecar process that
can run beside a future DVT operator. For this milestone, the sidecar remains
responsible only for the BLOC protocol path and measurement surface:

- HTTP control API for transaction submission, slot preparation/start, result
  polling, health checks, and Prometheus metrics.
- libp2p operator mesh for ACS and decryption-share traffic.
- mounted trusted-dealer cluster config for prototype key material.
- remote evaluator access through advertised HTTP URLs.

Cluster config supports both legacy local addresses and explicit
listen-vs-advertise fields. Local runs default to loopback addresses. Container
rehearsal runs listen on container interfaces while advertising dialable
addresses that other sidecars and evaluators can reach.

The primary distributed thesis evaluation shape is VM/EC2-per-sidecar rather
than an orchestrated container cluster. In that shape, each BLOC operator runs
on an independent VM with its own network identity, and a separate controller
machine runs `eval-remote`, artifact collection, and optional metrics
collection. This better matches the protocol model and keeps orchestration
behavior out of the main distributed latency results.

Prometheus-compatible `/metrics` is the live operational visibility interface.
It uses the official Go Prometheus client with bounded labels, counters for
events, gauges for current state, and seconds-based histograms for slot and HTTP
latency. Grafana dashboards query histogram buckets for p50/p95 panels.

Evaluator CSV/JSON files remain the offline experiment artifact format used for
chart generation and thesis tables. They intentionally preserve microsecond
columns such as `total_slot_us` for backwards compatibility with the existing
chart module; they are not mirrored one-to-one into Prometheus.

Builder API compatibility is not part of this deployment milestone. The later
Builder-boundary milestone may adapt the materialized transaction set into a
Builder-facing development API, but the current sidecar does not claim to build
valid Ethereum execution payloads.

## Cluster BTE Design

The BTE module exposes a cluster-facing API for raw transaction bytes:

- `EncryptTx` produces a public ciphertext that carries:
  - BTE capsule metadata,
  - AEAD-encrypted raw transaction bytes,
  - committed plaintext hash,
  - cluster and slot context.
- `PlanBatch` computes a deterministic `BatchPlan` over the agreed ciphertext order.
- `MakeShare` emits one threshold share per sub-batch.
- `CombineShares` recovers plaintexts once threshold shares are available.

`PlanBatch` uses the BEAT-MEV sub-batching optimization by default. For a
batch with `B` ciphertexts, it chooses `alpha = ceil(2*sqrt(B))`, corresponding
to the paper's `Opt-2` setting, then raises `alpha` if repeated puncture indices
require more sub-batches. This deterministic layout is part of the integrated
`bloc-node` path, including M1 evaluator runs.

The integrated prototype does not currently expose runtime switches for the
paper's normal/unoptimized combine path, `Opt-1` (`alpha = sqrt(B)`), or
parallel sub-batch combination. Those remain benchmark or future M2 comparison
dimensions, not M1 profile dimensions.

The current implementation is prototype-grade:

- trusted-dealer key generation is still used,
- public share-verifiability is not implemented,
- Ethereum execution, Builder API compatibility, and proposer signing are still
  out of scope.

## Mempool Inclusion-List Service

`mempool-il` is designed as a separate service with two internal responsibilities:

- mempool ingestion and classification,
- deterministic inclusion-list construction.

It reads pending transactions from configured RPC-backed sources, normalizes them into an in-memory indexed store, and exposes deterministic snapshots and bounded inclusion lists over HTTP.

For thesis data-realism tests, `mempool-il` also supports a
`replay-placeholder` source. This mode reads a deterministic corpus of real raw
signed Ethereum target transactions, validates them with `go-ethereum`,
encrypts each target once using BLOC public cluster material, signs a mock
placeholder Ethereum transaction, then parses that placeholder transaction's
calldata as the source of truth. The parsed inclusion-list items include:

- `target_tx_hash` and target size/type metadata,
- `encrypted_payload_hex`, derived from the placeholder calldata and consumed by sidecars,
- mock placeholder transaction hash/calldata/gas metadata.

The inclusion-list API does not expose raw target transaction bytes. The
sidecar-facing encrypted payload is extracted from an Ethereum-shaped
placeholder transaction rather than supplied as a parallel source field.
Execution-client validation and Builder API compatibility remain later
milestones.

## Canonical References

- Local developer workflow: [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- Validation matrix: [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)
- Major design history: [docs/DECISIONS.md](/bloc/docs/DECISIONS.md)
