# BLOC Evaluation Plan and Current Gaps

This document maps the research questions from `../papers/BLOC_Final.pdf` to
the current prototype and identifies what is still missing before the results
can support stronger deployment claims.

## RQ1: Slot-Level Timing

Current test path:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32,128 \
  --tx-size 256 \
  --bmax 128 \
  --tx-gas 21000 \
  --out-dir results/rq1-local
```

Metrics available now:

- total slot latency: `metrics.total_slot_ms`
- ACS/order latency: `metrics.acs_ms`
- deterministic merge and BTE planning latency: `metrics.plan_ms`
- share-generation latency: `metrics.share_generation_ms`
- post-commitment decrypt latency: `metrics.commit_to_plaintext_ms`
- success/consistency across nodes: `success`, `consistent`, `batch_id`
- agreed list/candidate count: `metrics.agreed_lists`,
  `metrics.agreed_ciphertexts`
- selected decrypted set size: `metrics.selected_ciphertexts`,
  `metrics.selected_gas`

Interpretation:

- The local prototype can answer whether the ACS+BTE pipeline completes within
  a 12s slot under local-process conditions.
- It cannot yet include real DVT threshold signing or Ethereum block
  publication latency.

Missing:

- multi-slot runs and p50/p95/p99 aggregation script;
- latency/loss emulator or AWS deployment harness;
- SSV/DVT signing integration.

## RQ2: Coordination and Cryptographic Overhead

Current test path:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32,128 \
  --tx-size 512 \
  --bmax 128 \
  --max-decrypted-gas 1344000 \
  --out-dir results/rq2-local

cd ../bte/btd-impl-main
go test ./be \
  -run '^$' \
  -bench '^BenchmarkHybridFullPath(8|32|128)$' \
  -benchtime=1x
```

Metrics available now:

- ACS and share message counts: `outbound_messages`, `inbound_messages`
- ACS and share byte counts: `outbound_bytes`, `inbound_bytes`
- ciphertext count and plaintext byte workload size
- skipped ciphertext count under blockspace caps:
  `metrics.skipped_ciphertexts`
- BTE standalone full-path benchmark timings

Missing:

- CPU time and peak RSS per operator;
- plaintext baseline submission path;
- separate timings for encryption, `MakeShare`, and `CombineShares` inside
  full node runs;
- threshold signing overhead.

## RQ3: Faults and Adversarial Behavior

Current test path:

```sh
# Censorship/omission style behavior.
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 16 \
  --tx-size 256 \
  --bmax 32 \
  --max-decrypted-gas 168000 \
  --fault 3:omit-proposal \
  --out-dir results/rq3-omit

# Decryption-share withholding.
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 16 \
  --tx-size 256 \
  --bmax 32 \
  --max-decrypted-gas 168000 \
  --fault 3:withhold-share \
  --out-dir results/rq3-withhold
```

Fault modes available now:

- `omit-proposal`: node proposes an empty encrypted batch.
- `withhold-share`: node participates in ACS but releases no BTE shares.
- `corrupt-share`: node sends malformed share encodings to peers.
- `--delay`: adds a fixed send delay for that node when running `bloc-node run`
  manually.

Correctness checks available now:

- all reporting nodes have the same `batch_id`;
- all reporting nodes have the same materialized transaction-set hashes;
- all reporting nodes have the same `plaintexts_hex`;
- runs fail cleanly on timeout if not enough shares are available.

Missing:

- targeted ciphertext censorship by hash;
- malformed ciphertext injection endpoint;
- public share-verifiability and attribution;
- explicit premature-disclosure assertions;
- Byzantine behavior that equivocates across peers.

## RQ4: Economic Cost

Current test path:

```sh
go run ./cmd/bloc-node eval-local \
  --nodes 4 \
  --batch-sizes 32 \
  --tx-size 256 \
  --tx-gas 21000 \
  --bmax 64 \
  --max-decrypted-gas 168000 \
  --out-dir results/rq4-blockspace
```

Current measurements:

- ciphertext byte size from `/tx` responses;
- network share/ACS bytes from evaluator outputs;
- local operator runtime from latency metrics.
- selected gas, selected ciphertexts, and skipped ciphertexts from evaluator
  outputs.

Prototype-level analysis possible now:

- compute encrypted payload size overhead versus raw transaction bytes;
- compute per-block ACS+BTE bandwidth per operator;
- sweep `--max-decrypted-gas` to estimate how decrypted transaction-set size
  changes latency, share traffic, and materialization time;
- estimate operator infrastructure cost from process runtime and bandwidth.

Missing:

- Ethereum placeholder transaction format;
- devnet gas estimator for placeholder calldata;
- deterministic replacement of placeholders with signed Ethereum transactions;
- historical base-fee / priority-fee trace ingestion;
- real proposer reward and operational-cost model.

## DVT/Ethereum Track

The current implementation is enough for prototype RQ1-RQ3 experiments, but not
for a full deployability claim. The DVT/Ethereum track must add:

- integration with an SSV/DVT proposer workflow or an equivalent threshold
  signing harness;
- Ethereum transaction parsing and syntactic validation;
- deterministic block materialization from committed placeholders;
- execution-client/devnet validation and gas measurement;
- DKG or documented key-management replacement for trusted-dealer configs.

## PBS Boundary

PBS/Bolt constraints, builder bids, builder proof verification, and
prefix-constraint-specific data types are intentionally out of scope for this
phase. The current artifact is a PBS-independent materialized transaction set
that can be consumed by a later block-building/signing boundary once that design
stabilizes.
