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
