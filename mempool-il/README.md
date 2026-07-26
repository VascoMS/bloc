# mempool-il

Standalone Go service that ingests pending Ethereum transactions, builds a deterministic mempool snapshot, and produces a deterministic bounded inclusion list.

For the cross-module architecture, read [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md).
For source normalization, replay-placeholder construction, list identity, and
HTTP-boundary details, read
[docs/modules/mempool-il.md](../docs/modules/mempool-il.md). For validation
expectations, read [docs/VALIDATION.md](../docs/VALIDATION.md).

## What It Does

- Reads pending transactions from one of multiple sources:
  - `txpool`
  - `public-pending`
  - `alchemy-pending`
  - `replay-placeholder`
- Classifies transactions as `plaintext` or `placeholder`
- Maintains an in-memory indexed mempool view
- Exposes deterministic snapshot and inclusion list over HTTP

## Requirements

- Go 1.24+
- Ethereum RPC endpoint

## Run

### Local node txpool mode

```sh
go run ./cmd/service \
  -source txpool \
  -rpc-url http://127.0.0.1:8545
```

### Public pending block mode

```sh
go run ./cmd/service \
  -source public-pending \
  -rpc-url https://your-rpc-endpoint
```

### Alchemy pending filter mode

```sh
go run ./cmd/service \
  -source alchemy-pending \
  -rpc-url https://eth-mainnet.g.alchemy.com/v2/<API_KEY> \
  -alchemy-ttl 5m
```

## CLI Flags

- `-source`: `txpool | public-pending | alchemy-pending | replay-placeholder`
- `-rpc-url`: JSON-RPC URL
- `-listen`: HTTP listen address
- `-poll-interval`: mempool polling interval
- `-max-items`: maximum inclusion-list length
- `-max-gas`: maximum inclusion-list gas
- `-max-block-gas`: block gas used when auto-computing the cap
- `-alchemy-ttl`: retention for `alchemy-pending` cache
- `-corpus`: signed transaction JSONL for `replay-placeholder`
- `-cluster-config`: public BLOC cluster configuration for replay encryption
- `-replay-slot`: slot bound into replay ciphertexts

When `-max-gas=0`, the service uses `2 * max_block_gas`.

## HTTP API

- `GET /healthz`
- `GET /snapshot`
- `GET /inclusion-list`

Example requests:

```sh
curl -s http://127.0.0.1:8080/healthz | jq
curl -s http://127.0.0.1:8080/snapshot | jq
curl -s http://127.0.0.1:8080/inclusion-list | jq
```

## Source Quality Notes

- `txpool`: closest to a real node mempool
- `public-pending`: pending-block candidate only
- `alchemy-pending`: approximate mempool view reconstructed from provider events and backfill
- `replay-placeholder`: deterministic thesis/mock mode; reads real signed target
  transactions from a corpus, encrypts them once using BLOC public cluster
  material, and exposes mock placeholder candidates with `encrypted_payload_hex`

### Replay Placeholder Mode

```sh
go run ./cmd/service \
  -source replay-placeholder \
  -cluster-config ../bloc-node/cluster.json \
  -corpus ../deploy/docker-compose/corpus/mock-targets.jsonl \
  -replay-slot 1
```

The corpus is JSONL with one labelled object per target transaction:

```json
{"class":"calldata_128","raw_tx":"0x..."}
```

The inclusion-list API exposes placeholder metadata and encrypted payloads. It
does not expose raw target transaction bytes to sidecar proposals.

## Client-Overhead Corpus And Report

The committed `client-overhead-targets.jsonl` corpus contains 500 deterministic
EIP-1559 transactions signed for development chain ID 1337:

| Class | Exact target calldata | Rows |
|---|---:|---:|
| `transfer` | 0 bytes | 100 |
| `calldata_128` | 128 bytes | 100 |
| `calldata_256` | 256 bytes | 100 |
| `calldata_1024` | 1,024 bytes | 100 |
| `calldata_4096` | 4,096 bytes | 100 |

The publicly derivable signer and payloads are test material. Never fund or
reuse the signer on a live chain. The strict client validator checks all 500
rows, exact balanced class sizes and counts, chain ID, EIP-1559 type, recoverable
signatures, EIP-7623 data-floor gas limits, and unique hashes. The ordinary
replay loader remains permissive enough to read unlabelled development
fixtures.

Generate raw client-overhead samples from the module root:

```sh
go run ./cmd/corpus-report \
  -corpus ../deploy/docker-compose/corpus/client-overhead-targets.jsonl \
  -cluster-config ../results/issue-13-client-overhead/cluster.json \
  -out ../results/issue-13-client-overhead/client_overhead.csv \
  -slot 1 \
  -samples-per-class 100
```

The output contains exactly 100 measurements per class in stable class/sample
order. Every row measures a different signed target, so the report does not
cycle transactions. Results remain class-specific; the command does not
calculate a weighted or pooled client summary.
`raw_bytes`, `ciphertext_bytes`, `placeholder_bytes`, and `calldata_bytes`
record the relevant encoded sizes. `encryption_us` times BTE encryption and
canonical ciphertext binary encoding.
`submission_serialization_us` times hex encoding and JSON serialization of
`{"raw_tx":"0x..."}` without network I/O. Encryption is randomized, so timing
and ciphertext contents vary between runs.

`carrier_gas_estimate` is the post-Pectra EIP-7623 data-only floor:

```text
tokens = zero calldata bytes + 4 * nonzero calldata bytes
carrier_gas_estimate = 21,000 + 10 * tokens
```

It is an estimate, not paid gas or an execution receipt. Generated reports,
public cluster material, and development operator secrets stay under ignored
`results/` paths.

### Full-Protocol Workload Share Methodology

The separate `mock-targets.jsonl` corpus is the deterministic input for the
full-path mock-placeholder rehearsal across client submission, ACS, merge, BTE
decryption, and materialization:

| Class | Exact target calldata | Protocol workload rows |
|---|---:|---:|
| `transfer` | 0 bytes | 28 |
| `calldata_128` | 128 bytes | 50 |
| `calldata_256` | 256 bytes | 12 |
| `calldata_1024` | 1,024 bytes | 8 |
| `calldata_4096` | 4,096 bytes | 2 |

The class weights approximate transaction frequency in one recent mainnet
sample for protocol workloads only. They do not determine client measurement
counts or produce a weighted client result. At `2026-07-26T15:01:55Z`, 360
blocks were read from
`ethereum-rpc.publicnode.com` with JSON-RPC `eth_getBlockByNumber` and full
transaction objects. Sampling started 64 blocks behind the reported head and
selected every twentieth block from 25,617,666 down to 25,610,486, covering
approximately 24 hours and 74,383 transactions.

For each transaction, payload size was `(len(input) - 2) / 2`, where `input` is
the `0x`-prefixed JSON-RPC field. Empty input was counted as zero bytes.
Contract-creation input was included as payload even though it is initcode, not
message-call calldata. There were 55 contract creations in the sample.

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

The zero-byte bin maps to `transfer`; 1–255 maps to the 128-byte
representative; 256–1,023 maps to 256; 1,024–4,095 maps to 1,024; and 4,096+
maps to 4,096. Rounding transaction shares to 100 rows gives
`28/50/12/8/2`. Byte share does not set row counts; it justifies retaining the
rare large-payload classes, which accounted for most sampled bytes.

This is a dated, one-day observational sample used to justify a benchmark
workload mix for the full protocol, not a universal Ethereum distribution. No
mainnet sampling
command is part of the repository. See [VALIDATION.md](../docs/VALIDATION.md)
for the evidence contract.

## Test

```sh
GOCACHE=/tmp/go-build go test ./...
```
