package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const blocEnvelopeProtocol = protocol.ID("/bloc/envelope/1.0.0")

// LibP2PTransport is the experimental P2P transport. Direct ACS/share messages
// use libp2p streams, while GossipSub topics are prepared for future broadcast
// message types.
type LibP2PTransport struct {
	node   *Node
	codec  EnvelopeCodec
	host   host.Host
	ps     *pubsub.PubSub
	topics map[string]*pubsub.Topic
	mu     sync.Mutex
}

func newLibP2PTransport(node *Node, codec EnvelopeCodec) *LibP2PTransport {
	return &LibP2PTransport{node: node, codec: codec, topics: make(map[string]*pubsub.Topic)}
}

// Start creates the libp2p host, installs the direct-envelope stream handler,
// subscribes to slot-scoped topics, and begins static peer connection retries.
func (t *LibP2PTransport) Start(ctx context.Context, handler EnvelopeHandler) error {
	priv, err := unmarshalLibP2PPrivateKey(t.node.self.P2PPrivKeyHex)
	if err != nil {
		return err
	}
	opts := []libp2p.Option{libp2p.ListenAddrStrings(t.node.self.P2PAddr)}
	if priv != nil {
		opts = append(opts, libp2p.Identity(priv))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return err
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		_ = h.Close()
		return err
	}
	t.host = h
	t.ps = ps
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
	log.Printf("node %d libp2p peer %s listening on %v", t.node.self.ID, h.ID(), h.Addrs())
	for _, kind := range []string{"acs", "share"} {
		if err := t.subscribe(ctx, kind, handler); err != nil {
			_ = h.Close()
			return err
		}
	}
	t.connectStaticPeers(ctx)
	return nil
}

func (t *LibP2PTransport) subscribe(ctx context.Context, kind string, handler EnvelopeHandler) error {
	topic, err := t.topic(kind)
	if err != nil {
		return err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return err
	}
	go func() {
		defer sub.Cancel()
		for {
			msg, err := sub.Next(ctx)
			if err != nil {
				return
			}
			if msg.ReceivedFrom == t.host.ID() {
				continue
			}
			env, err := t.codec.Decode(msg.Data)
			if err != nil {
				log.Printf("decode libp2p envelope: %v", err)
				continue
			}
			if env.From == t.node.self.ID {
				continue
			}
			if env.Direct && env.To != t.node.self.ID {
				continue
			}
			handler(env, len(msg.Data))
		}
	}()
	return nil
}

// Send uses streams for addressed direct envelopes and GossipSub for future
// broadcast envelopes.
func (t *LibP2PTransport) Send(ctx context.Context, _ uint64, env WireEnvelope) (int, error) {
	data, err := t.codec.Encode(env)
	if err != nil {
		return 0, err
	}
	if env.Direct {
		if err := t.sendStream(ctx, env.To, data); err != nil {
			return 0, err
		}
		return len(data), nil
	}
	topic, err := t.topic(env.Kind)
	if err != nil {
		return 0, err
	}
	if err := topic.Publish(ctx, data); err != nil {
		return 0, err
	}
	return len(data), nil
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
	_, err = s.Write(data)
	return err
}

// Close closes the libp2p host and all associated network resources.
func (t *LibP2PTransport) Close() error {
	if t.host != nil {
		return t.host.Close()
	}
	return nil
}

func (t *LibP2PTransport) topic(kind string) (*pubsub.Topic, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if topic := t.topics[kind]; topic != nil {
		return topic, nil
	}
	topic, err := t.ps.Join(fmt.Sprintf("bloc/%s/%d/%s", t.node.cfg.ClusterID, t.node.cfg.Slot, kind))
	if err != nil {
		return nil, err
	}
	t.topics[kind] = topic
	return topic, nil
}

func (t *LibP2PTransport) connectStaticPeers(ctx context.Context) {
	for _, cfg := range t.node.peers {
		if cfg.ID == t.node.self.ID || cfg.P2PAddr == "" || cfg.P2PPeerID == "" {
			continue
		}
		go t.connectPeerWithRetry(ctx, cfg)
	}
}

func (t *LibP2PTransport) connectPeerWithRetry(ctx context.Context, cfg NodeConfig) {
	fullAddr := cfg.P2PAddr + "/p2p/" + cfg.P2PPeerID
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
	for attempt := 0; attempt < 50; attempt++ {
		if ctx.Err() != nil {
			return
		}
		if err := t.host.Connect(ctx, *info); err == nil {
			log.Printf("node %d connected to libp2p peer %d (%s)", t.node.self.ID, cfg.ID, cfg.P2PPeerID)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("node %d could not connect to libp2p peer %d (%s)", t.node.self.ID, cfg.ID, cfg.P2PPeerID)
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
