# BLOC MVP Demo

This demo shows four local BLOC operator processes agreeing on encrypted
inclusion-list proposals, deterministically selecting a decrypted transaction
set, exchanging BTE decryption shares, and materializing the same signed
Ethereum transactions.

## Prerequisites

- Go toolchain compatible with `bloc-node/go.mod`.
- Local ports available. The script defaults to ports starting at `30000`.
- No Ethereum execution client is required.

## One-Command Demo

From `bloc-node/`:

```sh
./scripts/demo-local.sh
```

Artifacts are written to:

```text
results/mvp-demo/<timestamp>/
```

The script also writes a Markdown summary:

```text
results/mvp-demo/<timestamp>/DEMO_REPORT.md
```

To choose a different base port or skip libp2p:

```sh
BLOC_DEMO_BASE_PORT=35000 ./scripts/demo-local.sh
BLOC_SKIP_LIBP2P=1 ./scripts/demo-local.sh
```

## Scenarios

The script runs:

- `normal-tcp`: 4 nodes, signed Ethereum tx payloads, TCP transport.
- `blockspace-cap`: 4 nodes, `--max-decrypted-txs 4`, proves only the capped
  set is decrypted.
- `withhold-share`: one node withholds BTE shares; the remaining threshold
  should still complete.
- `libp2p-smoke`: optional static-peer libp2p run using protobuf BLOC messages.

Each scenario prints a compact summary:

```text
success=true consistent=true
agreed_lists=...
selected_txs=...
selected_gas=...
ethereum_tx_hashes=...
total_slot_ms=...
```

## Inspecting Results

Each scenario directory contains:

- `summary.json`: full evaluator output.
- `summary.csv`: compact metrics rows.
- `cluster.json`: generated local config.
- `node-*.log`: per-node process logs.
- `node-*-result.json`: materialized transaction-set artifacts.

The top-level `DEMO_REPORT.md` aggregates all scenario summaries into one
table and records the current MVP scope and exclusions.

Important fields:

- `batch_id`: BTE batch identity.
- `merged_set_hash`: deterministic encrypted-set identity.
- `ethereum_tx_hashes`: parsed hashes of decrypted signed Ethereum txs.
- `metrics.total_slot_ms`: end-to-end slot latency.
- `metrics.outbound_bytes`: ACS/share bandwidth by message kind.

To regenerate a report from an existing run:

```sh
go run ./cmd/bloc-node report \
  --dir results/mvp-demo/<timestamp> \
  --out results/mvp-demo/<timestamp>/DEMO_REPORT.md
```

## What This Proves

- Correct nodes agree on the same ACS output.
- Correct nodes deterministically merge inclusion lists.
- Decryption only happens after the agreed encrypted set is fixed.
- Decrypted payloads are syntactically valid signed Ethereum transactions.
- Configured blockspace caps affect the decrypted set deterministically.

## What This Does Not Prove

- No transaction is broadcast or executed on an Ethereum devnet.
- No PBS, builder, or DVT/SSV signing integration is included.
- Key shares are trusted-dealer demo material, not DKG output.
- libp2p uses static peers and is not production-hardened.
