package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stagedOutboundStream struct {
	writeStarted      chan struct{}
	writeRelease      chan struct{}
	closeWriteStarted chan struct{}
	closeWriteRelease chan struct{}
	closeStarted      chan struct{}
	closeRelease      chan struct{}
	written           []byte
	deadline          time.Time
	reset             bool
}

type transportTestCompletion struct {
	result transportSendResult
	err    error
}

func newStagedOutboundStream() *stagedOutboundStream {
	return &stagedOutboundStream{
		writeStarted:      make(chan struct{}),
		writeRelease:      make(chan struct{}),
		closeWriteStarted: make(chan struct{}),
		closeWriteRelease: make(chan struct{}),
		closeStarted:      make(chan struct{}),
		closeRelease:      make(chan struct{}),
	}
}

func (s *stagedOutboundStream) Write(data []byte) (int, error) {
	close(s.writeStarted)
	<-s.writeRelease
	s.written = append(s.written, data...)
	return len(data), nil
}

func (s *stagedOutboundStream) CloseWrite() error {
	close(s.closeWriteStarted)
	<-s.closeWriteRelease
	return nil
}

func (s *stagedOutboundStream) Close() error {
	close(s.closeStarted)
	<-s.closeRelease
	return nil
}

func (s *stagedOutboundStream) Reset() error {
	s.reset = true
	return nil
}

func (s *stagedOutboundStream) SetWriteDeadline(deadline time.Time) error {
	s.deadline = deadline
	return nil
}

func TestFreshTransportSeparatesSendPhases(t *testing.T) {
	node := &Node{
		cfg:   ConfigFile{Limits: ResourceLimits{MaxEnvelopeBytes: 1024}},
		self:  NodeConfig{ID: 0},
		peers: map[uint64]NodeConfig{1: {ID: 1}},
	}
	stream := newStagedOutboundStream()
	openStarted := make(chan struct{})
	openRelease := make(chan struct{})
	transport := newLibP2PTransport(node, fixedEnvelopeCodec{encoded: []byte("abc")})
	transport.openStream = func(ctx context.Context, operatorID uint64) (outboundStream, error) {
		if operatorID != 1 {
			t.Fatalf("open operator = %d, want 1", operatorID)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > defaultTransportSendTimeout {
			t.Fatalf("effective deadline = %v, ok=%t", deadline, ok)
		}
		close(openStarted)
		<-openRelease
		return stream, nil
	}

	done := make(chan transportTestCompletion, 1)
	go func() {
		result, err := transport.Send(context.Background(), 1, WireEnvelope{})
		done <- transportTestCompletion{result: result, err: err}
	}()

	<-openStarted
	assertTransportStillSending(t, done, "stream open")
	close(openRelease)
	<-stream.writeStarted
	assertTransportStillSending(t, done, "write")
	close(stream.writeRelease)
	<-stream.closeWriteStarted
	assertTransportStillSending(t, done, "close write")
	close(stream.closeWriteRelease)
	<-stream.closeStarted
	assertTransportStillSending(t, done, "close")
	close(stream.closeRelease)

	completed := <-done
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	result := completed.result
	if result.EncodedBytes != 3 || result.QueueWaitDuration != 0 || result.StreamReused {
		t.Fatalf("fresh result = %+v", result)
	}
	if result.EncodeDuration <= 0 || result.StreamOpenDuration <= 0 || result.WriteDuration <= 0 || result.FinalizeDuration <= 0 {
		t.Fatalf("phase timings were not separated: %+v", result)
	}
	if string(stream.written) != "abc" || stream.deadline.IsZero() || stream.reset {
		t.Fatalf("stream state: written=%q deadline=%v reset=%t", stream.written, stream.deadline, stream.reset)
	}
}

func TestFreshTransportReturnsPartialPhasesOnWriteFailure(t *testing.T) {
	node := &Node{
		cfg:   ConfigFile{Limits: ResourceLimits{MaxEnvelopeBytes: 1024}},
		self:  NodeConfig{ID: 0},
		peers: map[uint64]NodeConfig{1: {ID: 1}},
	}
	wantErr := errors.New("write failed")
	stream := &failingOutboundStream{writeErr: wantErr, writeDelay: time.Millisecond}
	transport := newLibP2PTransport(node, fixedEnvelopeCodec{encoded: []byte("abc")})
	transport.openStream = func(context.Context, uint64) (outboundStream, error) {
		time.Sleep(time.Millisecond)
		return stream, nil
	}

	result, err := transport.Send(t.Context(), 1, WireEnvelope{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("send error = %v, want %v", err, wantErr)
	}
	if result.EncodedBytes != 0 || result.StreamOpenDuration <= 0 || result.WriteDuration <= 0 || result.FinalizeDuration != 0 {
		t.Fatalf("partial result = %+v", result)
	}
	if !stream.reset || stream.closed {
		t.Fatalf("failed stream reset=%t closed=%t", stream.reset, stream.closed)
	}
}

type failingOutboundStream struct {
	writeErr   error
	writeDelay time.Duration
	reset      bool
	closed     bool
}

func (s *failingOutboundStream) Write([]byte) (int, error) {
	time.Sleep(s.writeDelay)
	return 0, s.writeErr
}
func (*failingOutboundStream) CloseWrite() error { return nil }
func (s *failingOutboundStream) Close() error    { s.closed = true; return nil }
func (s *failingOutboundStream) Reset() error    { s.reset = true; return nil }
func (*failingOutboundStream) SetWriteDeadline(time.Time) error {
	return nil
}

func assertTransportStillSending(t *testing.T, done <-chan transportTestCompletion, stage string) {
	t.Helper()
	select {
	case completed := <-done:
		t.Fatalf("send completed during %s: %+v, %v", stage, completed.result, completed.err)
	default:
	}
}
