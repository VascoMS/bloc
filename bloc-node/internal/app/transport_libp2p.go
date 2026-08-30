package app

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	blocEnvelopeProtocolFresh      = protocol.ID("/bloc/envelope/1.0.0")
	blocEnvelopeProtocolPersistent = protocol.ID("/bloc/envelope/2.0.0")
	persistentInboundQueueCapacity = 32
)

var errEnvelopeTooLarge = errors.New("protocol envelope exceeds configured maximum")

// LibP2PTransport carries addressed ACS and share messages over authenticated,
// multiplexed libp2p streams.
type LibP2PTransport struct {
	node          *Node
	codec         EnvelopeCodec
	host          host.Host
	peerOperators map[peer.ID]uint64
	openStream    func(context.Context, uint64) (outboundStream, error)
	handler       EnvelopeHandler

	persistentWriters map[uint64]*peerStreamWriter
	persistentStop    chan struct{}
	lifecycleCancel   context.CancelFunc
	lifecycleMu       sync.Mutex
	closing           bool
	closeOnce         sync.Once
	closeErr          error
	prewarmWG         sync.WaitGroup
	inboundWG         sync.WaitGroup
}

type outboundStream interface {
	io.Writer
	CloseWrite() error
	Close() error
	Reset() error
	SetWriteDeadline(time.Time) error
}

func newLibP2PTransport(node *Node, codec EnvelopeCodec) *LibP2PTransport {
	return &LibP2PTransport{
		node:              node,
		codec:             codec,
		persistentWriters: make(map[uint64]*peerStreamWriter),
		persistentStop:    make(chan struct{}),
	}
}

// Start creates the libp2p host, installs the direct-envelope stream handler,
// and begins static peer connection retries.
func (t *LibP2PTransport) Start(ctx context.Context, handler EnvelopeHandler) error {
	peerOperators, err := operatorByPeerID(t.node.peers)
	if err != nil {
		return err
	}
	t.peerOperators = peerOperators
	priv, err := unmarshalLibP2PPrivateKey(t.node.p2pPrivateKeyHex)
	if err != nil {
		return err
	}
	opts := []libp2p.Option{libp2p.ListenAddrStrings(t.node.self.p2pListenAddr())}
	if priv != nil {
		opts = append(opts, libp2p.Identity(priv))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return err
	}
	t.host = h
	t.handler = handler
	lifecycleCtx, lifecycleCancel := context.WithCancel(ctx)
	t.lifecycleMu.Lock()
	t.lifecycleCancel = lifecycleCancel
	t.lifecycleMu.Unlock()
	switch t.node.cfg.Network.StreamMode {
	case "", streamModeFresh:
		h.SetStreamHandler(blocEnvelopeProtocolFresh, func(stream network.Stream) {
			if !t.beginInboundHandler() {
				_ = stream.Reset()
				return
			}
			defer t.inboundWG.Done()
			t.handleFreshStream(stream)
		})
	case streamModePersistent:
		h.SetStreamHandler(blocEnvelopeProtocolPersistent, func(stream network.Stream) {
			if !t.beginInboundHandler() {
				_ = stream.Reset()
				return
			}
			defer t.inboundWG.Done()
			t.handlePersistentStream(stream)
		})
		t.startPersistentWriters(lifecycleCtx)
	default:
		lifecycleCancel()
		_ = h.Close()
		t.host = nil
		return fmt.Errorf("unsupported libp2p stream mode %q", t.node.cfg.Network.StreamMode)
	}
	log.Printf("event=libp2p_listen node_id=%d peer_id=%s listen_addrs=%v advertise_addr=%s", t.node.self.ID, h.ID(), h.Addrs(), t.node.self.p2pAdvertiseAddr())
	t.connectStaticPeers(lifecycleCtx)
	return nil
}

func (t *LibP2PTransport) beginInboundHandler() bool {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.closing {
		return false
	}
	t.inboundWG.Add(1)
	return true
}

func (t *LibP2PTransport) handleFreshStream(stream network.Stream) {
	defer stream.Close()
	remotePeer := stream.Conn().RemotePeer()
	operatorID, known := t.peerOperators[remotePeer]
	if !known {
		log.Printf("reject libp2p stream from unconfigured peer_id=%s", remotePeer)
		t.node.recordProtocolRejected("inbound", "authentication")
		return
	}
	data, err := readBoundedEnvelope(stream, t.node.cfg.Limits.MaxEnvelopeBytes)
	if err != nil {
		if errors.Is(err, errEnvelopeTooLarge) {
			t.node.recordProtocolRejected("inbound", "oversize")
			_ = stream.Reset()
			return
		}
		t.node.recordProtocolRejected("inbound", "decode")
		log.Printf("read libp2p stream: %v", err)
		return
	}
	envelope, err := t.codec.Decode(data)
	if err != nil {
		t.node.recordProtocolRejected("inbound", "decode")
		log.Printf("decode libp2p stream envelope: %v", err)
		return
	}
	if err := validateEnvelopePayload(envelope); err != nil {
		t.node.recordProtocolRejected("inbound", "payload")
		log.Printf("reject libp2p envelope peer_id=%s: %v", remotePeer, err)
		return
	}
	if err := validateAuthenticatedEnvelope(operatorID, t.node.self.ID, envelope); err != nil {
		t.node.recordProtocolRejected("inbound", "authentication")
		log.Printf("reject libp2p envelope peer_id=%s: %v", remotePeer, err)
		return
	}
	t.handler(envelope, len(data))
}

func (t *LibP2PTransport) handlePersistentStream(stream network.Stream) {
	defer stream.Close()
	remotePeer := stream.Conn().RemotePeer()
	operatorID, known := t.peerOperators[remotePeer]
	if !known {
		t.rejectPersistentStream(stream, "authentication", fmt.Errorf("unconfigured peer_id=%s", remotePeer))
		return
	}
	type inboundEnvelope struct {
		envelope     WireEnvelope
		encodedBytes int
	}
	deliveries := make(chan inboundEnvelope, persistentInboundQueueCapacity)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		for delivery := range deliveries {
			t.handler(delivery.envelope, delivery.encodedBytes)
		}
	}()
	defer func() {
		close(deliveries)
		<-dispatchDone
	}()
	reader := bufio.NewReader(stream)
	for {
		data, err := readEnvelopeFrame(reader, t.node.cfg.Limits.MaxEnvelopeBytes)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			reason := "decode"
			if errors.Is(err, errEnvelopeTooLarge) {
				reason = "oversize"
			}
			t.rejectPersistentStream(stream, reason, err)
			return
		}
		envelope, err := t.codec.Decode(data)
		if err != nil {
			t.rejectPersistentStream(stream, "decode", err)
			return
		}
		if err := validateEnvelopePayload(envelope); err != nil {
			t.rejectPersistentStream(stream, "payload", err)
			return
		}
		if err := validateAuthenticatedEnvelope(operatorID, t.node.self.ID, envelope); err != nil {
			t.rejectPersistentStream(stream, "authentication", err)
			return
		}
		select {
		case deliveries <- inboundEnvelope{envelope: envelope, encodedBytes: len(data)}:
		case <-t.persistentStop:
			return
		}
	}
}

func (t *LibP2PTransport) rejectPersistentStream(stream network.Stream, reason string, err error) {
	t.node.recordProtocolRejected("inbound", reason)
	log.Printf("reject persistent libp2p stream peer_id=%s reason=%s: %v", stream.Conn().RemotePeer(), reason, err)
	_ = stream.Reset()
}

func readBoundedEnvelope(reader io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("invalid maximum envelope size %d", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("%w: got more than %d bytes", errEnvelopeTooLarge, maxBytes)
	}
	return data, nil
}

func validateEnvelopePayload(env WireEnvelope) error {
	switch env.Kind {
	case "acs":
		if env.ACS == nil || env.Share != nil {
			return fmt.Errorf("acs envelope has invalid payload")
		}
	case "share":
		if env.Share == nil || env.ACS != nil {
			return fmt.Errorf("share envelope has invalid payload")
		}
	default:
		return fmt.Errorf("unsupported envelope kind %q", env.Kind)
	}
	return nil
}

func validateAuthenticatedEnvelope(operatorID, selfID uint64, env WireEnvelope) error {
	if env.From != operatorID {
		return fmt.Errorf("authenticated operator=%d asserted_from=%d", operatorID, env.From)
	}
	if !env.Direct || env.To != selfID {
		return fmt.Errorf("authenticated operator=%d direct=%t to=%d expected_to=%d", operatorID, env.Direct, env.To, selfID)
	}
	if env.Share != nil && env.Share.OperatorID != int(operatorID) {
		return fmt.Errorf("authenticated operator=%d asserted_share_operator=%d", operatorID, env.Share.OperatorID)
	}
	return nil
}

// Send writes one addressed envelope to a fresh logical stream. libp2p
// multiplexes these streams over persistent peer connections.
func (t *LibP2PTransport) Send(ctx context.Context, to uint64, env WireEnvelope) (transportSendResult, error) {
	var result transportSendResult
	if _, ok := t.node.peers[to]; !ok {
		return result, fmt.Errorf("unknown peer %d", to)
	}
	ctx, cancel := withTransportSendDeadline(ctx)
	defer cancel()
	env = authenticatedOutboundEnvelope(t.node.self.ID, to, env)
	started := time.Now()
	data, err := t.codec.Encode(env)
	result.EncodeDuration = time.Since(started)
	if err != nil {
		t.node.recordProtocolRejected("outbound", "payload")
		return result, err
	}
	if len(data) > t.node.cfg.Limits.MaxEnvelopeBytes {
		t.node.recordProtocolRejected("outbound", "oversize")
		return result, fmt.Errorf("%w: encoded %d bytes, maximum %d", errEnvelopeTooLarge, len(data), t.node.cfg.Limits.MaxEnvelopeBytes)
	}
	var streamResult transportSendResult
	if t.node.cfg.Network.StreamMode == streamModePersistent {
		writer, ok := t.persistentWriters[to]
		if !ok {
			return result, fmt.Errorf("persistent stream writer for peer %d is not running", to)
		}
		streamResult, err = writer.send(ctx, data)
	} else {
		streamResult, err = t.sendStream(ctx, to, data)
	}
	result.QueueWaitDuration = streamResult.QueueWaitDuration
	result.StreamOpenDuration = streamResult.StreamOpenDuration
	result.WriteDuration = streamResult.WriteDuration
	result.FinalizeDuration = streamResult.FinalizeDuration
	result.StreamReused = streamResult.StreamReused
	if err != nil {
		return result, err
	}
	result.EncodedBytes = len(data)
	return result, nil
}

func authenticatedOutboundEnvelope(from, to uint64, env WireEnvelope) WireEnvelope {
	env.From = from
	env.To = to
	env.Direct = true
	return env
}

func operatorByPeerID(peers map[uint64]NodeConfig) (map[peer.ID]uint64, error) {
	operators := make(map[peer.ID]uint64, len(peers))
	for operatorID, cfg := range peers {
		if cfg.P2PPeerID == "" {
			return nil, fmt.Errorf("operator %d has no configured libp2p peer id", operatorID)
		}
		peerID, err := peer.Decode(cfg.P2PPeerID)
		if err != nil {
			return nil, fmt.Errorf("decode libp2p peer id for operator %d: %w", operatorID, err)
		}
		if previous, duplicate := operators[peerID]; duplicate {
			return nil, fmt.Errorf("libp2p peer id %s is assigned to operators %d and %d", peerID, previous, operatorID)
		}
		operators[peerID] = operatorID
	}
	return operators, nil
}

// Ready reports whether this host has a direct connection to every configured
// peer. Evaluator runs use it as a startup barrier so connection backoff is not
// mistaken for protocol latency or allowed to drop initial ACS messages.
func (t *LibP2PTransport) Ready() bool {
	if t.host == nil {
		return false
	}
	for _, cfg := range t.node.peers {
		if cfg.ID == t.node.self.ID {
			continue
		}
		id, err := peer.Decode(cfg.P2PPeerID)
		if err != nil || t.host.Network().Connectedness(id) != network.Connected {
			return false
		}
		if t.node.cfg.Network.StreamMode == streamModePersistent {
			supported, err := t.host.Peerstore().SupportsProtocols(id, blocEnvelopeProtocolPersistent)
			if err != nil || len(supported) == 0 {
				return false
			}
			writer, ok := t.persistentWriters[cfg.ID]
			if !ok || !writer.ready.Load() {
				return false
			}
		}
	}
	return true
}

func (t *LibP2PTransport) sendStream(ctx context.Context, to uint64, data []byte) (transportSendResult, error) {
	var result transportSendResult
	started := time.Now()
	s, err := t.openOutboundStream(ctx, to)
	result.StreamOpenDuration = time.Since(started)
	if err != nil {
		return result, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := s.SetWriteDeadline(deadline); err != nil {
			_ = s.Reset()
			return result, err
		}
	}
	started = time.Now()
	err = writeAll(s, data)
	result.WriteDuration = time.Since(started)
	if err != nil {
		_ = s.Reset()
		return result, err
	}
	started = time.Now()
	if err := s.CloseWrite(); err != nil {
		result.FinalizeDuration = time.Since(started)
		_ = s.Reset()
		return result, err
	}
	if err := s.Close(); err != nil {
		result.FinalizeDuration = time.Since(started)
		_ = s.Reset()
		return result, err
	}
	result.FinalizeDuration = time.Since(started)
	return result, nil
}

func (t *LibP2PTransport) openOutboundStream(ctx context.Context, to uint64) (outboundStream, error) {
	if t.openStream != nil {
		return t.openStream(ctx, to)
	}
	cfg, ok := t.node.peers[to]
	if !ok {
		return nil, fmt.Errorf("unknown peer %d", to)
	}
	id, err := peer.Decode(cfg.P2PPeerID)
	if err != nil {
		return nil, err
	}
	if t.host == nil {
		return nil, errors.New("libp2p transport is not started")
	}
	return t.host.NewStream(ctx, id, blocEnvelopeProtocolFresh)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// Close stops transport admission and waits for persistent workers and inbound
// handlers before returning.
func (t *LibP2PTransport) Close() error {
	t.closeOnce.Do(func() {
		t.lifecycleMu.Lock()
		t.closing = true
		if t.lifecycleCancel != nil {
			t.lifecycleCancel()
		}
		close(t.persistentStop)
		t.lifecycleMu.Unlock()
		if t.host != nil {
			t.closeErr = t.host.Close()
		}
		for _, writer := range t.persistentWriters {
			<-writer.done
		}
		t.prewarmWG.Wait()
		t.inboundWG.Wait()
	})
	return t.closeErr
}

func (t *LibP2PTransport) startPersistentWriters(ctx context.Context) {
	for operatorID := range t.node.peers {
		if operatorID == t.node.self.ID {
			continue
		}
		writer := newPeerStreamWriter(operatorID, t.openPersistentOutboundStream, t.persistentStop)
		writer.prewarmOpen = t.openPersistentOutboundStreamWithoutDial
		t.persistentWriters[operatorID] = writer
		t.prewarmWG.Add(1)
		go func(operatorID uint64, writer *peerStreamWriter) {
			defer t.prewarmWG.Done()
			t.prewarmPersistentWriter(ctx, operatorID, writer)
		}(operatorID, writer)
	}
}

func (t *LibP2PTransport) prewarmPersistentWriter(ctx context.Context, operatorID uint64, writer *peerStreamWriter) {
	peerConfig := t.node.peers[operatorID]
	peerID, err := peer.Decode(peerConfig.P2PPeerID)
	if err != nil {
		return
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		if t.host.Network().Connectedness(peerID) == network.Connected {
			supported, supportsErr := t.host.Peerstore().SupportsProtocols(peerID, blocEnvelopeProtocolPersistent)
			if supportsErr == nil && len(supported) > 0 {
				openCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = writer.prewarmStream(openCtx)
				cancel()
				if err == nil {
					log.Printf("event=libp2p_persistent_stream_ready node_id=%d peer_id=%d", t.node.self.ID, operatorID)
					return
				}
			}
		}
		if err != nil && (attempt == 1 || attempt%25 == 0) {
			log.Printf("node %d retrying persistent stream to peer %d after attempt %d: %v", t.node.self.ID, operatorID, attempt, err)
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (t *LibP2PTransport) openPersistentOutboundStream(ctx context.Context, to uint64) (persistentWriteStream, error) {
	return t.openPersistentOutboundStreamWithContext(ctx, to)
}

func (t *LibP2PTransport) openPersistentOutboundStreamWithoutDial(ctx context.Context, to uint64) (persistentWriteStream, error) {
	return t.openPersistentOutboundStreamWithContext(network.WithNoDial(ctx, "bloc persistent stream prewarm"), to)
}

func (t *LibP2PTransport) openPersistentOutboundStreamWithContext(ctx context.Context, to uint64) (persistentWriteStream, error) {
	config, ok := t.node.peers[to]
	if !ok {
		return nil, fmt.Errorf("unknown peer %d", to)
	}
	peerID, err := peer.Decode(config.P2PPeerID)
	if err != nil {
		return nil, err
	}
	if t.host == nil {
		return nil, errors.New("libp2p transport is not started")
	}
	return t.host.NewStream(ctx, peerID, blocEnvelopeProtocolPersistent)
}

func (t *LibP2PTransport) connectStaticPeers(ctx context.Context) {
	for _, cfg := range t.node.peers {
		if cfg.ID <= t.node.self.ID || cfg.p2pAdvertiseAddr() == "" || cfg.P2PPeerID == "" {
			continue
		}
		go t.connectPeerWithRetry(ctx, cfg)
	}
}

func (t *LibP2PTransport) connectPeerWithRetry(ctx context.Context, cfg NodeConfig) {
	fullAddr := cfg.p2pAdvertiseAddr() + "/p2p/" + cfg.P2PPeerID
	addr, err := ma.NewMultiaddr(fullAddr)
	if err != nil {
		log.Printf("parse peer %d multiaddr %q: %v", cfg.ID, fullAddr, err)
		return
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		log.Printf("parse peer %d addr info: %v", cfg.ID, err)
		return
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		dialCtx, cancel := context.WithTimeout(network.WithForceDirectDial(ctx, "bloc static peer readiness"), 2*time.Second)
		err := t.host.Connect(dialCtx, *info)
		cancel()
		if err == nil {
			log.Printf("node %d connected to libp2p peer %d (%s)", t.node.self.ID, cfg.ID, cfg.P2PPeerID)
			return
		}
		if attempt == 1 || attempt%25 == 0 {
			log.Printf("node %d retrying libp2p peer %d after attempt %d: %v", t.node.self.ID, cfg.ID, attempt, err)
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func unmarshalLibP2PPrivateKey(raw string) (crypto.PrivKey, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	return crypto.UnmarshalPrivateKey(data)
}
