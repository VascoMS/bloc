package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bloc-node/internal/pb/blocv1"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"
)

func TestPersistentHandlerDeliversMultipleFrames(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolPersistent)
	if err != nil {
		t.Fatal(err)
	}

	codec := ProtoEnvelopeCodec{}
	var wantSizes []int
	for slot := uint64(1); slot <= 3; slot++ {
		envelope := validPersistentTestEnvelope(pair.sender.node.self.ID, pair.receiver.node.self.ID, slot)
		data, err := codec.Encode(envelope)
		if err != nil {
			t.Fatal(err)
		}
		wantSizes = append(wantSizes, len(data))
		if err := writeEnvelopeFrame(stream, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	for index, wantSize := range wantSizes {
		select {
		case delivery := <-pair.deliveries:
			if delivery.envelope.Slot != uint64(index+1) || delivery.encodedBytes != wantSize {
				t.Fatalf("delivery %d = %+v, want slot=%d bytes=%d", index, delivery, index+1, wantSize)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for delivery %d", index)
		}
	}
	select {
	case extra := <-pair.deliveries:
		t.Fatalf("unexpected extra delivery: %+v", extra)
	case <-time.After(20 * time.Millisecond):
	}
	if got := persistentRejectionCount(t, pair.receiver.node, "decode"); got != 0 {
		t.Fatalf("clean EOF recorded %d decode rejections", got)
	}
}

func TestPersistentHandlerContinuesReadingWhileProtocolHandlerBlocks(t *testing.T) {
	const frameCount = 32
	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	deliveries := make(chan struct{}, frameCount)
	var startedOnce sync.Once
	var releaseOnce sync.Once
	pair := newLibP2PTransportPairWithHandler(t, streamModePersistent, streamModePersistent, true,
		func(WireEnvelope, int) {
			startedOnce.Do(func() { close(handlerStarted) })
			<-handlerRelease
			deliveries <- struct{}{}
		})
	defer releaseOnce.Do(func() { close(handlerRelease) })
	pair.receiver.node.cfg.Limits.MaxEnvelopeBytes = 1 << 20

	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolPersistent)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	envelope := validPersistentTestEnvelope(pair.sender.node.self.ID, pair.receiver.node.self.ID, 1)
	envelope.Share.PointHex = strings.Repeat("a", 512<<10)
	data, err := (ProtoEnvelopeCodec{}).Encode(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnvelopeFrame(stream, data); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("protocol handler did not receive the first frame")
	}
	for index := 1; index < frameCount; index++ {
		if err := writeEnvelopeFrame(stream, data); err != nil {
			t.Fatalf("write frame %d while handler blocked: %v", index+1, err)
		}
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	releaseOnce.Do(func() { close(handlerRelease) })
	for index := 0; index < frameCount; index++ {
		select {
		case <-deliveries:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for dispatched frame %d", index+1)
		}
	}
}

func TestPersistentHandlerRejectsAuthenticatedEnvelopeViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WireEnvelope)
		reason string
	}{
		{name: "forged from", reason: "authentication", mutate: func(envelope *WireEnvelope) { envelope.From = 9 }},
		{name: "wrong recipient", reason: "authentication", mutate: func(envelope *WireEnvelope) { envelope.To = 9 }},
		{name: "not direct", reason: "authentication", mutate: func(envelope *WireEnvelope) { envelope.Direct = false }},
		{name: "forged share operator", reason: "authentication", mutate: func(envelope *WireEnvelope) { envelope.Share.OperatorID = 9 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
			envelope := validPersistentTestEnvelope(pair.sender.node.self.ID, pair.receiver.node.self.ID, 1)
			test.mutate(&envelope)
			data, err := (ProtoEnvelopeCodec{}).Encode(envelope)
			if err != nil {
				t.Fatal(err)
			}
			stream := writePersistentTestFrames(t, pair, data)
			waitForPersistentRejection(t, pair.receiver.node, test.reason, 1)
			assertPersistentStreamReset(t, stream)
			assertNoPersistentDelivery(t, pair.deliveries)
		})
	}
}

func TestPersistentHandlerRejectsInvalidPayloadUnion(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	data, err := proto.Marshal(&blocv1.Envelope{
		Version: 1,
		From:    pair.sender.node.self.ID,
		To:      pair.receiver.node.self.ID,
		Direct:  true,
		Kind:    "acs",
		Slot:    1,
		Payload: &blocv1.Envelope_Share{Share: &blocv1.WireShare{OperatorId: pair.sender.node.self.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := writePersistentTestFrames(t, pair, data)
	waitForPersistentRejection(t, pair.receiver.node, "payload", 1)
	assertPersistentStreamReset(t, stream)
	assertNoPersistentDelivery(t, pair.deliveries)
}

func TestPersistentHandlerRejectsUnknownRemotePeer(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, false)
	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolPersistent)
	if err != nil {
		t.Fatal(err)
	}
	data, err := (ProtoEnvelopeCodec{}).Encode(validPersistentTestEnvelope(0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnvelopeFrame(stream, data); err != nil && !errors.Is(err, network.ErrReset) {
		t.Fatal(err)
	}
	waitForPersistentRejection(t, pair.receiver.node, "authentication", 1)
	assertPersistentStreamReset(t, stream)
	assertNoPersistentDelivery(t, pair.deliveries)
}

func TestPersistentHandlerRejectsInvalidFrameAfterValidFrame(t *testing.T) {
	tests := []struct {
		name   string
		tail   func(maximum int) []byte
		reason string
	}{
		{name: "oversized", reason: "oversize", tail: func(maximum int) []byte {
			return binary.AppendUvarint(nil, uint64(maximum+1))
		}},
		{name: "truncated", reason: "decode", tail: func(int) []byte {
			return append(binary.AppendUvarint(nil, 3), 'a', 'b')
		}},
		{name: "undecodable", reason: "decode", tail: func(int) []byte {
			var framed bytes.Buffer
			if err := writeEnvelopeFrame(&framed, []byte{0xff, 0xff}); err != nil {
				panic(err)
			}
			return framed.Bytes()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
			valid, err := (ProtoEnvelopeCodec{}).Encode(validPersistentTestEnvelope(0, 1, 1))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolPersistent)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeEnvelopeFrame(stream, valid); err != nil {
				t.Fatal(err)
			}
			if err := writeAll(stream, test.tail(pair.receiver.node.cfg.Limits.MaxEnvelopeBytes)); err != nil {
				t.Fatal(err)
			}
			if test.name == "truncated" {
				_ = stream.CloseWrite()
			}
			select {
			case delivery := <-pair.deliveries:
				if delivery.envelope.Slot != 1 || delivery.encodedBytes != len(valid) {
					t.Fatalf("valid delivery = %+v, want bytes=%d", delivery, len(valid))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("valid frame was not delivered before invalid frame")
			}
			waitForPersistentRejection(t, pair.receiver.node, test.reason, 1)
			assertPersistentStreamReset(t, stream)
			assertNoPersistentDelivery(t, pair.deliveries)
		})
	}
}

func TestFreshHandlerKeepsV1WireCompatibilityAndModeIsolation(t *testing.T) {
	fresh := newLibP2PTransportPair(t, streamModeFresh, streamModeFresh, true)
	if !transportAdvertisesProtocol(fresh.receiver, blocEnvelopeProtocolFresh) ||
		transportAdvertisesProtocol(fresh.receiver, blocEnvelopeProtocolPersistent) {
		t.Fatalf("fresh protocols = %v", fresh.receiver.host.Mux().Protocols())
	}
	stream, err := fresh.sender.host.NewStream(t.Context(), fresh.receiver.host.ID(), blocEnvelopeProtocolFresh)
	if err != nil {
		t.Fatal(err)
	}
	data, err := (ProtoEnvelopeCodec{}).Encode(validPersistentTestEnvelope(0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAll(stream, data); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case delivery := <-fresh.deliveries:
		if delivery.envelope.Slot != 1 || delivery.encodedBytes != len(data) {
			t.Fatalf("fresh delivery = %+v", delivery)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fresh v1 envelope was not delivered")
	}

	persistent := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	if !transportAdvertisesProtocol(persistent.receiver, blocEnvelopeProtocolPersistent) ||
		transportAdvertisesProtocol(persistent.receiver, blocEnvelopeProtocolFresh) {
		t.Fatalf("persistent protocols = %v", persistent.receiver.host.Mux().Protocols())
	}
}

func TestPersistentPrewarmReadyAndReusesOneStream(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	waitForTransportReady(t, pair.sender)
	waitForTransportReady(t, pair.receiver)
	if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), blocEnvelopeProtocolPersistent); got != 1 {
		t.Fatalf("prewarmed sender streams = %d, want 1", got)
	}
	if got := countProtocolStreams(pair.receiver, pair.sender.host.ID(), blocEnvelopeProtocolPersistent, network.DirInbound); got != 1 {
		t.Fatalf("negotiated receiver streams = %d, want 1", got)
	}

	for slot := uint64(1); slot <= 2; slot++ {
		result, err := pair.sender.Send(t.Context(), pair.receiver.node.self.ID,
			validPersistentTestEnvelope(pair.sender.node.self.ID, pair.receiver.node.self.ID, slot))
		if err != nil {
			t.Fatalf("send slot %d: %v", slot, err)
		}
		if !result.StreamReused || result.StreamOpenDuration != 0 || result.FinalizeDuration != 0 {
			t.Fatalf("slot %d result = %+v, want prewarmed reuse", slot, result)
		}
	}
	for slot := uint64(1); slot <= 2; slot++ {
		select {
		case delivery := <-pair.deliveries:
			if delivery.envelope.Slot != slot {
				t.Fatalf("delivery slot = %d, want %d", delivery.envelope.Slot, slot)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for slot %d", slot)
		}
	}
	for index := 0; index < 5; index++ {
		if !pair.sender.Ready() {
			t.Fatalf("ready call %d returned false", index)
		}
	}
	if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), blocEnvelopeProtocolPersistent); got != 1 {
		t.Fatalf("streams after sends/readiness = %d, want 1", got)
	}
}

func TestPeerStreamWriterPrewarmCompletesProtocolNegotiation(t *testing.T) {
	stop := make(chan struct{})
	stream := &negotiatingPersistentStream{}
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		return stream, nil
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.prewarmStream(ctx); err != nil {
		t.Fatalf("prewarm stream: %v", err)
	}
	if !stream.negotiated.Load() {
		t.Fatal("prewarm returned before lazy protocol negotiation completed")
	}
	if !writer.ready.Load() {
		t.Fatal("writer did not become ready after protocol negotiation")
	}
}

func TestPersistentReadyRejectsMixedModePeer(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModeFresh, true)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if pair.sender.Ready() {
			t.Fatal("persistent transport became ready with a fresh-only peer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pair.receiver.Ready() {
		t.Fatal("fresh receiver did not retain connection-only readiness")
	}
	if got := countOpenProtocolStreams(pair.sender, pair.receiver.host.ID(), blocEnvelopeProtocolPersistent); got != 0 {
		t.Fatalf("mixed-mode v2 streams = %d, want 0", got)
	}
}

func TestPersistentResetFailsEnvelopeAndNextSendOpensReplacement(t *testing.T) {
	pair := newLibP2PTransportPair(t, streamModePersistent, streamModePersistent, true)
	waitForTransportReady(t, pair.sender)
	outbound := findProtocolStream(pair.sender, pair.receiver.host.ID(), blocEnvelopeProtocolPersistent, network.DirOutbound)
	if outbound == nil {
		t.Fatal("prewarmed outbound stream not found")
	}
	if err := outbound.Reset(); err != nil {
		t.Fatal(err)
	}

	failed, err := pair.sender.Send(t.Context(), 1, validPersistentTestEnvelope(0, 1, 10))
	if err == nil || failed.EncodedBytes != 0 {
		t.Fatalf("send on reset stream result=%+v err=%v", failed, err)
	}
	next, err := pair.sender.Send(t.Context(), 1, validPersistentTestEnvelope(0, 1, 11))
	if err != nil {
		t.Fatalf("replacement send: %v", err)
	}
	if next.StreamReused || next.StreamOpenDuration <= 0 || next.FinalizeDuration != 0 {
		t.Fatalf("replacement result = %+v", next)
	}
	select {
	case delivery := <-pair.deliveries:
		if delivery.envelope.Slot != 11 {
			t.Fatalf("failed envelope was delivered or replayed: %+v", delivery)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement envelope was not delivered")
	}
	assertNoPersistentDelivery(t, pair.deliveries)
}

func TestPersistentCloseWaitsForInboundHandlerAndIsIdempotent(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	pair := newLibP2PTransportPairWithHandler(t, streamModePersistent, streamModePersistent, true,
		func(WireEnvelope, int) {
			close(handlerStarted)
			<-handlerRelease
		})
	waitForTransportReady(t, pair.sender)
	if _, err := pair.sender.Send(t.Context(), 1, validPersistentTestEnvelope(0, 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("inbound handler did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- pair.receiver.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("close returned before inbound handler exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(handlerRelease)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not finish after handler release")
	}
	if err := pair.receiver.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if _, err := pair.receiver.Send(t.Context(), 0, validPersistentTestEnvelope(1, 0, 2)); !errors.Is(err, errPersistentWriterClosed) {
		t.Fatalf("send after close error = %v, want writer closed", err)
	}
}

func waitForTransportReady(t *testing.T, transport *LibP2PTransport) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if transport.Ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transport did not become ready")
}

func countOpenProtocolStreams(transport *LibP2PTransport, remotePeer peer.ID, protocolID protocol.ID) int {
	return countProtocolStreams(transport, remotePeer, protocolID, network.DirOutbound)
}

func countProtocolStreams(
	transport *LibP2PTransport,
	remotePeer peer.ID,
	protocolID protocol.ID,
	direction network.Direction,
) int {
	count := 0
	for _, connection := range transport.host.Network().ConnsToPeer(remotePeer) {
		for _, stream := range connection.GetStreams() {
			if stream.Protocol() == protocolID && stream.Stat().Direction == direction {
				count++
			}
		}
	}
	return count
}

func findProtocolStream(
	transport *LibP2PTransport,
	remotePeer peer.ID,
	protocolID protocol.ID,
	direction network.Direction,
) network.Stream {
	for _, connection := range transport.host.Network().ConnsToPeer(remotePeer) {
		for _, stream := range connection.GetStreams() {
			if stream.Protocol() == protocolID && stream.Stat().Direction == direction {
				return stream
			}
		}
	}
	return nil
}

func writePersistentTestFrames(t *testing.T, pair libP2PTransportPair, frames ...[]byte) network.Stream {
	t.Helper()
	stream, err := pair.sender.host.NewStream(t.Context(), pair.receiver.host.ID(), blocEnvelopeProtocolPersistent)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if err := writeEnvelopeFrame(stream, frame); err != nil {
			t.Fatal(err)
		}
	}
	return stream
}

func waitForPersistentRejection(t *testing.T, node *Node, reason string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := persistentRejectionCount(t, node, reason); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s rejection count = %d, want %d", reason, persistentRejectionCount(t, node, reason), want)
}

func persistentRejectionCount(t *testing.T, node *Node, reason string) int {
	t.Helper()
	body := scrapeNodeMetrics(t, node)
	needle := `bloc_protocol_envelopes_rejected_total{cluster_id="persistent-handler-test",direction="inbound",node_id="1",reason="` + reason + `"} 1`
	if strings.Contains(body, needle) {
		return 1
	}
	return 0
}

func assertPersistentStreamReset(t *testing.T, stream network.Stream) {
	t.Helper()
	if err := stream.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := stream.Read(one[:]); !errors.Is(err, network.ErrReset) {
		t.Fatalf("stream read error = %v, want reset", err)
	}
}

func assertNoPersistentDelivery(t *testing.T, deliveries <-chan persistentTestDelivery) {
	t.Helper()
	select {
	case delivery := <-deliveries:
		t.Fatalf("invalid envelope was delivered: %+v", delivery)
	case <-time.After(20 * time.Millisecond):
	}
}

type persistentTestDelivery struct {
	envelope     WireEnvelope
	encodedBytes int
}

type libP2PTransportPair struct {
	sender     *LibP2PTransport
	receiver   *LibP2PTransport
	deliveries chan persistentTestDelivery
}

func newLibP2PTransportPair(t *testing.T, senderMode, receiverMode string, receiverKnowsSender bool) libP2PTransportPair {
	return newLibP2PTransportPairWithHandler(t, senderMode, receiverMode, receiverKnowsSender, nil)
}

func newLibP2PTransportPairWithHandler(
	t *testing.T,
	senderMode, receiverMode string,
	receiverKnowsSender bool,
	receiverHandler EnvelopeHandler,
) libP2PTransportPair {
	t.Helper()
	senderPrivate, senderPeerID, err := generateLibP2PIdentity()
	if err != nil {
		t.Fatal(err)
	}
	receiverPrivate, receiverPeerID, err := generateLibP2PIdentity()
	if err != nil {
		t.Fatal(err)
	}
	senderConfig := NodeConfig{ID: 0, P2PListenAddr: "/ip4/127.0.0.1/tcp/0", P2PPeerID: senderPeerID}
	receiverConfig := NodeConfig{ID: 1, P2PListenAddr: "/ip4/127.0.0.1/tcp/0", P2PPeerID: receiverPeerID}
	senderPeers := map[uint64]NodeConfig{0: senderConfig, 1: receiverConfig}
	receiverPeers := map[uint64]NodeConfig{1: receiverConfig}
	if receiverKnowsSender {
		receiverPeers[0] = senderConfig
	}
	senderNode := persistentTestNode(senderConfig, senderPeers, senderPrivate, senderMode)
	receiverNode := persistentTestNode(receiverConfig, receiverPeers, receiverPrivate, receiverMode)
	sender := newLibP2PTransport(senderNode, ProtoEnvelopeCodec{})
	receiver := newLibP2PTransport(receiverNode, ProtoEnvelopeCodec{})
	deliveries := make(chan persistentTestDelivery, 16)
	ctx, cancel := context.WithCancel(context.Background())
	if receiverHandler == nil {
		receiverHandler = func(envelope WireEnvelope, encodedBytes int) {
			deliveries <- persistentTestDelivery{envelope: envelope, encodedBytes: encodedBytes}
		}
	}
	if err := receiver.Start(ctx, receiverHandler); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := sender.Start(ctx, func(WireEnvelope, int) {}); err != nil {
		_ = receiver.Close()
		cancel()
		t.Fatal(err)
	}
	if err := sender.host.Connect(ctx, peer.AddrInfo{ID: receiver.host.ID(), Addrs: receiver.host.Addrs()}); err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = sender.Close()
		_ = receiver.Close()
	})
	return libP2PTransportPair{sender: sender, receiver: receiver, deliveries: deliveries}
}

func persistentTestNode(self NodeConfig, peers map[uint64]NodeConfig, privateKey, mode string) *Node {
	return &Node{
		cfg: ConfigFile{
			ClusterID: "persistent-handler-test",
			Network:   NetworkConfig{Mode: "libp2p", StreamMode: mode},
			Limits:    ResourceLimits{MaxEnvelopeBytes: 1024},
		},
		self:             self,
		peers:            peers,
		p2pPrivateKeyHex: privateKey,
		observability:    newNodeMetrics("persistent-handler-test", self.ID),
	}
}

func validPersistentTestEnvelope(from, to, slot uint64) WireEnvelope {
	return WireEnvelope{
		From: from, To: to, Direct: true, Kind: "share", Slot: slot,
		Share: &WireShare{OperatorID: int(from)},
	}
}

func transportAdvertisesProtocol(transport *LibP2PTransport, want protocol.ID) bool {
	for _, advertised := range transport.host.Mux().Protocols() {
		if advertised == want {
			return true
		}
	}
	return false
}

func TestPeerStreamWriterSerializesFramesAndBoundsQueue(t *testing.T) {
	stop := make(chan struct{})
	stream := newBlockingPersistentStream()
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		return stream, nil
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	firstDone := sendPersistentAsync(writer, context.Background(), []byte("first"))
	<-stream.writeStarted
	secondDone := sendPersistentAsync(writer, context.Background(), []byte("second"))
	waitForPersistentQueueLength(t, writer, 1)

	thirdCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := writer.send(thirdCtx, []byte("third")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third send error = %v, want deadline exceeded", err)
	}
	if got := len(writer.queue); got != persistentQueueCapacity {
		t.Fatalf("queue length = %d, want %d", got, persistentQueueCapacity)
	}

	close(stream.writeRelease)
	assertPersistentSendSuccess(t, <-firstDone, 5, false)
	assertPersistentSendSuccess(t, <-secondDone, 6, true)

	reader := bufio.NewReader(bytes.NewReader(stream.bytes()))
	for index, want := range [][]byte{[]byte("first"), []byte("second")} {
		got, err := readEnvelopeFrame(reader, 64)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %q, %v; want %q", index, got, err, want)
		}
	}
	if _, err := readEnvelopeFrame(reader, 64); !errors.Is(err, io.EOF) {
		t.Fatalf("stream tail = %v, want EOF", err)
	}
}

func TestPeerStreamWriterCancellationBeforeAdmission(t *testing.T) {
	stop := make(chan struct{})
	stream := newBlockingPersistentStream()
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		return stream, nil
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	firstDone := sendPersistentAsync(writer, context.Background(), []byte("first"))
	<-stream.writeStarted
	secondDone := sendPersistentAsync(writer, context.Background(), []byte("second"))
	waitForPersistentQueueLength(t, writer, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writer.send(ctx, []byte("cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled send error = %v", err)
	}
	close(stream.writeRelease)
	assertPersistentSendSuccess(t, <-firstDone, 5, false)
	assertPersistentSendSuccess(t, <-secondDone, 6, true)
}

func TestPeerStreamWriterReportsOpenFailure(t *testing.T) {
	stop := make(chan struct{})
	wantErr := errors.New("open failed")
	writer := newPeerStreamWriter(7, func(_ context.Context, operatorID uint64) (persistentWriteStream, error) {
		if operatorID != 7 {
			return nil, errors.New("wrong operator")
		}
		return nil, wantErr
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	result, err := writer.send(context.Background(), []byte("message"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("send error = %v, want %v", err, wantErr)
	}
	if result.EncodedBytes != 0 || result.StreamReused || writer.ready.Load() {
		t.Fatalf("open failure result=%+v ready=%t", result, writer.ready.Load())
	}
}

func TestPeerStreamWriterResetsFailedWriteWithoutReplay(t *testing.T) {
	stop := make(chan struct{})
	writeErr := errors.New("uncertain write")
	failed := &errorAfterWriteStream{writeErr: writeErr}
	replacement := &capturePersistentStream{}
	var openMu sync.Mutex
	opens := 0
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		openMu.Lock()
		defer openMu.Unlock()
		opens++
		if opens == 1 {
			return failed, nil
		}
		return replacement, nil
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	failedPayload := []byte("failed-message")
	result, err := writer.send(context.Background(), failedPayload)
	if !errors.Is(err, writeErr) || result.EncodedBytes != 0 || !failed.reset {
		t.Fatalf("failed send result=%+v err=%v reset=%t", result, err, failed.reset)
	}
	next, err := writer.send(context.Background(), []byte("next"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: next}, 4, false)

	openMu.Lock()
	openCount := opens
	openMu.Unlock()
	if openCount != 2 {
		t.Fatalf("stream opens = %d, want 2", openCount)
	}
	if count := bytes.Count(failed.bytes(), failedPayload); count != 1 {
		t.Fatalf("failed payload wire occurrences = %d, want exactly one uncertain write", count)
	}
	if bytes.Contains(replacement.bytes(), failedPayload) {
		t.Fatal("failed payload was replayed on replacement stream")
	}
	reader := bufio.NewReader(bytes.NewReader(replacement.bytes()))
	got, err := readEnvelopeFrame(reader, 64)
	if err != nil || string(got) != "next" {
		t.Fatalf("replacement frame = %q, %v", got, err)
	}
}

func TestPeerStreamWriterCancellationDuringWriteResetsStream(t *testing.T) {
	stop := make(chan struct{})
	stream := newDeadlinePersistentStream()
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		return stream, nil
	}, stop)
	t.Cleanup(func() { stopPeerStreamWriter(t, stop, writer) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := writer.send(ctx, []byte("blocked"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked send error = %v, want deadline exceeded", err)
	}
	select {
	case <-stream.resetDone:
	case <-time.After(time.Second):
		t.Fatal("deadline failure did not reset the stream")
	}
	if writer.ready.Load() {
		t.Fatal("writer stayed ready after deadline failure")
	}
}

func TestPeerStreamWriterShutdownFailsActiveAndQueuedSends(t *testing.T) {
	stop := make(chan struct{})
	stream := newBlockingPersistentStream()
	writer := newPeerStreamWriter(1, func(context.Context, uint64) (persistentWriteStream, error) {
		return stream, nil
	}, stop)

	active := sendPersistentAsync(writer, context.Background(), []byte("active"))
	<-stream.writeStarted
	queued := sendPersistentAsync(writer, context.Background(), []byte("queued"))
	waitForPersistentQueueLength(t, writer, 1)
	close(stop)

	for name, result := range map[string]<-chan persistentSendCompletion{"active": active, "queued": queued} {
		select {
		case completion := <-result:
			if !errors.Is(completion.err, errPersistentWriterClosed) {
				t.Fatalf("%s error = %v, want writer closed", name, completion.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s caller did not return on shutdown", name)
		}
	}
	close(stream.writeRelease)
	select {
	case <-writer.done:
	case <-time.After(time.Second):
		t.Fatal("writer goroutine did not stop")
	}
	if !stream.reset {
		t.Fatal("shutdown did not reset the owned stream")
	}
	reader := bufio.NewReader(bytes.NewReader(stream.bytes()))
	got, err := readEnvelopeFrame(reader, 64)
	if err != nil || string(got) != "active" {
		t.Fatalf("active frame = %q, %v", got, err)
	}
	if _, err := readEnvelopeFrame(reader, 64); !errors.Is(err, io.EOF) {
		t.Fatalf("queued frame was written: %v", err)
	}
}

func sendPersistentAsync(writer *peerStreamWriter, ctx context.Context, payload []byte) <-chan persistentSendCompletion {
	done := make(chan persistentSendCompletion, 1)
	go func() {
		result, err := writer.send(ctx, payload)
		done <- persistentSendCompletion{result: result, err: err}
	}()
	return done
}

func assertPersistentSendSuccess(t *testing.T, completion persistentSendCompletion, encodedBytes int, reused bool) {
	t.Helper()
	if completion.err != nil {
		t.Fatal(completion.err)
	}
	result := completion.result
	if result.EncodedBytes != encodedBytes || result.StreamReused != reused || result.FinalizeDuration != 0 {
		t.Fatalf("send result = %+v, want bytes=%d reused=%t", result, encodedBytes, reused)
	}
	if result.QueueWaitDuration < 0 || result.StreamOpenDuration < 0 || result.WriteDuration <= 0 {
		t.Fatalf("invalid phase timings: %+v", result)
	}
}

func waitForPersistentQueueLength(t *testing.T, writer *peerStreamWriter, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(writer.queue) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue length = %d, want %d", len(writer.queue), want)
}

func stopPeerStreamWriter(t *testing.T, stop chan struct{}, writer *peerStreamWriter) {
	t.Helper()
	select {
	case <-stop:
	default:
		close(stop)
	}
	select {
	case <-writer.done:
	case <-time.After(time.Second):
		t.Fatal("writer goroutine did not stop")
	}
}

type blockingPersistentStream struct {
	mu           sync.Mutex
	buffer       bytes.Buffer
	writeStarted chan struct{}
	writeRelease chan struct{}
	writeOnce    sync.Once
	reset        bool
}

func newBlockingPersistentStream() *blockingPersistentStream {
	return &blockingPersistentStream{writeStarted: make(chan struct{}), writeRelease: make(chan struct{})}
}

func (s *blockingPersistentStream) Write(data []byte) (int, error) {
	s.writeOnce.Do(func() {
		close(s.writeStarted)
		<-s.writeRelease
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(data)
}

func (*blockingPersistentStream) Close() error { return nil }
func (s *blockingPersistentStream) Reset() error {
	s.mu.Lock()
	s.reset = true
	s.mu.Unlock()
	return nil
}
func (*blockingPersistentStream) SetWriteDeadline(time.Time) error { return nil }
func (s *blockingPersistentStream) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buffer.Bytes())
}

type capturePersistentStream struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	reset  bool
}

type negotiatingPersistentStream struct {
	capturePersistentStream
	negotiated atomic.Bool
}

func (s *negotiatingPersistentStream) Read([]byte) (int, error) {
	s.negotiated.Store(true)
	return 0, nil
}

func (*negotiatingPersistentStream) SetReadDeadline(time.Time) error { return nil }

func (s *capturePersistentStream) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(data)
}
func (*capturePersistentStream) Close() error { return nil }
func (s *capturePersistentStream) Reset() error {
	s.mu.Lock()
	s.reset = true
	s.mu.Unlock()
	return nil
}
func (*capturePersistentStream) SetWriteDeadline(time.Time) error { return nil }
func (s *capturePersistentStream) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buffer.Bytes())
}

type errorAfterWriteStream struct {
	capturePersistentStream
	writeErr error
	writes   int
}

func (s *errorAfterWriteStream) Write(data []byte) (int, error) {
	s.capturePersistentStream.mu.Lock()
	defer s.capturePersistentStream.mu.Unlock()
	n, _ := s.capturePersistentStream.buffer.Write(data)
	s.writes++
	if s.writes == 2 {
		return n, s.writeErr
	}
	return n, nil
}

type deadlinePersistentStream struct {
	mu        sync.Mutex
	deadline  time.Time
	resetOnce sync.Once
	resetDone chan struct{}
}

func newDeadlinePersistentStream() *deadlinePersistentStream {
	return &deadlinePersistentStream{resetDone: make(chan struct{})}
}

func (s *deadlinePersistentStream) Write([]byte) (int, error) {
	s.mu.Lock()
	deadline := s.deadline
	s.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	return 0, context.DeadlineExceeded
}
func (*deadlinePersistentStream) Close() error { return nil }
func (s *deadlinePersistentStream) Reset() error {
	s.resetOnce.Do(func() { close(s.resetDone) })
	return nil
}
func (s *deadlinePersistentStream) SetWriteDeadline(deadline time.Time) error {
	s.mu.Lock()
	s.deadline = deadline
	s.mu.Unlock()
	return nil
}
