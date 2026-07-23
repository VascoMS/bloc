package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type blockingSlotTransport struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *blockingSlotTransport) Start(context.Context, EnvelopeHandler) error { return nil }
func (t *blockingSlotTransport) Close() error                                 { return nil }
func (t *blockingSlotTransport) Send(context.Context, uint64, WireEnvelope) (int, error) {
	t.once.Do(func() { close(t.started) })
	<-t.release
	return 17, nil
}

func lifecycleTestNode(t *testing.T) *Node {
	t.Helper()
	n := &Node{
		cfg: ConfigFile{
			N: 4, BMax: 128, Threshold: 3, Slot: 1,
			Blockspace: BlockspaceConfig{DefaultTxGas: 21000},
		},
		self:     NodeConfig{ID: 0},
		nodeIDs:  []uint64{0, 1, 2, 3},
		lastSlot: 1,
	}
	n.slotState = n.newSlotState(1)
	t.Cleanup(func() { n.slot.Close() })
	return n
}

func TestPrepareSlotRequiresCompletedIncreasingSlot(t *testing.T) {
	n := lifecycleTestNode(t)
	if err := n.prepareSlot(2); err == nil {
		t.Fatal("prepared a replacement while the active slot was incomplete")
	}
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1}
	n.metrics.SubmittedTxs = 7
	n.mu.Unlock()
	if err := n.prepareSlot(2); err != nil {
		t.Fatalf("prepare completed slot: %v", err)
	}
	if n.id != 2 || n.phase != slotPrepared || n.result != nil || n.metrics.SubmittedTxs != 0 {
		t.Fatalf("replacement state was not fresh: id=%d phase=%s result=%v metrics=%+v", n.id, n.phase, n.result, n.metrics)
	}
	if err := n.prepareSlot(2); err == nil {
		t.Fatal("accepted a non-increasing slot id")
	}
}

func TestPrepareSlotAcceptsTerminalFailureAndResetsIt(t *testing.T) {
	n := lifecycleTestNode(t)
	n.markSlotFailed("decode")
	if n.phase != slotFailed || n.failure == nil {
		t.Fatalf("slot did not become terminally failed: phase=%s failure=%+v", n.phase, n.failure)
	}
	if err := n.prepareSlot(2); err != nil {
		t.Fatalf("prepare failed slot: %v", err)
	}
	if n.id != 2 || n.phase != slotPrepared || n.failure != nil {
		t.Fatalf("replacement state retained failure: id=%d phase=%s failure=%+v", n.id, n.phase, n.failure)
	}
}

func TestMarkSlotFailedIsIdempotentAndBounded(t *testing.T) {
	n := lifecycleTestNode(t)
	n.markSlotFailed("decode")
	first := *n.failure
	n.markSlotFailed("arbitrary dynamic error")
	if n.failedSlots != 1 {
		t.Fatalf("failed slots = %d, want 1", n.failedSlots)
	}
	if *n.failure != first {
		t.Fatalf("terminal failure changed: first=%+v current=%+v", first, *n.failure)
	}
	if first.Slot != 1 || first.Reason != "decode" || first.FailedAtUnixNano == 0 {
		t.Fatalf("unexpected terminal failure: %+v", first)
	}
	now := time.Now()
	n.finishEmptyMaterializedSet(now, now, now, now, now, AgreedInclusionSet{}, MergedEncryptedSet{})
	if n.result != nil || n.phase != slotFailed {
		t.Fatalf("late success replaced terminal failure: phase=%s result=%+v", n.phase, n.result)
	}

	unknown := lifecycleTestNode(t)
	unknown.markSlotFailed("an arbitrarily long dynamic error must not become a metric or API label")
	if unknown.failure == nil || unknown.failure.Reason != "unknown" {
		t.Fatalf("dynamic failure reason was not bounded: %+v", unknown.failure)
	}
}

func TestResultEndpointDistinguishesPendingSuccessFailureAndWrongSlot(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		n := lifecycleTestNode(t)
		rec := httptest.NewRecorder()
		n.handleResult(rec, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
		if rec.Code != http.StatusAccepted || rec.Body.String() != "{\"status\":\"pending\"}\n" {
			t.Fatalf("pending response = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		n := lifecycleTestNode(t)
		n.mu.Lock()
		n.phase = slotCompleted
		n.result = &Result{Slot: 1, NodeID: 0}
		n.mu.Unlock()
		rec := httptest.NewRecorder()
		n.handleResult(rec, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("success response = %d %s", rec.Code, rec.Body.String())
		}
		var result Result
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Slot != 1 || result.NodeID != 0 {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("terminal failure is stable", func(t *testing.T) {
		n := lifecycleTestNode(t)
		n.markSlotFailed("planning")
		var first string
		for attempt := 0; attempt < 2; attempt++ {
			rec := httptest.NewRecorder()
			n.handleResult(rec, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("failure response = %d %s", rec.Code, rec.Body.String())
			}
			if attempt == 0 {
				first = rec.Body.String()
			} else if rec.Body.String() != first {
				t.Fatalf("repeated failure changed: first=%s second=%s", first, rec.Body.String())
			}
			var body struct {
				Status string `json:"status"`
				SlotFailure
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != "failed" || body.Slot != 1 || body.Reason != "planning" || body.FailedAtUnixNano == 0 {
				t.Fatalf("unexpected failure body: %+v", body)
			}
		}
	})

	t.Run("wrong slot", func(t *testing.T) {
		n := lifecycleTestNode(t)
		n.markSlotFailed("decode")
		rec := httptest.NewRecorder()
		n.handleResult(rec, httptest.NewRequest(http.MethodGet, "/result?slot=9", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("wrong-slot response = %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestStartPublishesSynchronousFailureThroughResult(t *testing.T) {
	n := lifecycleTestNode(t)
	n.cfg.Provider.Mode = "unsupported"
	start := httptest.NewRecorder()
	n.handleStart(start, httptest.NewRequest(http.MethodPost, "/start?slot=1", nil))
	if start.Code/100 != 2 {
		t.Fatalf("start bypassed terminal result: %d %s", start.Code, start.Body.String())
	}
	result := httptest.NewRecorder()
	n.handleResult(result, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
	if result.Code != http.StatusUnprocessableEntity {
		t.Fatalf("terminal result = %d %s", result.Code, result.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		SlotFailure
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "failed" || body.Slot != 1 || body.Reason != "proposal" {
		t.Fatalf("unexpected terminal result: %+v", body)
	}
}

func TestFailureBeforeStartRemainsTerminalAndReplaceable(t *testing.T) {
	n := lifecycleTestNode(t)
	n.cfg.Provider.Mode = "unsupported"
	n.markSlotFailed("decode")
	_ = n.startConsensus()
	if n.phase != slotFailed || n.failure == nil || n.failure.Reason != "decode" {
		t.Fatalf("start reverted terminal failure: phase=%s failure=%+v", n.phase, n.failure)
	}
	if err := n.prepareSlot(2); err != nil {
		t.Fatalf("terminal slot was no longer replaceable: %v", err)
	}
}

func TestSuccessBeforeStartRemainsTerminalAndReplaceable(t *testing.T) {
	n := lifecycleTestNode(t)
	n.cfg.Provider.Mode = "unsupported"
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1, NodeID: 0}
	n.mu.Unlock()
	start := httptest.NewRecorder()
	n.handleStart(start, httptest.NewRequest(http.MethodPost, "/start?slot=1", nil))
	if start.Code/100 != 2 {
		t.Fatalf("start bypassed terminal success: %d %s", start.Code, start.Body.String())
	}
	if n.phase != slotCompleted || n.result == nil {
		t.Fatalf("start reverted terminal success: phase=%s result=%+v", n.phase, n.result)
	}
	if err := n.prepareSlot(2); err != nil {
		t.Fatalf("completed slot was no longer replaceable: %v", err)
	}
}

func TestStaleEnvelopeDoesNotTouchActiveMetrics(t *testing.T) {
	n := lifecycleTestNode(t)
	n.mu.Lock()
	n.phase = slotCompleted
	n.mu.Unlock()
	if err := n.prepareSlot(2); err != nil {
		t.Fatal(err)
	}
	n.handleEnvelope(WireEnvelope{From: 1, To: 0, Direct: true, Kind: "share", Slot: 1}, 123)
	if len(n.metrics.InboundMessages) != 0 || len(n.metrics.InboundBytes) != 0 {
		t.Fatalf("stale envelope contaminated active metrics: %+v", n.metrics)
	}
}

func TestRequestedSlotValidationIsBackwardCompatible(t *testing.T) {
	n := lifecycleTestNode(t)
	legacy := httptest.NewRequest("GET", "/result", nil)
	if err := n.validateRequestedSlot(legacy); err != nil {
		t.Fatalf("legacy request rejected: %v", err)
	}
	explicit := httptest.NewRequest("GET", "/result?slot=1", nil)
	if err := n.validateRequestedSlot(explicit); err != nil {
		t.Fatalf("active explicit slot rejected: %v", err)
	}
	stale := httptest.NewRequest("GET", "/result?slot=9", nil)
	if err := n.validateRequestedSlot(stale); err == nil {
		t.Fatal("wrong explicit slot accepted")
	}
}

func TestPrepareSlotWaitsForInflightSendAndResetsItsMetrics(t *testing.T) {
	n := lifecycleTestNode(t)
	transport := &blockingSlotTransport{started: make(chan struct{}), release: make(chan struct{})}
	n.transport = transport
	n.sendEnvelope(1, WireEnvelope{Kind: "acs", Slot: 1})
	<-transport.started
	n.mu.Lock()
	n.phase = slotCompleted
	n.mu.Unlock()

	prepared := make(chan error, 1)
	go func() { prepared <- n.prepareSlot(2) }()
	select {
	case err := <-prepared:
		t.Fatalf("slot replacement did not wait for the send: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(transport.release)
	if err := <-prepared; err != nil {
		t.Fatalf("prepare after send drain: %v", err)
	}
	if len(n.metrics.OutboundMessages) != 0 {
		t.Fatalf("prior-slot send contaminated new metrics: %+v", n.metrics.OutboundMessages)
	}
}

func TestStepACSSerializesConcurrentLocalSteps(t *testing.T) {
	n := lifecycleTestNode(t)
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := n.stepACS(func() error {
			close(started)
			<-release
			return nil
		})
		firstDone <- err
	}()
	<-started

	secondDone := make(chan error, 1)
	go func() {
		_, err := n.stepACS(func() error { return nil })
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second ACS step did not wait for first step: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ACS step failed: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second ACS step failed: %v", err)
	}
}

func TestSlotStatusIncludesACSProgress(t *testing.T) {
	n := lifecycleTestNode(t)
	req := httptest.NewRequest("GET", "/slot/status?slot=1", nil)
	rec := httptest.NewRecorder()
	n.handleSlotStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status code = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["acs"].(map[string]any); !ok {
		t.Fatalf("status did not include acs progress: %v", body)
	}
}
