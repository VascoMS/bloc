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
