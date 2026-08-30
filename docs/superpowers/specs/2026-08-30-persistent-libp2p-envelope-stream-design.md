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
underlying peer connection is persistent. The experiment tests the narrower
hypothesis that removing repeated application-stream setup and close work
reduces send duration and ACS latency over a WAN.

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

The transport owns one small writer state per recipient: a mutex and the
currently usable outbound stream.

`Send` keeps its synchronous completion contract:

1. Validate the recipient, overwrite routing fields with authenticated local
   values, protobuf-encode the envelope, and enforce `max_envelope_bytes`.
2. Lock the recipient's writer state. This serializes frames without adding a
   transport queue or message priority.
3. Reuse the cached stream, or open a v2 stream if none is usable.
4. Write the unsigned-varint encoded length and then the complete envelope with
   the existing short-write-safe behavior.
5. Unlock only after the write succeeds or fails, and return the encoded
   envelope byte count only on success.

Because callers already execute sends concurrently, time waiting for the
per-peer writer lock remains inside the existing per-message send duration.
That makes any serialization cost visible rather than hiding it behind an
asynchronous enqueue result. The lock provides serialization but makes no new
cross-message ordering guarantee; ACS already accepts reordered delivery.

No new queue, batching, priority, compression, or automatic retry is added.

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
peer after the underlying libp2p connection exists. Opening uses the same
cancelable lifecycle and bounded per-attempt behavior already used for static
peers.

`Ready` requires both the underlying connection and a cached outbound v2 stream
for every remote operator. This moves initial stream negotiation before the
evaluator's health barrier so the first measured ACS message does not pay the
setup cost being removed by the experiment.

Manual starts remain safe if a send races readiness: `Send` may open the absent
stream while holding that peer's writer lock. `Close` stops prewarming, closes
cached streams, and then closes the libp2p host.

## Failure Semantics

An open failure returns failure for the current envelope.

On any frame write or close-related failure, the sender resets and removes the
cached stream and returns failure for that envelope. It does not retry the
envelope because a partial write creates an uncertain-delivery boundary. The
next distinct send may open a replacement stream and write its own envelope.

This preserves the current fail-closed accounting rule: bytes and successful
delivery are recorded only after the complete transport write succeeds. It
does not add delivery acknowledgements or strengthen libp2p's transport write
into application-level receipt confirmation.

Initial opens and replacement opens emit bounded structured log events with
the local node, remote operator, stream mode, and reason (`prewarm` or
`replacement`). Resets emit the corresponding bounded reason. Existing ACS
send duration, byte, and failure diagnostics remain the primary comparison
metrics; this first iteration does not add a high-cardinality metric surface.

## Safety And Resource Invariants

- The configured libp2p peer ID remains the authenticated operator identity.
- `From`, `To`, and `Direct` remain transport-owned outbound fields.
- Every frame independently enforces the existing envelope and payload bounds.
- Only one writer can use a directed persistent stream at a time, preventing
  byte interleaving.
- There is no unbounded transport queue. Existing protocol sends may wait on
  the per-peer writer lock, and that wait is visible in send duration.
- No failed or uncertain write is silently replayed.
- A malformed peer loses the current stream rather than allowing decoder state
  to continue after an invalid frame.
- Fresh mode remains the compatibility and experiment baseline.

## Implementation Boundary

The expected implementation surface is:

- `bloc-node/internal/app/types.go` and configuration validation/generators for
  `network.stream_mode`;
- `bloc-node/internal/app/transport_libp2p.go` for v2 framing, directed stream
  state, prewarming, readiness, replacement, and shutdown;
- colocated configuration, framing, transport, authentication, resource, and
  lifecycle tests;
- `bloc-node/README.md`, `docs/modules/bloc-node.md`, `docs/VALIDATION.md`,
  `docs/DECISIONS.md`, and `docs/CHANGELOG.md` after behavior is implemented;
- the relevant local and EC2 generator/runbook surfaces only when required to
  select and retain the experiment mode.

No `sbc/hbbft` source, protobuf message schema, ACS trace schema, cryptographic
code, mempool behavior, Terraform topology, or node-count configuration changes
are in scope.

## Validation Strategy

Implementation begins with failing tests for:

- omitted mode defaulting to `fresh`, accepted explicit modes, and rejection of
  unknown modes;
- multiple valid envelopes delivered over one v2 stream;
- concurrent sends producing complete, non-interleaved frames;
- zero, oversized, overflowed, and truncated frame rejection before unsafe
  allocation or delivery;
- authenticated-peer and per-envelope validation on a persistent stream;
- one prewarmed outbound stream per remote peer and readiness staying false
  until all are established;
- write failure resetting the stream, reporting the current send as failed,
  and allowing a later distinct send to open a replacement without replay;
- clean transport shutdown with active persistent readers and writers; and
- unchanged v1 fresh-stream behavior.

Run `cd bloc-node && go test ./...` and focused transport/configuration race
coverage after the targeted tests pass. Run the existing local n4 protocol
evaluation in both modes from the same source and require consistent ACS output
with zero unexpected send failures or protocol rejections.

The first performance comparison uses n4, batches `8/32/128`, the same corpus,
identity material, schedule, diagnostics, and evaluator settings in both modes.
It reports p50/p95 only. All retained provenance must match except the explicit
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
- Initial persistent stream negotiation completes before readiness.
- Complete writes preserve existing success accounting; failed or uncertain
  writes are not replayed.
- Existing authentication and resource bounds apply independently to every
  frame.
- Targeted tests, the complete `bloc-node` suite, focused races, and local n4
  consistency validation pass in both modes.
- The matched comparison can attribute its only intended configuration change
  to `network.stream_mode`.
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
