package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthdm/hbbft"
	network "github.com/libp2p/go-libp2p/core/network"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
)

func TestPersistentLaneTransportRoutesControlAroundBlockedData(t *testing.T) {
	dataStream := newBlockingPersistentStream()
	controlStream := &capturePersistentStream{}
	codec := &countingEnvelopeCodec{delegate: ProtoEnvelopeCodec{}}
	transport := newControlledPersistentLaneTransport(codec, controlStream, dataStream)
	var releaseOnce sync.Once
	releaseData := func() { releaseOnce.Do(func() { close(dataStream.writeRelease) }) }
	t.Cleanup(func() {
		releaseData()
		_ = transport.Close()
	})

	dataDone := sendTransportAsync(transport, WireEnvelope{
		Kind: "share",
		Slot: 10,
		Share: &WireShare{
			OperatorID: 0,
			PointHex:   "data-payload",
		},
	})
	<-dataStream.writeStarted
	if got := codec.encodes.Load(); got != 1 {
		t.Fatalf("codec Encode calls after data Send = %d, want 1", got)
	}
	controlDone := sendTransportAsync(transport, WireEnvelope{
		Kind: "acs",
		Slot: 11,
		ACS:  slotACSMessage(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{RootHash: []byte("ready-root")}}),
	})

	var control transportSendCompletion
	select {
	case control = <-controlDone:
		if control.err != nil {
			t.Fatal(control.err)
		}
	case <-time.After(time.Second):
		t.Fatal("READY sent through LibP2PTransport.Send waited for blocked data lane")
	}
	if got := codec.encodes.Load(); got != 2 {
		t.Fatalf("codec Encode calls = %d, want one per Send", got)
	}
	select {
	case <-dataDone:
		t.Fatal("blocked data Send completed before release")
	default:
	}
	controlEnvelope, controlBytes := decodeCapturedPersistentFrame(t, controlStream.bytes())
	if control.result.EncodedBytes != controlBytes || controlEnvelope.Kind != "acs" || controlEnvelope.Slot != 11 {
		t.Fatalf("control result=%+v envelope=%+v bytes=%d", control.result, controlEnvelope, controlBytes)
	}
	broadcast, ok := controlEnvelope.ACS.Payload.Payload.(*hbbft.BroadcastMessage)
	if !ok {
		t.Fatalf("control payload = %T, want broadcast", controlEnvelope.ACS.Payload.Payload)
	}
	ready, ok := broadcast.Payload.(*hbbft.ReadyRequest)
	if !ok || string(ready.RootHash) != "ready-root" {
		t.Fatalf("control broadcast payload = %#v, want READY", broadcast.Payload)
	}

	releaseData()
	data := <-dataDone
	if data.err != nil {
		t.Fatal(data.err)
	}
	dataEnvelope, dataBytes := decodeCapturedPersistentFrame(t, dataStream.bytes())
	if data.result.EncodedBytes != dataBytes || dataEnvelope.Kind != "share" || dataEnvelope.Slot != 10 ||
		dataEnvelope.Share == nil || dataEnvelope.Share.PointHex != "data-payload" {
		t.Fatalf("data result=%+v envelope=%+v bytes=%d", data.result, dataEnvelope, dataBytes)
	}
}

func TestPersistentLaneTransportReadyRequiresBothWritersAndProtocols(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	pair.sender.node.cfg.Network.StreamMode = streamModePersistentLanes
	writers := newPeerStreamLaneWriters(1, func(context.Context, uint64, persistentStreamLane) (persistentWriteStream, error) {
		return &capturePersistentStream{}, nil
	}, pair.sender.persistentStop)
	pair.sender.persistentLaneWriters[1] = writers

	writers.control.ready.Store(true)
	writers.data.ready.Store(true)
	for index, protocolID := range []protocol.ID{blocEnvelopeProtocolControl, blocEnvelopeProtocolData} {
		if pair.sender.Ready() {
			t.Fatalf("lane transport ready with only %d of 2 protocols", index)
		}
		if err := pair.sender.host.Peerstore().AddProtocols(pair.receiver.host.ID(), protocolID); err != nil {
			t.Fatal(err)
		}
	}
	if !pair.sender.Ready() {
		t.Fatal("lane transport not ready with both protocols and writers")
	}
	writers.data.ready.Store(false)
	if pair.sender.Ready() {
		t.Fatal("lane transport ready while data writer was not ready")
	}
	writers.data.ready.Store(true)
	writers.control.ready.Store(false)
	if pair.sender.Ready() {
		t.Fatal("lane transport ready while control writer was not ready")
	}
}

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
}

func TestPersistentLaneHandlerRejectsWrongLane(t *testing.T) {
	tests := []struct {
		name                 string
		protocolID           protocol.ID
		envelope             func() WireEnvelope
		unaffectedProtocolID protocol.ID
		validEnvelope        func() WireEnvelope
	}{
		{
			name: "share on control", protocolID: blocEnvelopeProtocolControl,
			envelope:             func() WireEnvelope { return validPersistentTestEnvelope(0, 1, 1) },
			unaffectedProtocolID: blocEnvelopeProtocolData,
			validEnvelope:        func() WireEnvelope { return validPersistentTestEnvelope(0, 1, 2) },
		},
		{
			name: "ready on data", protocolID: blocEnvelopeProtocolData,
			envelope: func() WireEnvelope {
				return WireEnvelope{From: 0, To: 1, Direct: true, Kind: "acs", Slot: 1,
					ACS: slotACSMessage(&hbbft.BroadcastMessage{
						Payload: &hbbft.ReadyRequest{RootHash: []byte("root")},
					})}
			},
			unaffectedProtocolID: blocEnvelopeProtocolControl,
			validEnvelope: func() WireEnvelope {
				return WireEnvelope{Kind: "acs", Slot: 2, ACS: slotACSMessage(
					&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{RootHash: []byte("next")}},
				)}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair := newLibP2PTransportPair(t, streamModePersistentLanes, streamModePersistentLanes, true)
			waitForTransportReady(t, pair.sender)
			unaffectedStreams := countOpenProtocolStreams(
				pair.sender, pair.receiver.host.ID(), test.unaffectedProtocolID)
			if unaffectedStreams != 1 {
				t.Fatalf("prewarmed unaffected streams = %d, want 1", unaffectedStreams)
			}
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
			if connectedness := pair.sender.host.Network().Connectedness(pair.receiver.host.ID()); connectedness != network.Connected {
				t.Fatalf("peer connectedness after wrong-lane rejection = %s, want connected", connectedness)
			}

			result, err := pair.sender.Send(t.Context(), 1, test.validEnvelope())
			if err != nil {
				t.Fatalf("send on unaffected lane: %v", err)
			}
			if !result.StreamReused || result.StreamOpenDuration != 0 {
				t.Fatalf("unaffected lane send = %+v, want prewarmed stream reuse", result)
			}
			select {
			case delivery := <-pair.deliveries:
				if delivery.envelope.Slot != 2 || delivery.encodedBytes <= 0 {
					t.Fatalf("unaffected lane delivery = %+v", delivery)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for unaffected lane delivery")
			}
			if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), test.unaffectedProtocolID); got != unaffectedStreams {
				t.Fatalf("unaffected protocol streams after send = %d, want unchanged %d", got, unaffectedStreams)
			}
			if connectedness := pair.sender.host.Network().Connectedness(pair.receiver.host.ID()); connectedness != network.Connected {
				t.Fatalf("peer connectedness after unaffected send = %s, want connected", connectedness)
			}
		})
	}
}

func TestPersistentLaneHandlerAuthenticatesBeforeLaneValidation(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistentLanes, streamModePersistentLanes, true)
	waitForTransportReady(t, pair.sender)
	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolControl)
	if err != nil {
		t.Fatal(err)
	}
	envelope := validPersistentTestEnvelope(0, 1, 1)
	envelope.From = 9
	data, err := (ProtoEnvelopeCodec{}).Encode(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnvelopeFrame(stream, data); err != nil && !errors.Is(err, network.ErrReset) {
		t.Fatal(err)
	}
	waitForPersistentRejection(t, pair.receiver.node, "authentication", 1)
	if got := persistentRejectionCount(t, pair.receiver.node, "lane"); got != 0 {
		t.Fatalf("lane rejection count = %d, want 0", got)
	}
	assertPersistentStreamReset(t, stream)
	assertNoPersistentDelivery(t, pair.deliveries)
}

func TestPersistentLaneHandlerRejectsUnclassifiableSubtypeAsPayload(t *testing.T) {
	envelope := WireEnvelope{
		From: 0, To: 1, Direct: true, Kind: "acs", Slot: 1,
		ACS: &hbbft.SlotMessage{Payload: &hbbft.ACSMessage{}},
	}
	if err := validateEnvelopePayload(envelope); err != nil {
		t.Fatalf("unclassifiable fixture did not pass payload-union validation: %v", err)
	}
	pair := newLibP2PTransportPairWithReceiverCodec(
		t, streamModePersistentLanes, streamModePersistentLanes, true,
		fixedDecodedEnvelopeCodec{envelope: envelope})
	waitForTransportReady(t, pair.sender)
	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolControl)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnvelopeFrame(stream, []byte("unclassifiable-acs")); err != nil && !errors.Is(err, network.ErrReset) {
		t.Fatal(err)
	}
	waitForPersistentRejection(t, pair.receiver.node, "payload", 1)
	if got := persistentRejectionCount(t, pair.receiver.node, "lane"); got != 0 {
		t.Fatalf("lane rejection count = %d, want 0", got)
	}
	assertPersistentStreamReset(t, stream)
	assertNoPersistentDelivery(t, pair.deliveries)
}

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

func TestPersistentLaneTransportCloseWaitsForBothWorkers(t *testing.T) {
	dataStream := newBlockingPersistentStream()
	controlStream := newBlockingPersistentStream()
	transport := newControlledPersistentLaneTransport(ProtoEnvelopeCodec{}, controlStream, dataStream)
	var releaseDataOnce sync.Once
	var releaseControlOnce sync.Once
	releaseData := func() { releaseDataOnce.Do(func() { close(dataStream.writeRelease) }) }
	releaseControl := func() { releaseControlOnce.Do(func() { close(controlStream.writeRelease) }) }
	t.Cleanup(func() {
		releaseControl()
		releaseData()
		_ = transport.Close()
	})

	dataDone := sendTransportAsync(transport, WireEnvelope{Kind: "share", Share: &WireShare{OperatorID: 0}})
	controlDone := sendTransportAsync(transport, WireEnvelope{
		Kind: "acs",
		ACS:  slotACSMessage(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{}}),
	})
	<-dataStream.writeStarted
	<-controlStream.writeStarted
	closed := make(chan error, 1)
	go func() { closed <- transport.Close() }()

	select {
	case err := <-closed:
		t.Fatalf("Close returned before either lane worker exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseControl()
	select {
	case err := <-closed:
		t.Fatalf("Close returned while data lane worker remained blocked: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseData()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return after both lane workers exited")
	}
	for name, done := range map[string]<-chan struct{}{
		"control": transport.persistentLaneWriters[1].control.done,
		"data":    transport.persistentLaneWriters[1].data.done,
	} {
		select {
		case <-done:
		default:
			t.Fatalf("%s lane worker still running after Close", name)
		}
	}
	if !controlStream.reset || !dataStream.reset {
		t.Fatalf("Close reset control=%t data=%t, want both", controlStream.reset, dataStream.reset)
	}
	for name, completion := range map[string]transportSendCompletion{
		"control": <-controlDone,
		"data":    <-dataDone,
	} {
		if !errors.Is(completion.err, errPersistentWriterClosed) {
			t.Fatalf("%s send error = %v, want writer closed", name, completion.err)
		}
	}
}

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

type countingEnvelopeCodec struct {
	delegate EnvelopeCodec
	encodes  atomic.Int64
}

type fixedDecodedEnvelopeCodec struct {
	envelope WireEnvelope
}

func (fixedDecodedEnvelopeCodec) Encode(WireEnvelope) ([]byte, error) {
	return nil, errors.New("fixed decoded envelope codec does not encode")
}

func (c fixedDecodedEnvelopeCodec) Decode([]byte) (WireEnvelope, error) {
	return c.envelope, nil
}

func (c *countingEnvelopeCodec) Encode(envelope WireEnvelope) ([]byte, error) {
	c.encodes.Add(1)
	return c.delegate.Encode(envelope)
}

func (c *countingEnvelopeCodec) Decode(data []byte) (WireEnvelope, error) {
	return c.delegate.Decode(data)
}

type transportSendCompletion struct {
	result transportSendResult
	err    error
}

func sendTransportAsync(transport *LibP2PTransport, envelope WireEnvelope) <-chan transportSendCompletion {
	done := make(chan transportSendCompletion, 1)
	go func() {
		result, err := transport.Send(context.Background(), 1, envelope)
		done <- transportSendCompletion{result: result, err: err}
	}()
	return done
}

func newControlledPersistentLaneTransport(
	codec EnvelopeCodec,
	controlStream persistentWriteStream,
	dataStream persistentWriteStream,
) *LibP2PTransport {
	node := &Node{
		cfg: ConfigFile{
			Network: NetworkConfig{Mode: "libp2p", StreamMode: streamModePersistentLanes},
			Limits:  ResourceLimits{MaxEnvelopeBytes: 1 << 20},
		},
		self:  NodeConfig{ID: 0},
		peers: map[uint64]NodeConfig{0: {ID: 0}, 1: {ID: 1}},
	}
	transport := newLibP2PTransport(node, codec)
	transport.persistentLaneWriters[1] = newPeerStreamLaneWriters(1,
		func(_ context.Context, _ uint64, lane persistentStreamLane) (persistentWriteStream, error) {
			if lane == persistentLaneControl {
				return controlStream, nil
			}
			return dataStream, nil
		}, transport.persistentStop)
	return transport
}

func decodeCapturedPersistentFrame(t *testing.T, wire []byte) (WireEnvelope, int) {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(wire))
	data, err := readEnvelopeFrame(reader, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvelopeFrame(reader, 1<<20); !errors.Is(err, io.EOF) {
		t.Fatalf("captured stream has an extra frame: %v", err)
	}
	envelope, err := (ProtoEnvelopeCodec{}).Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return envelope, len(data)
}
