# PIC Evaluation Plan and Current Gaps

This document maps the research questions from `../papers/PIC_Final.pdf` to
the current prototype and identifies what is still missing before the results
can support stronger deployment claims.

## RQ1: Slot-Level Timing

Current test path:

```sh
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32,128 \
  --tx-size 256 \
  --bmax 128 \
  --out-dir results/rq1-local
```

Metrics available now:

- total slot latency: `metrics.total_slot_ms`
- ACS/order latency: `metrics.acs_ms`
- plan latency: `metrics.plan_ms`
- share-generation latency: `metrics.share_generation_ms`
- post-commitment decrypt latency: `metrics.commit_to_plaintext_ms`
- success/consistency across nodes: `success`, `consistent`, `batch_id`

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
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local \
  --nodes 4 \
  --batch-sizes 8,32,128 \
  --tx-size 512 \
  --bmax 128 \
  --out-dir results/rq2-local

cd ../bte/btd-impl-main
GOCACHE=/private/tmp/bte-go-cache go test ./be \
  -run '^$' \
  -bench '^BenchmarkHybridFullPath(8|32|128)$' \
  -benchtime=1x
```

Metrics available now:

- ACS and share message counts: `outbound_messages`, `inbound_messages`
- ACS and share byte counts: `outbound_bytes`, `inbound_bytes`
- ciphertext count and plaintext byte workload size
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
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local \
  --nodes 4 \
  --batch-sizes 16 \
  --tx-size 256 \
  --bmax 32 \
  --fault 3:omit-proposal \
  --out-dir results/rq3-omit

# Decryption-share withholding.
GOCACHE=/private/tmp/pic-node-go-cache go run ./cmd/pic-node eval-local \
  --nodes 4 \
  --batch-sizes 16 \
  --tx-size 256 \
  --bmax 32 \
  --fault 3:withhold-share \
  --out-dir results/rq3-withhold
```

Fault modes available now:

- `omit-proposal`: node proposes an empty encrypted batch.
- `withhold-share`: node participates in ACS but releases no BTE shares.
- `corrupt-share`: node sends malformed share encodings to peers.
- `--delay`: adds a fixed send delay for that node when running `pic-node run`
  manually.

Correctness checks available now:

- all reporting nodes have the same `batch_id`;
- all reporting nodes have the same `plaintexts_hex`;
- runs fail cleanly on timeout if not enough shares are available.

Missing:

- targeted ciphertext censorship by hash;
- malformed ciphertext injection endpoint;
- public share-verifiability and attribution;
- explicit premature-disclosure assertions;
- Byzantine behavior that equivocates across peers.

## RQ4: Economic Cost

Current measurements:

- ciphertext byte size from `/tx` responses;
- network share/ACS bytes from evaluator outputs;
- local operator runtime from latency metrics.

Prototype-level analysis possible now:

- compute encrypted payload size overhead versus raw transaction bytes;
- compute per-block ACS+BTE bandwidth per operator;
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
