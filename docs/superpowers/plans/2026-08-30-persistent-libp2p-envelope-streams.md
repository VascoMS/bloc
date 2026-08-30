# Persistent libp2p Envelope Streams Phase-One Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in, bounded persistent-stream transport for existing BLOC
envelopes and produce matched evidence that determines whether fresh libp2p
application-stream churn contributes to cross-region ACS latency.

**Architecture:** Preserve the current `/bloc/envelope/1.0.0` fresh-stream path
as the baseline and add `/bloc/envelope/2.0.0` for repeated varint-framed
envelopes. Each remote operator gets one dedicated writer goroutine, one owned
outbound stream, and a capacity-one queue. The transport returns bounded phase
timings so ACS diagnostics distinguish encode, queue, stream-open, frame-write,
and fresh-stream finalization time. Persistent readiness prewarms one directed
stream per peer after Identify advertises v2 support; failed or uncertain
writes reset the stream and are never replayed.

**Tech Stack:** Go 1.24, go-libp2p v0.48.0 with yamux and Identify, protobuf,
standard-library `encoding/binary` framing, JSON/CSV/JSONL evaluator artifacts,
Python 3 with pandas/pytest, and Bash campaign contracts.

**Spec:**
[Persistent libp2p Envelope Stream Experiment](../specs/2026-08-30-persistent-libp2p-envelope-stream-design.md)
and [GitHub Issue #23](https://github.com/VascoMS/bloc/issues/23)

## Global Constraints

- Keep `network.stream_mode` opt-in. Omission means `fresh`; unknown values
  fail before transport startup.
- Keep cluster config schema `bloc-cluster-v3`; the new field is additive and
  its omitted behavior is unchanged.
- Preserve RBC, BBA, ACS selection, thresholds, message contents, routing,
  authenticated peer-to-operator binding, and protobuf envelope encoding.
- Preserve `/bloc/envelope/1.0.0` and its one-envelope-per-stream behavior as
  the matched baseline. Do not silently fall back between v1 and v2.
- Use `/bloc/envelope/2.0.0` only for persistent mode and frame every envelope
  with an unsigned-varint length bounded by `max_envelope_bytes` before
  allocation.
- Use one writer goroutine and a channel of capacity one per remote operator.
  No application writer mutex, unbounded queue, priority, batching,
  compression, acknowledgement, or automatic replay is allowed.
- A `Send` without a caller deadline gets a ten-second internal deadline.
  Queue admission, queue residence, stream opening, and writing all remain
  cancelable and bounded.
- A partial or failed frame write has uncertain delivery: reset the stream,
  fail that envelope, and let only the next distinct envelope open a
  replacement.
- A successful transport write is not a receiver acknowledgement. Report ACS
  milestones alongside sender phases and never infer causality from a lower
  `Send` duration alone.
- Keep diagnostics bounded to the five fixed ACS subtypes. Do not add peer,
  slot, epoch, or stream IDs as Prometheus labels.
- Retain historical `bloc-acs-trace/v1` readers. New diagnostic runs emit
  `bloc-acs-trace/v2`; v1 and v2 records must not be combined into one matched
  result.
- Use the existing n=4 scope with batches `8,32,128`, 5 warm-ups, and 30
  measurements per cell. Report p50/p95/max, not p99.
- No live AWS allocation or campaign execution is authorized by this plan.
  Same-AZ and three-region canaries require local correctness and separate
  user authorization.
- Keep the result path lean: Tasks 1--9 produce and evaluate the local matched
  signal. Task 10 prepares the cloud campaign contract only after that gate
  justifies canaries; it does not start cloud resources.
- Every behavior change follows red-green-refactor: each named test first
  fails for the missing production behavior, then passes after the smallest
  implementation.

---

### Task 1: Make stream mode an explicit, retained configuration contract

**Files:**

- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/commands.go`
- Modify: `bloc-node/internal/app/ec2_config.go`
- Modify: `bloc-node/internal/app/campaign_materialize.go`
- Modify: `bloc-node/internal/app/eval.go`
- Modify: `bloc-node/internal/app/eval_persistent.go`
- Modify: `bloc-node/internal/app/eval_suite.go`
- Modify: `bloc-node/internal/app/eval_remote.go`
- Modify: `bloc-node/internal/app/config_security_test.go`
- Modify: `bloc-node/internal/app/main_test.go`
- Modify: `bloc-node/internal/app/deployment_test.go`
- Modify: `bloc-node/internal/app/campaign_materialize_test.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`

**Interfaces:**

```go
const (
	streamModeFresh      = "fresh"
	streamModePersistent = "persistent"
)

type NetworkConfig struct {
	Mode       string `json:"mode,omitempty"`
	StreamMode string `json:"stream_mode,omitempty"`
}
```

Add `StreamMode string` to `suiteOptions`, `suiteManifest`,
`remoteEvalConfig`, `ec2ConfigOptions`, and `campaignMaterializeOptions`. Add
`--stream-mode fresh|persistent` to `gen-config`, `gen-ec2-config`,
`eval-suite`, and `materialize-campaign-config`. The remote evaluator reads the
retained value from its config rather than accepting a second independent
override.

- [ ] **Step 1: Write failing normalization and validation tests**

Add table tests proving that an omitted value normalizes to `fresh`, both
explicit values validate, and `"reuse"`, whitespace-only, and mixed-case
values fail. Assert validation occurs while loading config, before a transport
is constructed.

```go
func TestNormalizeNetworkConfigDefaultsStreamModeToFresh(t *testing.T) {
	cfg := NetworkConfig{Mode: "libp2p"}
	normalizeNetworkConfig(&cfg)
	if cfg.StreamMode != streamModeFresh {
		t.Fatalf("stream mode = %q, want fresh", cfg.StreamMode)
	}
}

func TestValidateNetworkConfigRejectsUnknownStreamMode(t *testing.T) {
	cfg := NetworkConfig{Mode: "libp2p", StreamMode: "reuse"}
	if err := validateNetworkConfig(cfg); err == nil {
		t.Fatal("unknown stream mode was accepted")
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'TestNormalizeNetworkConfig|TestValidateNetworkConfig' -count=1`

Expected: compile failure because `StreamMode`, constants, and the network
normalizer/validator do not exist.

- [ ] **Step 3: Implement normalization and fail-closed validation**

Create `normalizeNetworkConfig(*NetworkConfig)` and
`validateNetworkConfig(NetworkConfig) error`. Call them from the existing
config normalization and validation path. Do not trim or case-fold a non-empty
value; retained inputs must state the exact accepted mode.

- [ ] **Step 4: Add failing generator and materializer retention tests**

Extend generator tests to parse the emitted cluster JSON and assert
`network.stream_mode`. Extend campaign materialization tests to assert that the
cluster config and remote evaluator config contain the same requested mode.
Extend evaluator option tests to reject an unknown mode and assert the suite
manifest records it.

```go
if cluster.Network.StreamMode != streamModePersistent ||
	remote.StreamMode != streamModePersistent {
	t.Fatalf("mode not retained: cluster=%q remote=%q",
		cluster.Network.StreamMode, remote.StreamMode)
}
```

- [ ] **Step 5: Run generator tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'StreamMode|MaterializedCampaign|GenEC2|EvalSuite' -count=1`

Expected: failures because flags, option propagation, and retained manifest
fields are absent.

- [ ] **Step 6: Wire the explicit mode through all config builders**

Use `fresh` as every CLI default. Pass the selected value into locally
generated configs, persistent evaluator cluster generations, EC2 configs, and
campaign materializations. Keep `suiteSchemaVersion` unchanged because the
manifest field is additive. Validate cluster and remote evaluator mode equality
before a remote run starts.

- [ ] **Step 7: Run the complete app tests**

Run: `cd bloc-node && go test ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 8: Commit the configuration contract**

```bash
git add bloc-node/internal/app
git commit -m "feat(bloc-node): add explicit libp2p stream mode"
```

### Task 2: Add phase-aware transport results and ACS trace v2

**Files:**

- Modify: `bloc-node/internal/app/transport.go`
- Modify: `bloc-node/internal/app/transport_libp2p.go`
- Create: `bloc-node/internal/app/transport_libp2p_test.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/node_slot_test.go`
- Modify: `bloc-node/internal/app/resource_safety_test.go`
- Modify: `sbc/hbbft/trace.go`
- Modify: `sbc/hbbft/bloc_slot.go`
- Modify: `sbc/hbbft/trace_test.go`
- Modify: `sbc/hbbft/bloc_slot_test.go`

**Interfaces:**

```go
const defaultTransportSendTimeout = 10 * time.Second

type transportSendResult struct {
	EncodedBytes     int
	EncodeDuration   time.Duration
	QueueWaitDuration time.Duration
	StreamOpenDuration time.Duration
	WriteDuration    time.Duration
	FinalizeDuration time.Duration
	StreamReused     bool
}

type Transport interface {
	Start(context.Context, EnvelopeHandler) error
	Send(context.Context, uint64, WireEnvelope) (transportSendResult, error)
	Close() error
}
```

```go
const (
	ACSTraceSchemaV1      = "bloc-acs-trace/v1"
	ACSTraceSchemaV2      = "bloc-acs-trace/v2"
	ACSTraceSchemaVersion = ACSTraceSchemaV2
)

type ACSSendPhaseTrace struct {
	Count   uint64 `json:"count"`
	TotalUS int64  `json:"total_us"`
	MaxUS   int64  `json:"max_us"`
}

type ACSSendObservation struct {
	Size       int
	Total      time.Duration
	Encode     time.Duration
	QueueWait  time.Duration
	StreamOpen time.Duration
	Write      time.Duration
	Finalize   time.Duration
	Reused     bool
	Err        error
}
```

Add the five fixed phase aggregates plus `StreamOpenCount` and
`StreamReuseCount` to `ACSMessageTrace`. Keep existing send count/total/max and
failure fields. Each successful send increments every phase count even when a
phase duration is zero; phase timings from failed sends remain available in the
transport result but do not enter successful latency aggregates. For fresh
mode `StreamOpenCount == SendCount`; for persistent mode a send that uses a
prewarmed or previously used stream increments `StreamReuseCount`.
Persistent `FinalizeDuration` is explicitly zero because it performs no
per-message half-close or close; the worker completion is not a wire phase.

- [ ] **Step 1: Write failing trace aggregation tests**

Record one reused success, one newly opened success, and one failure with
partial timings. Assert successful count/bytes and open/reuse counts change
only on successful complete writes, failures increment only
`SendFailureCount`, and phase maxima are independent.

```go
slot.RecordACSOutbound(ACSMessageReady, ACSSendObservation{
	Size: 512, Total: 9 * time.Millisecond,
	Encode: time.Millisecond, QueueWait: 2 * time.Millisecond,
	Write: 6 * time.Millisecond, Reused: true,
})
```

- [ ] **Step 2: Run hbbft tests and verify RED**

Run:
`cd sbc/hbbft && go test ./... -run 'Test.*ACS.*Outbound|Test.*SendPhase' -count=1`

Expected: compile failures because the observation and phase types do not
exist and `RecordACSOutbound` has the old signature.

- [ ] **Step 3: Implement bounded trace v2 aggregation**

Change the current trace constant to v2 while retaining named v1 and v2
constants for readers. Implement one locked helper that adds a duration using
non-negative microseconds. Initialize only the five existing subtype keys.
Deep-copy the added fixed values in trace snapshots. Do not add maps keyed by
peer or stream.

- [ ] **Step 4: Run hbbft tests and verify GREEN**

Run: `cd sbc/hbbft && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing app transport-result tests**

Update the two test transports to return `transportSendResult`. Add a node-slot
test where a fake transport returns distinct phase durations and prove the
slot trace receives them. Add a failure case proving outbound bytes remain
zero while partial phase data is not reclassified as success. In the new
libp2p transport test, inject an opener/fake stream whose `Write`, `CloseWrite`,
and `Close` stages are independently blocked and assert fresh mode attributes
open, write, and finalization to distinct result fields with zero queue time.

- [ ] **Step 6: Run app tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'Test.*Transport|Test.*ACS.*Send' -count=1`

Expected: compile failures at `Transport.Send` implementations and the old
node recording call.

- [ ] **Step 7: Change the transport boundary, instrument fresh mode, and adapt the node**

Measure total wall time around `Transport.Send` in `sendEnvelope`, convert the
app result into `hbbft.ACSSendObservation`, and continue calling
`recordOutbound` only after `err == nil`. Return partial phase values with an
error for bounded failure logging and unit assertions, but do not mix those
values into successful latency aggregates or count bytes as delivered. In the
fresh sender, replace the deferred close with explicit timing around encode,
`host.NewStream`, `writeAll`, and `CloseWrite` plus `Close`. Treat the last two
operations as finalization and reset on earlier errors. Apply the effective
ten-second deadline when the caller supplies none. Put that deadline helper in
`transport.go` so the persistent path reuses the exact policy.

- [ ] **Step 8: Run both Go module suites**

Run:
`cd sbc/hbbft && go test ./... -count=1 && cd ../../bloc-node && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 9: Commit the phase-aware result contract**

```bash
git add sbc/hbbft bloc-node/internal/app
git commit -m "feat(metrics): split ACS transport send phases"
```

### Task 3: Implement and prove bounded v2 frame encoding

**Files:**

- Create: `bloc-node/internal/app/transport_libp2p_frame.go`
- Create: `bloc-node/internal/app/transport_libp2p_frame_test.go`

**Interfaces:**

```go
func writeEnvelopeFrame(w io.Writer, payload []byte) error
func readEnvelopeFrame(r *bufio.Reader, maxEnvelopeBytes int) ([]byte, error)
```

The format is `uvarint(payload_length) || protobuf_payload`. Clean EOF before
any prefix byte ends a stream. Zero, overflowed, oversized, or truncated frames
are protocol errors. Bounds are checked before allocating the payload buffer.

- [ ] **Step 1: Write failing round-trip and concatenation tests**

Use a writer that returns short writes to prove the helper loops safely. Write
three frames to one buffer, read them in order, then assert the next read
returns clean `io.EOF`.

```go
for _, payload := range [][]byte{[]byte("a"), bytes.Repeat([]byte{2}, 128), []byte("z")} {
	if err := writeEnvelopeFrame(&buf, payload); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'TestEnvelopeFrame' -count=1`

Expected: compile failure because the frame helpers do not exist.

- [ ] **Step 3: Implement the minimum standard-library codec**

Use `binary.PutUvarint`, the existing `writeAll`, `binary.ReadUvarint`, and
`io.ReadFull`. Return sentinel-wrapped errors for zero, overflow, oversize, and
truncation so the handler can record a bounded rejection reason. Do not add
`go-msgio` or another framing dependency.

- [ ] **Step 4: Add failing hostile-frame tests**

Cover zero length, `max+1`, a ten-byte overflowed varint, a truncated prefix, a
truncated payload, a negative maximum, and a prefix advertising a very large
frame. Wrap the reader so the test can prove an oversized prefix is rejected
without attempting the advertised allocation/read.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run:
`cd bloc-node && go test ./internal/app -run 'TestEnvelopeFrame' -count=1`

Expected: PASS.

- [ ] **Step 6: Run resource and race coverage**

Run:
`cd bloc-node && go test -race ./internal/app -run 'TestEnvelopeFrame|Test.*Envelope.*Bound' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the frame codec**

```bash
git add bloc-node/internal/app/transport_libp2p_frame.go bloc-node/internal/app/transport_libp2p_frame_test.go
git commit -m "feat(transport): add bounded envelope framing"
```

### Task 4: Build the capacity-one per-peer writer in isolation

**Files:**

- Create: `bloc-node/internal/app/transport_libp2p_persistent.go`
- Create: `bloc-node/internal/app/transport_libp2p_persistent_test.go`

**Interfaces:**

```go
const (
	persistentQueueCapacity = 1
)

type persistentWriteStream interface {
	io.Writer
	Close() error
	Reset() error
	SetWriteDeadline(time.Time) error
}

type persistentSendRequest struct {
	ctx        context.Context
	payload    []byte
	enqueuedAt time.Time
	result     chan persistentSendCompletion // capacity one
}

type persistentSendCompletion struct {
	result transportSendResult
	err    error
}

type peerStreamWriter struct {
	operatorID uint64
	queue      chan persistentSendRequest
	open       func(context.Context, uint64) (persistentWriteStream, error)
	stop       <-chan struct{}
	ready      atomic.Bool
}
```

All mutable stream ownership remains inside the worker loop. A small lifecycle
lock or atomics may expose readiness/closed state, but no mutex may serialize
frame writes.

- [ ] **Step 1: Write the failing serialization and queue-capacity tests**

Use a blocking fake stream. Start one send that is writing, admit exactly one
waiting send, then assert a third send cannot enter before its short deadline.
Release the writer and decode the bytes to prove the first two complete frames
are non-interleaved.

- [ ] **Step 2: Run the focused worker tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'TestPeerStreamWriter' -count=1`

Expected: compile failure because the writer types do not exist.

- [ ] **Step 3: Implement effective deadlines and synchronous completion**

Create a helper that derives the earlier of the caller deadline and now plus
ten seconds. `Send` encodes before enqueue, records queue start after encode,
selects between queue admission, effective context completion, and transport
shutdown, then waits for the buffered worker result or its deadline. Copy or
treat the encoded byte slice as immutable after admission.

- [ ] **Step 4: Implement the exclusive worker loop**

For each request:

1. Skip it without opening or writing if its context already expired.
2. Reuse the owned stream or time a replacement open.
3. Set the effective write deadline.
4. Time one `writeEnvelopeFrame` call.
5. On failure, reset and clear the stream, publish failure, and never replay.
6. On success, set finalization to zero, publish the full result, and keep the
   stream for the next request.

Use buffered completion channels so a caller deadline cannot strand the worker
while it publishes the eventual completion.

- [ ] **Step 5: Add cancellation, failure, and replacement tests**

Cover deadline before admission, cancellation while waiting, cancellation
during a blocked write, open failure, partial write followed by reset, and a
later distinct send opening one replacement. Assert the failed payload occurs
at most once in captured bytes and is never replayed.

- [ ] **Step 6: Add shutdown and leak tests**

Stop admission, release or reset the active fake stream, fail the queued
request, and wait for the worker. Assert all callers return and use a bounded
goroutine-count/leak check consistent with existing resource tests.

- [ ] **Step 7: Run focused race coverage**

Run:
`cd bloc-node && go test -race ./internal/app -run 'TestPeerStreamWriter' -count=10`

Expected: PASS with exactly one waiting request maximum and no races.

- [ ] **Step 8: Commit the isolated writer**

```bash
git add bloc-node/internal/app/transport_libp2p_persistent.go bloc-node/internal/app/transport_libp2p_persistent_test.go
git commit -m "feat(transport): add bounded per-peer stream writer"
```

### Task 5: Add the authenticated persistent inbound loop

**Files:**

- Modify: `bloc-node/internal/app/transport_libp2p.go`
- Modify: `bloc-node/internal/app/transport_libp2p_persistent.go`
- Modify: `bloc-node/internal/app/transport_libp2p_persistent_test.go`
- Modify: `bloc-node/internal/app/resource_safety_test.go`

**Interfaces:**

```go
const (
	blocEnvelopeProtocolFresh      = protocol.ID("/bloc/envelope/1.0.0")
	blocEnvelopeProtocolPersistent = protocol.ID("/bloc/envelope/2.0.0")
)

func (t *LibP2PTransport) handlePersistentStream(stream network.Stream)
```

Fresh mode registers only v1. Persistent mode registers only v2. The v2
handler binds the remote peer ID to an operator once, then validates every
decoded envelope independently before invoking the existing handler.

- [ ] **Step 1: Write a failing multi-frame delivery test**

Build a real in-process two-host fixture using generated libp2p identities.
Open one v2 stream, write three valid framed protobuf envelopes, and assert the
receiver invokes its handler three times with the encoded protobuf sizes.

- [ ] **Step 2: Run the focused test and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'TestPersistentHandlerDeliversMultipleFrames' -count=1`

Expected: failure because v2 is not registered and no frame loop exists.

- [ ] **Step 3: Implement the v2 read loop using existing validation**

Resolve `stream.Conn().RemotePeer()` through the configured operator map once.
For every frame call the existing codec, `validateEnvelopePayload`, and
`validateAuthenticatedEnvelope`. Record inbound bytes only after complete
decode/authentication and invoke the unchanged `EnvelopeHandler` synchronously
as the current v1 handler does.

- [ ] **Step 4: Add hostile and authenticated-peer tests**

For each case below, assert no invalid envelope is delivered, the stream is
reset, and the bounded rejection metric/log reason is recorded:

- unknown remote peer;
- forged `From`;
- wrong `To` or `Direct=false`;
- share operator different from the authenticated peer;
- invalid payload union;
- oversized, truncated, or undecodable frame after one valid frame.

Also prove clean EOF after complete frames exits without marking a protocol
rejection.

- [ ] **Step 5: Run focused resource and race tests**

Run:
`cd bloc-node && go test -race ./internal/app -run 'TestPersistentHandler|Test.*AuthenticatedEnvelope|Test.*Envelope.*Bound' -count=1`

Expected: PASS.

- [ ] **Step 6: Prove fresh-wire compatibility**

Keep or add a v1 test that sends one unframed protobuf envelope followed by
`CloseWrite`, receives exactly one message, and observes the unchanged
`/bloc/envelope/1.0.0` protocol ID. Assert a fresh-mode node does not advertise
v2 and a persistent-mode node does not advertise v1.

- [ ] **Step 7: Run the complete app suite**

Run: `cd bloc-node && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit the inbound protocol**

```bash
git add bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_persistent.go bloc-node/internal/app/transport_libp2p_persistent_test.go bloc-node/internal/app/resource_safety_test.go
git commit -m "feat(transport): receive authenticated persistent frames"
```

### Task 6: Integrate prewarming, readiness, replacement, and shutdown

**Files:**

- Modify: `bloc-node/internal/app/transport_libp2p.go`
- Modify: `bloc-node/internal/app/transport_libp2p_persistent.go`
- Modify: `bloc-node/internal/app/transport_libp2p_test.go`
- Modify: `bloc-node/internal/app/node_slot_test.go`

**Interfaces:**

- `LibP2PTransport.Start` registers the mode-specific handler and starts one
  writer/prewarm lifecycle per remote operator in persistent mode.
- `Ready` in fresh mode retains connection-only behavior.
- `Ready` in persistent mode requires connection, peerstore-advertised v2
  support, and one cached outbound stream for every remote operator.
- `Close` stops admission, cancels prewarming, fails queued requests, resets or
  closes active streams, closes the host, and waits for outbound workers and
  inbound reader handlers.

- [ ] **Step 1: Write failing prewarm and readiness tests**

Start four in-process transports. Assert persistent readiness remains false
while one peer lacks v2 support and becomes true only after each directed
outbound stream is cached. Count opened streams and prove repeated `Ready`
calls do not open extras.

- [ ] **Step 2: Run readiness tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'TestPersistent.*Ready|TestPersistent.*Prewarm' -count=1`

Expected: failures because readiness still checks only connectedness.

- [ ] **Step 3: Implement Identify-gated prewarming**

After the existing static connection loop reports a connection, poll the
peerstore with `SupportsProtocols(peerID, blocEnvelopeProtocolPersistent)`
under the transport lifecycle context. Open with `network.WithNoDial` and the
existing bounded retry cadence. Mark a writer ready only after `NewStream`
returns and the worker owns the stream. Do not add an application handshake;
lazy multistream negotiation may complete with the first framed write.

- [ ] **Step 4: Add mixed-mode, replacement, and no-replay integration tests**

Prove fresh/persistent peers never become mutually ready, a reset stream makes
the next distinct envelope open exactly one replacement, and a failed
envelope is not replayed. Then prove two persistent peers deliver sequential
messages over one stream and report one open followed by reuse.

- [ ] **Step 5: Add shutdown tests with active readers and writers**

Block one inbound reader, one active outbound write, and one queued send. Call
`Close` and assert every goroutine exits within a test deadline, queued and
active sends return errors, and repeated `Close` is safe.

- [ ] **Step 6: Run focused race tests repeatedly**

Run:
`cd bloc-node && go test -race ./internal/app -run 'TestPersistent|TestFresh.*Phase|TestLibP2P.*Close' -count=10`

Expected: PASS without goroutine leaks, data races, duplicate writes, or extra
streams.

- [ ] **Step 7: Run all Go tests**

Run:
`cd sbc/hbbft && go test ./... -count=1 && cd ../../bloc-node && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit the integrated transport**

```bash
git add bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_persistent.go bloc-node/internal/app/transport_libp2p_test.go bloc-node/internal/app/node_slot_test.go
git commit -m "feat(transport): reuse prewarmed libp2p streams"
```

### Task 7: Retain trace v2 and stream mode in local evaluator artifacts

**Files:**

- Modify: `bloc-node/internal/app/acs_trace_artifact.go`
- Modify: `bloc-node/internal/app/acs_trace_artifact_test.go`
- Modify: `bloc-node/internal/app/eval_suite.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`
- Modify: `bloc-node/internal/app/eval_remote.go`
- Modify: `bloc-node/internal/app/eval_persistent.go`

**Artifact contract:**

- New runs with ACS tracing declare `acs_trace_schema=bloc-acs-trace/v2`.
- `stream_mode` appears in cluster config, remote evaluator config, suite
  manifest, and retained CSV rows.
- A run fails closed if those values are absent, unsupported, or disagree.
- Historical v1 artifact validation remains available, but matched
  fresh/persistent analysis requires v2 on both sides.

- [ ] **Step 1: Write failing Go artifact tests**

Add v2 fixtures with one populated phase summary. Assert validation succeeds
only when every run and detailed record matches the manifest schema and stream
mode. Retain a v1 fixture whose missing v2 fields remain valid as historical
evidence. Assert the node CSV exposes aggregate totals/maxima for all five
phases plus open/reuse counts.

- [ ] **Step 2: Run focused Go tests and verify RED**

Run:
`cd bloc-node && go test ./internal/app -run 'Test.*ACSTraceArtifact|Test.*StreamMode.*Manifest' -count=1`

Expected: failures because the artifact validator accepts only the current
single schema and does not compare stream mode.

- [ ] **Step 3: Implement dual-version reading and v2 emission**

Add a schema predicate that accepts exactly v1 or v2 when reading. Require v2
phase fields for a v2 manifest, and reject those records if their fixed phase
counts are internally inconsistent with successful send counts. Emit only v2
from newly traced node results. Add aggregate phase total/max and
open/reuse-count columns to node CSV without creating peer-level rows.

- [ ] **Step 4: Add stream-mode agreement tests for remote evaluation**

Assert `eval-suite` passes the selected mode to every generated cluster,
records it in the manifest, and refuses to reuse a config base whose mode
differs. Assert `eval-remote` accepts only an explicit valid mode from its
retained config, normalizes a historical omission to `fresh`, and copies that
exact normalized value into the evaluator manifest; the cloud materializer
agreement check remains Task 10.

- [ ] **Step 5: Run all local evaluator tests**

Run:

```bash
cd bloc-node
go test ./internal/app -run 'Test.*ACSTraceArtifact|Test.*StreamMode|Test.*EvalSuite|Test.*RemoteEval' -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the retained local evidence contract**

```bash
git add bloc-node/internal/app
git commit -m "feat(evaluation): retain persistent-stream experiment provenance"
```

### Task 8: Add matched fresh-versus-persistent attribution analysis

**Files:**

- Create: `latency-charts/src/bloc_latency_charts/transport_attribution.py`
- Modify: `latency-charts/src/bloc_latency_charts/cli.py`
- Modify: `latency-charts/src/bloc_latency_charts/__main__.py`
- Create: `latency-charts/tests/test_transport_attribution.py`

**Interfaces:**

- `TRACE_SCHEMA = "bloc-acs-trace/v2"`
- `MODES = ("fresh", "persistent")`
- `load_matched_transport_campaigns(fresh_root: Path,
  persistent_root: Path)` returns the validated run and message DataFrames.
- `summarize_transport_attribution(runs: pd.DataFrame,
  messages: pd.DataFrame)` returns the ACS and transport-phase summary
  DataFrames.

Expose a CLI that writes:

- `transport-acs-summary.csv`: topology, batch, mode, count, ACS and fixed ACS
  milestone p50/p95/max;
- `transport-phase-summary.csv`: topology, batch, mode, subtype, send and five
  phase p50/p95/max plus open/reuse counts;
- `transport-attribution.json`: provenance checks, deltas, and a bounded
  interpretation classification.

- [ ] **Step 1: Write failing provenance and matching tests**

Build small synthetic v2 artifact roots. Assert the loader rejects different
source SHA, image digest, n, threshold, batch set, corpus identity, seed,
schedule, warm-up/repetition counts, topology, trace schema, or any difference
other than `stream_mode`, command/output paths, timestamps, experiment ID, and
the resulting config-file hash.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:
`cd latency-charts && python -m pytest tests/test_transport_attribution.py -q`

Expected: import failure because the analysis module does not exist.

- [ ] **Step 3: Implement strict v2 loading and summaries**

Reuse stable percentile/statistics helpers from the package. Never load the
historical v1 attribution tables into this comparison. Group by fixed subtype,
not peer or slot. For 30 retained samples compute p50, p95, and max only. Use
the existing distribution-free 95% order-statistic intervals; define
`persistent-better` as its upper bound below the fresh lower bound,
`persistent-worse` as its lower bound above the fresh upper bound, and
`overlap` otherwise.

- [ ] **Step 4: Encode interpretation gates without claiming causality**

Classify each matched cell using these explicit rules:

- `sender-finalization-only`: persistent finalization is
  `persistent-better`, while ACS p50 intervals overlap and ACS p95 is not
  `persistent-better`;
- `queue-regression`: persistent queue wait is non-zero and ACS p50 is
  `persistent-worse`;
- `acs-signal`: ACS p50 is `persistent-better`, ACS p95 point estimates move in
  the same direction, and there are no new failures or consistency errors;
- `null-or-mixed`: no consistent direction.

The JSON must include the underlying values and state that classifications are
experiment outcomes, not proof of a transport root cause. The cross-batch
summary calls a direction stable only when at least two of three batches agree
and the third has no interval-separated opposite result.

- [ ] **Step 5: Add percentile and classification tests**

Use deterministic synthetic distributions for all four outcomes. Assert the
report never emits p99 and rejects a cell with fewer than 30 successful
measurements or any unexpected send failure.

- [ ] **Step 6: Run the complete latency-charts suite**

Run: `cd latency-charts && python -m pytest -q`

Expected: PASS.

- [ ] **Step 7: Commit the matched analyzer**

```bash
git add latency-charts/src/bloc_latency_charts latency-charts/tests/test_transport_attribution.py
git commit -m "feat(analysis): compare fresh and persistent stream phases"
```

### Task 9: Document, verify, and execute the local phase-one gate

**Files:**

- Modify: `bloc-node/README.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Review and modify only if current state changes: `docs/STATUS.md`

- [ ] **Step 1: Update canonical behavior and experiment documentation**

Document:

- v1 fresh and v2 persistent protocol IDs;
- capacity-one per-peer queue and synchronous completion semantics;
- deadline, uncertain-write/no-replay, readiness, and shutdown behavior;
- why no writer mutex or `go-msgio` dependency is used;
- that libp2p peer connections are already persistent and this phase tests
  logical stream reuse rather than reconnect avoidance;
- trace v2 phase meanings, especially fresh finalization versus receiver/ACS
  progress;
- the matched local and later canary acceptance gates.

Record the phase-one choice in `docs/DECISIONS.md` and the implementation in
`docs/CHANGELOG.md`. Update `docs/STATUS.md` only if the active blocker,
accepted evidence, baseline, milestone state, or immediate next action changes.

- [ ] **Step 2: Run formatting and static checks**

Run:

```bash
gofmt -w bloc-node/internal/app/*.go sbc/hbbft/*.go
git diff --check
cd bloc-node && go vet ./...
cd ../sbc/hbbft && go vet ./...
```

Expected: no formatting errors, whitespace errors, or vet findings.

- [ ] **Step 3: Run complete regression and race gates**

Run:

```bash
cd sbc/hbbft && go test ./... -count=1
cd ../../bloc-node && go test ./... -count=1
go test -race ./internal/app -run 'TestPersistent|TestFresh.*Phase|Test.*StreamMode' -count=3
cd ../latency-charts && python -m pytest -q
```

Expected: PASS.

- [ ] **Step 4: Run a short local correctness smoke in both modes**

Use the same source, seed, node count, and batch for both invocations:

```bash
cd bloc-node
go run ./cmd/bloc-node eval-suite --execution-mode persistent --stream-mode fresh --node-counts 4 --batch-sizes 8 --warmups 1 --repetitions 3 --seed 640 --acs-trace --experiment-id phase1-smoke-fresh --out-dir results/phase1-streams/smoke-fresh
go run ./cmd/bloc-node eval-suite --execution-mode persistent --stream-mode persistent --node-counts 4 --batch-sizes 8 --warmups 1 --repetitions 3 --seed 640 --acs-trace --experiment-id phase1-smoke-persistent --out-dir results/phase1-streams/smoke-persistent
```

Require consistent ACS outputs, trace v2, exact retained modes, zero protocol
rejections, and zero unexpected send failures. Treat smoke latency as
non-evidence.

- [ ] **Step 5: Run the retained local matched campaign**

After the smoke passes, run:

```bash
cd bloc-node
go run ./cmd/bloc-node eval-suite --execution-mode persistent --stream-mode fresh --node-counts 4 --batch-sizes 8,32,128 --warmups 5 --repetitions 30 --seed 640 --acs-trace --experiment-id phase1-local-fresh --out-dir results/phase1-streams/local-fresh
go run ./cmd/bloc-node eval-suite --execution-mode persistent --stream-mode persistent --node-counts 4 --batch-sizes 8,32,128 --warmups 5 --repetitions 30 --seed 640 --acs-trace --experiment-id phase1-local-persistent --out-dir results/phase1-streams/local-persistent
cd ../latency-charts
python -m bloc_latency_charts transport-attribution --fresh-root ../bloc-node/results/phase1-streams/local-fresh --persistent-root ../bloc-node/results/phase1-streams/local-persistent --out-dir ../bloc-node/results/phase1-streams/local-comparison
```

Retain the manifests, summaries, and classification. Do not tune queue capacity,
split streams, or move to gossipsub in response to the run; record a null or
negative result as valid evidence.

- [ ] **Step 6: Apply the local decision gate**

- If correctness, resource, or no-replay invariants fail, stop and fix the
  implementation before any performance interpretation.
- If only fresh finalization improves, conclude the prior send metric was
  inflated locally but the fresh-stream mechanism was not shown to drive ACS
  latency; this does not rule out a WAN effect.
- If persistent queue wait worsens ACS, retain fresh mode as the default and
  open a separately scoped control/bulk-stream design task only with approval.
- If ACS p50 and p95 improve consistently without new failures, keep
  persistent mode experimental and prepare separately authorized n=4 same-AZ
  and three-region canaries using the identical artifact contract.
- If the local result is null or mixed but correctness and phase attribution
  are sound, retain that result and allow the same canaries to test the actual
  WAN hypothesis; do not manufacture a local improvement requirement.

- [ ] **Step 7: Update task evidence and current status**

Post the validation commands, commit, local artifact paths, provenance result,
and interpretation to GitHub issue #23. If the local outcome changes accepted
evidence or immediate next actions, update `docs/STATUS.md` in the same commit;
otherwise record that it was reviewed and required no change.

- [ ] **Step 8: Commit documentation and local evidence references**

Do not commit ignored raw result directories. Commit only canonical docs and
small accepted summary references:

```bash
git add bloc-node/README.md docs/modules/bloc-node.md docs/VALIDATION.md docs/DECISIONS.md docs/CHANGELOG.md docs/STATUS.md
git commit -m "docs: record persistent-stream phase-one validation"
```

- [ ] **Step 9: Perform the branch handoff check**

Run:

```bash
git status --short --branch
git fetch origin
git rev-list --left-right --count origin/main...HEAD
git log --oneline --decorate -8
```

Report the final branch, commits, validation results, publication state,
retained divergent work, issue update, and `STATUS.md` review outcome. Do not
start AWS resources from this task.

### Task 10: Prepare the cloud canary contract only after a usable local gate

Execute this task only if Task 9 passes correctness, produces usable phase
attribution without the analyzer's `queue-regression` classification, and the
user wants the same-AZ and three-region canaries prepared. A local `acs-signal`
is helpful but not required because localhost does not exercise the WAN
hypothesis. A safety, resource, no-replay, or `queue-regression` failure ends
phase-one evidence work without this expansion.

**Files:**

- Modify: `scripts/lib/final-campaign-contract.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `deploy/ec2/run-final-campaign.sh`
- Modify: `scripts/lib/campaign_artifacts.py`
- Modify: `scripts/tests/test-final-campaign-contract.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test_campaign_artifacts.py`
- Modify: `deploy/ec2/README.md`
- Review and modify only if immediate actions change: `docs/STATUS.md`

**Cloud artifact contract:**

- `FINAL_STREAM_MODE` defaults to `fresh` and accepts only
  `fresh|persistent`.
- A phase-one canary requires
  `FINAL_ACS_TRACE_SCHEMA=bloc-acs-trace/v2`.
- `stream_mode` must agree across the materialized cluster config, remote
  evaluator config, phase manifest, evaluator manifest, and retained rows.
- Fresh and persistent canaries are separate immutable runs from the same
  source/image/corpus/schedule; the lifecycle never switches mode in place.

- [ ] **Step 1: Write failing shell contract tests**

Exercise `--stream-mode persistent --acs-trace-schema bloc-acs-trace/v2` and
assert the parsed/exported contract retains both values. Add rejection cases
for an unknown mode and for persistent phase-one comparison with v1 tracing.

- [ ] **Step 2: Write failing lifecycle and artifact tests**

Assert materialization receives `--stream-mode`, phase manifests retain it,
and the artifact collector rejects missing mode, cluster/remote disagreement,
phase/evaluator disagreement, v1/v2 mixing, and unsupported schemas.

- [ ] **Step 3: Run the new contract tests and verify RED**

Run:

```bash
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
python -m pytest scripts/tests/test_campaign_artifacts.py -q
```

Expected: the new cases fail because the cloud contract does not yet retain
stream mode or accept trace v2.

- [ ] **Step 4: Propagate mode and schema through the lifecycle**

Parse and export `FINAL_STREAM_MODE`, pass it to
`materialize-campaign-config`, and persist it in phase/evaluator manifests.
Permit trace schema empty, v1, or v2 for historical workflows, while requiring
v2 in the phase-one persistent-stream comparison. Update the Python artifact
validator to compare exact mode/schema values across all retained inputs.

- [ ] **Step 5: Run all cloud contract regressions**

Run:

```bash
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-race-gate-contract.sh
python -m pytest scripts/tests/test_campaign_artifacts.py -q
```

Expected: PASS without allocating or modifying AWS resources.

- [ ] **Step 6: Update the EC2 runbook and current status**

Document two separately authorized n=4 canaries per topology, one fresh and
one persistent, using batches `8,32,128`, 5 warm-ups, 30 measurements, the
same source/image/corpus/seed/schedule, and trace v2. State that p50/p95/max and
ACS milestones are the decision surface. Update `docs/STATUS.md` only if cloud
canaries become the immediate authorized next action.

- [ ] **Step 7: Commit the canary preparation**

```bash
git add scripts/lib scripts/tests deploy/ec2/run-final-campaign.sh deploy/ec2/README.md docs/STATUS.md
git commit -m "feat(campaign): retain libp2p stream mode in canaries"
```

- [ ] **Step 8: Perform the final branch handoff check**

Run:

```bash
git status --short --branch
git fetch origin
git rev-list --left-right --count origin/main...HEAD
git log --oneline --decorate -8
```

Report validation, publication state, issue evidence, retained local results,
and whether `docs/STATUS.md` changed. Do not launch a canary until the user
separately authorizes cloud execution.
