# bloc-node

`bloc-node` is the integrated BLOC prototype node and local evaluator. It combines:

- slot-scoped ACS from `sbc/hbbft`,
- cluster-facing batched threshold encryption from `bte/btd-impl-main`,
- deterministic inclusion-list merging and materialization,
- protobuf operator messaging over libp2p streams,
- evaluation and reporting commands.

For the cross-module system design, read [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md). For the validation matrix, read [docs/VALIDATION.md](/bloc/docs/VALIDATION.md). For the standard demo and experiment flow, read [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md).

This module is a prototype harness, not a production DVT client. It still uses trusted-dealer configs and a local evaluation environment.

## Quick Local Run

Generate a local cluster config:

```sh
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 16 \
  --max-decrypted-gas 63000 \
  --base-http-port 18300 \
  --base-p2p-port 19300 \
  --out bloc-cluster.local.json
```

Start four operators:

```sh
go run ./cmd/bloc-node run --config bloc-cluster.local.json --id 0 --out results/manual/node-0.json
go run ./cmd/bloc-node run --config bloc-cluster.local.json --id 1 --out results/manual/node-1.json
go run ./cmd/bloc-node run --config bloc-cluster.local.json --id 2 --out results/manual/node-2.json
go run ./cmd/bloc-node run --config bloc-cluster.local.json --id 3 --out results/manual/node-3.json
```

Submit one transaction to each operator:

```sh
go run ./cmd/bloc-node submit --url http://127.0.0.1:18300 --tx 0x010203 --gas 21000 --fee-wei 40 --from 0x01 --nonce 0
go run ./cmd/bloc-node submit --url http://127.0.0.1:18301 --tx 0x040506 --gas 21000 --fee-wei 30 --from 0x02 --nonce 0
go run ./cmd/bloc-node submit --url http://127.0.0.1:18302 --tx 0x070809 --gas 21000 --fee-wei 20 --from 0x03 --nonce 0
go run ./cmd/bloc-node submit --url http://127.0.0.1:18303 --tx 0x0a0b0c --gas 21000 --fee-wei 10 --from 0x04 --nonce 0
```

Trigger the slot and inspect results:

```sh
curl -s -X POST http://127.0.0.1:18300/start
curl -s http://127.0.0.1:18300/result
cat results/manual/node-0.json
```

All correct operators should report the same `batch_id`, merged-set hash, selected gas, `ethereum_tx_hashes`, and `plaintexts_hex` ordering.

## Config Notes

`gen-config` writes:

- operator HTTP and libp2p addresses,
- the BTE public key,
- one trusted-dealer secret share per operator,
- a shared `crs_seed_hex`,
- blockspace caps and defaults,
- provider mode,
- libp2p peer identity details.

ACS and BTE share messages use protobuf envelopes over authenticated,
multiplexed libp2p streams. The generated bindings live in
`proto/bloc/v1/messages.proto` and `internal/pb/blocv1/messages.pb.go`. The
local multiaddresses use TCP underneath; libp2p is not gRPC.

Regenerate the protobuf bindings after schema edits with:

```sh
protoc --go_out=. --go_opt=module=bloc-node proto/bloc/v1/messages.proto
```

## Local Evaluation Harness

Run a reproducible local experiment:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32 \
  --tx-size 256 \
  --bmax 64 \
  --max-decrypted-gas 252000 \
  --tx-gas 21000 \
  --base-port 24000 \
  --out-dir results/local
```

Run the professor-facing demo flow:

```sh
./scripts/demo-local.sh
```

Useful fault-injection examples:

```sh
go run ./cmd/bloc-node eval-local --fault 3:omit-proposal
go run ./cmd/bloc-node eval-local --fault 3:withhold-share
```

The harness writes `summary.json`, `summary.csv`, per-run configs, per-node logs, and per-node result artifacts under the chosen `results/` directory.

## Repeated Latency Suite

Use `eval-suite` for M1 latency statistics across a configuration matrix:

```sh
go run ./cmd/bloc-node eval-suite \
  --profile m1-baseline \
  --experiment-id m1-baseline \
  --out-dir results/m1-local/baseline-persistent
```

The profile runs 4/7/10 operators and 8/32/128 transactions over libp2p with
5 warmups plus 30 measurements per scenario: 9 scenarios and 315 runs. It uses
one persistent cluster per operator count, but constructs a fresh ACS and clean
protocol state for every slot. Cluster startup, slot preparation, and transaction
submission are recorded separately from protocol latency. The suite keeps raw
per-node results, uses the slowest correct node as the run-level latency, and produces p50/p95 summaries. See
[docs/VALIDATION.md](/bloc/docs/VALIDATION.md) for metric boundaries and the
short smoke command.

Use `--execution-mode isolated` when validating process startup and teardown on
every sample. Custom suites remain isolated unless persistent mode is requested.
