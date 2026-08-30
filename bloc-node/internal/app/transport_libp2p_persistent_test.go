package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

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
