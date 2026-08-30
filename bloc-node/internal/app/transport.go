package app

import (
	"context"
	"time"
)

const defaultTransportSendTimeout = 10 * time.Second

type transportSendResult struct {
	EncodedBytes       int
	EncodeDuration     time.Duration
	QueueWaitDuration  time.Duration
	StreamOpenDuration time.Duration
	WriteDuration      time.Duration
	FinalizeDuration   time.Duration
	StreamReused       bool
}

// EnvelopeHandler receives decoded transport messages with the encoded byte
// size used for per-kind bandwidth metrics.
type EnvelopeHandler func(WireEnvelope, int)

// Transport is the node-to-node networking boundary. Implementations decode
// inbound envelopes, preserve direct-message addressing, and return encoded
// sizes for successful sends.
type Transport interface {
	Start(context.Context, EnvelopeHandler) error
	Send(context.Context, uint64, WireEnvelope) (transportSendResult, error)
	Close() error
}

func withTransportSendDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, defaultTransportSendTimeout)
}
