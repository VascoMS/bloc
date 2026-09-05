package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hbbft"
)

type blockingSlotTransport struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	result  transportSendResult
	err     error
}

type discardSlotTransport struct{}

func (discardSlotTransport) Start(context.Context, EnvelopeHandler) error { return nil }
func (discardSlotTransport) Close() error                                 { return nil }
func (discardSlotTransport) Send(context.Context, uint64, WireEnvelope) (transportSendResult, error) {
	return transportSendResult{EncodedBytes: 1}, nil
}

type immediateQueueSlotTransport struct {
	sends chan WireEnvelope
}

func (t *immediateQueueSlotTransport) Start(context.Context, EnvelopeHandler) error { return nil }
func (t *immediateQueueSlotTransport) Close() error                                 { return nil }
func (t *immediateQueueSlotTransport) Send(_ context.Context, _ uint64, env WireEnvelope) (transportSendResult, error) {
	t.sends <- env
	return transportSendResult{EncodedBytes: 1}, nil
}

func (t *blockingSlotTransport) Start(context.Context, EnvelopeHandler) error { return nil }
func (t *blockingSlotTransport) Close() error                                 { return nil }
func (t *blockingSlotTransport) Send(context.Context, uint64, WireEnvelope) (transportSendResult, error) {
	t.once.Do(func() { close(t.started) })
	<-t.release
	result := t.result
	if result.EncodedBytes == 0 && t.err == nil {
		result.EncodedBytes = 17
	}
	return result, t.err
}

func waitForSlotCondition(t *testing.T, timeout, interval time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true before timeout")
		}
		time.Sleep(interval)
	}
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

func TestACSTraceLifecycleBeginsAtProposalReady(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.cfg.Limits = defaultResourceLimits()
	n.slotState = n.newSlotState(1)
	n.transport = discardSlotTransport{}

	if err := n.startConsensus(); err != nil {
		t.Fatal(err)
	}
	trace := n.slot.Trace()
	if !trace.Enabled || !trace.Aggregate.InputStarted.Recorded {
		t.Fatalf("trace did not begin with ACS input: %+v", trace)
	}
	if n.metricTimes.proposalReady.IsZero() {
		t.Fatal("proposal-ready metric origin was not captured")
	}
}

func TestACSTraceLifecycleCapturesNodeReceiptAndResult(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())
	out := &hbbft.SlotOutput{Slot: 1}

	n.captureACSTrace(out)
	if !out.ACSTrace.Adapter.NodeOutputReceived.Recorded ||
		!n.acsTrace.Adapter.NodeOutputReceived.Recorded {
		t.Fatalf("node receipt was not captured: output=%+v state=%+v", out.ACSTrace, n.acsTrace)
	}
	now := time.Now()
	n.finishEmptyMaterializedSet(now, now, now, now, now, AgreedInclusionSet{}, MergedEncryptedSet{})
	if n.result == nil || !n.result.ACSTrace.Adapter.NodeOutputReceived.Recorded {
		t.Fatalf("result omitted ACS trace: %+v", n.result)
	}
}

func TestPrepareSlotRetainsProposalLimitInFreshState(t *testing.T) {
	n := lifecycleTestNode(t)
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1}
	n.mu.Unlock()

	if err := n.prepareSlotWithLimit(2, 32); err != nil {
		t.Fatalf("prepare slot with limit: %v", err)
	}
	if n.id != 2 || n.proposalLimit != 32 {
		t.Fatalf("fresh slot id/limit = %d/%d, want 2/32", n.id, n.proposalLimit)
	}
	if err := n.prepareSlotWithLimit(3, n.cfg.BMax+1); err == nil {
		t.Fatal("proposal limit above BMax accepted")
	}
}

func TestPrepareSlotSetsInitialProposalLimitOnce(t *testing.T) {
	n := lifecycleTestNode(t)
	if err := n.prepareSlotWithLimit(1, 32); err != nil {
		t.Fatalf("set initial proposal limit: %v", err)
	}
	if n.proposalLimit != 32 {
		t.Fatalf("initial proposal limit = %d, want 32", n.proposalLimit)
	}
	if err := n.prepareSlotWithLimit(1, 8); err == nil {
		t.Fatal("reconfigured an already bounded slot")
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

	t.Run("trace-enabled terminal failure does not wait for finalization", func(t *testing.T) {
		n := lifecycleTestNode(t)
		n.slot.Close()
		n.cfg.Diagnostics.ACSTrace = true
		n.slotState = n.newSlotState(1)
		n.slot.BeginTrace(time.Now())
		n.slot.BeginACSOutbound(hbbft.ACSMessageReady)
		n.slot.SealACSOutbound()
		n.markSlotFailed("planning")
		rec := httptest.NewRecorder()
		n.handleResult(rec, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("trace-enabled failure response = %d %s", rec.Code, rec.Body.String())
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

func TestACSSubtypeInboundAccounting(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())

	n.handleEnvelope(WireEnvelope{
		From: 1,
		To:   0,
		Kind: "acs",
		Slot: 1,
		ACS: slotACSMessage(&hbbft.BroadcastMessage{
			Payload: &hbbft.ReadyRequest{RootHash: []byte("root")},
		}),
	}, 123)

	got := n.slot.Trace().Messages[hbbft.ACSMessageReady]
	if got.InboundCount != 1 || got.InboundBytes != 123 {
		t.Fatalf("READY inbound accounting = %+v", got)
	}
}

func TestACSSubtypeOutboundAccountingAndSlotIsolation(t *testing.T) {
	tests := []struct {
		name        string
		sendErr     error
		wantSuccess uint64
		wantFailure uint64
	}{
		{name: "success", wantSuccess: 1},
		{name: "failure", sendErr: errors.New("send failed"), wantFailure: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			n := lifecycleTestNode(t)
			n.slot.Close()
			n.cfg.Diagnostics.ACSTrace = true
			n.slotState = n.newSlotState(1)
			n.slot.BeginTrace(time.Now())
			oldSlot := n.slot
			transport := &blockingSlotTransport{
				started: make(chan struct{}),
				release: make(chan struct{}),
				result: transportSendResult{
					EncodedBytes:       29,
					EncodeDuration:     1 * time.Millisecond,
					QueueWaitDuration:  2 * time.Millisecond,
					StreamOpenDuration: 3 * time.Millisecond,
					WriteDuration:      4 * time.Millisecond,
					FinalizeDuration:   5 * time.Millisecond,
				},
				err: test.sendErr,
			}
			n.transport = transport
			n.sendEnvelope(1, WireEnvelope{
				Kind: "acs",
				Slot: 1,
				ACS: slotACSMessage(&hbbft.AgreementMessage{
					Message: &hbbft.BvalRequest{Value: true},
				}),
			})
			<-transport.started
			oldSlot.SealACSOutbound()
			n.mu.Lock()
			n.phase = slotCompleted
			n.mu.Unlock()
			prepared := make(chan error, 1)
			go func() { prepared <- n.prepareSlot(2) }()
			select {
			case err := <-prepared:
				t.Fatalf("slot replacement did not wait for send accounting: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			close(transport.release)
			if err := <-prepared; err != nil {
				t.Fatal(err)
			}

			got := oldSlot.Trace().Messages[hbbft.ACSMessageBVAL]
			if got.OutboundCount != test.wantSuccess || got.SendCount != test.wantSuccess ||
				got.SendFailureCount != test.wantFailure {
				t.Fatalf("BVAL outbound accounting = %+v", got)
			}
			if test.wantSuccess == 1 && (got.OutboundBytes != 29 || got.SendMaxUS > got.SendTotalUS) {
				t.Fatalf("successful BVAL accounting = %+v", got)
			}
			if test.wantSuccess == 1 {
				assertNodeSendPhase(t, "encode", got.Encode, 1000)
				assertNodeSendPhase(t, "queue wait", got.QueueWait, 2000)
				assertNodeSendPhase(t, "stream open", got.StreamOpen, 3000)
				assertNodeSendPhase(t, "write", got.Write, 4000)
				assertNodeSendPhase(t, "finalize", got.Finalize, 5000)
				if got.StreamOpenCount != 1 || got.StreamReuseCount != 0 {
					t.Fatalf("fresh stream accounting = %+v", got)
				}
			}
			if test.wantFailure == 1 && (got.OutboundBytes != 0 || got.SendTotalUS != 0) {
				t.Fatalf("failed BVAL counted as success: %+v", got)
			}
			if test.wantFailure == 1 && (got.Encode.Count != 0 || got.StreamOpen.Count != 0 || got.Write.Count != 0) {
				t.Fatalf("failed BVAL phases counted as successful latency: %+v", got)
			}
			if fresh := n.slot.Trace().Messages[hbbft.ACSMessageBVAL]; fresh != (hbbft.ACSMessageTrace{}) {
				t.Fatalf("old send contaminated new slot: %+v", fresh)
			}
		})
	}
}

func TestTraceEnabledResultWaitsForScheduledSendFinalization(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())
	transport := &blockingSlotTransport{
		started: make(chan struct{}), release: make(chan struct{}),
		result: transportSendResult{EncodedBytes: 29},
	}
	n.transport = transport
	token := n.slot.BeginACSOutbound(hbbft.ACSMessageReady)
	n.sendEnvelopeTracked(1, WireEnvelope{
		Kind: "acs", Slot: 1,
		ACS: slotACSMessage(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{RootHash: []byte("root")}}),
	}, token)
	<-transport.started
	n.slot.MarkNodeOutputReceived()
	n.slot.SealACSOutbound()
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1, NodeID: 0, Metrics: Metrics{ACSUS: 123, TotalSlotUS: 456}}
	n.mu.Unlock()

	pending := httptest.NewRecorder()
	n.handleResult(pending, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
	if pending.Code != http.StatusAccepted {
		t.Fatalf("pending trace result = %d %s", pending.Code, pending.Body.String())
	}
	trace := n.slot.Trace()
	if trace.Messages[hbbft.ACSMessageReady].PendingAtDecision != 1 || trace.Transport.Finalized {
		t.Fatalf("pending decision trace = %+v", trace)
	}

	close(transport.release)
	waitForSlotCondition(t, time.Second, time.Millisecond, n.slot.TraceFinalized)
	completed := httptest.NewRecorder()
	n.handleResult(completed, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
	if completed.Code != http.StatusOK {
		t.Fatalf("final result = %d %s", completed.Code, completed.Body.String())
	}
	var result Result
	if err := json.Unmarshal(completed.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.ACSTrace.Transport.Finalized || result.Metrics.ACSUS != 123 || result.Metrics.TotalSlotUS != 456 {
		t.Fatalf("final result changed metrics or omitted trace: %+v", result)
	}
}

func TestTraceEnabledResultFileWaitsForFinalization(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())
	transport := &blockingSlotTransport{started: make(chan struct{}), release: make(chan struct{})}
	n.transport = transport
	token := n.slot.BeginACSOutbound(hbbft.ACSMessageReady)
	n.sendEnvelopeTracked(1, WireEnvelope{Kind: "acs", Slot: 1, ACS: slotACSMessage(
		&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{RootHash: []byte("root")}},
	)}, token)
	<-transport.started
	n.slot.SealACSOutbound()
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1, NodeID: 0}
	n.mu.Unlock()

	path := filepath.Join(t.TempDir(), "result.json")
	go n.writeResultWhenReady(path)
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("result file published before trace completion: %v", err)
	}
	close(transport.release)
	waitForSlotCondition(t, 2*time.Second, 10*time.Millisecond, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var result Result
		return json.Unmarshal(data, &result) == nil && result.ACSTrace.Transport.Finalized
	})
}

func TestPrepareSlotRejectsUnfinalizedScheduledTrace(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())
	token := n.slot.BeginACSOutbound(hbbft.ACSMessageReady)
	n.slot.SealACSOutbound()
	n.mu.Lock()
	n.phase = slotCompleted
	n.result = &Result{Slot: 1, NodeID: 0}
	n.mu.Unlock()

	if err := n.prepareSlot(2); err == nil || !strings.Contains(err.Error(), "trace finalization pending") {
		t.Fatalf("prepare with pending trace error = %v", err)
	}
	token.Complete(hbbft.ACSSendObservation{Err: errors.New("terminal test send")})
	if err := n.prepareSlot(2); err != nil {
		t.Fatalf("prepare after trace finalization: %v", err)
	}
}

func TestCollectACSMessagesCompletesAdmittedTokensOnClassificationError(t *testing.T) {
	n := lifecycleTestNode(t)
	n.slot.Close()
	n.cfg.Diagnostics.ACSTrace = true
	n.slotState = n.newSlotState(1)
	n.slot.BeginTrace(time.Now())

	_, err := n.collectACSMessagesFrom([]hbbft.MessageTuple{
		{
			To: 1,
			Payload: slotACSMessage(&hbbft.AgreementMessage{
				Message: &hbbft.BvalRequest{Value: true},
			}),
		},
		{To: 2, Payload: &hbbft.SlotMessage{}},
	})
	if err == nil {
		t.Fatal("accepted an invalid emitted ACS message")
	}
	n.slot.SealACSOutbound()
	trace := n.slot.Trace()
	got := trace.Messages[hbbft.ACSMessageBVAL]
	if got.ScheduledCount != 1 || got.TerminalCount != 1 || got.SendFailureCount != 1 || !trace.Transport.Finalized {
		t.Fatalf("classification failure stranded an admitted token: %+v", trace)
	}
}

func assertNodeSendPhase(t *testing.T, name string, got hbbft.ACSSendPhaseTrace, wantUS int64) {
	t.Helper()
	if got.Count != 1 || got.TotalUS != wantUS || got.MaxUS != wantUS {
		t.Fatalf("%s phase = %+v, want one %dus observation", name, got, wantUS)
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

func TestStepACSSealsOutputBeforeImmediateDecisionSends(t *testing.T) {
	transport := &immediateQueueSlotTransport{sends: make(chan WireEnvelope, 4096)}
	nodes := make([]*Node, 4)
	for id := range nodes {
		n := lifecycleTestNode(t)
		n.slot.Close()
		n.self.ID = uint64(id)
		n.cfg.Diagnostics.ACSTrace = true
		n.slotState = n.newSlotState(1)
		n.slot.BeginTrace(time.Now())
		n.transport = transport
		nodes[id] = n
	}

	for id, n := range nodes {
		if _, err := n.stepACS(func() error { return n.slot.InputBatch([]byte{byte('a' + id)}) }); err != nil {
			t.Fatalf("input node %d: %v", id, err)
		}
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case env := <-transport.sends:
			target := nodes[env.To]
			var scheduledBefore uint64
			if target == nodes[0] {
				for _, subtype := range []hbbft.ACSMessageSubtype{
					hbbft.ACSMessageProof, hbbft.ACSMessageEcho, hbbft.ACSMessageReady,
					hbbft.ACSMessageBVAL, hbbft.ACSMessageAUX,
				} {
					scheduledBefore += target.slot.Trace().Messages[subtype].ScheduledCount
				}
			}
			output, err := target.stepACS(func() error {
				return target.slot.HandleMessage(env.From, env.ACS)
			})
			if err != nil {
				t.Fatalf("deliver ACS message from %d to %d: %v", env.From, env.To, err)
			}
			if target != nodes[0] || output == nil {
				continue
			}

			trace := target.slot.Trace()
			if !trace.Transport.Sealed {
				t.Fatal("output transition did not seal ACS sends")
			}
			var pendingAtDecision uint64
			var scheduled uint64
			for _, subtype := range []hbbft.ACSMessageSubtype{
				hbbft.ACSMessageProof, hbbft.ACSMessageEcho, hbbft.ACSMessageReady,
				hbbft.ACSMessageBVAL, hbbft.ACSMessageAUX,
			} {
				entry := trace.Messages[subtype]
				pendingAtDecision += entry.PendingAtDecision
				scheduled += entry.ScheduledCount
			}
			if pendingAtDecision == 0 {
				t.Fatalf("immediate decision sends were not frozen as pending: %+v", trace)
			}
			decisionScheduled := scheduled - scheduledBefore
			if decisionScheduled == 0 || pendingAtDecision < decisionScheduled {
				t.Fatalf("decision sends were not retained as pending: scheduled_before=%d decision_scheduled=%d pending=%d trace=%+v", scheduledBefore, decisionScheduled, pendingAtDecision, trace)
			}

			if _, err := target.stepACS(func() error { return nil }); err != nil {
				t.Fatalf("post-output ACS step: %v", err)
			}
			var afterScheduled uint64
			for _, subtype := range []hbbft.ACSMessageSubtype{
				hbbft.ACSMessageProof, hbbft.ACSMessageEcho, hbbft.ACSMessageReady,
				hbbft.ACSMessageBVAL, hbbft.ACSMessageAUX,
			} {
				afterScheduled += target.slot.Trace().Messages[subtype].ScheduledCount
			}
			if afterScheduled != scheduled {
				t.Fatalf("post-output ACS step admitted sends: before=%d after=%d", scheduled, afterScheduled)
			}
			return
		case <-deadline:
			t.Fatal("node 0 did not produce ACS output")
		}
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
	for _, key := range []string{"acs_trace_enabled", "acs_trace_finalized", "acs_trace_finalization_pending"} {
		if _, ok := body[key].(bool); !ok {
			t.Fatalf("status %q = %T, want bool: %v", key, body[key], body)
		}
	}
}
