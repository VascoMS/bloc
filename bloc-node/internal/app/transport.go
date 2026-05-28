package app

import "context"

// EnvelopeHandler receives decoded transport messages with the encoded byte
// size used for per-kind bandwidth metrics.
type EnvelopeHandler func(WireEnvelope, int)

// Transport is the node-to-node networking boundary. Implementations decode
// inbound envelopes, preserve direct-message addressing, and return encoded
// sizes for successful sends.
type Transport interface {
	Start(context.Context, EnvelopeHandler) error
	Send(context.Context, uint64, WireEnvelope) (int, error)
	Close() error
}

// newTransport selects the configured network backend. Unknown modes fall back
// to TCP so older configs remain runnable during local experiments.
func newTransport(node *Node) Transport {
	switch node.cfg.Network.Mode {
	case "libp2p":
		return newLibP2PTransport(node, ProtoEnvelopeCodec{})
	case "", "tcp":
		return newTCPTransport(node, GobEnvelopeCodec{})
	default:
		return newTCPTransport(node, GobEnvelopeCodec{})
	}
}
