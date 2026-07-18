# bloc-node

`bloc-node` is the integrated BLOC prototype node and local evaluator. It combines:

- slot-scoped ACS from `sbc/hbbft`,
- cluster-facing batched threshold encryption from `bte/btd-impl-main`,
- deterministic inclusion-list merging and materialization,
- protobuf operator messaging over libp2p streams,
- evaluation and reporting commands.

For the cross-module system design, read [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md).
For the node's protocol state machine, merge/share path, and failure semantics,
read [docs/modules/bloc-node.md](/bloc/docs/modules/bloc-node.md). For the
validation matrix, read [docs/VALIDATION.md](/bloc/docs/VALIDATION.md). For the
standard demo and experiment flow, read [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md).

This module is a prototype harness, not a production DVT client. It still uses
a trusted setup/key generator and a local evaluation environment.

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
  --out cluster.json
```

Start four operators:

```sh
go run ./cmd/bloc-node run --config cluster.json --secrets secrets/operator-0.json --id 0 --out results/manual/node-0.json
go run ./cmd/bloc-node run --config cluster.json --secrets secrets/operator-1.json --id 1 --out results/manual/node-1.json
go run ./cmd/bloc-node run --config cluster.json --secrets secrets/operator-2.json --id 2 --out results/manual/node-2.json
go run ./cmd/bloc-node run --config cluster.json --secrets secrets/operator-3.json --id 3 --out results/manual/node-3.json
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

`gen-config` writes three kinds of files:

- public `cluster.json` with operator HTTP/libp2p addresses and peer IDs,
- explicit HTTP/libp2p listen and advertised addresses for container deployments,
- the BTE public key plus the path and SHA-256 of public `cluster.crs`,
- one `secrets/operator-<id>.json` containing only that operator's
  trusted-dealer BTE share and libp2p private key,
- blockspace caps and defaults,
- provider mode.
- shared resource limits for encoded proposals, libp2p envelopes, and
  per-sub-batch recovery attempts.

The clean v2 config boundary rejects legacy files that combine the CRS seed,
all BTE shares, and all libp2p private keys. The public CRS does not contain the
setup seed/scalars, but it still retains inherited insecure diagonal elements;
it is not a production-secure setup.

ACS and BTE share messages use protobuf envelopes over authenticated,
multiplexed libp2p streams. The generated bindings live in
`proto/bloc/v1/messages.proto` and `internal/pb/blocv1/messages.pb.go`. The
local multiaddresses use TCP underneath; libp2p is not gRPC.

The v2 defaults are 8 MiB per encoded proposal, 16 MiB per inbound/outbound
envelope, and 256 cumulative recovery attempts per sub-batch. Share candidates
are restricted to authenticated configured operators, one batch identity, and
one point per sub-batch; planning prunes the pre-plan `N*BMax` bound to
`N*alpha`. Old v2 files without `limits` receive the defaults.

For deployment configs, use `--address-mode container`. The old `http_addr`
and `p2p_addr` fields remain backward-compatible defaults for local configs,
while the newer
`http_listen_addr`, `http_advertise_url`, `p2p_listen_addr`, and
`p2p_advertise_addr` fields separate local binding from dialable addresses.

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

## Container and Remote Evaluation

Build the sidecar image from the repository root:

```sh
docker build -f bloc-node/Dockerfile -t bloc-node:local .
```

Run a node inside a container with public config/CRS mounts plus only that
operator's secret file and `NODE_ID`:

```sh
docker run --rm \
  -e NODE_ID=0 \
  -v "$PWD/cluster.json:/config/cluster.json:ro" \
  -v "$PWD/cluster.crs:/config/cluster.crs:ro" \
  -v "$PWD/secrets/operator-0.json:/run/secrets/operator.json:ro" \
  bloc-node:local run --config /config/cluster.json --secrets /run/secrets/operator.json
```

The node exposes `/healthz`, `/metrics`, `/tx`, `/slot/prepare`,
`/slot/status`, `/start`, and `/result` on its configured HTTP listen address.

Use `eval-remote` when a sidecar cluster is already running. Docker Compose is
a useful local rehearsal for deployment mechanics, but the primary distributed
thesis evaluation target is one VM/EC2 instance per BLOC operator with a
separate controller running the evaluator.

Example against the local Docker Compose rehearsal:

```sh
go run ./cmd/bloc-node eval-remote \
  --config ../deploy/docker-compose/remote-eval.compose.json \
  --experiment-id compose-smoke \
  --batch-size 8 \
  --repetitions 1 \
  --out-dir results/distributed/compose-smoke
```

`eval-remote` does not spawn processes. It prepares slots, submits generated
signed Ethereum transactions, starts all sidecars, polls `/result`, verifies
cross-node consistency, and writes chart-compatible CSV/manifest outputs.

For VM/EC2-per-sidecar runs, generate a cluster config whose advertised HTTP
and libp2p addresses are reachable between the controller and operator hosts,
run one sidecar per machine, and point the remote-evaluator config at those
operator HTTP endpoints.

For mock-placeholder runs, start the sidecars with a mempool-backed provider and
run `eval-remote --tx-source mock-placeholder --mempool-url <mempool-il>`. In
that mode the evaluator does not submit `/tx` payloads; sidecars fetch encrypted
placeholder candidates from `mempool-il` and materialize the original target
transactions after threshold decryption.

## Local Campaign Runners

Protocol campaigns use the portable Bash entrypoints:

```sh
bash scripts/run-acs-safety-campaign.sh
bash scripts/run-merge-plan-campaign.sh --phase baseline --campaign-id <id>
```

Both support `--validate-only`; generated evidence remains under ignored
`results/` directories.
