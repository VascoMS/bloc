# ACS Trace Finalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish complete `bloc-acs-trace/v3` diagnostics for every ACS send emitted through the local-decision transition, while preserving all existing protocol and latency timestamp boundaries.

**Architecture:** Admit outbound trace tokens synchronously when `stepACS` drains emitted messages, then let the existing asynchronous send goroutines terminally complete those captured tokens exactly once. Seal the recorder when local ACS output reaches the node, retain `pending_at_decision`, and gate only successful diagnostic result publication until all pre-decision tokens have completed; v1/v2 remain readable and new evaluator runs require v3.

**Tech Stack:** Go 1.x, existing `sbc/hbbft` trace recorder and slot adapter, `bloc-node` HTTP/result lifecycle, JSONL artifact validator, CSV evaluator summaries, Go race detector.

**Spec:** `docs/superpowers/specs/2026-09-02-rbc-ready-stream-lanes-design.md`

## Global Constraints

- Begin from the integrated `codex/rbc-ready-self-admission` result, create a repository issue named `Finalize pre-decision ACS transport traces`, add it to the BLOC Thesis Prototype project, assign milestone `M5. Performance, Scaling, And Resource Evidence`, and set Project fields `Roadmap target=M5`, `Status=In progress`, `Priority=High`, and `Area=Evaluation`.
- Execute in a fresh worktree created with `superpowers:using-git-worktrees` on branch `codex/acs-trace-finalization`.
- Preserve the user's uncommitted `papers/ACS_Improvement.pdf`; never stage or rewrite it.
- The trace domain is exactly the ACS sends emitted up to and including the transition that produces local ACS output. Share sends and later ACS sends are outside the sealed trace.
- Count enabled-slot sends even if remote protocol progress emits them before the local proposal-ready clock origin; only monotonic milestone offsets depend on `BeginTrace`.
- Record `scheduled` synchronously before starting network I/O and one terminal completion on success, stale-slot rejection, cancellation, encoding failure, queue timeout, stream failure, or any other send exit.
- Do not wait inside RBC, BBA, ACS, merge/plan, share generation, or BTE reconstruction. `ACSUS`, `TotalSlotUS`, and existing stage timestamps retain their current boundaries.
- Gate only successful trace-enabled `/result` and result-file publication. Trace-disabled success and all terminal failures retain their current HTTP/file behavior.
- New traces use `bloc-acs-trace/v3`; v1 and v2 artifacts remain readable, but only v3 satisfies new-run validation.
- Keep trace state bounded by the five fixed ACS subtypes and configured proposer IDs. Do not add peer-, stream-, attempt-, or epoch-indexed collections.
- Do not change protobuf messages, ACS recipients, RBC/BBA thresholds, transport routing, or stream modes.
- Do not run cloud infrastructure. The local trace smoke may open loopback listeners only.
- End by checking branch/status/divergence, posting commands and evidence to the issue, and explicitly recording whether `docs/STATUS.md` changed.

---

### Task 1: Add a sealed, exactly-once outbound lifecycle to the trace recorder

**Files:**
- Create: `sbc/hbbft/trace_outbound.go`
- Modify: `sbc/hbbft/trace.go:8-144,403-445`
- Modify: `sbc/hbbft/bloc_slot.go:121-153`
- Test: `sbc/hbbft/trace_test.go:110-160`

**Interfaces:**
- Consumes: `ACSMessageSubtype`, `ACSSendObservation`, `traceRecorder.mu`, and the fixed `acsMessageSubtypes` set.
- Produces: `ACSTraceSchemaV3`, `ACSTransportTrace`, `ACSMessageTrace.ScheduledCount`, `ACSMessageTrace.TerminalCount`, `ACSMessageTrace.PendingAtDecision`, `*ACSSendToken`, `SlotACS.BeginACSOutbound(ACSMessageSubtype) *ACSSendToken`, `SlotACS.SealACSOutbound()`, and `SlotACS.TraceFinalized() bool`.

- [ ] **Step 1: Replace the outbound aggregate test with a failing lifecycle test**

Replace `TestACSOutboundAggregatesSuccessfulSendPhases` in `sbc/hbbft/trace_test.go` with:

```go
func TestACSOutboundLifecycleSealsAndFinalizesExactlyOnce(t *testing.T) {
	slot := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: []uint64{0, 1, 2, 3}},
		Slot:   1,
		Trace:  TraceOptions{Enabled: true},
	})
	t.Cleanup(slot.Close)
	slot.BeginTrace(time.Now())

	success := slot.BeginACSOutbound(ACSMessageReady)
	failure := slot.BeginACSOutbound(ACSMessageReady)
	beforeSeal := slot.Trace()
	if beforeSeal.SchemaVersion != ACSTraceSchemaV3 ||
		beforeSeal.Messages[ACSMessageReady].ScheduledCount != 2 ||
		beforeSeal.Messages[ACSMessageReady].TerminalCount != 0 {
		t.Fatalf("scheduled trace = %+v", beforeSeal)
	}

	slot.SealACSOutbound()
	sealed := slot.Trace()
	if !sealed.Transport.Sealed || sealed.Transport.Finalized ||
		sealed.Messages[ACSMessageReady].PendingAtDecision != 2 {
		t.Fatalf("sealed trace = %+v", sealed)
	}

	success.Complete(ACSSendObservation{
		Size: 512, Total: 9 * time.Millisecond,
		Encode: time.Millisecond, QueueWait: 2 * time.Millisecond,
		Write: 6 * time.Millisecond, Reused: true,
	})
	// A copied caller path may invoke Complete twice; the token must count once.
	success.Complete(ACSSendObservation{Err: errors.New("duplicate completion")})
	failure.Complete(ACSSendObservation{Err: errors.New("open failed")})

	final := slot.Trace()
	got := final.Messages[ACSMessageReady]
	if !final.Transport.Finalized || !slot.TraceFinalized() {
		t.Fatalf("trace did not finalize: %+v", final.Transport)
	}
	if got.ScheduledCount != 2 || got.TerminalCount != 2 ||
		got.OutboundCount != 1 || got.SendCount != 1 || got.SendFailureCount != 1 ||
		got.PendingAtDecision != 2 {
		t.Fatalf("terminal accounting = %+v", got)
	}
	assertSendPhase(t, "encode", got.Encode, 1, 1000, 1000)
	assertSendPhase(t, "queue wait", got.QueueWait, 1, 2000, 2000)
	assertSendPhase(t, "write", got.Write, 1, 6000, 6000)
	if got.StreamOpenCount != 0 || got.StreamReuseCount != 1 {
		t.Fatalf("stream accounting = %+v", got)
	}

	late := slot.BeginACSOutbound(ACSMessageReady)
	late.Complete(ACSSendObservation{Size: 9})
	if after := slot.Trace().Messages[ACSMessageReady]; after != got {
		t.Fatalf("post-seal send changed trace: before=%+v after=%+v", got, after)
	}
}

func TestDisabledACSOutboundTraceIsAlreadyFinal(t *testing.T) {
	slot := NewSlotACS(SlotConfig{Config: Config{N: 4, F: 1, ID: 0, Nodes: makeids(4)}, Slot: 1})
	t.Cleanup(slot.Close)
	token := slot.BeginACSOutbound(ACSMessageReady)
	token.Complete(ACSSendObservation{Err: errors.New("ignored")})
	slot.SealACSOutbound()
	if !slot.TraceFinalized() || slot.Trace().Enabled {
		t.Fatalf("disabled trace finalization = %+v", slot.Trace())
	}
}

func TestACSOutboundLifecycleIncludesSendBeforeProposalOrigin(t *testing.T) {
	slot := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: makeids(4)}, Slot: 1,
		Trace: TraceOptions{Enabled: true},
	})
	t.Cleanup(slot.Close)
	token := slot.BeginACSOutbound(ACSMessageEcho)
	token.Complete(ACSSendObservation{Size: 32, Total: time.Millisecond})
	slot.BeginTrace(time.Now())
	slot.SealACSOutbound()
	got := slot.Trace()
	message := got.Messages[ACSMessageEcho]
	if message.ScheduledCount != 1 || message.TerminalCount != 1 ||
		message.OutboundCount != 1 || !got.Transport.Finalized {
		t.Fatalf("pre-origin outbound accounting = %+v", got)
	}
}
```

- [ ] **Step 2: Run the lifecycle tests and verify the API is missing**

Run:

```bash
cd sbc/hbbft
go test ./... -run 'Test(ACSOutboundLifecycle|DisabledACSOutboundTrace)' -count=1
```

Expected: compilation fails because `ACSTraceSchemaV3`, `Transport`, `BeginACSOutbound`, `SealACSOutbound`, and `TraceFinalized` do not exist.

- [ ] **Step 3: Add the v3 schema fields**

In `sbc/hbbft/trace.go`, change the schema constants and structs to:

```go
const (
	ACSTraceSchemaV1      = "bloc-acs-trace/v1"
	ACSTraceSchemaV2      = "bloc-acs-trace/v2"
	ACSTraceSchemaV3      = "bloc-acs-trace/v3"
	ACSTraceSchemaVersion = ACSTraceSchemaV3
)

type ACSTrace struct {
	SchemaVersion string                                `json:"schema_version,omitempty"`
	Enabled       bool                                  `json:"enabled"`
	Transport     ACSTransportTrace                     `json:"transport"`
	Aggregate     ACSAggregateTrace                     `json:"aggregate"`
	Wait          ACSWaitTrace                          `json:"wait_us"`
	Adapter       ACSAdapterTrace                       `json:"adapter"`
	RBC           map[uint64]RBCTrace                   `json:"rbc,omitempty"`
	BBA           map[uint64]BBATrace                   `json:"bba,omitempty"`
	Messages      map[ACSMessageSubtype]ACSMessageTrace `json:"messages,omitempty"`
}

type ACSTransportTrace struct {
	Sealed    bool `json:"sealed"`
	Finalized bool `json:"finalized"`
}
```

Add these fields at the start of `ACSMessageTrace`:

```go
	ScheduledCount   uint64 `json:"scheduled_count"`
	TerminalCount    uint64 `json:"terminal_count"`
	PendingAtDecision uint64 `json:"pending_at_decision"`
```

Run `gofmt -w sbc/hbbft/trace.go` after the edit.

- [ ] **Step 4: Implement the token and recorder lifecycle**

Create `sbc/hbbft/trace_outbound.go` with:

```go
package hbbft

import "sync"

// ACSSendToken binds one scheduled ACS send to the recorder active when the
// message was emitted. Complete is safe to call more than once.
type ACSSendToken struct {
	once     sync.Once
	complete func(ACSSendObservation)
}

func (t *ACSSendToken) Complete(observation ACSSendObservation) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.complete != nil {
			t.complete(observation)
		}
	})
}

func (r *traceRecorder) beginMessageOutbound(subtype ACSMessageSubtype) *ACSSendToken {
	if r == nil || !r.enabled {
		return nil
	}
	r.mu.Lock()
	if r.trace.Transport.Sealed {
		r.mu.Unlock()
		return nil
	}
	entry, ok := r.trace.Messages[subtype]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	entry.ScheduledCount++
	r.trace.Messages[subtype] = entry
	r.mu.Unlock()
	return &ACSSendToken{complete: func(observation ACSSendObservation) {
		r.completeMessageOutbound(subtype, observation)
	}}
}

func (r *traceRecorder) completeMessageOutbound(subtype ACSMessageSubtype, observation ACSSendObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.trace.Messages[subtype]
	if !ok || entry.TerminalCount >= entry.ScheduledCount {
		return
	}
	entry.TerminalCount++
	recordCompletedSend(&entry, observation)
	r.trace.Messages[subtype] = entry
	if r.trace.Transport.Sealed && r.allMessagesTerminalLocked() {
		r.trace.Transport.Finalized = true
	}
}

func (r *traceRecorder) sealMessageOutbound() {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trace.Transport.Sealed {
		return
	}
	for _, subtype := range acsMessageSubtypes {
		entry := r.trace.Messages[subtype]
		entry.PendingAtDecision = entry.ScheduledCount - entry.TerminalCount
		r.trace.Messages[subtype] = entry
	}
	r.trace.Transport.Sealed = true
	r.trace.Transport.Finalized = r.allMessagesTerminalLocked()
}

func (r *traceRecorder) allMessagesTerminalLocked() bool {
	for _, subtype := range acsMessageSubtypes {
		entry := r.trace.Messages[subtype]
		if entry.TerminalCount != entry.ScheduledCount {
			return false
		}
	}
	return true
}

func (r *traceRecorder) traceFinalized() bool {
	if r == nil || !r.enabled {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trace.Transport.Finalized
}
```

Extract the success/failure portion of the old `recordMessageOutbound` body into:

```go
func recordCompletedSend(entry *ACSMessageTrace, observation ACSSendObservation) {
	if observation.Err != nil {
		entry.SendFailureCount++
		return
	}
	entry.OutboundCount++
	if observation.Size > 0 {
		entry.OutboundBytes += uint64(observation.Size)
	}
	entry.SendCount++
	durationUS := observation.Total.Microseconds()
	if durationUS < 0 {
		durationUS = 0
	}
	entry.SendTotalUS += durationUS
	if durationUS > entry.SendMaxUS {
		entry.SendMaxUS = durationUS
	}
	recordSendPhase(&entry.Encode, observation.Encode)
	recordSendPhase(&entry.QueueWait, observation.QueueWait)
	recordSendPhase(&entry.StreamOpen, observation.StreamOpen)
	recordSendPhase(&entry.Write, observation.Write)
	recordSendPhase(&entry.Finalize, observation.Finalize)
	if observation.Reused {
		entry.StreamReuseCount++
	} else {
		entry.StreamOpenCount++
	}
}
```

Retain `traceRecorder.recordMessageOutbound` as a compatibility wrapper:

```go
func (r *traceRecorder) recordMessageOutbound(subtype ACSMessageSubtype, observation ACSSendObservation) {
	token := r.beginMessageOutbound(subtype)
	token.Complete(observation)
}
```

- [ ] **Step 5: Expose the lifecycle through `SlotACS`**

Add to `sbc/hbbft/bloc_slot.go` after `RecordACSInbound`:

```go
func (s *SlotACS) BeginACSOutbound(subtype ACSMessageSubtype) *ACSSendToken {
	if s == nil || s.trace == nil {
		return nil
	}
	return s.trace.beginMessageOutbound(subtype)
}

func (s *SlotACS) SealACSOutbound() {
	if s == nil || s.trace == nil {
		return
	}
	s.trace.sealMessageOutbound()
}

func (s *SlotACS) TraceFinalized() bool {
	return s == nil || s.trace == nil || s.trace.traceFinalized()
}
```

Keep `RecordACSOutbound`, but rewrite it as `s.BeginACSOutbound(subtype).Complete(observation)` so existing non-node callers preserve atomic schedule-plus-complete behavior.

- [ ] **Step 6: Format and run the recorder tests**

Run:

```bash
gofmt -w sbc/hbbft/trace.go sbc/hbbft/trace_outbound.go sbc/hbbft/bloc_slot.go sbc/hbbft/trace_test.go
cd sbc/hbbft
go test ./... -run 'Test(ACSOutboundLifecycle|DisabledACSOutboundTrace|TraceRecorder)' -count=1
```

Expected: PASS, including deep-copy and bounded-key tests.

- [ ] **Step 7: Commit the trace lifecycle core**

```bash
git add sbc/hbbft/trace.go sbc/hbbft/trace_outbound.go sbc/hbbft/bloc_slot.go sbc/hbbft/trace_test.go
git commit -m "feat(hbbft): finalize scheduled ACS sends"
```

### Task 2: Record the first READY trigger and pre-admission threshold counts

**Files:**
- Modify: `sbc/hbbft/trace.go:66-75,286-317`
- Modify: `sbc/hbbft/rbc.go:280-321`
- Test: `sbc/hbbft/rbc_test.go:15-58`

**Interfaces:**
- Consumes: `RBC.emitReady(root []byte) error` from the integrated READY fix and `traceRecorder.recordRBC`.
- Produces: `RBCReadyTrigger`, `RBCReadyTriggerEchoQuorum`, `RBCReadyTriggerRelay`, `traceRecorder.recordRBCReady(uint64, RBCReadyTrigger, int, int)`, and `RBC.emitReady([]byte, RBCReadyTrigger) error`.

- [ ] **Step 1: Add failing trigger assertions for both READY paths**

At the end of `TestRBCTraceRecordsThresholdAndReconstructionMilestones`, add:

```go
	if got.ReadyTrigger != RBCReadyTriggerEchoQuorum ||
		got.ReadyTriggerEchoCount != 3 || got.ReadyTriggerReadyCount != 0 {
		t.Fatalf("ECHO-quorum READY trigger = %+v", got)
	}
```

Add a new test using the fixture from the READY plan:

```go
func TestRBCTraceRecordsReadyRelayTriggerBeforeSelfAdmission(t *testing.T) {
	base := time.Unix(550, 0)
	recorder := newTraceRecorder(makeids(4), true, func() time.Time { return base.Add(time.Microsecond) })
	recorder.begin(base)
	rbc := newRBC(Config{ID: 0, N: 4, F: 1, Nodes: makeids(4)}, 0, recorder)
	t.Cleanup(rbc.stop)
	value := []byte("traceable-rbc-payload!")
	shards, err := makeShards(rbc.enc, value)
	require.NoError(t, err)
	proofs, err := makeProofRequests(shards)
	require.NoError(t, err)

	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCEcho(t, rbc, 2, proofs[2])
	handleRBCReady(t, rbc, 1, proofs[0].RootHash)
	handleRBCReady(t, rbc, 2, proofs[0].RootHash)

	got := recorder.snapshot().RBC[0]
	if got.ReadyTrigger != RBCReadyTriggerRelay ||
		got.ReadyTriggerEchoCount != 2 || got.ReadyTriggerReadyCount != 2 {
		t.Fatalf("READY-relay trigger = %+v", got)
	}
}

func TestRBCTraceKeepsReadyTriggerBeforeProposalOrigin(t *testing.T) {
	recorder := newTraceRecorder(makeids(4), true, time.Now)
	recorder.recordRBCReady(0, RBCReadyTriggerRelay, 2, 2)
	got := recorder.snapshot().RBC[0]
	if got.ReadySent.Recorded || got.ReadyTrigger != RBCReadyTriggerRelay ||
		got.ReadyTriggerEchoCount != 2 || got.ReadyTriggerReadyCount != 2 {
		t.Fatalf("pre-origin READY context = %+v", got)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the fields are absent**

Run: `cd sbc/hbbft && go test ./... -run 'TestRBCTraceRecords(Threshold|ReadyRelay)' -count=1`

Expected: compilation fails on the missing trigger types and fields.

- [ ] **Step 3: Add bounded trigger values and fields**

In `sbc/hbbft/trace.go`, add:

```go
type RBCReadyTrigger string

const (
	RBCReadyTriggerEchoQuorum RBCReadyTrigger = "echo_quorum"
	RBCReadyTriggerRelay      RBCReadyTrigger = "ready_relay"
)
```

Extend `RBCTrace` with:

```go
	ReadyTrigger           RBCReadyTrigger `json:"ready_trigger,omitempty"`
	ReadyTriggerEchoCount  int             `json:"ready_trigger_echo_count,omitempty"`
	ReadyTriggerReadyCount int             `json:"ready_trigger_ready_count,omitempty"`
```

Add this recorder method:

```go
func (r *traceRecorder) recordRBCReady(proposerID uint64, trigger RBCReadyTrigger, echoCount, readyCount int) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.trace.RBC[proposerID]
	if !ok || entry.ReadyTrigger != "" {
		return
	}
	r.pointLocked(&entry.ReadySent)
	entry.ReadyTrigger = trigger
	entry.ReadyTriggerEchoCount = echoCount
	entry.ReadyTriggerReadyCount = readyCount
	r.trace.RBC[proposerID] = entry
}
```

- [ ] **Step 4: Capture counts before local self-admission**

Change the READY helper signature and trace call in `sbc/hbbft/rbc.go` to:

```go
func (r *RBC) emitReady(root []byte, trigger RBCReadyTrigger) error {
	if r.readySent {
		return r.tryDecodeValue(root)
	}
	if _, exists := r.recvReadys[r.ID]; exists {
		return fmt.Errorf("local ready already admitted for node %d", r.ID)
	}
	echoCount := r.countEchos(root)
	readyCount := r.countReadys(root)

	r.readySent = true
	r.recvReadys[r.ID] = root
	r.trace.recordRBCReady(r.proposerID, trigger, echoCount, readyCount)
	r.messages = append(r.messages, &BroadcastMessage{
		Payload: &ReadyRequest{RootHash: root},
	})
	return r.tryDecodeValue(root)
}
```

Call it as `r.emitReady(req.RootHash, RBCReadyTriggerEchoQuorum)` from `handleEchoRequest` and `r.emitReady(req.RootHash, RBCReadyTriggerRelay)` from `handleReadyRequest`. Remove the old `traceRBCReadySent` call path from READY emission; no other milestone changes.

- [ ] **Step 5: Format, test, and commit trigger attribution**

Run:

```bash
gofmt -w sbc/hbbft/trace.go sbc/hbbft/rbc.go sbc/hbbft/rbc_test.go
cd sbc/hbbft
go test ./... -run 'TestRBC(Trace|Ready|RejectsMixedRoot|RejectsReconstruction)' -count=1
```

Expected: PASS; the ECHO path reports counts `3/0`, the relay path reports `2/2`, and commitment regressions remain green.

```bash
git add sbc/hbbft/trace.go sbc/hbbft/rbc.go sbc/hbbft/rbc_test.go
git commit -m "feat(hbbft): trace RBC READY triggers"
```

### Task 3: Schedule before asynchronous send and gate diagnostic result publication

**Files:**
- Modify: `bloc-node/internal/app/node.go:233-251,307-325,592-631,763-773,1179-1225,1341-1374`
- Test: `bloc-node/internal/app/node_slot_test.go:81-119,193-260,399-480,526-541`

**Interfaces:**
- Consumes: `SlotACS.BeginACSOutbound`, `ACSSendToken.Complete`, `SlotACS.SealACSOutbound`, `SlotACS.Trace`, and `classifyACSMessage(*hbbft.SlotMessage) (hbbft.ACSMessageSubtype, error)`.
- Produces: `pendingEnvelope.traceToken *hbbft.ACSSendToken`, `Node.collectACSMessages() ([]pendingEnvelope, error)`, `Node.sendEnvelopeTracked(uint64, WireEnvelope, *hbbft.ACSSendToken)`, and `Node.publishableResultLocked() (*Result, bool)`; the latter requires the caller to hold `lifecycleMu.RLock` or `lifecycleMu.Lock`.

- [ ] **Step 1: Add a blocking-send result-gating regression**

Add this test beside `TestACSSubtypeOutboundAccountingAndSlotIsolation`:

```go
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
	require.Eventually(t, n.slot.TraceFinalized, time.Second, time.Millisecond)
	completed := httptest.NewRecorder()
	n.handleResult(completed, httptest.NewRequest(http.MethodGet, "/result?slot=1", nil))
	if completed.Code != http.StatusOK {
		t.Fatalf("final result = %d %s", completed.Code, completed.Body.String())
	}
	var result Result
	require.NoError(t, json.Unmarshal(completed.Body.Bytes(), &result))
	if !result.ACSTrace.Transport.Finalized || result.Metrics.ACSUS != 123 || result.Metrics.TotalSlotUS != 456 {
		t.Fatalf("final result changed metrics or omitted trace: %+v", result)
	}
}
```

Add a file-publication companion using the same traced node setup and blocking
transport:

```go
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
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var result Result
		return json.Unmarshal(data, &result) == nil && result.ACSTrace.Transport.Finalized
	}, 2*time.Second, 10*time.Millisecond)
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
```

Add `os`, `path/filepath`, and `strings` to `node_slot_test.go` imports if they are not
already present.

- [ ] **Step 2: Add status and failure compatibility assertions**

Extend `TestSlotStatusIncludesACSProgress` to require Boolean keys `acs_trace_enabled`, `acs_trace_finalized`, and `acs_trace_finalization_pending`. Add a trace-enabled subtest to `TestResultEndpointDistinguishesPendingSuccessFailureAndWrongSlot` that calls `markSlotFailed("planning")` and still expects HTTP `422` without waiting for trace finalization.

- [ ] **Step 3: Run the focused tests and confirm publication is currently premature**

Run:

```bash
cd bloc-node
go test ./internal/app -run 'Test(TraceEnabledResult|ResultEndpointDistinguishes|SlotStatusIncludes)' -count=1
```

Expected before implementation: compilation fails on `sendEnvelopeTracked`; once only that helper is temporarily stubbed, the result test exposes the current premature HTTP `200` behavior.

- [ ] **Step 4: Attach tokens while draining ACS messages**

Change `pendingEnvelope` to:

```go
type pendingEnvelope struct {
	to         uint64
	envelope   WireEnvelope
	traceToken *hbbft.ACSSendToken
}
```

Change `collectACSMessages` to return `([]pendingEnvelope, error)`. Classify each
`slotMsg` and begin its token before appending:

```go
		subtype, err := classifyACSMessage(slotMsg)
		if err != nil {
			return nil, fmt.Errorf("classify emitted ACS message: %w", err)
		}
		out = append(out, pendingEnvelope{
			to:         msg.To,
			envelope:   WireEnvelope{From: n.self.ID, Kind: "acs", Slot: n.id, ACS: slotMsg},
			traceToken: n.slot.BeginACSOutbound(subtype),
		})
	}
	return out, nil
```

This call is synchronous and occurs before `n.slot.Output()` at `stepACS:603`.
Update `stepACS` to handle the new error before consuming output:

```go
	messages, err := n.collectACSMessages()
	if err != nil {
		n.acsMu.Unlock()
		return nil, err
	}
	output := n.slot.Output()
	n.acsMu.Unlock()
	for _, env := range messages {
		n.sendEnvelopeTracked(env.to, env.envelope, env.traceToken)
	}
```

- [ ] **Step 5: Complete every asynchronous exit exactly once**

Keep `sendEnvelope` as the general wrapper for share sends and direct tests:

```go
func (n *Node) sendEnvelope(to uint64, env WireEnvelope) {
	var token *hbbft.ACSSendToken
	if env.Kind == "acs" && env.ACS != nil {
		if subtype, err := classifyACSMessage(env.ACS); err == nil {
			token = n.slot.BeginACSOutbound(subtype)
		}
	}
	n.sendEnvelopeTracked(to, env, token)
}
```

Move the old goroutine body to `sendEnvelopeTracked`. Register token completion after the lifecycle unlock defer so completion runs first in LIFO order:

```go
func (n *Node) sendEnvelopeTracked(to uint64, env WireEnvelope, token *hbbft.ACSSendToken) {
	go func() {
		n.lifecycleMu.RLock()
		defer n.lifecycleMu.RUnlock()
		observation := hbbft.ACSSendObservation{}
		defer func() { token.Complete(observation) }()
		if env.Slot != n.id {
			observation.Err = fmt.Errorf("stale outbound slot %d, active slot %d", env.Slot, n.id)
			return
		}
		if n.faults.Delay > 0 {
			time.Sleep(n.faults.Delay)
		}
		env.From = n.self.ID
		env.To = to
		env.Direct = true
		sendStarted := time.Now()
		result, err := n.transport.Send(context.Background(), to, env)
		observation = hbbft.ACSSendObservation{
			Size: result.EncodedBytes, Total: time.Since(sendStarted),
			Encode: result.EncodeDuration, QueueWait: result.QueueWaitDuration,
			StreamOpen: result.StreamOpenDuration, Write: result.WriteDuration,
			Finalize: result.FinalizeDuration, Reused: result.StreamReused, Err: err,
		}
		if err != nil {
			log.Printf("send %s to %d failed: %v", env.Kind, to, err)
			return
		}
		n.recordOutbound(env.Kind, result.EncodedBytes)
	}()
}
```

Delete the old post-send `n.slot.RecordACSOutbound` block. The token closure owns the original slot recorder even if a later slot becomes active.

- [ ] **Step 6: Seal at node receipt and add one publication helper**

Change `captureACSTrace` to call `n.slot.SealACSOutbound()` immediately after `MarkNodeOutputReceived()` and before the snapshot.

Add:

```go
// publishableResultLocked returns a detached result carrying the latest final
// trace. The caller must hold lifecycleMu for the active slot.
func (n *Node) publishableResultLocked() (*Result, bool) {
	trace := n.slot.Trace()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.result == nil || (trace.Enabled && !trace.Transport.Finalized) {
		return nil, false
	}
	result := *n.result
	result.ACSTrace = trace
	return &result, true
}
```

Update `handleResult` so it returns a terminal failure first, then HTTP `200` only from `publishableResultLocked`, otherwise the existing HTTP `202 {"status":"pending"}`. Update `writeResultWhenReady` to use the same ordering and helper while holding `lifecycleMu.RLock`.

In `prepareSlotWithLimit`, after confirming the phase is `slotCompleted` or
`slotFailed` and before closing the old slot, add:

```go
	if phase == slotCompleted && n.cfg.Diagnostics.ACSTrace && !n.slot.TraceFinalized() {
		return fmt.Errorf("slot %d trace finalization pending", n.id)
	}
```

Terminal failures remain replaceable immediately because they do not publish a
successful diagnostic artifact.

- [ ] **Step 7: Expose diagnostic finalization without changing material completion**

In `handleSlotStatus`, snapshot the trace and add:

```go
	trace := n.slot.Trace()
	status["acs_trace_enabled"] = trace.Enabled
	status["acs_trace_finalized"] = !trace.Enabled || trace.Transport.Finalized
	status["acs_trace_finalization_pending"] = trace.Enabled && trace.Transport.Sealed && !trace.Transport.Finalized
```

Keep `complete` defined as `n.result != nil`; it remains the materialization state, not the diagnostic publication state.

- [ ] **Step 8: Format and run node lifecycle tests**

Run:

```bash
gofmt -w bloc-node/internal/app/node.go bloc-node/internal/app/node_slot_test.go
cd bloc-node
go test ./internal/app -run 'Test(ACSSubtype|TraceEnabledResult|ACSTraceLifecycle|PrepareSlotWaits|ResultEndpointDistinguishes|SlotStatusIncludes)' -count=1
go test -race ./internal/app -run 'Test(ACSSubtype|TraceEnabledResult|PrepareSlotWaits|StepACS)' -count=1
```

Expected: PASS with no race. The blocked trace returns `202`, the released trace returns `200`, failure remains `422`, and the old-slot trace—not the new slot—receives completion.

- [ ] **Step 9: Commit node integration**

```bash
git add bloc-node/internal/app/node.go bloc-node/internal/app/node_slot_test.go
git commit -m "fix(node): await final ACS trace publication"
```

### Task 4: Fail closed on incomplete v3 artifacts and expose bounded CSV totals

**Files:**
- Modify: `bloc-node/internal/app/acs_trace_artifact.go:60-100,143-329,408-459,474-525`
- Test: `bloc-node/internal/app/acs_trace_artifact_test.go:80-125,250-330,393-435`
- Modify: `bloc-node/internal/app/eval_suite.go:751-842`
- Test: `bloc-node/internal/app/eval_suite_test.go:250-335`

**Interfaces:**
- Consumes: `hbbft.ACSTraceSchemaV3`, `hbbft.ACSTransportTrace`, and v3 message lifecycle fields from Tasks 1-3.
- Produces: v1/v2/v3 artifact readers, v3-only new-run validation, and CSV columns `acs_trace_sealed`, `acs_trace_finalized`, `acs_scheduled_sends`, `acs_terminal_sends`, and `acs_pending_at_decision`.

- [ ] **Step 1: Add v3 fail-closed artifact cases**

In `TestValidateACSTraceArtifactFailsClosed`, add mutations with these expected categories:

```go
{
	name: "v3 trace not finalized", want: "transport trace is not sealed and finalized",
	mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
		runs[0].Results[0].ACSTrace.Transport.Finalized = false
		(*records)[0].Transport.Finalized = false
	},
},
{
	name: "scheduled terminal mismatch", want: "scheduled count",
	mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
		message := runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady]
		message.ScheduledCount++
		runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady] = message
		(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
	},
},
{
	name: "terminal outcome mismatch", want: "terminal count",
	mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
		message := runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady]
		message.TerminalCount++
		runs[0].Results[0].ACSTrace.Messages[hbbft.ACSMessageReady] = message
		(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
	},
},
{
	name: "missing READY trigger", want: "missing READY trigger",
	mutate: func(_ *suiteManifest, runs []EvalRun, records *[]acsTraceArtifactRecord) {
		ready := runs[0].Results[0].ACSTrace.RBC[0]
		ready.ReadySent = hbbft.TracePoint{Recorded: true, OffsetUS: 1}
		ready.ReadyTrigger = ""
		runs[0].Results[0].ACSTrace.RBC[0] = ready
		(*records)[0] = newACSTraceArtifactRecord(runs[0], runs[0].Results[0])
	},
},
```

Add a test that explicitly sets manifest, result, and records to `ACSTraceSchemaV2`, zeroes `Transport` and lifecycle fields, and still expects `validateACSTraceArtifact` to pass. Keep the existing v1 test unchanged.

- [ ] **Step 2: Run artifact tests and confirm v3 incompleteness is accepted today**

Run: `cd bloc-node && go test ./internal/app -run 'TestValidateACSTraceArtifact' -count=1`

Expected before implementation: the new fail-closed cases fail because the validator ignores `Transport`, `ScheduledCount`, and `TerminalCount`.

- [ ] **Step 3: Add transport state to deterministic artifact records**

Add `Transport hbbft.ACSTransportTrace \`json:"transport"\`` to `acsTraceArtifactRecord`, copy it in `newACSTraceArtifactRecord`, and include it in the artifact/result `reflect.DeepEqual` block.

Change `acsTraceSchemaForRuns` to require `hbbft.ACSTraceSchemaVersion` for nonempty new-run schemas. Change `supportedACSTraceSchema` to accept V1, V2, and V3.

- [ ] **Step 4: Enforce the final v3 equations**

At the start of `validateACSTraceRecord`, add:

```go
	if record.SchemaVersion == hbbft.ACSTraceSchemaV3 &&
		(!record.Transport.Sealed || !record.Transport.Finalized) {
		return fmt.Errorf("transport trace is not sealed and finalized")
	}
```

In `validateMessageRecords`, apply existing phase validation for both V2 and V3, then call:

```go
func validateV3MessageLifecycle(subtype hbbft.ACSMessageSubtype, trace hbbft.ACSMessageTrace) error {
	if trace.ScheduledCount != trace.TerminalCount {
		return fmt.Errorf("ACS message subtype %q scheduled count %d does not match terminal count %d", subtype, trace.ScheduledCount, trace.TerminalCount)
	}
	if trace.TerminalCount != trace.OutboundCount+trace.SendFailureCount {
		return fmt.Errorf("ACS message subtype %q terminal count %d does not match successful plus failed outcomes %d", subtype, trace.TerminalCount, trace.OutboundCount+trace.SendFailureCount)
	}
	if trace.SendCount != trace.OutboundCount || trace.PendingAtDecision > trace.ScheduledCount {
		return fmt.Errorf("ACS message subtype %q has inconsistent successful or pending counts", subtype)
	}
	return nil
}
```

Change the call to
`validateRBCRecords(record.RBC, members, record.SchemaVersion)` and the helper
signature to:

```go
func validateRBCRecords(records []acsRBCTraceRecord, members map[uint64]struct{}, schema string) error
```

For v3 records, validate READY context inside that helper. An empty trigger is
valid only when `ReadySent.Recorded` is false and both counts are zero. A
nonempty trigger is allowed with an unrecorded timestamp because remote
progress can emit READY before the local proposal-ready origin. Require one of
the two enum values and nonnegative counts. Derive
`n := len(members)` and `f := (n-1)/3`; require
`ReadyTriggerEchoCount >= n-f` for `echo_quorum`, or
`ReadyTriggerReadyCount >= f+1` for `ready_relay`. Return errors containing
`missing READY trigger`, `invalid READY trigger`, or `READY trigger threshold`
so the failing fixtures are stable and diagnostic.

- [ ] **Step 5: Make the artifact fixture a valid finalized v3 trace**

In `artifactTestTrace`, set `Transport: hbbft.ACSTransportTrace{Sealed: true, Finalized: true}`. For every fixed subtype, initialize lifecycle values consistently; zero is valid. For the two populated messages used by CSV tests, set `ScheduledCount` and `TerminalCount` to `OutboundCount+SendFailureCount` and retain any nonzero `PendingAtDecision` only as historical state.

- [ ] **Step 6: Extend the node-measurement summary**

Insert these columns after `acs_trace_schema` in `writeNodeMeasurements`:

```text
acs_trace_sealed
acs_trace_finalized
acs_scheduled_sends
acs_terminal_sends
acs_pending_at_decision
```

Change `acsTraceSummaryValues` to `columnCount = 42`, sum the three lifecycle counts across messages, and emit:

```go
		trace.SchemaVersion,
		strconv.FormatBool(trace.Transport.Sealed),
		strconv.FormatBool(trace.Transport.Finalized),
		strconv.FormatUint(scheduledCount, 10),
		strconv.FormatUint(terminalCount, 10),
		strconv.FormatUint(pendingAtDecision, 10),
```

before the existing milestone values. Extend `TestNodeMeasurementsIncludeACSTraceSummary` with exact expected totals and both Boolean fields.

- [ ] **Step 7: Run artifact and evaluator output tests**

Run:

```bash
gofmt -w bloc-node/internal/app/acs_trace_artifact.go bloc-node/internal/app/acs_trace_artifact_test.go bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_suite_test.go
cd bloc-node
go test ./internal/app -run 'Test(ValidateACSTraceArtifact|WriteACSTraceArtifact|WriteSuiteOutputsGatesACSTrace|NodeMeasurementsIncludeACSTrace)' -count=1
```

Expected: PASS. V1/V2 fixtures are readable, new run schema selection is V3, and an incomplete V3 record fails before artifact acceptance.

- [ ] **Step 8: Commit artifact enforcement**

```bash
git add bloc-node/internal/app/acs_trace_artifact.go bloc-node/internal/app/acs_trace_artifact_test.go bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_suite_test.go
git commit -m "feat(eval): require finalized ACS trace v3"
```

### Task 5: Run the full local gate and update canonical documentation

**Files:**
- Modify: `sbc/hbbft/README.md`
- Modify: `bloc-node/README.md`
- Modify: `docs/modules/hbbft.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Consumes: finalized v3 recorder, node publication gate, artifact validator, and evaluator summaries from Tasks 1-4.
- Produces: validated local evidence and canonical v3 interpretation for the later stream-lane experiment.

- [ ] **Step 1: Update the trace contracts in canonical docs**

Document these exact points across the owning README/deep-dive sections:

```markdown
- New diagnostics use `bloc-acs-trace/v3`; v1/v2 remain readable historical
  formats.
- The trace admits ACS sends synchronously when emitted, seals at local ACS
  output, and finalizes after every admitted send terminates.
- `pending_at_decision` is historical and is not reduced as sends finish.
- Successful phase totals describe sender-side completion, not remote receipt.
- Trace-enabled result publication waits for finalization, but `ACSUS`, protocol
  progress, merge/plan, share generation, and materialization do not.
- READY trigger context is `echo_quorum` or `ready_relay` and records matching
  ECHO/READY counts before local self-admission.
```

In `docs/VALIDATION.md`, change the local trace-smoke acceptance to manifest schema `bloc-acs-trace/v3` and require every record to be sealed/finalized with `scheduled=terminal=successful+failed` per subtype. Retain the v1/v2 compatibility note.

- [ ] **Step 2: Run all affected normal tests**

Run:

```bash
cd sbc/hbbft && go test ./... -count=1
cd bloc-node && go test ./... -count=1
```

Expected: both module suites pass.

- [ ] **Step 3: Run concurrency and safety gates**

Run:

```bash
cd sbc/hbbft && go test -race ./... -run 'Test(RBC|ACS|BBA|SlotACS|Trace)' -count=1
cd bloc-node && go test -race ./internal/app -run 'Test(ACS|Trace|StepACS|PrepareSlot|Result|Eval)' -count=1
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

Expected: all commands exit `0`; no race is reported; the safety campaign retains only successful, cross-node-consistent measured slots.

- [ ] **Step 4: Measure observer overhead**

Run from `sbc/hbbft`:

```bash
go test -run '^$' -bench '^BenchmarkACSTrace$' -benchmem -count 10
```

Retain the raw output under ignored `results/local/acs-trace-v3-finalization/`. Compute each `n4/n7` and batch `8/32/128` trace-on median against its trace-off median. Expected gate: no trace-enabled cell exceeds its matched trace-disabled median by more than `2%`; if one does, leave the issue open and do not proceed to the lane experiment.

- [ ] **Step 5: Run the local finalized-artifact smoke**

Run:

```bash
cd bloc-node
go run ./cmd/bloc-node eval-suite \
  --execution-mode isolated --node-counts 4 --batch-sizes 8 --bmax 8 \
  --warmups 0 --repetitions 1 --repetition-blocks 1 \
  --timeout 30s --deadline 12s --acs-trace \
  --experiment-id acs-trace-v3-smoke --out-dir results/local/acs-trace-v3-smoke
```

Expected: one successful, consistent measured run; manifest schema `bloc-acs-trace/v3`; four JSONL records; every record sealed and finalized; every subtype satisfies the final equations. If loopback listeners are blocked by the sandbox, rerun only with the required local-listener approval and record both attempts.

- [ ] **Step 6: Record the change and resolve the trace risk**

Append to `docs/CHANGELOG.md`:

```markdown
- Added complete pre-decision ACS send accounting in `bloc-acs-trace/v3` with
  synchronous scheduling, exactly-once terminal completion, immutable
  pending-at-decision counts, READY trigger context, fail-closed artifacts, and
  result-publication finalization without changing protocol latency boundaries.
```

In `docs/STATUS.md`, set `Last reviewed` to the execution date, remove the risk headed `ACS transport traces are right-censored at local output`, retain all unrelated risks, and make the persistent control/data lane implementation the first immediate action. Do not change the active milestone or last-known-good baseline without separate authorization.

- [ ] **Step 7: Inspect, commit, and post evidence**

Run:

```bash
git diff --check
git status --short --branch
git diff -- sbc/hbbft/README.md bloc-node/README.md docs/modules/hbbft.md docs/modules/bloc-node.md docs/VALIDATION.md docs/CHANGELOG.md docs/STATUS.md
```

Then commit only the seven documentation files:

```bash
git add sbc/hbbft/README.md bloc-node/README.md docs/modules/hbbft.md docs/modules/bloc-node.md docs/VALIDATION.md docs/CHANGELOG.md docs/STATUS.md
git commit -m "docs: define finalized ACS trace v3"
```

Post the exact test, race, benchmark, safety, and smoke outcomes to `Finalize pre-decision ACS transport traces`.

- [ ] **Step 8: Perform the final repository gate**

Run:

```bash
git status --short --branch
git log -5 --oneline
git rev-list --left-right --count main...HEAD
```

Expected: five focused task commits are present; no task-owned file is modified; the user's PDF remains unstaged if it exists in this worktree. Close the issue only if the v3 smoke, final equations, race gates, safety campaign, and 2% observer limit all pass.
