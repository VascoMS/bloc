# mempool-il

Standalone Go service that ingests pending Ethereum transactions, builds a deterministic mempool snapshot, and produces a deterministic bounded inclusion list.

For the cross-module architecture, read [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md). For validation expectations, read [docs/VALIDATION.md](/bloc/docs/VALIDATION.md).

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

- Go 1.22+
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

- `-source`: `txpool | public-pending | alchemy-pending`
- `-rpc-url`: JSON-RPC URL
- `-listen`: HTTP listen address
- `-poll-interval`: mempool polling interval
- `-max-items`: maximum inclusion-list length
- `-max-gas`: maximum inclusion-list gas
- `-max-block-gas`: block gas used when auto-computing the cap
- `-alchemy-ttl`: retention for `alchemy-pending` cache

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

The corpus is JSONL with one object per target transaction:

```json
{"raw_tx":"0x..."}
```

The inclusion-list API exposes placeholder metadata and encrypted payloads. It
does not expose raw target transaction bytes to sidecar proposals.

## Test

```sh
GOCACHE=/tmp/go-build go test ./...
```
