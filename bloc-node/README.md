# BLOC Node Prototype

This module wires the local HoneyBadger ACS implementation to the BEAT-MEV
batched threshold encryption code so separate operator processes can agree on
encrypted inclusion lists and decrypt a deterministic transaction set only after
agreement.

It follows the prototype sequence from `../papers/BLOC_Final.pdf`:

1. Clients submit raw transaction bytes plus plaintext scheduling metadata to an
   operator HTTP endpoint. The local evaluator generates signed Ethereum
   transaction bytes for this payload.
2. The operator encrypts the bytes as a BTE ciphertext placeholder for the
   configured cluster, slot, and index.
3. Each operator proposes one slot-scoped `InclusionList` to ACS.
4. After ACS outputs the common subset, every operator deterministically merges
   the agreed lists, applies the configured decrypted blockspace cap, and builds
   the same BTE `BatchPlan`.
5. Operators gossip threshold decryption shares for the selected set.
6. Once each sub-batch has `t` shares, every operator combines shares and emits
   the same plaintext transaction set.

This is a deployability harness, not a production DVT client. It uses a
trusted-dealer config generator, syntactically valid signed Ethereum
transactions in the evaluator, and a pluggable node-to-node transport. The
default transport is the original local TCP mode; `--network libp2p` enables a
static-peer libp2p Gossipsub backend.

## Quick Local Run

From this directory:

```sh
go run ./cmd/bloc-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 16 \
  --max-decrypted-gas 63000 \
  --network tcp \
  --base-consensus-port 19300 \
  --base-http-port 18300 \
  --out bloc-cluster.local.json
```

Start four operators in separate terminals:

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

Trigger the slot:

```sh
curl -s -X POST http://127.0.0.1:18300/start
curl -s -X POST http://127.0.0.1:18301/start
curl -s -X POST http://127.0.0.1:18302/start
curl -s -X POST http://127.0.0.1:18303/start
```

Read results:

```sh
curl -s http://127.0.0.1:18300/result
cat results/manual/node-0.json
```

HoneyBadger ACS guarantees a common subset of at least `N - f` proposals, so in
a four-node run the agreed set may contain three of the four submitted operator
lists. All correct operators should report the same `batch_id`, merged-set hash,
selected gas, `ethereum_tx_hashes`, and `plaintexts_hex` ordering.

## Config Notes

`gen-config` writes:

- operator TCP and HTTP addresses;
- the BTE public key;
- one trusted-dealer secret share per operator;
- a shared `crs_seed_hex`, so each process derives the same BEAT-MEV PRF/CRS;
- `blockspace.max_decrypted_gas`, where `0` preserves uncapped behavior;
- `blockspace.max_decrypted_txs`, where `0` means `bmax`;
- `blockspace.default_tx_gas`, used for raw submissions without metadata;
- `provider.mode`, currently `direct` or `mempool-http`.
- `network.mode`, currently `tcp` or `libp2p`;
- libp2p multiaddrs and operator peer IDs for static-peer P2P runs.

In libp2p mode, node-to-node ACS and BTE share messages are serialized with the
generated Go bindings from `proto/bloc/v1/messages.proto` and
`internal/pb/blocv1/messages.pb.go`. Regenerate them after schema edits with:

```sh
protoc --go_out=. --go_opt=module=bloc-node proto/bloc/v1/messages.proto
```

The TCP transport remains a compatibility path that uses Go `gob`.

The current implementation intentionally has no PBS/builder interaction and no
prefix-constraint data model. It materializes a PBS-independent decrypted
transaction set.

The CRS seed is prototype plumbing. A hardened version should replace this with
a stable public CRS artifact and DKG-generated key shares.

## Local Evaluation Harness

Run a reproducible local experiment without manually opening four terminals:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32 \
  --tx-size 256 \
  --bmax 64 \
  --max-decrypted-gas 252000 \
  --tx-gas 21000 \
  --network tcp \
  --base-port 24000 \
  --out-dir results/local
```

The evaluator submits deterministic EIP-1559 transactions signed by local test
keys. These transactions are syntactically valid and recoverable with
go-ethereum, but the harness does not broadcast them or require funded accounts.
`--tx-size` is interpreted as a minimum encoded transaction size; deterministic
calldata padding is added when necessary.

For the professor-facing MVP flow, run:

```sh
./scripts/demo-local.sh
```

See `MVP_DEMO.md` for scenario descriptions and expected output.

The harness writes:

- `summary.json`: full per-run and per-node results.
- `summary.csv`: compact rows for plotting latency and bandwidth.
- one run directory per configuration, including generated config, node logs,
  and per-node result JSON.

Blockspace sweep example:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 32 \
  --tx-size 256 \
  --bmax 64 \
  --tx-gas 21000 \
  --max-decrypted-gas 168000 \
  --out-dir results/blockspace-8tx
```

Static-peer libp2p smoke run:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8 \
  --tx-size 256 \
  --bmax 16 \
  --network libp2p \
  --base-port 26000 \
  --out-dir results/libp2p-local
```

Useful fault-injection examples:

```sh
# One node proposes an empty batch but still participates otherwise.
go run ./cmd/bloc-node eval-local --fault 3:omit-proposal

# One node withholds BTE decryption shares.
go run ./cmd/bloc-node eval-local --fault 3:withhold-share
```
