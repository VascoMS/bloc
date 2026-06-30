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

This keeps mempool logic, ordering, and decryption concerns separated.

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
and Kubernetes runs listen on container interfaces while advertising service or
pod DNS names that other sidecars and evaluators can dial.

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
