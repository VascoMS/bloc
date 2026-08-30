package app

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

const persistentQueueCapacity = 1

var errPersistentWriterClosed = errors.New("persistent stream writer is closed")

type persistentWriteStream interface {
	io.Writer
	Close() error
	Reset() error
	SetWriteDeadline(time.Time) error
}

type persistentSendRequest struct {
	ctx        context.Context
	payload    []byte
	enqueuedAt time.Time
	result     chan persistentSendCompletion
}

type persistentSendCompletion struct {
	result transportSendResult
	err    error
}

type persistentPrewarmRequest struct {
	ctx    context.Context
	result chan error
}

type peerStreamWriter struct {
	operatorID  uint64
	queue       chan persistentSendRequest
	prewarm     chan persistentPrewarmRequest
	open        func(context.Context, uint64) (persistentWriteStream, error)
	prewarmOpen func(context.Context, uint64) (persistentWriteStream, error)
	stop        <-chan struct{}
	ready       atomic.Bool
	done        chan struct{}
}

func newPeerStreamWriter(
	operatorID uint64,
	open func(context.Context, uint64) (persistentWriteStream, error),
	stop <-chan struct{},
) *peerStreamWriter {
	writer := &peerStreamWriter{
		operatorID:  operatorID,
		queue:       make(chan persistentSendRequest, persistentQueueCapacity),
		prewarm:     make(chan persistentPrewarmRequest, 1),
		open:        open,
		prewarmOpen: open,
		stop:        stop,
		done:        make(chan struct{}),
	}
	go writer.run()
	return writer
}

func (w *peerStreamWriter) prewarmStream(ctx context.Context) error {
	request := persistentPrewarmRequest{ctx: ctx, result: make(chan error, 1)}
	select {
	case w.prewarm <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stop:
		return errPersistentWriterClosed
	}
	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stop:
		return errPersistentWriterClosed
	}
}

func (w *peerStreamWriter) send(ctx context.Context, payload []byte) (transportSendResult, error) {
	var result transportSendResult
	if len(payload) == 0 {
		return result, errEnvelopeFrameZeroLength
	}
	ctx, cancel := withTransportSendDeadline(ctx)
	defer cancel()
	request := persistentSendRequest{
		ctx:        ctx,
		payload:    append([]byte(nil), payload...),
		enqueuedAt: time.Now(),
		result:     make(chan persistentSendCompletion, 1),
	}
	select {
	case w.queue <- request:
	case <-ctx.Done():
		result.QueueWaitDuration = time.Since(request.enqueuedAt)
		return result, ctx.Err()
	case <-w.stop:
		return result, errPersistentWriterClosed
	}
	select {
	case completion := <-request.result:
		return completion.result, completion.err
	case <-ctx.Done():
		return result, ctx.Err()
	case <-w.stop:
		return result, errPersistentWriterClosed
	}
}

func (w *peerStreamWriter) run() {
	defer close(w.done)
	var stream persistentWriteStream
	for {
		select {
		case <-w.stop:
			w.stopStream(stream)
			w.failQueued()
			return
		case request := <-w.queue:
			if persistentWriterStopped(w.stop) {
				request.result <- persistentSendCompletion{err: errPersistentWriterClosed}
				w.stopStream(stream)
				w.failQueued()
				return
			}
			stream = w.writeRequest(stream, request)
		case request := <-w.prewarm:
			if persistentWriterStopped(w.stop) {
				request.result <- errPersistentWriterClosed
				w.stopStream(stream)
				w.failQueued()
				return
			}
			stream = w.openPrewarmedStream(stream, request)
		}
	}
}

func (w *peerStreamWriter) openPrewarmedStream(stream persistentWriteStream, request persistentPrewarmRequest) persistentWriteStream {
	if err := request.ctx.Err(); err != nil {
		request.result <- err
		return stream
	}
	if stream != nil {
		request.result <- nil
		return stream
	}
	opened, err := w.prewarmOpen(request.ctx, w.operatorID)
	if err != nil {
		request.result <- err
		return nil
	}
	if opened == nil {
		request.result <- errors.New("persistent stream prewarmer returned a nil stream")
		return nil
	}
	if persistentWriterStopped(w.stop) {
		_ = opened.Reset()
		request.result <- errPersistentWriterClosed
		return nil
	}
	w.ready.Store(true)
	request.result <- nil
	return opened
}

func (w *peerStreamWriter) writeRequest(stream persistentWriteStream, request persistentSendRequest) persistentWriteStream {
	result := transportSendResult{QueueWaitDuration: time.Since(request.enqueuedAt)}
	if err := request.ctx.Err(); err != nil {
		request.result <- persistentSendCompletion{result: result, err: err}
		return stream
	}
	if stream == nil {
		started := time.Now()
		opened, err := w.open(request.ctx, w.operatorID)
		result.StreamOpenDuration = time.Since(started)
		if err != nil {
			request.result <- persistentSendCompletion{result: result, err: err}
			return nil
		}
		if opened == nil {
			err := errors.New("persistent stream opener returned a nil stream")
			request.result <- persistentSendCompletion{result: result, err: err}
			return nil
		}
		stream = opened
		w.ready.Store(true)
	} else {
		result.StreamReused = true
	}
	if err := request.ctx.Err(); err != nil {
		w.resetStream(stream)
		request.result <- persistentSendCompletion{result: result, err: err}
		return nil
	}
	deadline, ok := request.ctx.Deadline()
	if !ok {
		w.resetStream(stream)
		err := errors.New("persistent send has no effective deadline")
		request.result <- persistentSendCompletion{result: result, err: err}
		return nil
	}
	if err := stream.SetWriteDeadline(deadline); err != nil {
		w.resetStream(stream)
		request.result <- persistentSendCompletion{result: result, err: err}
		return nil
	}
	started := time.Now()
	err := writeEnvelopeFrame(stream, request.payload)
	result.WriteDuration = time.Since(started)
	if err != nil {
		w.resetStream(stream)
		request.result <- persistentSendCompletion{result: result, err: err}
		return nil
	}
	if persistentWriterStopped(w.stop) {
		w.resetStream(stream)
		request.result <- persistentSendCompletion{result: result, err: errPersistentWriterClosed}
		return nil
	}
	result.EncodedBytes = len(request.payload)
	request.result <- persistentSendCompletion{result: result}
	return stream
}

func (w *peerStreamWriter) resetStream(stream persistentWriteStream) {
	if stream != nil {
		_ = stream.Reset()
	}
	w.ready.Store(false)
}

func (w *peerStreamWriter) stopStream(stream persistentWriteStream) {
	w.resetStream(stream)
}

func (w *peerStreamWriter) failQueued() {
	for {
		select {
		case request := <-w.queue:
			request.result <- persistentSendCompletion{err: errPersistentWriterClosed}
		default:
			for {
				select {
				case request := <-w.prewarm:
					request.result <- errPersistentWriterClosed
				default:
					return
				}
			}
		}
	}
}

func persistentWriterStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
