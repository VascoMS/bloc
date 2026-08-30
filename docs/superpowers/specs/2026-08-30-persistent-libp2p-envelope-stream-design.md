# Persistent libp2p Envelope Stream Experiment

## Objective

Test whether reusing one framed libp2p application stream per directed operator
pair reduces the cross-region ACS latency attributed by issue #23 to the
transport and quorum path.

The experiment changes only how existing authenticated `WireEnvelope` values
are carried over the already-persistent libp2p peer connection. It does not
change RBC, BBA, ACS selection, message contents, addressing, sender binding,
thresholds, or retry semantics.

## Evidence And Hypothesis

Issue #23's matched n4 campaigns retained 90 successful and consistent
measurements per topology. ACS p50 increased from `15.908/26.381/59.482 ms` in
the same AZ to `185.691/235.464/500.357 ms` across three regions for batches
`8/32/128`. Adapter work remained small, send failures stayed at zero, and BBA
epoch depth and message counts did not increase. Per-message send maxima rose
to approximately `70--422 ms`, and the first RBC output was especially
payload-sensitive at batch 128.

The current transport opens, negotiates, writes, and closes one logical
`/bloc/envelope/1.0.0` stream for every addressed envelope even though the
underlying peer connection is persistent. In the steady-state go-libp2p path,
protocol selection is lazy: the protocol header and first envelope are sent
without waiting for the selection response. The current sender then closes the
stream and waits for that response before `Send` returns. Because node sends
run in goroutines, this can inflate sender-side send duration without placing
the same full delay on the ACS critical path.

The experiment therefore tests the narrower hypothesis that removing repeated
application-stream control traffic, protocol-selection completion, handler
churn, and mux scheduling reduces transport contention and ACS latency over a
WAN. It must separately report queue, open, write, and finalization time so a
lower `Send` duration is not mistaken for a lower receiver-delivery or ACS
duration.

This is a mechanism hypothesis, not an accepted causal claim. The fresh and
persistent modes must be measured from the same candidate source before the
result is interpreted.

## Selected Approach

Add an opt-in persistent stream mode alongside the existing fresh-stream
baseline.

Each node maintains one outbound application stream to every other configured
operator. A peer pair therefore has two directed streams: one opened by each
peer for its outbound envelopes. The streams use a new protocol ID,
`/bloc/envelope/2.0.0`, and carry repeated unsigned-varint-length-prefixed
protobuf envelopes.

The alternative approaches are deliberately deferred:

1. A priority queue on one persistent stream could reduce delay for selected
   message types, but no current evidence justifies classifying one ACS message
   type as less progress-critical than another.
2. Separate control and bulk streams could isolate large RBC frames, but they
   introduce a second mechanism before a single-stream reuse experiment has
   established whether fresh-stream churn matters.
3. Replacing direct envelopes with gossipsub would also change dissemination,
   routing, and duplication semantics, so it cannot isolate the transport
   mechanism identified by issue #23.

## Configuration And Compatibility

Extend `network` in the shared cluster configuration with
`stream_mode: "fresh" | "persistent"`.

- Omitted `stream_mode` defaults to `fresh`, preserving existing configs and
  campaign behavior.
- Any other value fails configuration validation before transport startup.
- `fresh` continues to use `/bloc/envelope/1.0.0` and one envelope per stream.
- `persistent` uses `/bloc/envelope/2.0.0` and repeated framed envelopes.

The configuration version remains `bloc-cluster-v3` because the field is
optional and its omission preserves the current behavior. Config generators
and materializers expose the mode explicitly so retained campaign inputs show
which transport was exercised.

A persistent-mode node registers only the v2 envelope handler for experiment
traffic. Stream prewarming therefore detects a mixed-mode cluster instead of
silently interpreting v2 framing with v1 EOF semantics.

## Outbound Data Path

The transport owns one writer worker per recipient. Each worker exclusively
owns its current outbound stream and receives immutable encoded envelopes over
a capacity-one channel. The channel is the only application transport queue:
one envelope may be in progress and at most one may wait for that peer.

`Send` keeps its synchronous completion contract:

1. Validate the recipient, overwrite routing fields with authenticated local
   values, protobuf-encode the envelope, and enforce `max_envelope_bytes`.
2. Enqueue one request on the recipient's capacity-one channel. Queue admission
   is bounded by the caller deadline or a ten-second internal send deadline
   when the caller supplies none.
3. The dedicated worker drops an already-cancelled queued request, otherwise
   reuses its current stream or opens a v2 replacement.
4. The worker writes the unsigned-varint encoded length and then the complete
   envelope with the existing short-write-safe behavior. It sets the stream
   write deadline to the request's effective deadline.
5. The worker publishes one buffered completion containing phase timings and
   success or failure. `Send` returns only after that completion; successful
   accounting still means the complete frame was accepted by the transport,
   not acknowledged by the remote application.

Persistent sends record zero per-message finalization time because they do not
half-close or close the reused stream. Worker result publication is local
bookkeeping, not a wire phase.

The worker, rather than a mutex shared by arbitrary caller goroutines,
serializes frame writes. This makes backlog explicit and bounded. Queue wait
remains part of the existing per-message send duration. FIFO order applies only
after concurrent callers have successfully entered the per-peer channel; ACS
continues to tolerate delivery reordering across peers and competing callers.

No priority, batching, compression, delivery acknowledgement, or automatic
retry is added.

## Inbound Data Path

The v2 handler authenticates the remote libp2p peer once when the stream is
accepted, then reads frames until EOF, reset, cancellation, or validation
failure.

For every frame it:

1. decodes a bounded unsigned-varint length;
2. requires the length to be between one and `max_envelope_bytes` before
   allocating the frame buffer;
3. reads exactly that many bytes;
4. protobuf-decodes the existing `WireEnvelope`;
5. applies the existing payload, recipient, direct-addressing, authenticated
   sender, and share-operator checks; and
6. invokes the unchanged `EnvelopeHandler` with the protobuf envelope size.

An overflowing, truncated, oversized, undecodable, unauthenticated, or invalid
frame resets the whole stream. A later outbound send may establish a new
stream, but the invalid frame is never delivered.

## Startup, Readiness, And Shutdown

Persistent mode asynchronously opens one outbound v2 stream to every configured
peer after the underlying libp2p connection exists and Identify reports that
the peer supports v2. Opening uses the same cancelable lifecycle and bounded
per-attempt behavior already used for static peers. The open sends the yamux
stream-control frame before measurement; lazy multistream selection remains
pipelined with the first framed write.

`Ready` requires the underlying connection, advertised v2 support, and a cached
outbound v2 stream for every remote operator. This moves yamux stream creation
before the evaluator's health barrier and detects a mixed-mode cluster without
inventing an application-level handshake. It does not claim that lazy protocol
selection has completed before the first framed write.

Manual starts remain safe if a send races readiness: the recipient worker may
open the absent stream for that request. `Close` stops admission, cancels
prewarming, fails queued requests, interrupts active streams through host
shutdown, and waits for workers and inbound readers to exit.

## Failure Semantics

An open failure returns failure for the current envelope.

On any frame write, deadline, reset, or connection failure, the worker resets
and removes the cached stream and returns failure for that envelope. It does not
retry the envelope because a partial write creates an uncertain-delivery
boundary. The next distinct queued send may open a replacement stream and write
only its own envelope.

This preserves the current fail-closed accounting rule: bytes and successful
delivery are recorded only after the complete transport write succeeds. It
does not add delivery acknowledgements or strengthen libp2p's transport write
into application-level receipt confirmation.

Initial opens and replacement opens emit bounded structured log events with
the local node, remote operator, stream mode, and reason (`prewarm` or
`replacement`). Resets emit the corresponding bounded reason. The opt-in ACS
diagnostic schema is versioned to `bloc-acs-trace/v2` and adds fixed per-subtype
totals/maxima for encode, queue wait, stream open, frame write, and per-message
finalization plus open/reuse counts. It adds no peer, slot, stream, or epoch
labels and no high-cardinality Prometheus surface.

## Safety And Resource Invariants

- The configured libp2p peer ID remains the authenticated operator identity.
- `From`, `To`, and `Direct` remain transport-owned outbound fields.
- Every frame independently enforces the existing envelope and payload bounds.
- Only the recipient's worker writes its directed persistent stream, preventing
  byte interleaving without a writer mutex.
- There is no unbounded transport queue. The capacity-one channel admits at
  most one waiting envelope per peer, and its wait is visible in send duration.
- A request that cannot enter the queue before its effective deadline fails
  without writing any bytes.
- No failed or uncertain write is silently replayed.
- A malformed peer loses the current stream rather than allowing decoder state
  to continue after an invalid frame.
- Fresh mode remains the compatibility and experiment baseline.

## Implementation Boundary

The expected implementation surface is:

- `bloc-node/internal/app/types.go` and configuration validation/generators for
  `network.stream_mode`;
- `bloc-node/internal/app/transport.go` for bounded per-send phase results;
- focused libp2p transport files for v2 framing, the capacity-one directed
  writer, prewarming, readiness, replacement, and shutdown;
- the bounded ACS trace/evaluator surface for `bloc-acs-trace/v2` transport
  phase aggregation and matched-mode analysis;
- colocated configuration, framing, transport, authentication, resource, and
  lifecycle tests;
- `bloc-node/README.md`, `docs/modules/bloc-node.md`, `docs/VALIDATION.md`,
  `docs/DECISIONS.md`, and `docs/CHANGELOG.md` after behavior is implemented;
- the relevant local and EC2 generator/runbook surfaces only when required to
  select and retain the experiment mode.

No RBC/BBA/ACS transition, protobuf message schema, cryptographic code, mempool
behavior, Terraform topology, or node-count configuration change is in scope.
The only `sbc/hbbft` change is the bounded diagnostic value/aggregation surface.

## Validation Strategy

Implementation begins with failing tests for:

- omitted mode defaulting to `fresh`, accepted explicit modes, and rejection of
  unknown modes;
- multiple valid envelopes delivered over one v2 stream;
- concurrent sends producing complete, non-interleaved frames;
- capacity-one backpressure, deadline expiry before admission, and cancellation
  after admission without goroutine or stream leaks;
- zero, oversized, overflowed, and truncated frame rejection before unsafe
  allocation or delivery;
- authenticated-peer and per-envelope validation on a persistent stream;
- one prewarmed outbound stream per remote peer and readiness staying false
  until all are established;
- write failure resetting the stream, reporting the current send as failed,
  and allowing a later distinct send to open a replacement without replay;
- clean transport shutdown with active persistent readers and writers; and
- unchanged v1 fresh-stream wire behavior; and
- phase accounting proving fresh finalization is separated from receiver-facing
  write time and persistent queue wait is not hidden.

Run `cd bloc-node && go test ./...` and focused transport/configuration race
coverage after the targeted tests pass. Run the existing local n4 protocol
evaluation in both modes from the same source and require consistent ACS output
with zero unexpected send failures or protocol rejections.

The first performance comparison uses n4, batches `8/32/128`, the same corpus,
identity material, schedule, diagnostics, and evaluator settings in both modes.
It reports p50/p95 only. It compares ACS milestones as well as send phase
totals/maxima; a reduction confined to finalization/send-return time is not an
ACS improvement. All retained provenance must match except the explicit
`network.stream_mode` and its derived public-config identity.

Only after local correctness and a usable latency signal may the same matched
fresh/persistent comparison proceed to same-AZ and three-region canaries. Cloud
execution remains separately authorized. No improvement threshold is required
for correctness; a null or negative result rejects the stream-reuse hypothesis
and is retained as evidence rather than prompting unmeasured tuning.

## Acceptance Criteria

- Fresh mode remains byte- and behavior-compatible with the current transport.
- Persistent mode negotiates at most one active outbound v2 stream per directed
  peer during healthy operation and carries multiple validated envelopes on it.
- Initial yamux stream creation and v2 support detection complete before
  readiness; lazy protocol selection remains safe on the first framed write.
- Each directed peer has one writer, one in-progress frame, and at most one
  queued frame.
- Complete writes preserve existing success accounting; failed or uncertain
  writes are not replayed.
- Existing authentication and resource bounds apply independently to every
  frame.
- Targeted tests, the complete `bloc-node` suite, focused races, and local n4
  consistency validation pass in both modes.
- The matched comparison can attribute its only intended configuration change
  to `network.stream_mode`.
- Diagnostic output separates encode, queue, open, write, and finalization time
  and does not equate sender completion with receiver delivery.
- Results determine whether to retain the mode, reject it, or design a measured
  follow-up for writer contention or large-frame interference.

## Non-Goals

- Declaring the persistent mode production-ready.
- Prioritizing BVAL, AUX, READY, PROOF, ECHO, or share messages.
- Adding separate control and bulk streams.
- Changing broadcast topology, quorum thresholds, ACS state, or message counts.
- Adding acknowledgements, retries, delivery deduplication, compression, or
  envelope batching.
- Replacing direct messaging with pubsub.
- Expanding the first experiment to n7 or publishing p99 from 30 observations.

## External Precedents

- The [libp2p connection
  specification](https://github.com/libp2p/specs/blob/master/connections/README.md)
  defines logically independent, backpressured streams over a persistent secure
  peer connection and negotiates an application protocol when each new stream
  is opened.
- The [libp2p pubsub stream-management
  specification](https://github.com/libp2p/specs/blob/master/pubsub/README.md)
  uses separately negotiated inbound and outbound streams for continuous peer
  messaging.
- The maintained [go-libp2p-pubsub writer
  path](https://github.com/libp2p/go-libp2p-pubsub/blob/master/comm.go) writes
  repeated length-delimited protobuf messages to a retained outbound stream and
  resets the stream after write failure.
- [Substrate's libp2p notification
  protocols](https://paritytech.github.io/polkadot-sdk/master/sc_network/index.html)
  use framed directed substreams for ongoing notifications while retaining
  separate one-stream-per-operation request/response protocols.

These precedents support the framing and lifetime mechanics. They do not prove
that stream reuse will improve BLOC's latency; the matched experiment provides
that evidence.
