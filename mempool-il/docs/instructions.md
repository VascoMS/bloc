# Mempool + Inclusion List Module MVP

## Purpose

Build a standalone Go module/service that:

1. reads transactions from a local Ethereum execution client's txpool,
2. parses and classifies transactions,
3. stores them in an in-memory indexed mempool view,
4. generates deterministic snapshots,
5. builds a deterministic bounded inclusion list from those snapshots.

This project should be implemented as a single standalone service with two internal responsibilities:

- mempool ingestion
- inclusion list creation

These should be implemented as separate internal packages, but within the same project.

---

## Scope

## In scope

- polling a local execution client over JSON-RPC
- reading `txpool_content`
- flattening txpool data into a normalized transaction list
- classifying transactions as either:
  - plaintext transactions
  - placeholder transactions
- parsing placeholder transaction calldata
- maintaining an in-memory transaction store
- exporting deterministic mempool snapshots
- building deterministic inclusion lists
- exposing local HTTP endpoints for snapshot and inclusion list retrieval
- unit tests for core logic

## Out of scope

Do not implement any of the following:

- distributed consensus
- networking between operators
- threshold cryptography
- decryption
- signing
- block building
- persistent storage
- websocket subscriptions
- support for multiple execution clients
- advanced fee market simulation
- cryptographic verification of placeholder proofs

---

## One-sentence summary

Implement a local service that turns a live txpool into a deterministic, bounded inclusion list.

---

## High-level architecture

```text
Execution Client (Geth)
        |
        | JSON-RPC
        v
+-----------------------------+
|    Mempool + IL Service     |
|                             |
|  mempool/                   |
|   - rpc polling             |
|   - tx flattening           |
|   - tx classification       |
|   - placeholder parsing     |
|   - in-memory store         |
|   - snapshot generation     |
|                             |
|  inclusion/                 |
|   - filtering               |
|   - deduplication           |
|   - fee scoring             |
|   - sorting                 |
|   - gas/count capping       |
|   - list hashing            |
|                             |
|  api/                       |
|   - /healthz                |
|   - /snapshot               |
|   - /inclusion-list         |
+-----------------------------+