# RBC READY Self-Admission And Persistent Stream Lanes

## Objective

Reduce avoidable latency in the `n=4, f=1` ACS path while correcting a reliable
broadcast liveness defect and preserving the existing RBC, BBA, and ACS wire
semantics.

The focused program has three implementation phases:

1. admit a node's own READY whenever either RBC trigger makes it emit READY;
2. make only the trace changes required to measure those READY transitions and
   complete pre-decision transport accounting; and
3. test separate persistent control and data stream lanes per peer.

The Merkle-construction optimization, GossipSub dissemination, serialization
micro-optimizations, and replacement RBC protocols are deferred. Cloud
execution is not part of implementation and remains separately authorized.

## Background And Evidence

The accepted issue #23 phase-one campaign compared fresh and persistent libp2p
application streams at `n=4, f=1`, batches `8/32/128`, in the same AZ and across
three regions. Persistent streams improved same-AZ ACS p50 but did not produce a
three-region ACS signal. At batch 128, the persistent three-region arm reported
`523.570 ms` ACS p50 and `545.796 ms` median aggregate queue wait per node trace.
READY sends had approximately `149 ms` median sender-side queue wait.

Source review found two directly relevant implementation problems:

- The ECHO-quorum READY path queues READY and processes it locally, but the
  `F+1` READY relay path queues READY without adding the node's own READY to
  `recvReadys`. For `n=4, f=1`, two remote READYs cause a relay, but the node
  still waits for a third remote READY instead of counting its own vote. If the
  remaining participant withholds, this becomes a liveness failure.
- Persistent mode has one capacity-one writer and one application stream per
  peer. Large PROOF/ECHO frames and small READY/BVAL/AUX frames therefore share
  one FIFO. A control message cannot bypass a large frame already being written
  or waiting in the queue.

The transport summaries in current `bloc-acs-trace/v2` artifacts are also
right-censored. ACS sends run asynchronously, while the node snapshots the
trace at local ACS output. A send completing after that snapshot updates the
live recorder but not the retained result. The accepted trace corpus should
have contained 1,440 ECHO and 1,440 READY completions per stream mode; retained
fresh traces contained 1,372 ECHOs and 1,138 READYs, while persistent traces
contained 1,429 ECHOs and 1,422 READYs. `ACSUS` remains valid, but incomplete
queue and phase summaries cannot reliably attribute a lane experiment.

Repeated Merkle construction is computationally redundant, especially when
post-reconstruction verification generates all proofs and reads only one root.
However, the complete measured reconstruction interval is approximately
`5.3 ms` at batch 128 and includes Reed-Solomon work. That cost is negligible
relative to the current three-region ACS latency and is not in this program.

## Selected Program And Ordering

The implementation order is deliberately linear:

1. **READY self-admission:** correct liveness before performance work.
2. **Minimal trace finalization:** establish trustworthy evidence without
   changing the ACS latency boundary.
3. **Persistent control/data lanes:** isolate small progress messages from
   large RBC frames while keeping the current single-stream mode as the
   control.
4. **Matched evidence campaign:** run only after the three implementation
   phases pass their local and safety gates and after explicit cloud
   authorization.

Each implementation phase has its own repository issue, Project item, and
short-lived branch. Each branch starts from the integrated predecessor so its
validation has one attributable change. The evidence campaign is a separate
tracked task because it has its own provenance and acceptance contract.

The tasks target the active M5 milestone. The READY correction is a liveness
fix, but it is scheduled now because it affects the latency-critical M5 path
and the validity of the planned experiment. The later M6 fault campaign remains
responsible for broader adversarial evidence.

## Alternatives Considered

### Optimize Merkle construction in the current program

This would remove real CPU and allocation waste, but its expected contribution
is small compared with the measured WAN and persistent-queue delays. It is
deferred until CPU profiles or larger committees show it is material.

### Run the lane experiment with the current trace

This would preserve less implementation work, but outstanding sends at ACS
decision preferentially omit slow queue observations. A positive result could
therefore be a measurement artifact. Minimal trace finalization is retained as
an evidence prerequisite.

### Prioritize messages on one persistent stream

A priority queue can reorder messages that have not started writing, but it
cannot interrupt a large frame already being written. It does not isolate the
control path sufficiently for the measured head-of-line mechanism.

### Replace direct streams with GossipSub

GossipSub changes dissemination, routing, duplication, and failure semantics.
It does not isolate application-stream head-of-line blocking and remains
outside this program with issue #23.

## Phase 1: RBC READY Self-Admission

### State transition

Both READY triggers use one internal emission operation:

```text
emitReady(root, trigger)
    require READY has not already been sent
    mark READY sent
    admit the local READY into recvReadys
    enqueue exactly one READY broadcast
    retry decoding
```

The trigger is either:

- `echo_quorum`, when matching ECHOs reach `N-F`; or
- `ready_relay`, when matching READYs reach `F+1`.

Trigger counts describe the matching state immediately before local
self-admission. Phase 1 may retain the trigger as internal call-site context but
does not change the trace schema; phase 2 records that context. The operation
executes inside the existing serialized RBC event loop, so marking,
self-admission, and message emission are one local state transition. It must not
recursively invoke the inbound READY handler.

### Invariants

- A locally emitted READY contributes exactly once to `recvReadys`.
- An RBC instance emits at most one READY.
- `N-F`, `F+1`, `2F+1`, and reconstruction thresholds do not change.
- Self-admission does not bypass the requirement for more than `F` matching,
  valid ECHO shards before reconstruction.
- Duplicate remote READY behavior remains unchanged.
- Mixed-root filtering and post-reconstruction commitment verification remain
  unchanged.
- No wire type or message recipient changes.

For `n=4, f=1`, two matching remote READYs plus the locally relayed READY reach
the required three READYs. If two valid matching ECHO shards are available,
the node can reconstruct without a third remote READY.

### Failure behavior

An invalid or duplicate inbound READY continues to fail through the existing
handler rules. Self-admission is internal state, not a synthetic unauthenticated
inbound message. An error during a later decode attempt must not undo the READY
state or enqueue a second READY.

## Phase 2: Minimal Trace Finalization

### Trace domain

The trace covers all ACS sends emitted up to and including the state transition
that produces local ACS output. The node already drains messages from that
transition before handing the output to `handleACSOutput`, so it can establish
this boundary without waiting inside ACS.

For every in-domain send, tracing records two lifecycle events:

1. **Scheduled:** recorded synchronously before the background send starts.
2. **Completed:** recorded when the transport attempt terminates successfully
   or with an error.

At local output, the node records its existing decision milestones, closes
admission to the pre-decision trace set, and records the number of scheduled
sends still pending. The trace becomes final when every send in that closed set
has a terminal outcome.

New post-decision sends are not added to this latency trace. Every exit path for
an admitted send, including stale-slot rejection, cancellation, encoding
failure, queue timeout, stream failure, and successful write, must publish one
terminal completion.

### Result publication

Tracing must not block ACS, RBC, BBA, merge/plan work, share generation, or BTE
reconstruction. When tracing is enabled, only diagnostic result publication is
held until the pre-decision trace is final. The timestamps used for `ACSUS`,
`TotalSlotUS`, and all existing stage metrics are recorded at their current
boundaries and are not extended by trace finalization.

Trace-disabled result behavior remains unchanged. A trace-enabled `/result`
request returns the existing pending response until both the materialized
result exists and its trace is final. File-based result publication observes
the same gate. Slot status continues to distinguish protocol/materialization
completion and additionally exposes whether diagnostic trace finalization is
pending. The transport's existing bounded send deadline prevents an admitted
send from keeping the trace pending indefinitely.

### Schema

New artifacts use `bloc-acs-trace/v3`. Historical v1 and v2 artifacts remain
readable but do not satisfy the v3 completion contract.

Each message subtype exposes enough bounded aggregate state to distinguish:

- scheduled attempts;
- terminal completions;
- successful writes and bytes;
- failed attempts; and
- attempts pending at local ACS decision.

`pending_at_decision` is an immutable snapshot of how many sends were still in
flight when ACS produced output. Finalization does not erase that historical
value. Current pending work is derived as `scheduled-terminal_completions` and
must be zero before a v3 artifact is published.

A final v3 trace requires:

```text
scheduled = successful + failed
scheduled - terminal completions = 0
```

Existing phase totals and maxima continue to describe successful sender-side
transport attempts. They do not claim remote receipt.

Each RBC trace also records the first READY emission trigger as
`echo_quorum` or `ready_relay`, plus the matching ECHO and READY counts before
self-admission. The trigger is bounded to the existing proposer-indexed RBC
entries and introduces no peer, stream, or epoch cardinality.

### Concurrency and slot isolation

Send admission captures the active slot recorder synchronously. Completion
updates that captured recorder rather than whatever slot is active later. Trace
closure and completion accounting use the recorder's existing synchronization
boundary and must not acquire locks in an order that can deadlock node result
publication or slot replacement.

The finalization gate ensures a slot cannot be accepted as a complete
diagnostic artifact while one of its tracked sends remains outstanding. A
replacement slot starts with empty counters and cannot receive completions from
the preceding slot.

## Phase 3: Persistent Control And Data Lanes

### Configuration and compatibility

Add `network.stream_mode: "persistent-lanes"` alongside the existing `fresh`
and `persistent` values.

- Omitted mode behavior does not change.
- `fresh` retains `/bloc/envelope/1.0.0`.
- `persistent` retains `/bloc/envelope/2.0.0` and one writer per peer.
- `persistent-lanes` uses distinct control and data protocol IDs and registers
  only those handlers for experiment traffic.
- Unknown values continue to fail configuration validation before startup.

The proposed protocol IDs are:

```text
/bloc/envelope/3.0.0/control
/bloc/envelope/3.0.0/data
```

They carry the same length-prefixed protobuf `WireEnvelope` framing and do not
change the protobuf schema.

### Lane routing

Every remote peer has two independent `peerStreamWriter` instances:

| Lane | Envelope payloads |
| --- | --- |
| Control | RBC READY, BBA BVAL, BBA AUX |
| Data | RBC PROOF, RBC ECHO, decryption share |

Each writer has its own capacity-one queue, worker goroutine, persistent stream,
deadline accounting, readiness state, and reset/reopen lifecycle. Both streams
remain multiplexed over the same authenticated libp2p peer connection.

Outbound routing classifies the decoded envelope type before selecting a
writer. An invalid or unclassifiable envelope fails rather than defaulting to a
lane. `Send` retains its synchronous per-attempt completion contract inside the
node's existing asynchronous send goroutine.

### Inbound enforcement

The control and data protocol handlers share framing, size checks, protobuf
decoding, authenticated-peer checks, recipient checks, and the common node
handler. After authentication and payload validation, the transport classifies
the envelope and requires it to match the protocol lane.

A wrong-lane envelope is rejected, counted with a bounded rejection reason, and
resets only the offending stream. This prevents an honest implementation error
or a peer sending PROOF/ECHO on the control protocol from silently recreating
the same application-level FIFO.

The design does not claim Byzantine quality-of-service isolation. A malicious
peer may still construct an abnormally large nominal control payload within the
global envelope limit. Subtype-specific size limits are outside this program.

### Ordering and asynchronous delivery

- FIFO order is preserved within each lane after admission to that lane's
  bounded writer.
- No ordering is promised between control and data lanes.
- RBC must continue to tolerate READY arriving before ECHO. Existing handlers
  retain READY state and retry decoding when sufficient ECHOs arrive.
- BVAL and AUX share the control lane, preserving their per-peer lane order.
- Recipients, message counts, payloads, and application bytes do not change.

### Readiness, recovery, and shutdown

Persistent-lane readiness requires the underlying peer connection, support for
both lane protocol IDs, and one negotiated, prewarmed outbound stream per lane
for every remote operator. Logs identify the peer and lane.

A write or deadline failure resets only its lane and fails the current envelope
without replay, preserving the existing uncertain-delivery rule. A later
distinct send may open a replacement for that lane; the other lane remains
usable. Transport shutdown stops admission, fails both bounded queues, resets
both streams, and waits for both workers and inbound handlers.

Separate application streams remove the local one-writer FIFO dependency. They
do not reduce payload volume, protocol rounds, libp2p connection congestion,
TCP packet-loss head-of-line blocking, or operating-system scheduling delay.

## Implementation And Documentation Boundaries

### READY issue

- Branch: `codex/rbc-ready-self-admission`
- Primary source: `sbc/hbbft/rbc.go` and colocated tests.
- Canonical documents: `docs/modules/hbbft.md`, `docs/VALIDATION.md`,
  `docs/CHANGELOG.md`, and `docs/STATUS.md` when the open risk is resolved.
- Non-goals: tracing, Merkle construction, transport, and threshold redesign.

### Trace issue

- Branch: `codex/acs-trace-finalization`
- Primary source: `sbc/hbbft/trace.go`, the slot adapter, `bloc-node` send and
  result lifecycle, trace artifact validation, and evaluator summaries.
- Canonical documents: `sbc/hbbft/README.md`, `bloc-node/README.md`,
  `docs/modules/hbbft.md`, `docs/modules/bloc-node.md`, `docs/VALIDATION.md`, and
  `docs/CHANGELOG.md`.
- Non-goals: protocol thresholds, transport routing, new unbounded events, and
  changes to latency timestamp boundaries.

### Stream-lane issue

- Branch: `codex/persistent-stream-lanes`
- Primary source: `bloc-node` transport, stream-mode configuration,
  materializers, evaluator manifests, and focused transport tests.
- Canonical documents: `bloc-node/README.md`, `docs/modules/bloc-node.md`,
  `docs/VALIDATION.md`, `docs/DECISIONS.md`, and `docs/CHANGELOG.md`.
- Non-goals: GossipSub, RBC dissemination, compression, batching, priority
  queues, delivery acknowledgements, protobuf changes, and byte reduction.

Every non-trivial task must have a GitHub issue and BLOC Thesis Prototype
Project item before implementation. Issues name their canonical documents,
dependencies, acceptance criteria, and validation commands. Material progress
and evidence belong in the issue; `STATUS.md` retains only milestone-level
risks, accepted evidence, baseline, and immediate actions.

## Validation Strategy

### READY self-admission tests

Implementation begins with a deterministic failing `n=4, f=1` regression:

1. deliver two distinct valid matching ECHOs, below the `N-F` trigger;
2. deliver two matching remote READYs;
3. withhold the remaining participant's READY; and
4. require one outbound READY, three stored matching READYs including self, and
   successful output.

Additional tests require no output with only one matching ECHO, no duplicate
READY when the ECHO threshold is reached later, preservation of the ECHO-quorum
path, and unchanged mixed-root and wrong-root rejection.

Acceptance requires the targeted regression, the complete `sbc/hbbft` suite,
the relevant RBC/ACS/BBA race selection, and the full ACS safety campaign. The
safety artifact must retain only successful, cross-node-consistent measured
slots under its documented fault model.

### Trace finalization tests

A blocking transport test schedules sends, holds them before completion,
produces local ACS output, and proves that the decision snapshot reports the
pending attempts. After release, the final trace must account for every success
or failure exactly once.

Coverage includes successful and failed terminal attempts, result publication
gating, unchanged metric timestamps, old-slot isolation, v1/v2 compatibility,
v3 fail-closed artifact validation, and both READY trigger values and counts.

The existing paired `BenchmarkACSTrace` gate remains: no trace-enabled cell may
exceed its matching trace-disabled median local ACS time by more than 2%.
Affected normal and race suites, a local trace artifact smoke, and the ACS
safety campaign must pass.

### Stream-lane tests

The principal head-of-line regression uses blocking fake streams:

1. one data write remains in progress;
2. a second data send fills the capacity-one data queue;
3. a control READY is submitted; and
4. the READY must complete before either data send is released.

Additional coverage includes every subtype and share routing, FIFO within each
lane, READY-before-ECHO delivery, per-lane bounds, two-lane prewarm/readiness,
mixed-mode readiness failure, inbound wrong-lane rejection, lane-local reset
and replacement, stream reuse, shutdown, and concurrent race stress.

Before any cloud work, a matched local diagnostic compares `persistent` and
`persistent-lanes` at `n=4`, batches `8/32/128`, five warmups, and 30 measured
repetitions from identical material and schedules. It is a mechanism and
regression gate, not topology evidence. A stable control-queue regression
blocks cloud escalation; a null local ACS result does not.

The complete `bloc-node` and `hbbft` suites, relevant race gates, local
evaluation, and the final ACS safety campaign must pass on the clean candidate.

## Matched Same-AZ And Three-Region Campaign

Campaign execution is a separate issue and requires explicit live
authorization. The proposed matrix uses one final integrated source:

| Dimension | Values |
| --- | --- |
| Topology | same AZ, three region |
| Committee | `n=4, f=1` |
| Batch | `8`, `32`, `128` |
| Stream mode | `persistent`, `persistent-lanes` |
| Warmups | 5 per cell |
| Measurements | 30 per cell |
| Trace | finalized `bloc-acs-trace/v3` |

The two modes differ only in stream configuration. Identity material, public
CRS, BTE shares, corpus, schedule, instance class, image, and deployment tooling
remain matched. New and historical measurements are not merged into one
campaign. The accepted issue #23 results provide context only because their
source and trace completion semantics differ.

Artifact acceptance requires 30 successful and consistent observations per
cell, no missed deadlines, zero ACS send failures, exact provenance and schedule
matching, a final trace for every node, and balanced scheduled/completed counts
for every subtype. Thirty observations support p50 and exploratory p95/maximum
reporting, not p99.

The primary mechanism metric is READY queue wait in three-region batch 128. A
strong positive result requires:

- at least a 20% reduction in median READY queue wait;
- a 95% bootstrap interval for the median difference below zero;
- at least a 5% reduction in three-region batch-128 ACS p50; and
- no statistically supported ACS regression in another batch or topology.

Results are classified as:

- **adopt:** control queueing and ACS latency improve without regression;
- **mechanism only:** control queueing improves but ACS latency does not, so the
  mode remains experimental;
- **reject:** queueing regresses, correctness fails, sends fail, or another cell
  shows a material latency regression; or
- **tail follow-up:** p50 is inconclusive but p95/maximum suggests an effect, in
  which case a separately authorized batch-128 campaign with at least 100
  measurements is required before a tail claim.

## Program Acceptance

The focused program is complete only when:

- the READY-withholding schedule completes by counting the local READY exactly
  once;
- all RBC safety and commitment checks remain green;
- every retained v3 diagnostic trace has complete pre-decision send accounting;
- the split-lane transport proves deterministic control/data queue isolation;
- existing fresh and persistent modes remain compatible;
- all named module, race, local evaluation, and ACS safety gates pass from clean
  committed sources;
- canonical documentation and issue evidence match implemented behavior; and
- the lane mode remains experimental unless matched topology evidence meets the
  adoption criteria.

No Merkle, GossipSub, alternate-RBC, serialization, n7 latency, p99, production
Byzantine-resilience, or bandwidth-reduction claim is part of this design.
