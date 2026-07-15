# Decisions

Use this file for major architecture, protocol, and workflow decisions.

## Entry Format

```md
## 000X. Decision title

- Date: YYYY-MM-DD
- Status: Proposed | Accepted | Superseded
- Context:
- Options considered:
- Decision:
- Rationale:
- Consequences:
- Related files:
```

## 0001. Use a slot-scoped ACS adapter instead of the old HoneyBadger driver

- Date: 2026-06-21
- Status: Accepted
- Context: The original `hbbft` top-level driver owns a local transaction buffer and recursively advances epochs, which does not match the BLOC need for one slot, one candidate batch per participant, and one common-subset result.
- Options considered: Reuse the old HoneyBadger driver directly; patch HoneyBadger in place; add a separate slot-scoped adapter over ACS.
- Decision: Add a dedicated slot-scoped adapter that wraps one ACS instance per slot and leaves post-agreement processing outside the ACS core.
- Rationale: This preserves the consensus core while giving `bloc-node` a clean boundary for externally supplied candidate batches and deterministic post-agreement processing.
- Consequences: The repo now has two conceptual top-level paths in `hbbft`: the original driver and the BLOC slot path. Documentation must make that distinction explicit.
- Related files: `sbc/hbbft/bloc_slot.go`, `docs/modules/hbbft.md`

## 0002. Use deterministic batch planning and proposer ordering after consensus

- Date: 2026-06-21
- Status: Accepted
- Context: ACS outputs accepted proposer payloads, but block materialization and decryption require every honest operator to compute the same ordered encrypted set and the same sub-batch layout.
- Options considered: Preserve raw map iteration; rely on input order from runtime behavior; sort deterministically and compute a shared `BatchPlan`.
- Decision: Deterministically order accepted proposer batches and compute a deterministic `BatchPlan` from the consensus-fixed ciphertext list.
- Rationale: Ordering and sub-batching must not depend on runtime map iteration or transport timing.
- Consequences: Any change to ordering or planning rules must be treated as protocol-significant and validated for reproducibility.
- Related files: `bloc-node/internal/app/inclusion/merge.go`, `bte/btd-impl-main/be/cluster.go`

## 0003. Use hybrid BTE payload encryption for raw transaction bytes

- Date: 2026-06-21
- Status: Accepted
- Context: The BEAT-MEV primitive encrypts messages in `GT`, while the BLOC prototype needs to carry raw Ethereum transaction bytes.
- Options considered: Restrict payloads to group-encoded data; design a separate placeholder-only abstraction; use hybrid encryption with a BTE-encrypted capsule secret and AEAD payload.
- Decision: Use a hybrid design where BTE encrypts a capsule secret and AES-GCM protects the raw transaction bytes.
- Rationale: This keeps timing-of-release in the threshold cryptography while allowing realistic byte payloads for the prototype.
- Consequences: Payload integrity depends on both successful threshold reconstruction and the committed plaintext hash check.
- Related files: `bte/btd-impl-main/be/cluster.go`, `docs/modules/bte.md`

## 0004. Combine any valid threshold share subset instead of depending on a fixed share order

- Date: 2026-06-21
- Status: Accepted
- Context: In practical separate-process runs, nodes can collect more than `t` candidate shares and some subsets may be invalid or target the wrong batch.
- Options considered: Always use the first `t` shares; sort into one fixed subset; filter by batch and try valid threshold subsets until reconstruction succeeds.
- Decision: Filter by the local agreed batch and sub-batch, then combine any valid threshold subset rather than targeting a fixed operator set.
- Rationale: Threshold correctness should not depend on one hard-coded operator ordering, and wrong-batch shares must never count toward reconstruction.
- Consequences: Share combination logic is more defensive, and public share-verifiability remains a future hardening step.
- Related files: `bloc-node/internal/app/node.go`, `bte/btd-impl-main/be/cluster.go`, `docs/modules/bloc-node.md`, `docs/modules/bte.md`

## 0005. Standardize operator messaging on libp2p streams

- Date: 2026-06-24
- Status: Accepted
- Context: The legacy TCP backend opened one operating-system connection per envelope and produced socket exhaustion during the repeated M1 campaign. Maintaining raw TCP and libp2p also doubled the transport matrix without representing two target deployment architectures.
- Options considered: Add persistent framing to the raw TCP backend; retain TCP only for compatibility; remove raw TCP and rely on direct libp2p streams.
- Decision: Remove the raw TCP/gob transport and carry all addressed ACS and decryption-share messages as protobuf envelopes over libp2p streams.
- Rationale: libp2p already provides persistent authenticated peer connections and multiplexed streams over the configured TCP multiaddresses, avoiding custom connection management while preserving explicit message accounting.
- Consequences: `tcp` configurations and transport-selection CLI flags are intentionally unsupported; M1 contains nine libp2p scenarios and 315 runs; historical TCP result files remain chart-readable.
- Related files: `bloc-node/internal/app/transport_libp2p.go`, `bloc-node/internal/app/eval_suite.go`, `docs/VALIDATION.md`

## 0006. Measure repeated slots on persistent local clusters

- Date: 2026-06-24
- Status: Accepted
- Context: Per-run process isolation spent roughly 12–15 seconds rebuilding configuration, BTE state, processes, and the libp2p mesh around a protocol path normally completing in under three seconds.
- Options considered: Keep every sample process-isolated; keep all operator-count clusters concurrently; run one persistent cluster at a time and replace only slot-scoped state.
- Decision: M1 runs one persistent cluster per operator count, executes one active slot at a time, and creates and closes a fresh ACS state machine for every sample. Isolated execution remains available explicitly.
- Rationale: This removes unrelated setup from campaign wall time without mixing consensus, share, transaction, result, or metric state between observations, and avoids contention from 21 simultaneously active node processes.
- Consequences: M1 ordering is seeded within each operator-count group; cluster startup, preparation, and submission are reported separately; a failed slot is retained and its cluster is restarted before further measurements.
- Related files: `bloc-node/internal/app/node.go`, `bloc-node/internal/app/eval_persistent.go`, `sbc/hbbft/bloc_slot.go`

## 0007. Prioritize distributed sidecar deployment before Builder API compatibility

- Date: 2026-06-28
- Status: Accepted
- Context: The next thesis milestone needs cloud/distributed latency and performance evidence for the BLOC sidecar. The proposed SSV-BLOC architecture eventually reaches a Builder API boundary, but production block-building would require execution-payload construction, beacon-client compatibility, and signing-boundary work that should not be implied by deployment evidence.
- Options considered: implement Builder API compatibility first; implement a Builder-shaped mock before deployment; defer Builder API work and first make the sidecar containerized, observable, and remotely evaluable.
- Decision: Defer Builder API compatibility and focus the active milestone on a containerized `bloc-node` sidecar, listen-vs-advertise config, Prometheus/Grafana visibility, Docker Compose/EC2 deployment artifacts, and a remote evaluator for already-running clusters.
- Rationale: Distributed deployment and observability are prerequisites for thesis-grade sidecar latency/performance evidence, while Builder API compatibility is a separate integration boundary that can be added once the runtime is measurable.
- Consequences: `bloc-node` now exposes deployment-oriented config fields and `/metrics`; deployment artifacts live under `deploy/`; `eval-remote` is the canonical path for measuring non-local sidecar clusters; Builder/PBS/SSV signing claims remain deferred.
- Update: Decision 0010 refines the main evaluation substrate: VM/EC2-per-sidecar is the primary distributed metric-gathering path; earlier orchestrated-container artifacts are retained only as out-of-scope historical deployment material.
- Related files: `bloc-node/internal/app/commands.go`, `bloc-node/internal/app/metrics.go`, `bloc-node/internal/app/eval_remote.go`, `deploy/`, `docs/STATUS.md`, `docs/VALIDATION.md`, `docs/ROADMAP.md`

## 0008. Use Prometheus-native metrics for live sidecar visibility

- Date: 2026-06-28
- Status: Accepted
- Context: The deployment milestone needs Grafana/Prometheus visibility that behaves like operational monitoring, not just local evaluator fields rendered as scrape text.
- Options considered: keep hand-rendered latest-slot gauges; mirror every evaluator CSV field as a Prometheus metric; use official Prometheus collectors with counters, gauges, histograms, base units, and bounded labels.
- Decision: Use the official Go Prometheus client for `/metrics`. Slot and HTTP latencies are seconds-based histograms; events and byte/message totals are counters; current state is exposed as gauges. Evaluator CSV/JSON remains the offline artifact format.
- Rationale: Prometheus/Grafana dashboards need stable metric names, low-cardinality labels, and histogram-safe p50/p95 queries, while thesis charts still benefit from existing per-run CSV outputs.
- Consequences: Grafana panels must use `histogram_quantile()` over `_bucket` series for live p50/p95 latency. Prometheus labels must not include slot IDs, batch IDs, transaction hashes, URLs, peer IDs, or free-form errors.
- Related files: `bloc-node/internal/app/metrics.go`, `deploy/docker-compose/grafana/dashboards/bloc-sidecar.json`, `docs/VALIDATION.md`

## 0009. Use mock placeholders for realistic transaction-source tests

- Date: 2026-06-28
- Status: Accepted
- Context: Real public mempool transactions are ordinary Ethereum transactions, not BLOC placeholder transactions whose calldata already carries encrypted target payloads. Treating fetched public transactions as placeholders would misrepresent the architecture.
- Options considered: submit public transactions directly to sidecars; have every sidecar independently encrypt the same fetched transaction; introduce a mock external placeholder producer.
- Decision: Use `mempool-il` replay-placeholder mode as a mock external submitter/searcher. It validates real signed target transactions from a corpus, encrypts each target once with BLOC public cluster material, signs mock placeholder transactions, parses their calldata, and exposes normalized candidates whose `encrypted_payload_hex` is derived from that calldata.
- Rationale: The model preserves the separation between target transaction, placeholder transaction, and BTE encrypted payload, while keeping thesis runs deterministic.
- Consequences: Sidecars consume encrypted payloads parsed from placeholder transactions and do not independently re-encrypt the same public transaction. Live public mempool ingestion and Builder/execution validation remain later work.
- Related files: `mempool-il/internal/mempool/replay_placeholder.go`, `bloc-node/internal/app/provider.go`, `deploy/docker-compose/compose.mock-placeholder.yaml`, `docs/ARCHITECTURE.md`, `docs/VALIDATION.md`

## 0010. Use VM-per-operator deployment for primary distributed evidence

- Date: 2026-07-04
- Status: Accepted
- Context: The thesis needs distributed ACS/BTE latency and overhead evidence that maps cleanly to DVT-style operator independence. A single orchestrated container cluster introduces one administrative/control-plane/failure domain and measurement confounders such as scheduling, service routing, probes, restarts, throttling, and cluster control-plane effects.
- Options considered: use a managed container cluster as the main distributed evaluation substrate; use Docker Compose only; run one BLOC sidecar on each independent VM/EC2 instance and drive the cluster from a separate controller.
- Decision: Keep local `eval-local`/`eval-suite` runs as the clean protocol baseline, keep Docker Compose as a local deployment-mechanics rehearsal, and use one VM/EC2 instance per BLOC operator as the primary distributed thesis evaluation environment. A separate controller machine runs `eval-remote`, artifact collection, and optional Prometheus/Grafana or OpenTelemetry collection. Earlier orchestrated-container manifests remain in the repo only as out-of-scope historical deployment artifacts.
- Rationale: VM-per-operator deployment maps directly to the protocol model: one operator, one machine, one network identity. It reduces orchestration-specific confounders and is easier to explain when reporting ACS/BTE latency under distributed network conditions.
- Consequences: M3 distributed campaigns should target VM/EC2-per-sidecar clusters and clearly separate those results from local M1 baselines and local deployment rehearsals. Orchestrated-container deployment is out of scope for the current roadmap and should not appear in metric collection plans unless explicitly revived later.
- Related files: `docs/STATUS.md`, `docs/VALIDATION.md`, `docs/ROADMAP.md`, `docs/WORKFLOWS.md`, `docs/ARCHITECTURE.md`

## 0011. Complete ACS only from validated BBA decisions

- Date: 2026-07-15
- Status: Accepted
- Context: An optimized cross-AZ n4 campaign produced three-list and four-list ACS outputs at different correct operators. A project-added shortcut completed ACS from all RBC outputs before every BBA result existed, while the imported BBA implementation counted AUX messages whose values had not entered the local BV-broadcast set. Reordered delivery could therefore produce different apparent singleton decisions.
- Options considered: retain the all-RBC shortcut for liveness; repair only ACS completion; restore ACS completion and also enforce BBA AUX validity.
- Decision: RBC output alone never selects common-subset membership. ACS waits for all BBA results and every true result's RBC payload, and returns exactly the true proposers. BBA counts only AUX messages carrying values already admitted to `binValues` and re-evaluates pending AUX messages when a new value is admitted.
- Rationale: RBC proves proposal availability and consistency, while BBA decides inclusion. Mixing those responsibilities weakens common-subset agreement under reordered asynchronous delivery.
- Consequences: Safety is tested over 1,000 fixed delivery schedules plus a 100-slot batch-128 gate and a complete n4/n7 matrix. Any future liveness repair must preserve these completion and validity rules; the all-RBC shortcut cannot return as a workaround.
- Related files: `sbc/hbbft/acs.go`, `sbc/hbbft/bba.go`, `bloc-node/scripts/run-acs-safety-campaign.ps1`, `docs/VALIDATION.md`

## 0012. Bind post-ACS BTE decoding to slot context and repair collision planning

- Date: 2026-07-15
- Status: Accepted
- Context: Post-ACS processing accepted internally consistent ciphertexts without requiring the active cluster and slot, retained mutable canonical-byte aliases until planning, and could reject a valid repeated-index batch when frequency ties made the default round-robin layout collide.
- Options considered: validate only after decoding in `bloc-node`; break every generic BTE API by requiring runtime context; add scope-bound APIs while preserving generic callers, freeze batch identity during decode, and use a fallback only when the existing layout collides.
- Decision: Production `bloc-node` uses additive scope-bound BTE APIs and fails the slot on any selected metadata or structural mismatch. `DecodedBatch` owns its `BatchID` and returns independently owned ciphertext copies. Planning preserves every existing collision-free layout and otherwise assigns each item to the least-loaded eligible sub-batch, breaking ties by sub-batch ID.
- Rationale: The application must prevent cross-slot or cross-cluster replay without removing useful generic library operations. Preserving the current fast path keeps existing protocol identities stable, while a deterministic fallback accepts layouts that satisfy the BTE distinct-index invariant but were previously rejected accidentally.
- Consequences: No wire, hash, `BatchID`, or `alpha` definition changes. Formerly successful plans remain identical; formerly rejected collision layouts now require all operators to run the corrected implementation. Operators must be upgraded as one image before processing new slots.
- Related files: `bte/btd-impl-main/be/cluster.go`, `bloc-node/internal/app/node.go`, `docs/modules/bte.md`, `docs/modules/bloc-node.md`

## 0013. Separate the system architecture from canonical module deep dives

- Date: 2026-07-15
- Status: Accepted
- Context: `docs/ARCHITECTURE.md` mixed system boundaries with detailed merge/planner internals, deployment topology, evaluator metrics, and module-specific limitations. `hbbft` and `bloc-node` had no canonical implementation deep dives, while the BTE note duplicated and eventually contradicted the root document.
- Options considered: continue expanding the root architecture; put internals in module READMEs; keep one top-down root architecture and centralized canonical module documents under `docs/modules/`.
- Decision: `docs/ARCHITECTURE.md` owns the trust model, module boundaries, end-to-end handoffs, identities, and cross-module invariants. `docs/modules/{bloc-node,mempool-il,hbbft,bte}.md` own stage algorithms, state, wire formats, concurrency, failure semantics, paper mapping, tests, and limitations. Module READMEs remain operational entry points.
- Rationale: A layered structure lets thesis reviewers understand the complete protocol before drilling into source-backed implementation detail, while giving future changes one unambiguous documentation owner.
- Consequences: Architecture work must update the affected module deep dive as well as the root document when a cross-module boundary changes. `CLUSTER_BTE.md` remains only as a compatibility pointer. Historical review findings live under `docs/archive/` and are not competing current architecture.
- Related files: `docs/ARCHITECTURE.md`, `docs/modules/`, `docs/archive/PROTOCOL_IMPLEMENTATION_REVIEW_2026-07.md`, `AGENTS.md`, `docs/CODEX_GUIDE.md`
