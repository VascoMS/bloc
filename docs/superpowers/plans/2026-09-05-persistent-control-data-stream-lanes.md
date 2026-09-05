# Persistent Control/Data Stream Lanes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an experimental `persistent-lanes` transport mode with independent per-peer control and data streams so READY/BVAL/AUX messages do not wait in the application FIFO behind PROOF/ECHO/share frames.

**Architecture:** Reuse the existing capacity-one `peerStreamWriter` unchanged, but instantiate two writers per remote peer and bind each to a distinct libp2p protocol ID. A single fail-closed classifier routes outbound envelopes and enforces the lane on inbound authenticated frames; the existing `fresh` and `persistent` implementations remain byte-for-byte compatible controls.

**Tech Stack:** Go 1.x, go-libp2p streams and multistream protocol negotiation, existing length-prefixed protobuf `WireEnvelope`, `peerStreamWriter`, Go race detector, local `eval-suite` with finalized `bloc-acs-trace/v3`.

**Spec:** `docs/superpowers/specs/2026-09-02-rbc-ready-stream-lanes-design.md`

## Global Constraints

- Begin from the integrated `codex/acs-trace-finalization` result, create a repository issue named `Add persistent ACS control and data stream lanes`, add it to the BLOC Thesis Prototype project, assign milestone `M5. Performance, Scaling, And Resource Evidence`, and set Project fields `Roadmap target=M5`, `Status=In progress`, `Priority=High`, and `Area=Networking`.
- Execute in a fresh worktree created with `superpowers:using-git-worktrees` on branch `codex/persistent-stream-lanes`.
- Preserve the user's uncommitted `papers/ACS_Improvement.pdf`; never stage or modify it.
- Add exactly one mode string: `persistent-lanes`. Omitted mode still normalizes to `fresh`; existing `fresh` and `persistent` protocol IDs and behavior do not change.
- Use `/bloc/envelope/3.0.0/control` for READY/BVAL/AUX and `/bloc/envelope/3.0.0/data` for PROOF/ECHO/share.
- Do not change protobuf schemas, envelope bytes, recipients, message counts, RBC/BBA thresholds, send deadlines, queue capacity, or uncertain-delivery/no-replay semantics.
- Each peer gets two independent `peerStreamWriter` instances, queues, worker goroutines, streams, readiness flags, reset/reopen lifecycles, and prewarm operations.
- Invalid or unclassifiable outbound envelopes fail; authenticated inbound wrong-lane envelopes increment bounded rejection reason `lane` and reset only the offending stream.
- Preserve FIFO within a lane and make no cross-lane ordering claim. READY-before-ECHO remains valid through the RBC retry logic covered by the READY plan.
- Both lane protocols and both prewarmed writers are required for readiness. A failure resets only its lane; shutdown waits for both writers and all inbound handlers.
- The mode is experimental until a separately authorized matched topology campaign meets the design spec's adoption criteria. This plan performs no cloud operation.
- End by checking branch/status/divergence, posting local validation evidence to the issue, and reporting the `docs/STATUS.md` review outcome.

---

### Task 1: Add the mode and a single fail-closed lane classifier

**Files:**
- Modify: `bloc-node/internal/app/types.go:15-31`
- Modify: `bloc-node/internal/app/config.go:216-235`
- Modify: `bloc-node/internal/app/commands.go:20-79`
- Modify: `bloc-node/internal/app/ec2_config.go:96-153`
- Create: `bloc-node/internal/app/transport_libp2p_lanes.go`
- Create: `bloc-node/internal/app/transport_libp2p_lanes_test.go`
- Modify: `bloc-node/internal/app/main_test.go:54-81`
- Modify: `bloc-node/internal/app/config_security_test.go:213-245`
- Modify: `bloc-node/internal/app/deployment_test.go:308-319`
- Modify: `bloc-node/internal/app/campaign_materialize.go:183-203`
- Modify: `bloc-node/internal/app/campaign_materialize_test.go:59-96`
- Modify: `bloc-node/internal/app/eval_suite.go:411-493`
- Modify: `bloc-node/internal/app/eval_suite_test.go:101-119`

**Interfaces:**
- Consumes: `validateEnvelopePayload(WireEnvelope) error` and `classifyACSMessage(*hbbft.SlotMessage) (hbbft.ACSMessageSubtype, error)`.
- Produces: `streamModePersistentLanes`, `persistentStreamLane`, `persistentLaneControl`, `persistentLaneData`, `persistentLaneProtocol(persistentStreamLane) (protocol.ID, error)`, and `classifyEnvelopeLane(WireEnvelope) (persistentStreamLane, error)`.

- [ ] **Step 1: Write the failing table-driven classifier test**

Create `bloc-node/internal/app/transport_libp2p_lanes_test.go` with:

```go
package app

import (
	"testing"

	"github.com/anthdm/hbbft"
)

func TestClassifyEnvelopeLane(t *testing.T) {
	tests := []struct {
		name string
		env  WireEnvelope
		want persistentStreamLane
	}{
		{name: "proof", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ProofRequest{}}), want: persistentLaneData},
		{name: "echo", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.EchoRequest{}}), want: persistentLaneData},
		{name: "ready", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{}}), want: persistentLaneControl},
		{name: "bval", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.BvalRequest{}}), want: persistentLaneControl},
		{name: "aux", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.AuxRequest{}}), want: persistentLaneControl},
		{name: "share", env: WireEnvelope{Kind: "share", Share: &WireShare{}}, want: persistentLaneData},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyEnvelopeLane(test.env)
			if err != nil || got != test.want {
				t.Fatalf("lane = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestClassifyEnvelopeLaneRejectsInvalidEnvelope(t *testing.T) {
	for _, env := range []WireEnvelope{
		{},
		{Kind: "acs"},
		{Kind: "share"},
		{Kind: "acs", ACS: &hbbft.SlotMessage{}},
	} {
		if lane, err := classifyEnvelopeLane(env); err == nil {
			t.Fatalf("invalid envelope classified as %q: %+v", lane, env)
		}
	}
}

func laneACS(payload any) WireEnvelope {
	return WireEnvelope{Kind: "acs", ACS: slotACSMessage(payload)}
}
```

- [ ] **Step 2: Extend configuration tests before adding the constant**

In `TestEvalSuiteStreamModeDefaultsAndOverrides`, add:

```go
	lanes, err := parseEvalSuiteOptions([]string{"--stream-mode", "persistent-lanes"})
	if err != nil {
		t.Fatal(err)
	}
	if lanes.StreamMode != streamModePersistentLanes {
		t.Fatalf("lane stream mode = %q, want persistent-lanes", lanes.StreamMode)
	}
```

Convert `TestGenConfigRetainsRequestedStreamMode` to run its current generation
and read-back assertions for both `streamModePersistent` and
`streamModePersistentLanes`, passing the table value after `--stream-mode`.
In `TestMaterializeCampaignConfigEnablesACSTraceOnlyWhenRequested`, set
`options.StreamMode = streamModePersistentLanes` and require both generated
configs to preserve exactly `streamModePersistentLanes`. Keep the unknown
`reuse` failure unchanged.

Extend `TestValidateNetworkConfigAcceptsSupportedStreamModes` to iterate over
`streamModeFresh`, `streamModePersistent`, and `streamModePersistentLanes`.
Extend `TestParseEC2ConfigStreamModeDefaultsAndRejectsUnknown` with:

```go
	lanes, err := parseEC2ConfigOptions([]string{"--stream-mode", "persistent-lanes"})
	if err != nil {
		t.Fatal(err)
	}
	if lanes.StreamMode != streamModePersistentLanes {
		t.Fatalf("EC2 lane stream mode = %q", lanes.StreamMode)
	}
```

- [ ] **Step 3: Run the focused tests and verify missing mode/types**

Run:

```bash
cd bloc-node
go test ./internal/app -run 'Test(ClassifyEnvelopeLane|EvalSuiteStreamMode|GenConfigRetainsRequestedStreamMode|MaterializeCampaignConfigEnables)' -count=1
```

Expected: compilation fails because the lane types, classifier, and `streamModePersistentLanes` do not exist.

- [ ] **Step 4: Add the mode and exact protocol/lane types**

Extend the constants in `types.go`:

```go
	streamModeFresh           = "fresh"
	streamModePersistent      = "persistent"
	streamModePersistentLanes = "persistent-lanes"
```

Change `validateNetworkConfig` to accept all three constants and return:

```go
return fmt.Errorf("network.stream_mode must be %q, %q, or %q, got %q",
	streamModeFresh, streamModePersistent, streamModePersistentLanes, network.StreamMode)
```

Update all four CLI help strings in `commands.go`, `campaign_materialize.go`,
`ec2_config.go`, and `eval_suite.go` to
`libp2p envelope streams: fresh, persistent, or persistent-lanes`.

- [ ] **Step 5: Implement the classifier without changing encoding**

Create the non-test portion of `transport_libp2p_lanes.go`:

```go
package app

import (
	"fmt"

	"github.com/anthdm/hbbft"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
)

type persistentStreamLane string

const (
	persistentLaneControl persistentStreamLane = "control"
	persistentLaneData    persistentStreamLane = "data"

	blocEnvelopeProtocolControl = protocol.ID("/bloc/envelope/3.0.0/control")
	blocEnvelopeProtocolData    = protocol.ID("/bloc/envelope/3.0.0/data")
)

var persistentStreamLanes = [...]persistentStreamLane{
	persistentLaneControl,
	persistentLaneData,
}

func persistentLaneProtocol(lane persistentStreamLane) (protocol.ID, error) {
	switch lane {
	case persistentLaneControl:
		return blocEnvelopeProtocolControl, nil
	case persistentLaneData:
		return blocEnvelopeProtocolData, nil
	default:
		return "", fmt.Errorf("unknown persistent stream lane %q", lane)
	}
}

func classifyEnvelopeLane(env WireEnvelope) (persistentStreamLane, error) {
	if err := validateEnvelopePayload(env); err != nil {
		return "", err
	}
	if env.Kind == "share" {
		return persistentLaneData, nil
	}
	subtype, err := classifyACSMessage(env.ACS)
	if err != nil {
		return "", err
	}
	switch subtype {
	case hbbft.ACSMessageReady, hbbft.ACSMessageBVAL, hbbft.ACSMessageAUX:
		return persistentLaneControl, nil
	case hbbft.ACSMessageProof, hbbft.ACSMessageEcho:
		return persistentLaneData, nil
	default:
		return "", fmt.Errorf("unmapped ACS message subtype %q", subtype)
	}
}
```

- [ ] **Step 6: Format, test, and commit configuration/classification**

Run:

```bash
gofmt -w bloc-node/internal/app/types.go bloc-node/internal/app/config.go bloc-node/internal/app/commands.go bloc-node/internal/app/ec2_config.go bloc-node/internal/app/transport_libp2p_lanes.go bloc-node/internal/app/transport_libp2p_lanes_test.go bloc-node/internal/app/main_test.go bloc-node/internal/app/config_security_test.go bloc-node/internal/app/deployment_test.go bloc-node/internal/app/campaign_materialize.go bloc-node/internal/app/campaign_materialize_test.go bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_suite_test.go
cd bloc-node
go test ./internal/app -run 'Test(ClassifyEnvelopeLane|ValidateNetworkConfig|EvalSuiteStreamMode|GenConfigRetainsRequestedStreamMode|ReadConfigRejectsUnknownStreamMode|ParseEC2ConfigStreamMode|MaterializeCampaignConfigEnables)' -count=1
```

Expected: PASS; fresh remains the default and `reuse` remains invalid.

```bash
git add bloc-node/internal/app/types.go bloc-node/internal/app/config.go bloc-node/internal/app/commands.go bloc-node/internal/app/ec2_config.go bloc-node/internal/app/transport_libp2p_lanes.go bloc-node/internal/app/transport_libp2p_lanes_test.go bloc-node/internal/app/main_test.go bloc-node/internal/app/config_security_test.go bloc-node/internal/app/deployment_test.go bloc-node/internal/app/campaign_materialize.go bloc-node/internal/app/campaign_materialize_test.go bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_suite_test.go
git commit -m "feat(config): add persistent stream lane mode"
```

### Task 2: Build two independent writers and route outbound traffic

**Files:**
- Modify: `bloc-node/internal/app/transport_libp2p_lanes.go`
- Modify: `bloc-node/internal/app/transport_libp2p.go:23-50,60-67,283-324,351-378,448-543`
- Test: `bloc-node/internal/app/transport_libp2p_lanes_test.go`
- Test: `bloc-node/internal/app/transport_libp2p_persistent_test.go:646-888`

**Interfaces:**
- Consumes: `newPeerStreamWriter(uint64, persistentStreamOpener, <-chan struct{}) *peerStreamWriter`, `peerStreamWriter.send`, `peerStreamWriter.prewarmStream`, and lane classification from Task 1.
- Produces: `peerStreamLaneWriters`, `newPeerStreamLaneWriters`, `peerStreamLaneWriters.writer`, `LibP2PTransport.persistentLaneWriters`, `startPersistentLaneWriters`, and lane-specific open/prewarm functions.

- [ ] **Step 1: Write the failing application head-of-line isolation test**

Expand the test file imports to `bytes`, `context`, `errors`, `sync`, `testing`,
`time`, `github.com/anthdm/hbbft`, and
`github.com/libp2p/go-libp2p/core/protocol`. Add:

```go
func TestPersistentLaneControlBypassesBlockedDataWriter(t *testing.T) {
	stop := make(chan struct{})
	dataStream := newBlockingPersistentStream()
	controlStream := &capturePersistentStream{}
	var releaseOnce sync.Once
	releaseData := func() { releaseOnce.Do(func() { close(dataStream.writeRelease) }) }
	writers := newPeerStreamLaneWriters(1, func(_ context.Context, _ uint64, lane persistentStreamLane) (persistentWriteStream, error) {
		if lane == persistentLaneData {
			return dataStream, nil
		}
		return controlStream, nil
	}, stop)
	t.Cleanup(func() {
		close(stop)
		releaseData()
		<-writers.data.done
		<-writers.control.done
	})

	firstData := sendPersistentAsync(writers.data, context.Background(), []byte("large-data-1"))
	<-dataStream.writeStarted
	secondData := sendPersistentAsync(writers.data, context.Background(), []byte("large-data-2"))
	waitForPersistentQueueLength(t, writers.data, 1)
	control := sendPersistentAsync(writers.control, context.Background(), []byte("ready"))

	select {
	case completion := <-control:
		if completion.err != nil || completion.result.EncodedBytes != len("ready") {
			t.Fatalf("control completion = %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("control send waited for blocked data lane")
	}
	select {
	case <-firstData:
		t.Fatal("blocked data send completed before release")
	default:
	}
	releaseData()
	assertPersistentSendSuccess(t, <-firstData, len("large-data-1"), false)
	assertPersistentSendSuccess(t, <-secondData, len("large-data-2"), true)
}
```

- [ ] **Step 2: Add a failing per-lane reset isolation test**

Add:

```go
func TestPersistentLaneResetDoesNotResetOrReplayOtherLane(t *testing.T) {
	stop := make(chan struct{})
	wantErr := errors.New("uncertain control write")
	failedControl := &errorAfterWriteStream{writeErr: wantErr}
	replacementControl := &capturePersistentStream{}
	dataStream := &capturePersistentStream{}
	controlOpens := 0
	writers := newPeerStreamLaneWriters(1, func(_ context.Context, _ uint64, lane persistentStreamLane) (persistentWriteStream, error) {
		if lane == persistentLaneData {
			return dataStream, nil
		}
		controlOpens++
		if controlOpens == 1 {
			return failedControl, nil
		}
		return replacementControl, nil
	}, stop)
	t.Cleanup(func() {
		close(stop)
		<-writers.data.done
		<-writers.control.done
	})

	failedPayload := []byte("ready-one")
	if _, err := writers.control.send(context.Background(), failedPayload); !errors.Is(err, wantErr) {
		t.Fatalf("control failure = %v, want %v", err, wantErr)
	}
	if !failedControl.reset {
		t.Fatal("failed control stream was not reset")
	}
	dataFirst, err := writers.data.send(context.Background(), []byte("proof"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: dataFirst}, len("proof"), false)
	dataSecond, err := writers.data.send(context.Background(), []byte("echo"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: dataSecond}, len("echo"), true)
	if dataStream.reset {
		t.Fatal("control failure reset the data stream")
	}

	next, err := writers.control.send(context.Background(), []byte("ready-two"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: next}, len("ready-two"), false)
	if controlOpens != 2 || bytes.Contains(replacementControl.bytes(), failedPayload) {
		t.Fatalf("control replacement opens=%d replay=%t", controlOpens, bytes.Contains(replacementControl.bytes(), failedPayload))
	}
}
```

This test reuses the existing fake stream types and does not change
`peerStreamWriter`.

- [ ] **Step 3: Run the writer tests and verify the lane grouping is missing**

Run: `cd bloc-node && go test ./internal/app -run 'TestPersistentLane(ControlBypasses|Reset)' -count=1`

Expected: compilation fails on `newPeerStreamLaneWriters` and its fields.

- [ ] **Step 4: Implement the writer grouping**

Add to `transport_libp2p_lanes.go`:

```go
type persistentLaneStreamOpener func(context.Context, uint64, persistentStreamLane) (persistentWriteStream, error)

type peerStreamLaneWriters struct {
	control *peerStreamWriter
	data    *peerStreamWriter
}

func newPeerStreamLaneWriters(operatorID uint64, open persistentLaneStreamOpener, stop <-chan struct{}) *peerStreamLaneWriters {
	newWriter := func(lane persistentStreamLane) *peerStreamWriter {
		return newPeerStreamWriter(operatorID, func(ctx context.Context, to uint64) (persistentWriteStream, error) {
			return open(ctx, to, lane)
		}, stop)
	}
	return &peerStreamLaneWriters{
		control: newWriter(persistentLaneControl),
		data:    newWriter(persistentLaneData),
	}
}

func (w *peerStreamLaneWriters) writer(lane persistentStreamLane) (*peerStreamWriter, error) {
	if w == nil {
		return nil, fmt.Errorf("persistent lane writers are nil")
	}
	switch lane {
	case persistentLaneControl:
		return w.control, nil
	case persistentLaneData:
		return w.data, nil
	default:
		return nil, fmt.Errorf("unknown persistent stream lane %q", lane)
	}
}
```

Add the required `context` import. Do not modify `peerStreamWriter` or its queue capacity.

- [ ] **Step 5: Add lane writer ownership to the transport**

Initialize `persistentLaneWriters map[uint64]*peerStreamLaneWriters` alongside
the existing `persistentWriters`. Add:

```go
func (t *LibP2PTransport) startPersistentLaneWriters(ctx context.Context) {
	for operatorID := range t.node.peers {
		if operatorID == t.node.self.ID {
			continue
		}
		writers := newPeerStreamLaneWriters(operatorID, t.openPersistentLaneOutboundStream, t.persistentStop)
		writers.control.prewarmOpen = func(ctx context.Context, to uint64) (persistentWriteStream, error) {
			return t.openPersistentLaneOutboundStreamWithoutDial(ctx, to, persistentLaneControl)
		}
		writers.data.prewarmOpen = func(ctx context.Context, to uint64) (persistentWriteStream, error) {
			return t.openPersistentLaneOutboundStreamWithoutDial(ctx, to, persistentLaneData)
		}
		t.persistentLaneWriters[operatorID] = writers
		for _, lane := range persistentStreamLanes {
			writer, err := writers.writer(lane)
			if err != nil {
				panic(err)
			}
			protocolID, err := persistentLaneProtocol(lane)
			if err != nil {
				panic(err)
			}
			t.prewarmWG.Add(1)
			go func(operatorID uint64, writer *peerStreamWriter, protocolID protocol.ID, lane persistentStreamLane) {
				defer t.prewarmWG.Done()
				t.prewarmPersistentWriter(ctx, operatorID, writer, protocolID, string(lane))
			}(operatorID, writer, protocolID, lane)
		}
	}
}
```

Refactor the existing prewarm loop into this exact shared signature:

```go
func (t *LibP2PTransport) prewarmPersistentWriter(
	ctx context.Context,
	operatorID uint64,
	writer *peerStreamWriter,
	protocolID protocol.ID,
	lane string,
)
```

The existing v2 call passes `blocEnvelopeProtocolPersistent, "single"`; lane calls pass their v3 protocol and `string(lane)`. The ready log becomes:

```go
log.Printf("event=libp2p_persistent_stream_ready node_id=%d peer_id=%d lane=%s", t.node.self.ID, operatorID, lane)
```

- [ ] **Step 6: Open the exact lane protocol and route `Send`**

Add:

```go
func (t *LibP2PTransport) openPersistentLaneOutboundStream(ctx context.Context, to uint64, lane persistentStreamLane) (persistentWriteStream, error) {
	return t.openPersistentLaneOutboundStreamWithContext(ctx, to, lane)
}

func (t *LibP2PTransport) openPersistentLaneOutboundStreamWithoutDial(ctx context.Context, to uint64, lane persistentStreamLane) (persistentWriteStream, error) {
	return t.openPersistentLaneOutboundStreamWithContext(network.WithNoDial(ctx, "bloc persistent lane prewarm"), to, lane)
}

func (t *LibP2PTransport) openPersistentLaneOutboundStreamWithContext(ctx context.Context, to uint64, lane persistentStreamLane) (persistentWriteStream, error) {
	config, ok := t.node.peers[to]
	if !ok {
		return nil, fmt.Errorf("unknown peer %d", to)
	}
	peerID, err := peer.Decode(config.P2PPeerID)
	if err != nil {
		return nil, err
	}
	protocolID, err := persistentLaneProtocol(lane)
	if err != nil {
		return nil, err
	}
	if t.host == nil {
		return nil, errors.New("libp2p transport is not started")
	}
	return t.host.NewStream(ctx, peerID, protocolID)
}
```

In `Send`, immediately after `authenticatedOutboundEnvelope` and before
encoding, add:

```go
	var lane persistentStreamLane
	if t.node.cfg.Network.StreamMode == streamModePersistentLanes {
		lane, err = classifyEnvelopeLane(env)
		if err != nil {
			t.node.recordProtocolRejected("outbound", "payload")
			return result, err
		}
	}
```

Replace the current two-way writer selection with:

```go
	switch t.node.cfg.Network.StreamMode {
	case streamModePersistent:
		writer, ok := t.persistentWriters[to]
		if !ok {
			return result, fmt.Errorf("persistent stream writer for peer %d is not running", to)
		}
		streamResult, err = writer.send(ctx, data)
	case streamModePersistentLanes:
		writers, ok := t.persistentLaneWriters[to]
		if !ok {
			return result, fmt.Errorf("persistent lane writers for peer %d are not running", to)
		}
		writer, laneErr := writers.writer(lane)
		if laneErr != nil {
			return result, laneErr
		}
		streamResult, err = writer.send(ctx, data)
	default:
		streamResult, err = t.sendStream(ctx, to, data)
	}
```

Do not encode a second time and keep the existing phase-result copy code.

- [ ] **Step 7: Require both writers for readiness and shutdown**

Add this branch inside the per-peer loop in `Ready`:

```go
		if t.node.cfg.Network.StreamMode == streamModePersistentLanes {
			supported, err := t.host.Peerstore().SupportsProtocols(
				id, blocEnvelopeProtocolControl, blocEnvelopeProtocolData)
			if err != nil || len(supported) != len(persistentStreamLanes) {
				return false
			}
			writers, ok := t.persistentLaneWriters[cfg.ID]
			if !ok || writers.control == nil || writers.data == nil ||
				!writers.control.ready.Load() || !writers.data.ready.Load() {
				return false
			}
		}
```

In `Close`, wait for both workers:

```go
		for _, writers := range t.persistentLaneWriters {
			<-writers.control.done
			<-writers.data.done
		}
```

Keep the existing v2 readiness and close loops unchanged.

- [ ] **Step 8: Format and run writer/routing tests**

Run:

```bash
gofmt -w bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_lanes.go bloc-node/internal/app/transport_libp2p_lanes_test.go
cd bloc-node
go test ./internal/app -run 'Test(PersistentLane|PeerStreamWriter)' -count=1
go test -race ./internal/app -run 'Test(PersistentLane|PeerStreamWriter)' -count=1
```

Expected: PASS; control completes while data remains blocked, reset/no-replay is lane-local, and all existing single-writer tests remain green.

- [ ] **Step 9: Commit outbound lane isolation**

```bash
git add bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_lanes.go bloc-node/internal/app/transport_libp2p_lanes_test.go
git commit -m "feat(transport): isolate persistent control and data writers"
```

### Task 3: Register both protocols and enforce inbound lane integrity

**Files:**
- Modify: `bloc-node/internal/app/transport_libp2p.go:69-124,175-238`
- Test: `bloc-node/internal/app/transport_libp2p_lanes_test.go`
- Test: `bloc-node/internal/app/transport_libp2p_persistent_test.go:23-430,545-644`

**Interfaces:**
- Consumes: `persistentLaneProtocol`, `classifyEnvelopeLane`, `handlePersistentStream`, authentication/payload validators, and test transport-pair helpers.
- Produces: `LibP2PTransport.handlePersistentLaneStream(network.Stream, persistentStreamLane)` and v3 protocol registration with bounded wrong-lane rejection.

- [ ] **Step 1: Write failing protocol-advertisement and two-lane readiness tests**

Add:

```go
func TestPersistentLanesPrewarmBothProtocolsAndReuseStreams(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistentLanes, streamModePersistentLanes, true)
	waitForTransportReady(t, pair.sender)
	waitForTransportReady(t, pair.receiver)
	for _, protocolID := range []protocol.ID{blocEnvelopeProtocolControl, blocEnvelopeProtocolData} {
		if !transportAdvertisesProtocol(pair.receiver, protocolID) {
			t.Fatalf("missing advertised protocol %s", protocolID)
		}
		if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), protocolID); got != 1 {
			t.Fatalf("prewarmed streams for %s = %d, want 1", protocolID, got)
		}
	}
	if transportAdvertisesProtocol(pair.receiver, blocEnvelopeProtocolFresh) ||
		transportAdvertisesProtocol(pair.receiver, blocEnvelopeProtocolPersistent) {
		t.Fatalf("lane mode advertised legacy protocols: %v", pair.receiver.host.Mux().Protocols())
	}
}
```

Append to the same test:

```go
	ready := WireEnvelope{Kind: "acs", Slot: 1, ACS: slotACSMessage(
		&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{RootHash: []byte("root")}},
	)}
	share := validPersistentTestEnvelope(0, 1, 2)
	for name, env := range map[string]WireEnvelope{"ready": ready, "share": share} {
		result, err := pair.sender.Send(t.Context(), 1, env)
		if err != nil || !result.StreamReused || result.StreamOpenDuration != 0 {
			t.Fatalf("%s send = %+v, %v", name, result, err)
		}
	}
	for index := 0; index < 2; index++ {
		select {
		case delivery := <-pair.deliveries:
			if delivery.encodedBytes <= 0 {
				t.Fatalf("delivery omitted encoded size: %+v", delivery)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for lane delivery %d", index)
		}
	}
```

Do not assert cross-lane delivery order.

- [ ] **Step 2: Write the failing wrong-lane rejection test**

Add:

```go
func TestPersistentLaneHandlerRejectsWrongLane(t *testing.T) {
	tests := []struct {
		name       string
		protocolID protocol.ID
		envelope   func() WireEnvelope
	}{
		{
			name: "share on control", protocolID: blocEnvelopeProtocolControl,
			envelope: func() WireEnvelope { return validPersistentTestEnvelope(0, 1, 1) },
		},
		{
			name: "ready on data", protocolID: blocEnvelopeProtocolData,
			envelope: func() WireEnvelope {
				return WireEnvelope{From: 0, To: 1, Direct: true, Kind: "acs", Slot: 1,
					ACS: slotACSMessage(&hbbft.BroadcastMessage{
						Payload: &hbbft.ReadyRequest{RootHash: []byte("root")},
					})}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair := newLibP2PTransportPair(t, streamModePersistentLanes, streamModePersistentLanes, true)
			stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), test.protocolID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := (ProtoEnvelopeCodec{}).Encode(test.envelope())
			if err != nil {
				t.Fatal(err)
			}
			if err := writeEnvelopeFrame(stream, data); err != nil && !errors.Is(err, network.ErrReset) {
				t.Fatal(err)
			}
			waitForPersistentRejection(t, pair.receiver.node, "lane", 1)
			assertPersistentStreamReset(t, stream)
			assertNoPersistentDelivery(t, pair.deliveries)
		})
	}
}
```

Add the existing libp2p `network` import to the lane test file. Each subtest
uses a fresh pair, so the one rejection proves the handler reset only the
stream carrying the wrong-lane frame and delivered no application envelope.

- [ ] **Step 3: Add a mixed-mode readiness regression**

Add:

```go
func TestPersistentLanesReadyRejectsPersistentOnlyPeer(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistentLanes, streamModePersistent, true)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if pair.sender.Ready() {
			t.Fatal("lane transport became ready with a v2-only peer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, protocolID := range []protocol.ID{blocEnvelopeProtocolControl, blocEnvelopeProtocolData} {
		if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), protocolID); got != 0 {
			t.Fatalf("mixed-mode streams for %s = %d, want 0", protocolID, got)
		}
	}
}
```

- [ ] **Step 4: Run integration tests and verify v3 handlers are absent**

Run:

```bash
cd bloc-node
go test ./internal/app -run 'TestPersistent(LanesPrewarm|LaneWrong|LanesReadyRejectsMixed)' -count=1
```

Expected: FAIL because `Start` rejects `persistent-lanes` or the v3 protocol handlers are not registered.

- [ ] **Step 5: Register only the two v3 handlers in lane mode**

Add this `streamModePersistentLanes` case in `Start`:

```go
	case streamModePersistentLanes:
		for _, lane := range persistentStreamLanes {
			protocolID, err := persistentLaneProtocol(lane)
			if err != nil {
				lifecycleCancel()
				_ = h.Close()
				t.host = nil
				return err
			}
			lane := lane
			h.SetStreamHandler(protocolID, func(stream network.Stream) {
				if !t.beginInboundHandler() {
					_ = stream.Reset()
					return
				}
				defer t.inboundWG.Done()
				t.handlePersistentLaneStream(stream, lane)
			})
		}
		t.startPersistentLaneWriters(lifecycleCtx)
```

Do not register v1 or v2 handlers in this mode.

- [ ] **Step 6: Share the framed reader and enforce lane after authentication**

Refactor v2 `handlePersistentStream` into:

```go
func (t *LibP2PTransport) handlePersistentStream(stream network.Stream) {
	t.handlePersistentStreamForLane(stream, "")
}

func (t *LibP2PTransport) handlePersistentLaneStream(stream network.Stream, lane persistentStreamLane) {
	t.handlePersistentStreamForLane(stream, lane)
}
```

Keep the current reader/dispatch loop in `handlePersistentStreamForLane`. After `validateEnvelopePayload` and `validateAuthenticatedEnvelope` succeed, add:

```go
		if expectedLane != "" {
			actualLane, err := classifyEnvelopeLane(envelope)
			if err != nil {
				t.rejectPersistentStream(stream, "payload", err)
				return
			}
			if actualLane != expectedLane {
				t.rejectPersistentStream(stream, "lane", fmt.Errorf(
					"envelope lane %q does not match protocol lane %q", actualLane, expectedLane))
				return
			}
		}
```

The empty lane keeps v2 behavior unchanged. Because each inbound handler owns one libp2p stream, `rejectPersistentStream` resets only the offending lane.

- [ ] **Step 7: Verify legacy compatibility, framing, and races**

Run:

```bash
gofmt -w bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_lanes_test.go
cd bloc-node
go test ./internal/app -run 'Test(FreshHandlerKeepsV1|PersistentHandler|PersistentPrewarm|PersistentReset|PersistentClose|PersistentLane)' -count=1
go test -race ./internal/app -run 'Test(PersistentHandler|PersistentLane|PersistentClose)' -count=1
```

Expected: PASS with no race; v1/v2 advertised protocols and delivery behavior remain unchanged, lane peers advertise exactly v3 control/data, and wrong-lane frames are rejected as `lane`.

- [ ] **Step 8: Commit inbound enforcement**

```bash
git add bloc-node/internal/app/transport_libp2p.go bloc-node/internal/app/transport_libp2p_lanes_test.go bloc-node/internal/app/transport_libp2p_persistent_test.go
git commit -m "feat(transport): enforce persistent envelope lanes"
```

### Task 4: Run the local mechanism gate and update canonical documentation

**Files:**
- Modify: `bloc-node/README.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Consumes: `persistent-lanes`, complete v3 trace artifacts, and all lane transport tests from Tasks 1-3.
- Produces: a locally validated experimental mode, retained ignored mechanism artifacts, and canonical operational/interpretation guidance.

- [ ] **Step 1: Document the exact compatibility and routing matrix**

Add this table to the transport section in `bloc-node/README.md` and `docs/modules/bloc-node.md`:

```markdown
| Stream mode | Protocols | Writers per remote peer | Routing |
| --- | --- | ---: | --- |
| `fresh` | `/bloc/envelope/1.0.0` | 0 persistent | one stream per envelope |
| `persistent` | `/bloc/envelope/2.0.0` | 1 | all envelope types share one FIFO |
| `persistent-lanes` | `/bloc/envelope/3.0.0/control`, `/bloc/envelope/3.0.0/data` | 2 | READY/BVAL/AUX on control; PROOF/ECHO/share on data |
```

Document that v3 is an experimental application-stream isolation mode, not a bandwidth optimization: payload volume, recipient counts, protocol rounds, libp2p connection congestion, and TCP loss head-of-line blocking remain unchanged.

- [ ] **Step 2: Record the design decision and validation contract**

Append a decision to `docs/DECISIONS.md` stating:

```markdown
Use two persistent application streams per peer for the lane experiment rather
than a priority queue on one stream. A priority queue cannot preempt a large
frame already being written. The existing single persistent stream remains the
control, wrong-lane inbound frames fail closed, and adoption requires matched
same-AZ/three-region evidence under the finalized v3 trace contract.
```

In `docs/VALIDATION.md`, require all subtype routing, independent queue, wrong-lane, mixed-mode, readiness, reset/no-replay, shutdown, normal, race, local evaluation, trace artifact, and ACS safety gates named in this plan.

- [ ] **Step 3: Run complete module and focused race suites**

Run:

```bash
cd sbc/hbbft && go test ./... -count=1
cd bloc-node && go test ./... -count=1
cd bloc-node && go test -race ./internal/app -run 'Test(ACS|Trace|Persistent|LibP2P|PrepareSlot|Eval)' -count=1
```

Expected: all commands pass with no race.

- [ ] **Step 4: Run local end-to-end and safety gates**

Run:

```bash
cd bloc-node
go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent --node-counts 4 --batch-sizes 8 --bmax 8 \
  --warmups 0 --repetitions 1 --repetition-blocks 1 --seed 20260621 \
  --timeout 30s --deadline 12s --acs-trace --stream-mode persistent-lanes \
  --experiment-id local-lane-smoke --out-dir results/local/acs-persistent-lanes/smoke

cd ..
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

Expected: the local suite run completes consistently with four finalized v3
node traces; the safety campaign passes every deterministic schedule, sustained
gate, and compatibility case.

- [ ] **Step 5: Run the matched local mechanism diagnostic**

Run both modes with identical scenario inputs and seed:

```bash
cd bloc-node
go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent --node-counts 4 --batch-sizes 8,32,128 --bmax 128 \
  --warmups 5 --repetitions 30 --repetition-blocks 1 --seed 20260621 \
  --timeout 30s --deadline 12s --acs-trace --stream-mode persistent \
  --experiment-id local-lane-control --out-dir results/local/acs-persistent-lanes/control

go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent --node-counts 4 --batch-sizes 8,32,128 --bmax 128 \
  --warmups 5 --repetitions 30 --repetition-blocks 1 --seed 20260621 \
  --timeout 30s --deadline 12s --acs-trace --stream-mode persistent-lanes \
  --experiment-id local-lane-treatment --out-dir results/local/acs-persistent-lanes/treatment
```

Expected mechanism gate: every measured slot succeeds and is cross-node consistent; all traces are finalized v3 with zero send failures; READY queue-wait does not show a stable regression in lane mode. Treat this as a local mechanism/regression result only. A null ACS improvement does not fail implementation; unstable control queueing, correctness failure, or send failure blocks cloud escalation.

- [ ] **Step 6: Record implementation and live status**

Append to `docs/CHANGELOG.md`:

```markdown
- Added experimental `persistent-lanes` transport mode with independent
  control/data writers and protocols per peer, fail-closed routing and inbound
  enforcement, two-lane readiness/prewarm, lane-local reset/no-replay, and
  deterministic head-of-line isolation coverage while preserving fresh and
  single-stream persistent modes.
```

Review `docs/STATUS.md`, set its date to the execution date, and record the local mechanism outcome. Remove the lane implementation from immediate actions. Do not mark the mode adopted, select a new milestone, accept a new baseline, or add cloud results. Make the separately authorized matched same-AZ/three-region campaign the next action only if all local gates pass.

- [ ] **Step 7: Inspect and commit documentation/evidence state**

Run:

```bash
git diff --check
git status --short --branch
git diff -- bloc-node/README.md docs/modules/bloc-node.md docs/VALIDATION.md docs/DECISIONS.md docs/CHANGELOG.md docs/STATUS.md
```

Then commit only the six documentation files:

```bash
git add bloc-node/README.md docs/modules/bloc-node.md docs/VALIDATION.md docs/DECISIONS.md docs/CHANGELOG.md docs/STATUS.md
git commit -m "docs: define persistent stream lane experiment"
```

- [ ] **Step 8: Post evidence and perform the final repository gate**

Post exact normal/race/safety/local-diagnostic commands, outcomes, artifact roots, and any rejected attempt to `Add persistent ACS control and data stream lanes`. Then run:

```bash
git status --short --branch
git log -4 --oneline
git rev-list --left-right --count main...HEAD
```

Expected: four focused task commits; no task-owned changes; the user's PDF remains unstaged if present. Close the implementation issue only if all local acceptance gates pass. The cloud evidence issue remains separate and must not be started without explicit live authorization.
