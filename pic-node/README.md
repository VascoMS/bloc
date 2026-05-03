# PIC Node Prototype

This module wires the local HoneyBadger ACS implementation to the BEAT-MEV
batched threshold encryption code so separate operator processes can agree on an
encrypted batch and decrypt it only after agreement.

It follows the prototype sequence from `../papers/PIC_Final.pdf`:

1. Clients submit raw transaction bytes to an operator HTTP endpoint.
2. The operator encrypts the bytes as a BTE ciphertext placeholder for the
   configured cluster, slot, and index.
3. Operators run slot-scoped ACS over encrypted placeholders only.
4. After ACS outputs the common subset, every operator derives the same ordered
   ciphertext batch and deterministic BTE `BatchPlan`.
5. Operators gossip threshold decryption shares for the committed batch ID.
6. Once each sub-batch has `t` shares, every operator combines shares and emits
   the same plaintext transaction set.

This is a deployability harness, not a production DVT client. It uses a
trusted-dealer config generator, local TCP/gob messages, and raw byte payloads
instead of signed Ethereum transactions.

## Quick Local Run

From this directory:

```sh
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node gen-config \
  --nodes 4 \
  --threshold 3 \
  --bmax 16 \
  --base-consensus-port 19300 \
  --base-http-port 18300 \
  --out pic-cluster.local.json
```

Start four operators in separate terminals:

```sh
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node run --config pic-cluster.local.json --id 0 --out /private/tmp/pic-node0.json
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node run --config pic-cluster.local.json --id 1 --out /private/tmp/pic-node1.json
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node run --config pic-cluster.local.json --id 2 --out /private/tmp/pic-node2.json
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node run --config pic-cluster.local.json --id 3 --out /private/tmp/pic-node3.json
```

Submit one transaction to each operator:

```sh
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node submit --url http://127.0.0.1:18300 --tx 0x010203
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node submit --url http://127.0.0.1:18301 --tx 0x040506
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node submit --url http://127.0.0.1:18302 --tx 0x070809
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node submit --url http://127.0.0.1:18303 --tx 0x0a0b0c
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
cat /private/tmp/pic-node0.json
```

HoneyBadger ACS guarantees a common subset of at least `N - f` proposals, so in
a four-node run the agreed batch may contain three of the four submitted
operator batches. All correct operators should report the same `batch_id` and
the same `plaintexts_hex` ordering.

## Config Notes

`gen-config` writes:

- operator TCP and HTTP addresses;
- the BTE public key;
- one trusted-dealer secret share per operator;
- a shared `crs_seed_hex`, so each process derives the same BEAT-MEV PRF/CRS.

The CRS seed is prototype plumbing. A hardened version should replace this with
a stable public CRS artifact and DKG-generated key shares.

## Local Evaluation Harness

Run a reproducible local experiment without manually opening four terminals:

```sh
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32 \
  --tx-size 256 \
  --bmax 64 \
  --base-port 24000 \
  --out-dir results/local
```

The harness writes:

- `summary.json`: full per-run and per-node results.
- `summary.csv`: compact rows for plotting latency and bandwidth.
- one run directory per configuration, including generated config, node logs,
  and per-node result JSON.

Useful fault-injection examples:

```sh
# One node proposes an empty batch but still participates otherwise.
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local --fault 3:omit-proposal

# One node withholds BTE decryption shares.
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local --fault 3:withhold-share
```
