# ACS Critical-Path Latency Attribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add bounded, monotonic ACS tracing and matched analysis artifacts
that identify the RBC, BBA, completion, adapter, and transport gates responsible
for the accepted cross-region ACS latency without changing protocol behavior.

**Architecture:** A mutex-protected recorder owned by one `SlotACS` receives
bounded events from the existing ACS, RBC, and BBA event loops and exposes a
deep-copy snapshot in `SlotOutput`. `bloc-node` supplies the proposal-ready
monotonic origin, adds bounded transport summaries, and persists aggregate and
detailed trace artifacts. A separate Python analysis module compares matched
same-AZ and three-region diagnostic runs using milestone offsets rather than an
invalid additive decomposition of concurrent RBC/BBA work.

**Tech Stack:** Go 1.24 modules (`sbc/hbbft`, `bloc-node`), libp2p transport,
JSON/CSV/JSONL evaluator artifacts, Python 3 with pandas/matplotlib/pytest,
Bash campaign validators.

**Spec:** [GitHub Issue #23](https://github.com/VascoMS/bloc/issues/23)

## Global Constraints

- Preserve current RBC, BBA, ACS membership, thresholds, output, message
  sequence, error, and consumptive-output semantics.
- Preserve the top-level protobuf wire contract and existing `kind=acs` value.
- Preserve legacy `acs_us` and historical artifact compatibility.
- Use only process-local monotonic durations; never subtract timestamps from
  different hosts.
- Keep trace cardinality bounded by configured membership and the five fixed
  ACS subtypes `proof`, `echo`, `ready`, `bval`, and `aux`.
- Detailed slot/proposer/peer/epoch data must not become Prometheus labels.
- An artifact requires ACS tracing only when its manifest explicitly enables
  `bloc-acs-trace/v1`.
- Diagnostic campaigns use 5 warm-ups and 30 measured attempts per cell and
  support p50/p95/max, not p99.
- No AWS allocation or live campaign is authorized by this plan.
- New behavior follows strict red-green-refactor cycles; each named test must
  fail for the missing production behavior before implementation.

---

### Task 1: Define the bounded trace value model and recorder

**Files:**

- Create: `sbc/hbbft/trace.go`
- Create: `sbc/hbbft/trace_test.go`

**Interfaces:**

- Consumes: configured proposer IDs and a process-local `func() time.Time`.
- Produces:
  - `const ACSTraceSchemaVersion = "bloc-acs-trace/v1"`
  - `type TracePoint struct { Recorded bool; OffsetUS int64 }`
  - `type ACSTrace`, `ACSAggregateTrace`, `RBCTrace`, `BBATrace`,
    `ACSWaitTrace`, `ACSAdapterTrace`, and `ACSMessageTrace`
  - internal `newTraceRecorder(nodes []uint64, enabled bool, now func() time.Time) *traceRecorder`
  - `(*traceRecorder).begin(time.Time)` and `(*traceRecorder).snapshot() ACSTrace`

- [ ] **Step 1: Write the failing construction and disabled-path tests**

```go
func TestTraceRecorderBeginsAtProposalReadyAndReturnsDeepCopy(t *testing.T) {
	base := time.Unix(100, 0)
	now := base
	recorder := newTraceRecorder([]uint64{0, 1, 2, 3}, true, func() time.Time { return now })
	recorder.begin(base)
	now = base.Add(25 * time.Microsecond)
	recorder.recordAggregate(traceACSInputStarted)
	recorder.recordRBC(0, traceRBCProofAccepted)

	first := recorder.snapshot()
	if !first.Aggregate.InputStarted.Recorded || first.Aggregate.InputStarted.OffsetUS != 25 {
		t.Fatalf("input start = %+v", first.Aggregate.InputStarted)
	}
	first.RBC[0] = RBCTrace{}
	second := recorder.snapshot()
	if !second.Aggregate.InputStarted.Recorded || !second.RBC[0].ProofAccepted.Recorded {
		t.Fatal("snapshot mutation changed recorder state")
	}
}

func TestDisabledTraceRecorderReturnsDisabledEmptySnapshot(t *testing.T) {
	recorder := newTraceRecorder([]uint64{0, 1, 2, 3}, false, time.Now)
	recorder.begin(time.Now())
	recorder.recordAggregate(traceACSInputStarted)
	got := recorder.snapshot()
	if got.Enabled || got.SchemaVersion != "" || len(got.RBC) != 0 || len(got.BBA) != 0 {
		t.Fatalf("disabled trace leaked state: %+v", got)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `cd sbc/hbbft && go test ./... -run 'TestTraceRecorder' -count=1`

Expected: compile failure because `newTraceRecorder`, trace types, and event
constants do not exist.

- [ ] **Step 3: Implement the minimal value model and recorder**

```go
const ACSTraceSchemaVersion = "bloc-acs-trace/v1"

type TracePoint struct {
	Recorded bool  `json:"recorded"`
	OffsetUS int64 `json:"offset_us"`
}

type ACSMessageSubtype string

const (
	ACSMessageProof ACSMessageSubtype = "proof"
	ACSMessageEcho  ACSMessageSubtype = "echo"
	ACSMessageReady ACSMessageSubtype = "ready"
	ACSMessageBVAL  ACSMessageSubtype = "bval"
	ACSMessageAUX   ACSMessageSubtype = "aux"
)

type ACSTrace struct {
	SchemaVersion string                            `json:"schema_version,omitempty"`
	Enabled       bool                              `json:"enabled"`
	Aggregate     ACSAggregateTrace                 `json:"aggregate"`
	Wait          ACSWaitTrace                      `json:"wait_us"`
	Adapter       ACSAdapterTrace                   `json:"adapter"`
	RBC           map[uint64]RBCTrace                `json:"rbc,omitempty"`
	BBA           map[uint64]BBATrace                `json:"bba,omitempty"`
	Messages      map[ACSMessageSubtype]ACSMessageTrace `json:"messages,omitempty"`
}

type traceRecorder struct {
	mu      sync.Mutex
	enabled bool
	now     func() time.Time
	base    time.Time
	trace   ACSTrace
}
```

Initialize only configured proposer IDs and fixed subtype keys. `pointLocked`
must ignore repeated events and negative offsets. `snapshot` must clone every
map and slice.

- [ ] **Step 4: Run the focused tests and verify GREEN**

Run: `cd sbc/hbbft && go test ./... -run 'TestTraceRecorder' -count=1`

Expected: PASS.

- [ ] **Step 5: Add bounds and one-shot mutation tests**

Add literal assertions proving an unknown proposer cannot create an entry, a
second event cannot replace the first timestamp, and each fixed message subtype
exists exactly once in the initial snapshot.

- [ ] **Step 6: Run the complete hbbft suite**

Run: `cd sbc/hbbft && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the trace foundation**

```bash
git add sbc/hbbft/trace.go sbc/hbbft/trace_test.go
git commit -m "feat(hbbft): add bounded ACS trace recorder"
```

### Task 2: Share the recorder across SlotACS, ACS, RBC, and BBA

**Files:**

- Modify: `sbc/hbbft/acs.go`
- Modify: `sbc/hbbft/rbc.go`
- Modify: `sbc/hbbft/bba.go`
- Modify: `sbc/hbbft/bloc_slot.go`
- Modify: `sbc/hbbft/acs_test.go`
- Modify: `sbc/hbbft/bloc_slot_test.go`

**Interfaces:**

- Consumes: Task 1 `traceRecorder`.
- Produces:
  - `type TraceOptions struct { Enabled bool; Now func() time.Time }`
  - `SlotConfig.Trace TraceOptions`
  - `(*SlotACS).BeginTrace(proposalReady time.Time)`
  - `(*SlotACS).Trace() ACSTrace`
  - internal `newACS`, `newRBC`, and `newBBA` constructors that accept the
    shared recorder while public constructors retain their signatures.

- [ ] **Step 1: Write failing shared-recorder and compatibility tests**

```go
func TestSlotACSBeginTraceUsesOneRecorderAcrossChildren(t *testing.T) {
	base := time.Unix(200, 0)
	now := base
	slot := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: makeids(4)},
		Slot: 9,
		Trace: TraceOptions{Enabled: true, Now: func() time.Time { return now }},
	})
	slot.BeginTrace(base)
	if slot.acs.trace == nil || slot.acs.rbcInstances[0].trace != slot.acs.trace ||
		slot.acs.bbaInstances[0].trace != slot.acs.trace {
		t.Fatal("ACS children do not share the slot recorder")
	}
}

func TestNewACSPreservesTraceDisabledCompatibility(t *testing.T) {
	acs := NewACS(Config{N: 4, ID: 0, Nodes: makeids(4)})
	if got := acs.trace.snapshot(); got.Enabled {
		t.Fatalf("legacy constructor enabled tracing: %+v", got)
	}
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run: `cd sbc/hbbft && go test ./... -run 'TestSlotACSBeginTrace|TestNewACSPreserves' -count=1`

Expected: compile failure because trace options and child recorder fields do
not exist.

- [ ] **Step 3: Add constructor plumbing without recording events**

```go
func NewACS(cfg Config) *ACS {
	return newACS(cfg, newTraceRecorder(cfg.Nodes, false, time.Now))
}

func newACS(cfg Config, trace *traceRecorder) *ACS {
	if cfg.F == 0 {
		cfg.F = (cfg.N - 1) / 3
	}
	if trace == nil {
		trace = newTraceRecorder(cfg.Nodes, false, time.Now)
	}
	acs := &ACS{
		Config: cfg, rbcInstances: make(map[uint64]*RBC),
		bbaInstances: make(map[uint64]*BBA),
		rbcResults: make(map[uint64][]byte), bbaResults: make(map[uint64]bool),
		messageQue: newMessageQue(), closeCh: make(chan struct{}),
		inputCh: make(chan acsInputTuple), messageCh: make(chan acsMessageTuple),
		progressCh: make(chan acsProgressTuple), trace: trace,
	}
	for _, id := range cfg.Nodes {
		acs.rbcInstances[id] = newRBC(cfg, id, trace)
		acs.bbaInstances[id] = newBBA(cfg, id, trace)
	}
	go acs.run()
	return acs
}

func NewRBC(cfg Config, proposerID uint64) *RBC {
	return newRBC(cfg, proposerID, newTraceRecorder(cfg.Nodes, false, time.Now))
}

func NewBBA(cfg Config) *BBA {
	return newBBA(cfg, 0, newTraceRecorder(cfg.Nodes, false, time.Now))
}
```

`NewSlotACS` creates one recorder, calls `newACS`, and stores it on `SlotACS`.
Do not change public constructor parameters used by the inherited HoneyBadger
driver or existing tests.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `cd sbc/hbbft && go test ./... -run 'TestSlotACSBeginTrace|TestNewACSPreserves' -count=1`

Expected: PASS.

- [ ] **Step 5: Run the complete hbbft suite and race-selected constructors**

Run: `cd sbc/hbbft && go test ./... -count=1`

Run: `cd sbc/hbbft && go test -race ./... -run 'TestNew(ACS|RBC|BBA)|TestSlotACS' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit constructor plumbing**

```bash
git add sbc/hbbft/acs.go sbc/hbbft/rbc.go sbc/hbbft/bba.go sbc/hbbft/bloc_slot.go sbc/hbbft/acs_test.go sbc/hbbft/bloc_slot_test.go
git commit -m "refactor(hbbft): share slot trace recorder"
```

### Task 3: Record RBC milestones without changing RBC output

**Files:**

- Modify: `sbc/hbbft/trace.go`
- Modify: `sbc/hbbft/rbc.go`
- Modify: `sbc/hbbft/rbc_test.go`
- Modify: `sbc/hbbft/trace_test.go`

**Interfaces:**

- Consumes: shared recorder from Task 2.
- Produces: `recordRBC(proposerID uint64, event rbcTraceEvent)` with events
  `proof_accepted`, `echo_sent`, `ready_sent`, `decode_eligible`,
  `reconstruction_started`, `reconstruction_finished`, and `output_stored`.

- [ ] **Step 1: Write a failing threshold/reconstruction trace test**

Use the existing literal four-node proof fixture. Begin at offset zero, advance
the fake clock before each delivery, and assert:

```go
got := rbc.trace.snapshot().RBC[rbc.proposerID]
if !got.ProofAccepted.Recorded || !got.EchoSent.Recorded ||
	!got.ReadySent.Recorded || !got.DecodeEligible.Recorded ||
	!got.ReconstructionStarted.Recorded ||
	!got.ReconstructionFinished.Recorded || !got.OutputStored.Recorded {
	t.Fatalf("incomplete RBC trace: %+v", got)
}
if got.ReconstructionStarted.OffsetUS > got.ReconstructionFinished.OffsetUS ||
	got.ReconstructionFinished.OffsetUS > got.OutputStored.OffsetUS {
	t.Fatalf("RBC reconstruction order: %+v", got)
}
```

Name the mutation: removing any event call or moving decode eligibility after
output must fail this test.

- [ ] **Step 2: Run the RBC trace test and verify RED**

Run: `cd sbc/hbbft && go test ./... -run TestRBCTraceRecordsThresholdAndReconstructionMilestones -count=1`

Expected: compile failure because `RBCTrace` lacks the milestone fields.

- [ ] **Step 3: Add minimal event recording at existing transitions**

Record after successful proof verification, when `echoSent` and `readySent`
change, immediately before and after `enc.Reconstruct` plus commitment rebuild,
and when `outputDecoded` becomes true. Do not record on rejected or duplicate
messages.

- [ ] **Step 4: Run focused and complete RBC tests**

Run: `cd sbc/hbbft && go test ./... -run 'TestRBC|TestMakeProof|TestMixedRoot' -count=1`

Expected: PASS.

- [ ] **Step 5: Add retry and duplicate tests**

Assert a failed/incomplete reconstruction attempt does not set
`ReconstructionFinished` or `OutputStored`, a later successful attempt does,
and duplicate delivery cannot replace the original event offsets.

- [ ] **Step 6: Run hbbft normal and focused race suites**

Run: `cd sbc/hbbft && go test ./... -count=1`

Run: `cd sbc/hbbft && go test -race ./... -run 'TestRBC' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit RBC tracing**

```bash
git add sbc/hbbft/trace.go sbc/hbbft/trace_test.go sbc/hbbft/rbc.go sbc/hbbft/rbc_test.go
git commit -m "feat(hbbft): trace RBC quorum milestones"
```

### Task 4: Record BBA milestones, epochs, and ACS completion gates

**Files:**

- Modify: `sbc/hbbft/trace.go`
- Modify: `sbc/hbbft/bba.go`
- Modify: `sbc/hbbft/acs.go`
- Modify: `sbc/hbbft/bba_test.go`
- Modify: `sbc/hbbft/acs_test.go`

**Interfaces:**

- Consumes: Task 3 trace model.
- Produces:
  - `recordBBA(proposerID uint64, event bbaTraceEvent, value bool, epoch uint32)`
  - `recordAggregate(event aggregateTraceEvent)`
  - `transitionWait(reason string)` and `finishWait()`
  - aggregate milestones and wait totals required by Issue #23.

- [ ] **Step 1: Write failing BBA event tests**

Extend the real BBA epoch fixture to assert input value/time, first bin value,
first AUX, valid AUX quorum, decision value/time, maximum epoch, and done time.
Use literal epochs and values; do not derive expectations from `progress()`.

- [ ] **Step 2: Verify BBA RED**

Run: `cd sbc/hbbft && go test ./... -run 'TestBBATrace' -count=1`

Expected: compile failure for missing BBA trace fields/event methods.

- [ ] **Step 3: Record BBA transitions in the existing event loop**

```go
func (b *BBA) inputValue(val bool) error {
	if b.epoch != 0 || b.estimated != nil {
		return nil
	}
	b.trace.recordBBA(b.proposerID, traceBBAInput, val, b.epoch)
	b.estimated = val
	b.sentBvals = append(b.sentBvals, val)
	b.addMessage(NewAgreementMessage(int(b.epoch), &BvalRequest{val}))
	return b.handleBvalRequest(b.ID, val)
}
```

Record bin/AUX/quorum/decision/done only where the corresponding state first
changes. Update `MaxEpoch` monotonically without creating per-epoch slices.

- [ ] **Step 4: Verify BBA GREEN and run the BBA suite**

Run: `cd sbc/hbbft && go test ./... -run 'TestBBA' -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing ACS aggregate and wait-attribution tests**

Use direct four-node ACS state fixtures and the fake clock to assert these
literal state transitions:

```go
recorder.begin(base)
recorder.transitionWait("waiting_for_n_minus_f_true_bba_results")
now = base.Add(50 * time.Microsecond)
recorder.transitionWait("waiting_for_all_bba_results")
now = base.Add(80 * time.Microsecond)
recorder.transitionWait("waiting_for_truthy_rbc_outputs")
now = base.Add(100 * time.Microsecond)
recorder.finishWait()
got := recorder.snapshot().Wait
if got.TrueBBAQuorumUS != 50 || got.AllBBAUS != 30 || got.TruthyRBCUS != 20 {
	t.Fatalf("wait attribution = %+v", got)
}
```

Also assert first/`N-F` RBC output, first/`N-F` true decision, false-input
injection, all-BBA, truthy-RBC-ready, and core-decision milestones.

- [ ] **Step 6: Verify ACS RED**

Run: `cd sbc/hbbft && go test ./... -run 'TestACSTrace|TestTraceWait' -count=1`

Expected: failures because aggregate recording and wait transitions are absent.

- [ ] **Step 7: Implement aggregate transitions without changing decisions**

Call one helper after existing map insertions and from
`tryCompleteAgreement`. Derive aggregate counts from existing `rbcResults` and
`bbaResults`; do not add parallel decision state. Transition wait categories
after each accepted event and finish the active category at core decision.

- [ ] **Step 8: Verify ACS GREEN, full suite, and race suite**

Run: `cd sbc/hbbft && go test ./... -count=1`

Run: `cd sbc/hbbft && go test -race ./... -run 'Test(BBA|ACS|RBC|SlotACS)' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit BBA and ACS gate tracing**

```bash
git add sbc/hbbft/trace.go sbc/hbbft/bba.go sbc/hbbft/acs.go sbc/hbbft/bba_test.go sbc/hbbft/acs_test.go
git commit -m "feat(hbbft): trace BBA and ACS completion gates"
```

### Task 5: Separate core decision, slot adapter, and node receipt

**Files:**

- Modify: `sbc/hbbft/bloc_slot.go`
- Modify: `sbc/hbbft/bloc_slot_test.go`
- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/config.go`
- Modify: `bloc-node/internal/app/config_security_test.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/node_slot_test.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`

**Interfaces:**

- Consumes: complete hbbft core trace.
- Produces:
  - `SlotOutput.ACSTrace ACSTrace`
  - `(*SlotACS).MarkNodeOutputReceived()`
  - `DiagnosticsConfig{ACSTrace bool}` in `ConfigFile.Diagnostics`
  - node result field `ACSTrace hbbft.ACSTrace`
  - adapter milestones `CommonSubsetDecoded`, `BlockBodyBuilt`, and
    `NodeOutputReceived`.

- [ ] **Step 1: Write failing SlotACS boundary tests**

Inject a fake builder that advances the clock and assert core decision occurs
before decode completion and block-body completion. Consume `SlotOutput` and
assert its trace is a deep-copy snapshot.

- [ ] **Step 2: Verify SlotACS RED**

Run: `cd sbc/hbbft && go test ./... -run 'TestSlotACSTrace' -count=1`

Expected: compile failure because `SlotOutput.ACSTrace` and adapter points do
not exist.

- [ ] **Step 3: Record adapter milestones and attach the snapshot**

```go
decodedSubset, err := decodeCommonSubset(subset)
if err != nil { return err }
s.trace.recordAdapter(traceCommonSubsetDecoded)
ordered := orderedBatches(decodedSubset)
builder := s.blockBuilder
if builder == nil {
	builder = EncodeSlotBlockBody
}
blockBody, err := builder(s.slot, ordered)
if err != nil { return err }
s.trace.recordAdapter(traceBlockBodyBuilt)
output := &SlotOutput{
	Slot:           s.slot,
	CommonSubset:   decodedSubset,
	OrderedBatches: ordered,
	BlockBody:      blockBody,
	ACSTrace:       s.trace.snapshot(),
}
```

- [ ] **Step 4: Verify SlotACS GREEN and full hbbft suite**

Run: `cd sbc/hbbft && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing strict-config and node-origin tests**

```go
func TestConfigAcceptsOptionalACSTraceDiagnostics(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{"--nodes", "4", "--threshold", "3", "--bmax", "8", "--out", configPath}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil { t.Fatal(err) }
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil { t.Fatal(err) }
	document["diagnostics"] = map[string]any{"acs_trace": true}
	raw, err = json.Marshal(document)
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(configPath, raw, 0o600); err != nil { t.Fatal(err) }
	cfg, err := readConfig(configPath)
	if err != nil { t.Fatal(err) }
	if !cfg.Diagnostics.ACSTrace { t.Fatal("ACS trace diagnostics were not enabled") }
}
```

In `node_slot_test.go`, extend the real slot lifecycle fixture with diagnostics
enabled. Assert `newSlotState` supplies enabled trace options, `startConsensus`
uses the captured `proposalReady` value as the origin immediately before
`InputBatch`, and `handleACSOutput` refreshes the snapshot after recording node
receipt while leaving the legacy `ACSUS` interval unchanged.

- [ ] **Step 6: Verify node/config RED**

Run: `cd bloc-node && go test ./internal/app -run 'TestConfigAcceptsOptionalACSTrace|TestACSTraceLifecycle' -count=1`

Expected: strict JSON rejects `diagnostics`, or fields are missing.

- [ ] **Step 7: Add diagnostics config, begin trace, and snapshot into Result**

```go
type DiagnosticsConfig struct {
	ACSTrace bool `json:"acs_trace,omitempty"`
}
```

Add `Diagnostics DiagnosticsConfig` with JSON tag
`json:"diagnostics,omitempty"` between `ConfigFile.Limits` and
`ConfigFile.CRSBytes`; retain every existing field and tag verbatim.

Pass `TraceOptions{Enabled: n.cfg.Diagnostics.ACSTrace}` when creating every
new slot. Immediately after proposal readiness is captured and before
`InputBatch`, call `n.slot.BeginTrace(proposalReady)`. When output crosses into
the node, call `n.slot.MarkNodeOutputReceived()`, replace `out.ACSTrace` with
`n.slot.Trace()`, and copy that latest snapshot into the eventual `Result`.

- [ ] **Step 8: Verify node/config GREEN and compatibility**

Run: `cd bloc-node && go test ./internal/app -run 'TestConfig|TestStartConsensus|TestRefreshMetrics' -count=1`

Run: `cd bloc-node && go test ./... -count=1`

Expected: PASS and legacy fixtures still load with tracing disabled.

- [ ] **Step 9: Commit adapter/node boundary integration**

```bash
git add sbc/hbbft/bloc_slot.go sbc/hbbft/bloc_slot_test.go bloc-node/internal/app/types.go bloc-node/internal/app/config.go bloc-node/internal/app/config_security_test.go bloc-node/internal/app/node.go bloc-node/internal/app/node_slot_test.go bloc-node/internal/app/eval_suite_test.go
git commit -m "feat(bloc-node): expose ACS boundary trace"
```

### Task 6: Classify ACS wire traffic and summarize local send cost

**Files:**

- Create: `bloc-node/internal/app/acs_trace.go`
- Create: `bloc-node/internal/app/acs_trace_test.go`
- Modify: `bloc-node/internal/app/node.go`
- Modify: `bloc-node/internal/app/types.go`
- Modify: `bloc-node/internal/app/node_slot_test.go`

**Interfaces:**

- Consumes: concrete `hbbft.SlotMessage` payloads and Task 5 node trace.
- Produces:
  - `func classifyACSMessage(*hbbft.SlotMessage) (hbbft.ACSMessageSubtype, error)`
  - `recordACSInbound(subtype, size)` and
    `recordACSOutbound(subtype, size, duration, sendErr)` on the active slot
  - bounded per-subtype inbound/outbound count/bytes and send count/total/max/failure count.

- [ ] **Step 1: Write failing table-driven classifier tests**

Use one real message for each of PROOF, ECHO, READY, BVAL, and AUX. Literal
expected subtype strings must be `proof`, `echo`, `ready`, `bval`, and `aux`.
Nil and unknown nested payloads must return errors.

- [ ] **Step 2: Verify classifier RED**

Run: `cd bloc-node && go test ./internal/app -run TestClassifyACSMessage -count=1`

Expected: compile failure because the classifier does not exist.

- [ ] **Step 3: Implement the minimal exhaustive type switch**

```go
func classifyACSMessage(msg *hbbft.SlotMessage) (hbbft.ACSMessageSubtype, error) {
	if msg == nil || msg.Payload == nil { return "", errInvalidACSSubtype }
	switch payload := msg.Payload.Payload.(type) {
	case *hbbft.BroadcastMessage:
		switch payload.Payload.(type) {
		case *hbbft.ProofRequest:
			return hbbft.ACSMessageProof, nil
		case *hbbft.EchoRequest:
			return hbbft.ACSMessageEcho, nil
		case *hbbft.ReadyRequest:
			return hbbft.ACSMessageReady, nil
		}
	case *hbbft.AgreementMessage:
		switch payload.Message.(type) {
		case *hbbft.BvalRequest:
			return hbbft.ACSMessageBVAL, nil
		case *hbbft.AuxRequest:
			return hbbft.ACSMessageAUX, nil
		}
	}
	return "", errInvalidACSSubtype
}
```

- [ ] **Step 4: Verify classifier GREEN**

Run: `cd bloc-node && go test ./internal/app -run TestClassifyACSMessage -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing concurrent accounting tests**

Use the real blocking transport pattern to prove successful sends count bytes
and duration, failures increment only failure count, inbound counts are recorded
after authenticated decode, and a stale-slot completion cannot mutate the new
slot trace.

- [ ] **Step 6: Verify accounting RED**

Run: `cd bloc-node && go test ./internal/app -run 'TestACSSubtype|TestPrepareSlotWaitsForInflightSend' -count=1`

Expected: subtype summaries remain zero.

- [ ] **Step 7: Integrate accounting under existing lifecycle isolation**

Classify before launching an ACS send goroutine. Capture `sendStarted :=
time.Now()`, and record `time.Since(sendStarted)` only against the same active
slot. Keep existing top-level `recordInbound("acs", size)` and
`recordOutbound("acs", size)` unchanged.

- [ ] **Step 8: Run normal and focused race suites**

Run: `cd bloc-node && go test ./... -count=1`

Run: `cd bloc-node && go test -race ./internal/app -run 'Test(ACSSubtype|PrepareSlotWaitsForInflightSend|StepACS)' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit bounded wire accounting**

```bash
git add bloc-node/internal/app/acs_trace.go bloc-node/internal/app/acs_trace_test.go bloc-node/internal/app/node.go bloc-node/internal/app/types.go bloc-node/internal/app/node_slot_test.go
git commit -m "feat(bloc-node): attribute ACS wire traffic"
```

### Task 7: Persist trace summaries and detailed JSONL artifacts

**Files:**

- Modify: `bloc-node/internal/app/eval_suite.go`
- Modify: `bloc-node/internal/app/eval_suite_test.go`
- Create: `bloc-node/internal/app/acs_trace_artifact.go`
- Create: `bloc-node/internal/app/acs_trace_artifact_test.go`

**Interfaces:**

- Consumes: `Result.ACSTrace` and existing `EvalRun` identity.
- Produces:
  - manifest field `ACSTraceSchema string`;
  - aggregate trace columns in `node_measurements.csv`;
  - `acs_trace.jsonl` records keyed by measurement block, run ID, node ID,
    and slot;
  - `validateACSTraceArtifact(manifest suiteManifest, runs []EvalRun, path string) error`.

- [ ] **Step 1: Write failing writer round-trip tests**

Build one literal `EvalRun` with two node results and hand-authored trace
offsets. Assert exact CSV headers/values and two JSONL keys. Assert legacy runs
with an empty manifest trace schema do not require the JSONL file.

- [ ] **Step 2: Verify writer RED**

Run: `cd bloc-node && go test ./internal/app -run 'TestWriteACSTrace|TestNodeMeasurementsIncludeACS' -count=1`

Expected: writer or columns do not exist.

- [ ] **Step 3: Implement deterministic writer ordering**

Sort records by measurement block, run ID, node ID, proposer ID, then subtype.
Use `json.Encoder` once per bounded record and preserve existing block-scoped
attempt identity.

- [ ] **Step 4: Verify writer GREEN**

Run: `cd bloc-node && go test ./internal/app -run 'TestWriteACSTrace|TestNodeMeasurementsIncludeACS' -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing fail-closed validator tests**

Cover missing node, duplicate key, unknown schema, negative offset, core
decision after node receipt, unknown proposer, missing fixed subtype, and
aggregate/detail count mismatch. Each fixture changes one field and asserts the
specific error category.

- [ ] **Step 6: Verify validator RED**

Run: `cd bloc-node && go test ./internal/app -run TestValidateACSTraceArtifact -count=1`

Expected: compile failure or invalid fixtures are accepted.

- [ ] **Step 7: Implement manifest-gated validation**

```go
if manifest.ACSTraceSchema == "" {
	return nil
}
if manifest.ACSTraceSchema != hbbft.ACSTraceSchemaVersion {
	return fmt.Errorf("unsupported ACS trace schema %q", manifest.ACSTraceSchema)
}
```

Then validate membership, uniqueness, ordering, counts, and trace/legacy ACS
reconciliation. Use a documented tolerance covering adjacent monotonic clock
reads rather than silently rewriting values.

- [ ] **Step 8: Run complete bloc-node tests**

Run: `cd bloc-node && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 9: Commit evaluator artifacts**

```bash
git add bloc-node/internal/app/eval_suite.go bloc-node/internal/app/eval_suite_test.go bloc-node/internal/app/acs_trace_artifact.go bloc-node/internal/app/acs_trace_artifact_test.go
git commit -m "feat(bloc-node): persist validated ACS traces"
```

### Task 8: Add deployment artifact gates for diagnostic mode

**Files:**

- Modify: `scripts/lib/campaign_artifacts.py`
- Modify: `scripts/tests/test_campaign_artifacts.py`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `deploy/ec2/README.md`

**Interfaces:**

- Consumes: Task 7 manifest and artifacts.
- Produces: side-effect-free `--validate-only` acceptance of trace-disabled
  historical campaigns and trace-required diagnostic campaigns.

- [ ] **Step 1: Confirm the shared manifest/finalizer boundary**

Run: `rg -n 'assert-final-phase|artifact.*valid|suite_schema|node_measurements' deploy/ec2 scripts bloc-node/internal/app`

The boundary is `final_run_campaign_lifecycle` in
`scripts/lib/final-campaign-lifecycle.sh`, which invokes the shared
`assert-final-phase` implementation in `scripts/lib/campaign_artifacts.py`.
Record those two files in the issue comment before modifying them; do not
duplicate validation in the same-AZ and three-region adapters.

- [ ] **Step 2: Write failing diagnostic artifact contract tests**

Add fixtures where diagnostics are enabled with a complete trace and with one
missing/duplicate/unknown-schema trace. Run the actual validator entrypoint and
assert exit 0 only for the complete fixture. Retain a trace-disabled historical
fixture that passes.

- [ ] **Step 3: Verify deployment RED**

Run: `python3 -m unittest scripts.tests.test_campaign_artifacts`

Run: `bash scripts/tests/test-final-campaign-lifecycle.sh`

Expected: diagnostic fixtures are not distinguished.

- [ ] **Step 4: Pass diagnostic flags through the shared lifecycle**

Add one explicit diagnostic mode and schema value. Do not add a topology-only
branch. Ensure recovered `acs_trace.jsonl` is mandatory only in that mode.

- [ ] **Step 5: Verify deployment GREEN and portability**

Run:

```bash
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/test-campaign-runners.sh
bash -n scripts/lib/final-campaign-lifecycle.sh deploy/ec2/run-final-campaign.sh
```

Then run both exact same-AZ/three-region `--validate-only` examples added to
`deploy/ec2/README.md`; the lifecycle test call log must remain empty so these
paths prove they do not reach AWS or allocation helpers.

- [ ] **Step 6: Commit deployment gates**

```bash
git add scripts/lib/campaign_artifacts.py scripts/tests/test_campaign_artifacts.py scripts/lib/final-campaign-lifecycle.sh scripts/tests/test-final-campaign-lifecycle.sh deploy/ec2/README.md
git commit -m "feat(deploy): validate ACS diagnostic artifacts"
```

### Task 9: Build overlap-aware ACS attribution analysis

**Files:**

- Create: `latency-charts/src/bloc_latency_charts/acs_attribution.py`
- Create: `latency-charts/tests/test_acs_attribution.py`
- Modify: `latency-charts/pyproject.toml`
- Modify: `latency-charts/README.md`

**Interfaces:**

- Consumes: matched manifest, `run_measurements.csv`,
  `node_measurements.csv`, and `acs_trace.jsonl` from Task 7.
- Produces: `acs-milestone-summary.csv`, `acs-wait-summary.csv`,
  `acs-message-summary.csv`, `acs-critical-node-summary.csv`,
  `acs-hypotheses.csv`, `REPORT.md`, and PNG/SVG figures.

- [ ] **Step 1: Write failing loader and matching tests**

Use temporary same-AZ and three-region roots with literal `n=4`, batch-8 trace
rows. Assert the loader rejects source/image/corpus/config/schema/schedule
mismatches and accepts a fully matched pair.

- [ ] **Step 2: Verify loader RED**

Run: `cd latency-charts && python -m pytest tests/test_acs_attribution.py -q`

Expected: import failure because the module does not exist.

- [ ] **Step 3: Implement strict matched loading**

```python
TRACE_SCHEMA = "bloc-acs-trace/v1"

def load_matched_diagnostics(same_az: Path, three_region: Path) -> MatchedDiagnostics:
    left = load_diagnostic_root(same_az)
    right = load_diagnostic_root(three_region)
    assert_matching_contract(left.manifest, right.manifest)
    return MatchedDiagnostics(left, right)
```

Keep all failed/timed-out attempts in outcome counts while excluding them from
successful latency distributions.

- [ ] **Step 4: Write failing statistic and critical-node tests**

Use hand-sorted values to assert Type-7 p50/p95/max, p99 suppression at 30,
separate slowest-total/slowest-ACS node selection, and no sum of overlapping
per-proposer durations.

- [ ] **Step 5: Verify statistics RED**

Run: `cd latency-charts && python -m pytest tests/test_acs_attribution.py -q`

Expected: missing summary functions or wrong literal values.

- [ ] **Step 6: Implement summaries and fixed hypothesis rules**

Use predeclared thresholds encoded in one documented rule table. Each
hypothesis output is exactly `supported`, `contradicted`, or `unresolved`, with
the metric and comparison that produced the outcome. Manual report prose may
explain a result but cannot override the CSV classification.

- [ ] **Step 7: Write failing output/render tests**

Assert all contracted CSV/report/PNG/SVG files exist, report provenance names
both roots, and a 30-observation fixture contains no p99 value or p99 claim.

- [ ] **Step 8: Implement CLI and rendering, then verify GREEN**

Run: `cd latency-charts && python -m pytest tests/test_acs_attribution.py -q`

Run: `cd latency-charts && python -m pytest -q`

Expected: PASS.

- [ ] **Step 9: Commit attribution analysis**

```bash
git add latency-charts/src/bloc_latency_charts/acs_attribution.py latency-charts/tests/test_acs_attribution.py latency-charts/pyproject.toml latency-charts/README.md
git commit -m "feat(charts): analyze ACS critical-path traces"
```

### Task 10: Measure observer overhead and run the complete local gate

**Files:**

- Create or modify: `sbc/hbbft/trace_benchmark_test.go`
- Modify: `bloc-node/scripts/run-acs-safety-campaign.sh` only if diagnostic
  artifact capture requires an explicit trace flag
- Modify: `docs/VALIDATION.md`
- Modify: `docs/modules/hbbft.md`
- Modify: `docs/modules/bloc-node.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/STATUS.md` only if blocker/evidence/baseline/next-action state changes

**Interfaces:**

- Consumes: Tasks 1-9.
- Produces: trace-off/trace-on benchmark evidence, complete local validation,
  canonical implementation semantics, and a live-campaign authorization gate.

- [ ] **Step 1: Write benchmark cases before optimizing the observer**

Define paired benchmarks for `n=4/7` and literal proposal payload sizes matching
the retained batch-8/32/128 encoded proposal ranges. Use the same delivery
schedule and input bytes for trace-off/on sub-benchmarks and report allocations.

- [ ] **Step 2: Run benchmarks and retain output under ignored results**

Run: `cd sbc/hbbft && go test -run '^$' -bench BenchmarkACSTrace -benchmem -count 10`

Retain raw output below an ignored Issue #23 local evidence directory. Compute
the paired median delta; do not proceed to VM authorization if trace-on exceeds
the approved 2% median local ACS threshold.

- [ ] **Step 3: Run all normal module suites**

Run:

```bash
cd sbc/hbbft && go test ./... -count=1
cd bloc-node && go test ./... -count=1
cd mempool-il && go test ./... -count=1
cd bte/btd-impl-main && go test ./... -count=1
cd latency-charts && python -m pytest -q
```

Expected: all pass.

- [ ] **Step 4: Run race and ACS safety gates**

Run:

```bash
cd sbc/hbbft && go test -race ./... -run 'Test(RBC|ACS|BBA|SlotACS)' -count=1
cd bloc-node && go test -race ./internal/app -run 'Test(ACS|StepACS|PrepareSlot|Eval)' -count=1
bash bloc-node/scripts/run-acs-safety-campaign.sh
```

Expected: all pass with no changed agreed subset, message-sequence, or
consistency result.

- [ ] **Step 5: Run runner/artifact and side-effect-free topology gates**

Run the complete campaign-runner suite, ACS trace artifact validator fixtures,
and exact same-AZ/three-region diagnostic `--validate-only` commands documented
in `deploy/ec2/README.md`. Confirm no AWS API or allocation occurs.

- [ ] **Step 6: Update canonical documentation**

Document trace semantics, disabled-path compatibility, bounded cardinality,
artifact schema, overlap-aware interpretation, observer overhead, validation
commands, and the explicit requirement for separate live authorization. Do not
record a dominant stage before accepted diagnostic evidence exists.

- [ ] **Step 7: Verify docs and complete diff hygiene**

Run: `git diff --check`

Perform the `docs/VALIDATION.md` documentation-only link, ownership, command,
and coherence review for all changed Markdown.

- [ ] **Step 8: Commit the locally validated implementation**

```bash
git add sbc/hbbft bloc-node latency-charts deploy/ec2 scripts docs
git commit -m "docs: validate ACS latency instrumentation"
```

- [ ] **Step 9: Post the local checkpoint to Issue #23**

Post the commits, exact validation outcomes, trace overhead, remaining
limitations, and the statement that no AWS work occurred. Leave the project
item `In progress` until diagnostic evidence and conclusions satisfy the issue.

### Task 11: Execute matched diagnostic campaigns after explicit authorization

**Files:**

- Generated output only under ignored `results/` directories until evidence is
  accepted.
- Canonical documents named in Issue #23 only after acceptance/rejection.

**Interfaces:**

- Consumes: the immutable diagnostic source/image/corpus/config/trace schema
  validated by Task 10 plus separate user authorization.
- Produces: matched same-AZ/three-region diagnostic artifacts and an accepted
  or rejected hypothesis report.

- [ ] **Step 1: Stop and request live authorization**

Provide exact instance counts, regions, expected duration/cost, experiment IDs,
source SHA, image digests, corpus IDs, seed, and cleanup contract. Do not run
Terraform plan/apply or AWS allocation before approval.

- [ ] **Step 2: Run the approved 5-warm-up/30-measurement matched matrix**

Use `n={4,7}`, `t={3,5}`, batches `{8,32,128}`, resource sampler off, balanced
blocks, and mandatory network/trace/recovery/cleanup validation.

- [ ] **Step 3: Analyze without mixing accepted Issue #15/#16 distributions**

Run `bloc_latency_charts.acs_attribution` only on the matched diagnostic roots.
Retain failures and timeouts outside successful quantiles and withhold p99.

- [ ] **Step 4: Apply the escalation rule**

If a predeclared hypothesis remains unresolved, request authorization for only
the minimum unresolved cells at 100 observations. Do not expand the whole
matrix or collect 1,000 observations under this issue without a new decision.

- [ ] **Step 5: Record conclusions and open the separate optimization issue**

Update the issue and canonical evidence documents with supported,
contradicted, and unresolved hypotheses. Open an optimization issue only for
the measured dominant mechanism; do not change ACS semantics on this branch.
