# mempool-il

Standalone Go service that ingests pending Ethereum transactions, builds a deterministic mempool snapshot, and produces a deterministic bounded inclusion list.

## What it does

- Reads pending transactions from one of multiple sources:
  - `txpool` (local execution node via `txpool_content`)
  - `public-pending` (public RPC via `eth_getBlockByNumber("pending", true)`)
  - `alchemy-pending` (public RPC filter polling + tx backfill)
- Classifies transactions as `plaintext` or `placeholder`
- Maintains an in-memory indexed mempool view
- Exposes deterministic snapshot and inclusion list over HTTP

## Requirements

- Go 1.22+
- Ethereum RPC endpoint

## Run

### 1) Local node txpool mode (recommended for fidelity)

```bash
go run ./cmd/service \
  -source txpool \
  -rpc-url http://127.0.0.1:8545
```

### 2) Public pending block mode

```bash
go run ./cmd/service \
  -source public-pending \
  -rpc-url https://your-rpc-endpoint
```

### 3) Alchemy pending filter mode

```bash
go run ./cmd/service \
  -source alchemy-pending \
  -rpc-url https://eth-mainnet.g.alchemy.com/v2/<API_KEY> \
  -alchemy-ttl 5m
```

## CLI flags

- `-source`: `txpool | public-pending | alchemy-pending` (default: `txpool`)
- `-rpc-url`: JSON-RPC URL (default: `http://127.0.0.1:8545`)
- `-listen`: HTTP listen address (default: `:8080`)
- `-poll-interval`: mempool polling interval (default: `2s`)
- `-max-items`: max txs in inclusion list (default: `128`)
- `-max-gas`: max total gas in inclusion list (default: `0`, meaning auto)
- `-max-block-gas`: max block gas used for auto cap (default: `30000000`)
- `-alchemy-ttl`: retention for `alchemy-pending` cache (default: `5m`)

When `-max-gas=0`, the service uses:

- `inclusion_list_max_gas = 2 * max_block_gas`

## HTTP API

Service binds to `:8080` by default.

- `GET /healthz`
  - basic liveness response
- `GET /snapshot`
  - deterministic snapshot of current in-memory mempool view
- `GET /inclusion-list`
  - deterministic bounded inclusion list built from the snapshot

### Example requests

```bash
curl -s http://127.0.0.1:8080/healthz | jq
curl -s http://127.0.0.1:8080/snapshot | jq
curl -s http://127.0.0.1:8080/inclusion-list | jq
```

## Notes on source quality

- `txpool`: closest to real node mempool (best for determinism/fidelity).
- `public-pending`: pending block candidate only, not full mempool.
- `alchemy-pending`: approximate mempool view reconstructed from provider filter events and tx lookups.

## Test

```bash
GOCACHE=/tmp/go-build go test ./...
```
