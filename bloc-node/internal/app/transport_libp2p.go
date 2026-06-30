package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const blocEnvelopeProtocol = protocol.ID("/bloc/envelope/1.0.0")

// LibP2PTransport carries addressed ACS and share messages over authenticated,
// multiplexed libp2p streams.
type LibP2PTransport struct {
	node  *Node
	codec EnvelopeCodec
	host  host.Host
}

func newLibP2PTransport(node *Node, codec EnvelopeCodec) *LibP2PTransport {
	return &LibP2PTransport{node: node, codec: codec}
}

// Start creates the libp2p host, installs the direct-envelope stream handler,
// and begins static peer connection retries.
func (t *LibP2PTransport) Start(ctx context.Context, handler EnvelopeHandler) error {
	priv, err := unmarshalLibP2PPrivateKey(t.node.self.P2PPrivKeyHex)
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
	h.SetStreamHandler(blocEnvelopeProtocol, func(s network.Stream) {
		defer s.Close()
		data, err := io.ReadAll(s)
		if err != nil {
			log.Printf("read libp2p stream: %v", err)
			return
		}
		env, err := t.codec.Decode(data)
		if err != nil {
			log.Printf("decode libp2p stream envelope: %v", err)
			return
		}
		if env.From == t.node.self.ID {
			return
		}
		if env.Direct && env.To != t.node.self.ID {
			return
		}
		handler(env, len(data))
	})
	log.Printf("event=libp2p_listen node_id=%d peer_id=%s listen_addrs=%v advertise_addr=%s", t.node.self.ID, h.ID(), h.Addrs(), t.node.self.p2pAdvertiseAddr())
	t.connectStaticPeers(ctx)
	return nil
}

// Send writes one addressed envelope to a fresh logical stream. libp2p
// multiplexes these streams over persistent peer connections.
func (t *LibP2PTransport) Send(ctx context.Context, _ uint64, env WireEnvelope) (int, error) {
	data, err := t.codec.Encode(env)
	if err != nil {
		return 0, err
	}
	if !env.Direct {
		return 0, fmt.Errorf("libp2p transport requires an addressed direct envelope")
	}
	if err := t.sendStream(ctx, env.To, data); err != nil {
		return 0, err
	}
	return len(data), nil
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
	}
	return true
}

func (t *LibP2PTransport) sendStream(ctx context.Context, to uint64, data []byte) error {
	cfg, ok := t.node.peers[to]
	if !ok {
		return fmt.Errorf("unknown peer %d", to)
	}
	id, err := peer.Decode(cfg.P2PPeerID)
	if err != nil {
		return err
	}
	s, err := t.host.NewStream(ctx, id, blocEnvelopeProtocol)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := writeAll(s, data); err != nil {
		return err
	}
	return s.CloseWrite()
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

// Close closes the libp2p host and all associated network resources.
func (t *LibP2PTransport) Close() error {
	if t.host != nil {
		return t.host.Close()
	}
	return nil
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
