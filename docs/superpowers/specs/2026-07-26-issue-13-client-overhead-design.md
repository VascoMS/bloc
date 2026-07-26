# Issue 13 Client-Overhead Corpus Design

## Objective

Issue #13 adds a deterministic, representative Ethereum transaction corpus and
a local benchmark that compares plaintext submission preparation with encrypted
placeholder construction. The result is raw `client_overhead.csv` evidence for
the thesis, with at least 100 measurements per transaction class.

The implementation remains local. It does not query mainnet, submit
transactions, or commit live-chain key material.

## Corpus Contract

The committed corpus contains exactly 100 valid EIP-1559 transactions signed for
chain ID 1337. Each JSONL record contains a class label and `raw_tx`. Validation
derives the class again from the decoded transaction rather than trusting the
label.

| Class | Exact calldata bytes | Corpus rows |
|---|---:|---:|
| `transfer` | 0 | 28 |
| `calldata_128` | 128 | 50 |
| `calldata_256` | 256 | 12 |
| `calldata_1024` | 1,024 | 8 |
| `calldata_4096` | 4,096 | 2 |

All hashes are unique. Transactions use fixed development-only key derivation,
deterministic payloads, and distinct nonces. The repository commits signed raw
transactions, not live-chain secrets.

## Workload-Share Rationale

The corpus weights approximate transaction frequency in a recent mainnet
sample. They are not presented as a universal or longitudinal Ethereum
distribution.

The sample was collected at `2026-07-26T15:01:55Z` from the public
`ethereum-rpc.publicnode.com` endpoint through Ethereum JSON-RPC
`eth_getBlockByNumber` with full transaction objects:

- newest sampled block: 25,617,666;
- oldest sampled block: 25,610,486;
- finality offset from the reported head: 64 blocks;
- selection: every twentieth block, descending from the newest block;
- sampled blocks: 360, spanning approximately 24 hours;
- sampled transactions: 74,383; and
- contract-creation transactions: 55.

For each transaction, payload bytes were calculated from the hex-encoded
`input` field as `(len(input) - 2) / 2`. Empty input was counted in the zero-byte
class. Contract-creation input was counted as payload even though it is initcode
rather than message-call calldata.

For each bin:

```text
transaction share = transactions in bin / all sampled transactions * 100
calldata byte share = input bytes in bin / all sampled input bytes * 100
```

| Observed input bytes | Transaction share | Calldata byte share |
|---|---:|---:|
| 0 | 28.408% | 0.000% |
| 1–127 | 43.388% | 5.621% |
| 128–255 | 6.114% | 2.187% |
| 256–1,023 | 11.919% | 12.798% |
| 1,024–4,095 | 7.924% | 29.444% |
| 4,096+ | 2.246% | 49.951% |

The fixed issue classes approximate these ranges as follows:

- zero bytes maps to `transfer`;
- 1–255 bytes maps to the 128-byte representative;
- 256–1,023 bytes maps to the 256-byte representative;
- 1,024–4,095 bytes maps to the 1,024-byte representative; and
- 4,096 bytes and above maps to the 4,096-byte representative.

Rounding the resulting transaction shares to a 100-row corpus produces
`28/50/12/8/2`. The byte-share figures do not determine row counts; they justify
retaining the rare large-payload classes because those classes account for most
sampled calldata bytes.

The canonical mempool README and validation guide will preserve this
methodology, table, mapping, date, and sample caveat. No mainnet sampling command
will be added to the repository.

Method references:

- Ethereum JSON-RPC `eth_getBlockByNumber`:
  <https://ethereum.org/developers/docs/apis/json-rpc/>
- Post-Pectra calldata and block-size analysis:
  <https://ethresear.ch/t/post-pectra-effects-on-ethereum-reorg-rate-propagation-and-block-size/23018>

## Report Architecture

`mempool-il/cmd/corpus-report` is a thin local command over focused report and
corpus-validation functions in `mempool-il/internal/mempool`.

The command accepts a corpus path, cluster configuration, output path, slot, and
samples-per-class value. It defaults to at least 100 samples per class and writes
generated output under an ignored `results/` directory.

The report path:

1. Loads and fully validates the corpus before opening the output.
2. Groups targets by their derived class.
3. Cycles deterministically through each class until it has the requested
   measurement count.
4. Times plaintext submission serialization for the signed raw target.
5. Times BTE encryption of those same signed target bytes.
6. Builds and signs the existing mock placeholder from the ciphertext.
7. Records byte counts and the carrier-gas estimate.
8. Writes rows in stable class and sample-index order.

Encryption remains randomized, so ciphertext contents and timings are not
expected to be byte-for-byte reproducible. Corpus membership, row ordering,
schema, class counts, and size calculations are deterministic.

## Measurement Definitions

`client_overhead.csv` contains:

```text
class
sample_index
target_hash
raw_bytes
ciphertext_bytes
placeholder_bytes
calldata_bytes
carrier_gas_estimate
encryption_us
submission_serialization_us
```

- `raw_bytes` is the signed target transaction length.
- `ciphertext_bytes` is the encoded BTE ciphertext length.
- `placeholder_bytes` is the signed placeholder transaction length.
- `calldata_bytes` is the placeholder transaction's calldata length.
- `encryption_us` measures only BTE encryption, excluding placeholder signing
  and encoding.
- `submission_serialization_us` measures hex encoding and JSON serialization of
  the plaintext raw-transaction submission request. It excludes network I/O.

`carrier_gas_estimate` is explicitly an estimate for a data-only carrier under
the post-Pectra EIP-7623 floor:

```text
tokens = zero calldata bytes + 4 * nonzero calldata bytes
carrier_gas_estimate = 21,000 + 10 * tokens
```

It is not described as paid gas, an execution receipt, or a prediction for a
different chain configuration.

## Error Handling

Corpus validation fails before measurement when a row has invalid JSON or hex,
cannot decode as a signed Ethereum transaction, has the wrong chain ID, has no
recoverable sender, duplicates another transaction hash, has an unknown or
mismatched class, violates the exact class distribution, or violates its exact
calldata length.

The report also fails on invalid cluster configuration, encryption or
placeholder construction errors, an output path that cannot be created, or a
samples-per-class value below 100. It must not silently emit a partial artifact.

## Test Strategy

Test-driven implementation begins with failing tests for:

- exactly 100 committed corpus rows;
- the `28/50/12/8/2` class distribution;
- exact class calldata lengths;
- valid signatures, recoverable senders, chain ID 1337, and unique hashes;
- report header and row schema;
- at least 100 rows per class;
- stable class and sample ordering;
- byte-count definitions;
- plaintext submission serialization;
- the EIP-7623 carrier-gas estimate; and
- expected validation failures.

Focused tests use temporary fixtures and injectable boundaries where needed to
avoid turning unit tests into timing assertions. An integration test exercises
the real encryption and placeholder path on a small fixture. Completion requires
both:

```sh
cd mempool-il && go test ./...
cd bloc-node && go test ./...
```

## Documentation and Tracking

The implementation updates:

- `mempool-il/README.md`;
- `docs/modules/mempool-il.md`;
- `docs/VALIDATION.md`;
- `docs/CHANGELOG.md`; and
- `docs/STATUS.md` when the accepted artifact changes evidence completeness or
  immediate next actions.

The issue receives the validation outcome and artifact description. It is closed
only after its acceptance criteria are satisfied.

## Non-Goals

- No mainnet RPC sampler is committed.
- No transaction is submitted to a live chain.
- No network latency or mining latency is measured.
- No generated CSV is committed outside the repository's ignored results
  convention.
- No paid-gas claim is made.
