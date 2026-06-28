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
- Related files: `sbc/hbbft/bloc_slot.go`, `docs/archive/BLOC_HoneyBadger_Implementation_Note.md`

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
- Related files: `bte/btd-impl-main/be/cluster.go`, `bte/btd-impl-main/CLUSTER_BTE.md`

## 0004. Combine any valid threshold share subset instead of depending on a fixed share order

- Date: 2026-06-21
- Status: Accepted
- Context: In practical separate-process runs, nodes can collect more than `t` candidate shares and some subsets may be invalid or target the wrong batch.
- Options considered: Always use the first `t` shares; sort into one fixed subset; filter by batch and try valid threshold subsets until reconstruction succeeds.
- Decision: Filter by the local agreed batch and sub-batch, then combine any valid threshold subset rather than targeting a fixed operator set.
- Rationale: Threshold correctness should not depend on one hard-coded operator ordering, and wrong-batch shares must never count toward reconstruction.
- Consequences: Share combination logic is more defensive, and public share-verifiability remains a future hardening step.
- Related files: `bloc-node/internal/app/main_test.go`, `docs/archive/THRESHOLD_SHARE_ISSUE.md`

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
