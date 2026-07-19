# Protocol Implementation Review — July 2026

## Purpose And Baseline

This historical record captures the source-led review used to rewrite the BLOC
architecture documentation. Canonical current behavior belongs in
[ARCHITECTURE.md](../../docs/ARCHITECTURE.md) and
[`docs/modules/`](../../docs/modules/); this file retains review coverage,
confirmed findings, paper deviations, and follow-up questions.

- Review date: 2026-07-15
- Branch: `codex/ciphertext-decode-optimization`
- Base commit: `d27db3fc Fix ACS common-subset agreement safety`
- Source of truth: current working tree, including the uncommitted Merge + Plan
  correctness patch
- Review scope: `bloc-node`, `mempool-il`, `sbc/hbbft`, and
  `bte/btd-impl-main`
- Out of scope: code fixes, evaluator redesign, deployment changes, charts,
  EC2/cross-AZ execution, performance optimization, and cryptographic audit

Dirty protocol files present at the baseline were:

- `bloc-node/internal/app/node.go`
- `bloc-node/internal/app/main_test.go`
- `bloc-node/internal/app/merge_plan_benchmark_test.go`
- `bte/btd-impl-main/be/cluster.go`
- `bte/btd-impl-main/be/cluster_test.go`
- `bte/btd-impl-main/TESTING.md`

Existing dirty documentation plus unrelated Kubernetes manifests,
`docs/WORKFLOWS.md`, and `bloc-node/remote-eval.k8s-local.json` were preserved.
No production source change is part of this review.

## Method

For each stage, the review traced the production entry point through state
mutation, serialization, identity derivation, validation, failure handling,
network handoff, and validating tests. Existing documentation and the following
papers were treated as claims to compare with code rather than implementation
truth:

- [BLOC Final](../../papers/BLOC_Final.pdf)
- [BEAT-MEV](../../papers/BEAT-MEV.pdf)
- [The Honey Badger of BFT Protocols](../../papers/honeybadger.pdf)
- [ACS Improvement](../../papers/ACS_Improvement.pdf)

The terms `implemented`, `adapted`, `deferred`, and `contradicted` below mean:

- **Implemented:** the code follows the reviewed paper operation at the
  prototype abstraction level.
- **Adapted:** the paper operation exists with an explicit repository-specific
  integration or encoding.
- **Deferred:** the paper/system assumption or surrounding component is not
  implemented.
- **Contradicted:** current code does not satisfy the stated algorithm or
  security assumption.

## Coverage Matrix

### `mempool-il`

| Protocol stage | Production files | Evidence |
| --- | --- | --- |
| Process/source wiring | `cmd/service/main.go`, `internal/mempool/reader.go` | reader/source contracts and module tests |
| `txpool` normalization | `internal/mempool/rpc.go` | RPC helpers and classifier/store tests |
| Public pending source | `internal/mempool/public_rpc.go` | `public_rpc_test.go` |
| Alchemy cache/replacement | `internal/mempool/alchemy_pending.go` | `alchemy_pending_test.go` |
| Placeholder classification | `internal/mempool/classifier.go`, `types.go` | `classifier_test.go` |
| Replay target encryption | `internal/mempool/replay_placeholder.go` | `replay_placeholder_test.go` |
| Snapshot ownership/identity | `internal/mempool/store.go` | `store_test.go` |
| Bounded deterministic list | `internal/inclusion/builder.go` | `builder_test.go`, `mock_placeholder_test.go` |
| HTTP boundary | `internal/api/server.go` | source/list tests; no dedicated method/auth tests |

### `sbc/hbbft`

| Protocol stage | Production files | Evidence |
| --- | --- | --- |
| RBC erasure/Merkle broadcast | `rbc.go` | `rbc_test.go`, HoneyBadger paper comparison |
| BBA BV/AUX/epoch state | `bba.go` | `bba_test.go`, HoneyBadger paper comparison |
| ACS orchestration/completion | `acs.go` | `acs_test.go`, reordered-delivery campaign |
| Message queue ownership | `message_que.go` | `message_que_test.go` |
| Slot adaptation/output order | `bloc_slot.go` | `bloc_slot_test.go` |
| In-memory transport contract | `transport.go`, `local_transport.go` | `transport_test.go`; not active network transport |
| Inherited recurring driver | `honey_badger.go`, `buffer.go`, `transaction.go` | explicitly inactive for BLOC; inherited tests only |

### BTE

| Protocol stage | Production files | Evidence |
| --- | --- | --- |
| Pairing-suite adaptation | `curves/curves.go` | construction use and BEAT-MEV comparison |
| PRF setup/evaluation | `prf/prf.go` | BTD round trips and source inspection |
| Threshold ElGamal | `elgamal/elgamal.go` | `elgamal_test.go` |
| BTD encrypt/prove/share/combine | `be/btd.go` | cluster tests and inherited benchmarks |
| Hybrid envelope and context | `be/cluster.go` | round-trip, mutation, and scope tests |
| Canonical decoding/ownership | `be/cluster.go` | decoder, fuzz-seed, and deep-copy tests |
| Deterministic planning | `be/cluster.go` | collision fallback, golden membership, property tests |
| Candidate-share combination | `be/cluster.go` | threshold subset and invalid-extra tests |

### `bloc-node`

| Protocol stage | Production files | Evidence |
| --- | --- | --- |
| CLI/config/trusted setup | `app.go`, `commands.go`, `config.go`, `crypto.go`, `types.go` | configuration/deployment tests |
| Direct and mempool proposals | `node.go`, `provider.go`, `ethdemo/tx.go` | provider and transaction tests |
| Inclusion-list encoding | `inclusion/types.go`, `inclusion/proto.go`, protobuf schema/bindings | protobuf round-trip tests |
| Agreed/merged identities | `inclusion/merge.go` | deterministic merge and bound tests |
| Operator wire conversion | `codec.go`, `wire.go`, `proto/bloc/v1/messages.proto` | complete payload round trips |
| libp2p delivery | `transport.go`, `transport_libp2p.go` | write-completion test and evaluator evidence |
| Slot/ACS lifecycle | `node.go` | slot, stale-message, and ACS serialization tests |
| Decode/plan/share/combine | `node.go` plus BTE boundary | correctness patch and combine tests |
| Result and observability | `node.go`, `http.go`, `metrics.go` | deployment/metrics and evaluator tests |
| Evaluator/report support | `eval*.go`, `tx_source.go`, `report.go` | reviewed only at protocol/metric handoffs |

## Severity Model

- **P0 — critical:** invalidates a core confidentiality, authenticated-channel,
  or consensus-safety assumption.
- **P1 — high:** credible Byzantine liveness/resource-exhaustion risk or major
  missing verification boundary.
- **P2 — medium:** deterministic correctness, terminal-state, interoperability,
  or input-hardening gap within the prototype.
- **P3 — low:** misleading API, unused behavior, maintainability, or
  documentation defect without an immediate protocol impact.

Severity describes the gap relative to the paper/protocol claim. It does not
mean the repository has represented this prototype as production-ready.

## Confirmed Findings

### PIR-001 — Prototype PRF setup exposes setup secrets and insecure diagonal elements

- Severity/category: **P0 — security/cryptography**
- Remediation status (2026-07-15): **partially remediated**. Active production
  consumers now receive a versioned public-point artifact and no setup seed or
  scalar trapdoor. The artifact deliberately retains the inherited `j == i`
  elements, so the finding remains open and no secure-CRS claim is justified.
- Stage: BTE setup
- Evidence: `PRFSetupFromSeed` in `prf/prf.go` derives `xi` and `zi` scalars from
  distributed `CRSSeedHex` and constructs every `g2zixj`, including `j == i`.
  The adjacent `PRFSetup` comment explicitly says those diagonal elements must
  not be published in a real setup.
- Impact: the integrated deterministic setup does not realize BEAT-MEV's secure
  public-parameter assumption. Anyone with the shared seed can reproduce setup
  trapdoors rather than receiving only public group elements.
- Follow-up: replace seed distribution with a serialized public CRS that omits
  toxic scalars/diagonal elements, define setup generation/MPC, and add an
  independent cryptographic review before confidentiality claims.

### PIR-002 — Shared cluster configuration contains every secret share and network private key

- Severity/category: **P0 — security/key custody**
- Remediation status (2026-07-15): **remediated for active prototype
  workflows**. Local, Docker Compose, and EC2 now use public cluster/CRS files
  plus one operator-local secret file; legacy combined config is rejected.
  Trusted-dealer generation and hardened secret storage remain limitations.
- Stage: cluster setup/deployment
- Evidence: `ConfigFile` contains `Shares []ShareConfig` and every
  `P2PPrivKeyHex`; `genConfig` writes all entries into one file; `newNode`
  selects one share but can read the rest.
- Impact: possession or compromise of one mounted shared config defeats
  threshold share isolation and exposes every configured libp2p identity.
- Follow-up: split public cluster configuration from per-operator secrets,
  provision one share/identity per host, and replace trusted-dealer generation
  with the planned DKG/key-custody boundary.

### PIR-003 — RBC reconstruction mixes roots and omits the post-decode root check

- Severity/category: **P0 — correctness/consensus safety**
- Stage: reliable broadcast output
- Evidence: `RBC.tryDecodeValue` checks `countReadys(hash)` and
  `countEchos(hash)`, then copies every entry in `recvEchos` into Reed-Solomon
  reconstruction without filtering `RootHash == hash`. It does not reconstruct
  all shards and verify the Merkle root as required by the HoneyBadger RBC
  algorithm.
- Impact: under a Byzantine proposer and mixed-root ECHOs, a node can
  reconstruct from shards that did not authenticate to the root that satisfied
  READY/ECHO thresholds. Existing tests cover one-fault delivery but not this
  equivocation schedule.
- Follow-up: filter ECHOs to the target root, reconstruct, recompute/compare the
  Merkle root, and add adversarial mixed-root/reordered tests before further
  distributed evidence is accepted.

### PIR-004 — Application quorum sender is not bound to authenticated libp2p peer

- Severity/category: **P0 — security/consensus authentication**
- Remediation status (2026-07-15): **remediated**. Inbound streams require a
  configured authenticated peer, and envelope/share sender claims must match
  its unique operator mapping. Outbound addressing is overwritten from local
  transport state.
- Stage: operator transport ingress
- Evidence: the stream handler in `transport_libp2p.go` decodes
  `Envelope.From` and passes it to ACS/share handling without comparing it with
  `s.Conn().RemotePeer()` or the configured peer-ID-to-operator mapping. It also
  does not explicitly reject unconfigured inbound peers.
- Impact: libp2p authenticates the connection, but a connected peer controls
  the sender ID used by RBC/BBA distinct-sender thresholds and share
  deduplication. This violates the consensus package's authenticated-channel
  assumption.
- Follow-up: derive operator ID from remote peer identity, reject membership
  mismatches, ignore or remove sender claims from the wire, and add spoofing
  tests at the transport/node boundary.

### PIR-005 — BBA uses a predictable epoch-parity placeholder coin

- Severity/category: **P1 — liveness/security**
- Stage: binary agreement epoch choice
- Evidence: `BBA.tryOutputAgreement` sets `coin := b.epoch%2 == 0` and contains a
  TODO for a real common coin.
- Impact: this does not satisfy the unpredictable shared-coin assumption used
  for asynchronous probabilistic termination. An adversarial scheduler can
  reason about every future coin value.
- Follow-up: implement and test a threshold/common-randomness protocol with
  slot/proposer/epoch domain separation, or explicitly narrow all claims to a
  nonadversarial deterministic prototype.

### PIR-006 — Conflicting AUX from one sender overwrites prior BBA state

- Severity/category: **P1 — correctness/Byzantine handling**
- Stage: BBA AUX admission
- Evidence: `handleAuxRequest` assigns `recvAux[senderID] = val` without
  rejecting a second, conflicting value from the same sender.
- Impact: delivery order of Byzantine equivocations can change which value is
  counted for that sender after `binValues` admission. This case is absent from
  current BBA reordered-delivery tests.
- Follow-up: define one accepted AUX per sender/epoch, reject equivocation, and
  add schedules containing duplicate/conflicting AUX messages.

### PIR-007 — Multi-epoch future BBA messages can be dropped during queue drain

- Severity/category: **P1 — liveness/message scheduling**
- Stage: BBA epoch advancement
- Evidence: future messages append to `delayedMessages`. During advancement the
  code ranges the same slice, may reappend messages still ahead of the new
  epoch, and then unconditionally replaces the slice with empty.
- Impact: a valid message from a faster honest instance more than one epoch
  ahead can be discarded, weakening asynchronous liveness.
- Follow-up: partition delayed messages by epoch, retain those still in the
  future, and add multi-epoch reordering tests.

### PIR-008 — Share admission is not membership-bound or publicly verifiable

- Severity/category: **P1 — security/resource exhaustion**
- Remediation status (2026-07-17): **resource-exhaustion portion remediated for
  active prototype workflows**. Authenticated membership/index checks,
  per-operator candidate bounds, post-plan pruning, and cumulative subset
  attempt budgets are enforced. Public share correctness proofs remain open.
- Stage: share ingress and combine
- Evidence: `addWireShare` trusts claimed `OperatorID`, creates the Kyber share
  index from it, and `addShare` admits any new claimed identity. Threshold
  admission counts those identities. `CombineShares` then enumerates
  threshold-sized subsets because shares have no public correctness proof.
- Impact: a peer can cause premature combine attempts, retain an unbounded set
  of claimed operators, and drive combinatorial reconstruction work with
  invalid extras. Malformed shares are not attributable.
- Follow-up: bind sender to configured membership and share index, cap one
  candidate per configured operator/sub-batch, and implement public share
  verification before subset search.

### PIR-009 — Inbound libp2p envelope size is unbounded

- Severity/category: **P1 — availability/resource exhaustion**
- Remediation status (2026-07-17): **remediated**. Shared v2 configuration
  bounds encoded proposals and inbound/outbound envelopes; oversized inbound
  streams are reset before protobuf decoding and emit bounded-label metrics.
- Stage: operator transport ingress
- Evidence: the stream handler calls `io.ReadAll(s)` without a protocol maximum
  or `LimitReader`.
- Impact: any peer able to open the protocol can force large memory allocation
  before protobuf validation.
- Follow-up: define an envelope maximum from `BMax`/proposal bounds, use bounded
  framing or `LimitReader`, reset oversized streams, and add boundary tests.

### PIR-010 — Slot failure is fail-closed but not terminal

- Severity/category: **P2 — liveness/operability**
- Stage: application failure handling
- Evidence: `markSlotFailed` increments counters/Prometheus state but does not
  store a reason in `slotState`, set a failed phase, or publish an error result.
  `prepareSlot` accepts only `completed` slots.
- Impact: malformed selected data correctly produces no plaintext, but
  controllers see a permanently pending slot until timeout and must restart the
  process before another slot.
- Follow-up: add a terminal failed state/result with deterministic reason codes
  while preserving the no-filter/no-refill rule.

### PIR-011 — Invalid decrypted Ethereum bytes still complete the slot

- Severity/category: **P2 — protocol completeness/correctness**
- Stage: materialization
- Evidence: after successful BTE/hash validation, `tryCombine` converts an
  `ethdemo.Parse` error into an `ERROR:` string and still stores a completed
  `Result`.
- Impact: current behavior is neither a fail-closed slot nor a deterministic
  retain-placeholder fallback, and no execution client validates stateful
  transaction semantics.
- Follow-up: specify the materialization/fallback rule before Builder or
  execution integration and test identical behavior across nodes.

### PIR-012 — External cluster configuration is insufficiently validated

- Severity/category: **P2 — input hardening/correctness**
- Stage: node construction and direct submission
- Evidence: `newNode` checks local membership/share and network mode but not all
  `N/Nodes/F/threshold/BMax` constraints. Direct submission computes
  `len(pending) % BMax`, so zero `BMax` can panic.
- Impact: hand-written or corrupted configuration can produce panics,
  inconsistent consensus membership, or invalid threshold behavior instead of
  a startup error.
- Follow-up: centralize full configuration validation before creating PRF, ACS,
  transport, or HTTP state and add malformed-config table tests.

### PIR-013 — Mempool provider fetch has no explicit timeout

- Severity/category: **P2 — liveness/integration**
- Stage: proposal construction
- Evidence: `fetchMempoolInclusionList` uses package-level `http.Get`.
- Impact: an unresponsive source can block the one-shot start path indefinitely
  and leave the slot running without a proposal.
- Follow-up: inject a bounded reusable client and expose timeout failure through
  the terminal slot-failure boundary.

### PIR-014 — Public parameter field `N` represents the index domain, not committee size

- Severity/category: **P3 — API clarity/interoperability**
- Stage: BTE construction/validation
- Evidence: both cluster constructors assign `PublicParams.N = btd.B`, while
  committee `n` is stored separately in lower-level state; ciphertext index
  validation uses `Params.N`.
- Impact: generic callers can misread public metadata and derive an incorrect
  committee-size assumption.
- Follow-up: rename the field to an index-domain term or populate separate
  `BMax` and committee-size fields without changing validation semantics.

### PIR-015 — Replay `Loop` configuration is unused

- Severity/category: **P3 — maintainability**
- Stage: replay source
- Evidence: `ReplayPlaceholderConfig.Loop` is copied into the client but never
  affects corpus traversal or slot behavior.
- Impact: callers can believe looping behavior is configurable when every slot
  always encrypts the full corpus.
- Follow-up: remove the field or implement and document exact cursor/loop
  semantics.

### PIR-016 — Architecture ownership and several references were stale

- Severity/category: **P3 — documentation**
- Stage: repository guidance
- Evidence: the old root architecture mixed protocol internals, deployment,
  metrics, and campaign interpretation; `hbbft` had no implementation deep
  dive; `CLUSTER_BTE.md` still said collisions were rejected; Decisions 0001
  and 0004 referenced absent archive files.
- Impact: reviewers could not identify one source of truth and could infer
  behavior that no longer matched the correctness patch.
- Follow-up: completed by the root/module documentation split and link repair
  recorded in Decision 0013.

## Open Questions And Required Follow-Up Tests

These items require a focused patch or adversarial harness before the exact
impact can be closed:

1. Construct mixed-root RBC schedules for `N=4/7` and determine every possible
   divergent/corrupted output before implementing PIR-003.
2. Add conflicting-AUX and multi-epoch-future schedules to bound PIR-006 and
   PIR-007 against the corrected ACS completion rule.
3. Add a real libp2p integration test that opens a stream as one peer while
   claiming another operator ID.
4. Measure and cap memory/CPU growth for oversized envelopes and many invalid
   share identities; do not optimize subset search before membership bounds are
   correct.
5. Audit every public in-memory BTE API with nil/mutated Kyber objects. Decoder
   inputs are hardened, but direct callers can construct structures that bypass
   deserialization invariants.
6. Decide the terminal failure schema and invalid-Ethereum fallback before
   implementing either behavior.

## Paper-To-Code Traceability

| Paper concept | Implementation | Classification | Review result |
| --- | --- | --- | --- |
| External encrypted candidate/inclusion-list pipeline | `mempool-il` replay source and `bloc-node` provider | Adapted | Deterministic prototype boundary; no production inclusion-list standard |
| One slot of encrypted agreement followed by reveal | `SlotACS` plus `bloc-node.handleACSOutput` | Adapted | Implemented integration; Builder/DVT/execution boundary deferred |
| HoneyBadger `N` RBC + `N` ABA ACS | `rbc.go`, `bba.go`, `acs.go` | Implemented/adapted | Corrected ACS selection rule follows BBA decisions |
| HoneyBadger erasure/Merkle RBC reconstruction | `RBC.tryDecodeValue` | Contradicted | Mixed-root filtering and root recomputation are missing |
| Unpredictable common coin | `BBA.tryOutputAgreement` | Contradicted/deferred | Deterministic epoch parity is only a placeholder |
| Authenticated asynchronous channels | libp2p plus protobuf envelope | Implemented for configured prototype membership | Remote peer IDs are uniquely mapped to operators and bind envelope/share sender claims |
| BEAT-MEV puncturable-PRF BTE | `prf`, `elgamal`, `be/btd.go` | Implemented at prototype level | Core operations and proof equations present |
| BEAT-MEV secure setup/public parameters | serialized public-point CRS | Contradicted | Seed/trapdoor distribution is removed, but inherited diagonal CRS elements remain insecure |
| BEAT-MEV Opt-2 sub-batching | `arrangeBatchFor` | Adapted | `ceil(2*sqrt(B))`, collision-frequency raise, deterministic repair |
| Threshold operator key custody/DKG | trusted-dealer config | Deferred/contradicted | All shares are present in one config; no DKG |
| Raw Ethereum byte support | AES-GCM hybrid envelope | Adapted | Repository-specific context-bound extension around BTE capsule |
| Publicly verifiable decryption shares | subset trial combine | Deferred | Invalid extras are tolerated by search, not verified or attributable |

## Documentation Discrepancies Corrected

- Root architecture now owns only system boundaries, end-to-end handoffs,
  identities, trust model, and cross-module invariants.
- Four module deep dives own production algorithms, state, wire formats,
  concurrency, failure semantics, paper mapping, tests, and limitations.
- The BTE document now reflects scoped decoding, immutable decoded batches,
  AEAD-shape checks, and deterministic collision fallback.
- The consensus document distinguishes paper behavior, active BLOC adaptation,
  inherited HoneyBadger code, and confirmed implementation deviations.
- The node document distinguishes fail-closed selected-data errors from
  per-item Ethereum materialization errors.
- Module READMEs and agent routing point to canonical module documents rather
  than duplicating implementation narratives.

## Validation Record

Validation results are filled from the reviewed working tree before this task
is closed:

| Check | Result |
| --- | --- |
| `go test ./...` — `mempool-il` | Passed |
| `go test ./...` — `sbc/hbbft` | Passed |
| `go test ./...` — `bte/btd-impl-main` | Passed |
| `go test ./...` — `bloc-node` | Passed |
| Changed Markdown local-link resolution | Passed: 21 changed Markdown files |
| Source/symbol reference audit | Passed: 18 principal symbols plus coverage-map inspection |
| `git diff --check` | Passed; only repository line-ending notices |
| Non-document source hash comparison | Passed: all 7 pre-existing source/deployment changes unchanged |
