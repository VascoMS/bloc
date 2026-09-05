package hbbft

import (
	"sync"
	"time"
)

const (
	ACSTraceSchemaV1      = "bloc-acs-trace/v1"
	ACSTraceSchemaV2      = "bloc-acs-trace/v2"
	ACSTraceSchemaV3      = "bloc-acs-trace/v3"
	ACSTraceSchemaVersion = ACSTraceSchemaV3
)

// TracePoint is a process-local monotonic offset from proposal readiness.
// Recorded distinguishes an event at offset zero from an event that did not
// occur.
type TracePoint struct {
	Recorded bool  `json:"recorded"`
	OffsetUS int64 `json:"offset_us"`
}

// ACSMessageSubtype is one of the fixed ACS wire-message categories.
type ACSMessageSubtype string

const (
	ACSMessageProof ACSMessageSubtype = "proof"
	ACSMessageEcho  ACSMessageSubtype = "echo"
	ACSMessageReady ACSMessageSubtype = "ready"
	ACSMessageBVAL  ACSMessageSubtype = "bval"
	ACSMessageAUX   ACSMessageSubtype = "aux"
)

var acsMessageSubtypes = [...]ACSMessageSubtype{
	ACSMessageProof,
	ACSMessageEcho,
	ACSMessageReady,
	ACSMessageBVAL,
	ACSMessageAUX,
}

type RBCReadyTrigger string

const (
	RBCReadyTriggerEchoQuorum RBCReadyTrigger = "echo_quorum"
	RBCReadyTriggerRelay      RBCReadyTrigger = "ready_relay"
)

// ACSTrace contains one bounded diagnostic record for an ACS slot.
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

// ACSAggregateTrace captures milestones shared across proposer instances.
type ACSAggregateTrace struct {
	InputStarted       TracePoint `json:"input_started"`
	FirstRBCOutput     TracePoint `json:"first_rbc_output"`
	RBCOutputQuorum    TracePoint `json:"rbc_output_quorum"`
	FirstTrueBBA       TracePoint `json:"first_true_bba"`
	TrueBBAQuorum      TracePoint `json:"true_bba_quorum"`
	FalseInputInjected TracePoint `json:"false_input_injected"`
	AllBBADecided      TracePoint `json:"all_bba_decided"`
	TruthyRBCReady     TracePoint `json:"truthy_rbc_ready"`
	CoreDecision       TracePoint `json:"core_decision"`
}

// RBCTrace captures bounded reliable-broadcast milestones for one proposer.
type RBCTrace struct {
	ProofAccepted          TracePoint      `json:"proof_accepted"`
	EchoSent               TracePoint      `json:"echo_sent"`
	ReadySent              TracePoint      `json:"ready_sent"`
	ReadyTrigger           RBCReadyTrigger `json:"ready_trigger,omitempty"`
	ReadyTriggerEchoCount  int             `json:"ready_trigger_echo_count,omitempty"`
	ReadyTriggerReadyCount int             `json:"ready_trigger_ready_count,omitempty"`
	DecodeEligible         TracePoint      `json:"decode_eligible"`
	ReconstructionStarted  TracePoint      `json:"reconstruction_started"`
	ReconstructionFinished TracePoint      `json:"reconstruction_finished"`
	OutputStored           TracePoint      `json:"output_stored"`
}

// BBATrace captures bounded binary-agreement milestones for one proposer.
type BBATrace struct {
	Input          TracePoint `json:"input"`
	InputValue     bool       `json:"input_value"`
	FirstBinValue  TracePoint `json:"first_bin_value"`
	FirstBin       bool       `json:"first_bin"`
	FirstAux       TracePoint `json:"first_aux"`
	FirstAuxValue  bool       `json:"first_aux_value"`
	ValidAuxQuorum TracePoint `json:"valid_aux_quorum"`
	Decision       TracePoint `json:"decision"`
	DecisionValue  bool       `json:"decision_value"`
	Done           TracePoint `json:"done"`
	MaxEpoch       uint32     `json:"max_epoch"`
}

// ACSWaitTrace attributes mutually exclusive ACS completion wait states.
type ACSWaitTrace struct {
	TrueBBAQuorumUS int64 `json:"true_bba_quorum_us"`
	AllBBAUS        int64 `json:"all_bba_us"`
	TruthyRBCUS     int64 `json:"truthy_rbc_us"`
}

// ACSAdapterTrace separates the hbbft decision from local slot/node work.
type ACSAdapterTrace struct {
	CommonSubsetDecoded TracePoint `json:"common_subset_decoded"`
	BlockBodyBuilt      TracePoint `json:"block_body_built"`
	NodeOutputReceived  TracePoint `json:"node_output_received"`
}

// ACSSendPhaseTrace contains bounded timing for one fixed transport phase.
type ACSSendPhaseTrace struct {
	Count   uint64 `json:"count"`
	TotalUS int64  `json:"total_us"`
	MaxUS   int64  `json:"max_us"`
}

// ACSSendObservation contains one completed transport attempt. A successful
// observation represents a complete transport write, not remote receipt.
type ACSSendObservation struct {
	Size       int
	Total      time.Duration
	Encode     time.Duration
	QueueWait  time.Duration
	StreamOpen time.Duration
	Write      time.Duration
	Finalize   time.Duration
	Reused     bool
	Err        error
}

// ACSMessageTrace contains bounded per-subtype transport accounting.
type ACSMessageTrace struct {
	ScheduledCount    uint64            `json:"scheduled_count"`
	TerminalCount     uint64            `json:"terminal_count"`
	PendingAtDecision uint64            `json:"pending_at_decision"`
	InboundCount      uint64            `json:"inbound_count"`
	InboundBytes      uint64            `json:"inbound_bytes"`
	OutboundCount     uint64            `json:"outbound_count"`
	OutboundBytes     uint64            `json:"outbound_bytes"`
	SendCount         uint64            `json:"send_count"`
	SendTotalUS       int64             `json:"send_total_us"`
	SendMaxUS         int64             `json:"send_max_us"`
	SendFailureCount  uint64            `json:"send_failure_count"`
	Encode            ACSSendPhaseTrace `json:"encode"`
	QueueWait         ACSSendPhaseTrace `json:"queue_wait"`
	StreamOpen        ACSSendPhaseTrace `json:"stream_open"`
	Write             ACSSendPhaseTrace `json:"write"`
	Finalize          ACSSendPhaseTrace `json:"finalize"`
	StreamOpenCount   uint64            `json:"stream_open_count"`
	StreamReuseCount  uint64            `json:"stream_reuse_count"`
}

type aggregateTraceEvent uint8

const (
	traceACSInputStarted aggregateTraceEvent = iota
	traceACSFirstRBCOutput
	traceACSRBCOutputQuorum
	traceACSFirstTrueBBA
	traceACSTrueBBAQuorum
	traceACSFalseInputInjected
	traceACSAllBBADecided
	traceACSTruthyRBCReady
	traceACSCoreDecision
)

type rbcTraceEvent uint8

const (
	traceRBCProofAccepted rbcTraceEvent = iota
	traceRBCEchoSent
	traceRBCReadySent
	traceRBCDecodeEligible
	traceRBCReconstructionStarted
	traceRBCReconstructionFinished
	traceRBCOutputStored
)

type bbaTraceEvent uint8

const (
	traceBBAInput bbaTraceEvent = iota
	traceBBAFirstBinValue
	traceBBAFirstAux
	traceBBAValidAuxQuorum
	traceBBADecision
	traceBBADone
	traceBBAEpoch
)

type adapterTraceEvent uint8

const (
	traceCommonSubsetDecoded adapterTraceEvent = iota
	traceBlockBodyBuilt
	traceNodeOutputReceived
)

type traceRecorder struct {
	mu          sync.Mutex
	enabled     bool
	now         func() time.Time
	base        time.Time
	started     bool
	waitReason  string
	waitStarted time.Time
	trace       ACSTrace
}

func newTraceRecorder(nodes []uint64, enabled bool, now func() time.Time) *traceRecorder {
	if now == nil {
		now = time.Now
	}
	recorder := &traceRecorder{enabled: enabled, now: now}
	if !enabled {
		return recorder
	}
	recorder.trace = ACSTrace{
		SchemaVersion: ACSTraceSchemaVersion,
		Enabled:       true,
		RBC:           make(map[uint64]RBCTrace, len(nodes)),
		BBA:           make(map[uint64]BBATrace, len(nodes)),
		Messages:      make(map[ACSMessageSubtype]ACSMessageTrace, len(acsMessageSubtypes)),
	}
	for _, id := range nodes {
		recorder.trace.RBC[id] = RBCTrace{}
		recorder.trace.BBA[id] = BBATrace{}
	}
	for _, subtype := range acsMessageSubtypes {
		recorder.trace.Messages[subtype] = ACSMessageTrace{}
	}
	return recorder
}

func (r *traceRecorder) begin(base time.Time) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.base = base
	r.started = true
}

func (r *traceRecorder) pointLocked(point *TracePoint) bool {
	if point.Recorded || !r.started {
		return false
	}
	offset := r.now().Sub(r.base).Microseconds()
	if offset < 0 {
		return false
	}
	point.Recorded = true
	point.OffsetUS = offset
	return true
}

func (r *traceRecorder) recordAggregate(event aggregateTraceEvent) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var point *TracePoint
	switch event {
	case traceACSInputStarted:
		point = &r.trace.Aggregate.InputStarted
	case traceACSFirstRBCOutput:
		point = &r.trace.Aggregate.FirstRBCOutput
	case traceACSRBCOutputQuorum:
		point = &r.trace.Aggregate.RBCOutputQuorum
	case traceACSFirstTrueBBA:
		point = &r.trace.Aggregate.FirstTrueBBA
	case traceACSTrueBBAQuorum:
		point = &r.trace.Aggregate.TrueBBAQuorum
	case traceACSFalseInputInjected:
		point = &r.trace.Aggregate.FalseInputInjected
	case traceACSAllBBADecided:
		point = &r.trace.Aggregate.AllBBADecided
	case traceACSTruthyRBCReady:
		point = &r.trace.Aggregate.TruthyRBCReady
	case traceACSCoreDecision:
		point = &r.trace.Aggregate.CoreDecision
	default:
		return
	}
	r.pointLocked(point)
}

func (r *traceRecorder) recordRBC(proposerID uint64, event rbcTraceEvent) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.trace.RBC[proposerID]
	if !ok {
		return
	}
	var point *TracePoint
	switch event {
	case traceRBCProofAccepted:
		point = &entry.ProofAccepted
	case traceRBCEchoSent:
		point = &entry.EchoSent
	case traceRBCReadySent:
		point = &entry.ReadySent
	case traceRBCDecodeEligible:
		point = &entry.DecodeEligible
	case traceRBCReconstructionStarted:
		point = &entry.ReconstructionStarted
	case traceRBCReconstructionFinished:
		point = &entry.ReconstructionFinished
	case traceRBCOutputStored:
		point = &entry.OutputStored
	default:
		return
	}
	r.pointLocked(point)
	r.trace.RBC[proposerID] = entry
}

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

func (r *traceRecorder) recordBBA(proposerID uint64, event bbaTraceEvent, value bool, epoch uint32) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return
	}
	entry, ok := r.trace.BBA[proposerID]
	if !ok {
		return
	}
	if epoch > entry.MaxEpoch {
		entry.MaxEpoch = epoch
	}
	switch event {
	case traceBBAInput:
		if r.pointLocked(&entry.Input) {
			entry.InputValue = value
		}
	case traceBBAFirstBinValue:
		if r.pointLocked(&entry.FirstBinValue) {
			entry.FirstBin = value
		}
	case traceBBAFirstAux:
		if r.pointLocked(&entry.FirstAux) {
			entry.FirstAuxValue = value
		}
	case traceBBAValidAuxQuorum:
		r.pointLocked(&entry.ValidAuxQuorum)
	case traceBBADecision:
		if r.pointLocked(&entry.Decision) {
			entry.DecisionValue = value
		}
	case traceBBADone:
		r.pointLocked(&entry.Done)
	case traceBBAEpoch:
	default:
		return
	}
	r.trace.BBA[proposerID] = entry
}

func (r *traceRecorder) recordAdapter(event adapterTraceEvent) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var point *TracePoint
	switch event {
	case traceCommonSubsetDecoded:
		point = &r.trace.Adapter.CommonSubsetDecoded
	case traceBlockBodyBuilt:
		point = &r.trace.Adapter.BlockBodyBuilt
	case traceNodeOutputReceived:
		point = &r.trace.Adapter.NodeOutputReceived
	default:
		return
	}
	r.pointLocked(point)
}

func (r *traceRecorder) recordMessageInbound(subtype ACSMessageSubtype, size int) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return
	}
	entry, ok := r.trace.Messages[subtype]
	if !ok {
		return
	}
	entry.InboundCount++
	if size > 0 {
		entry.InboundBytes += uint64(size)
	}
	r.trace.Messages[subtype] = entry
}

func (r *traceRecorder) recordMessageOutbound(subtype ACSMessageSubtype, observation ACSSendObservation) {
	token := r.beginMessageOutbound(subtype)
	token.Complete(observation)
}

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

func recordSendPhase(phase *ACSSendPhaseTrace, duration time.Duration) {
	durationUS := duration.Microseconds()
	if durationUS < 0 {
		durationUS = 0
	}
	phase.Count++
	phase.TotalUS += durationUS
	if durationUS > phase.MaxUS {
		phase.MaxUS = durationUS
	}
}

func (r *traceRecorder) transitionWait(reason string) {
	if r == nil || !r.enabled || !validTraceWaitReason(reason) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.waitReason == reason {
		return
	}
	now := r.now()
	if now.Before(r.base) || (!r.waitStarted.IsZero() && now.Before(r.waitStarted)) {
		return
	}
	r.finishWaitLocked(now)
	r.waitReason = reason
	r.waitStarted = now
}

func (r *traceRecorder) finishWait() {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.waitReason == "" {
		return
	}
	now := r.now()
	if now.Before(r.waitStarted) {
		return
	}
	r.finishWaitLocked(now)
}

func (r *traceRecorder) finishWaitLocked(now time.Time) {
	if r.waitReason == "" {
		return
	}
	durationUS := now.Sub(r.waitStarted).Microseconds()
	switch r.waitReason {
	case waitForTrueBBAResults:
		r.trace.Wait.TrueBBAQuorumUS += durationUS
	case waitForAllBBAResults:
		r.trace.Wait.AllBBAUS += durationUS
	case waitForTruthyRBCOutputs:
		r.trace.Wait.TruthyRBCUS += durationUS
	}
	r.waitReason = ""
	r.waitStarted = time.Time{}
}

func validTraceWaitReason(reason string) bool {
	switch reason {
	case waitForTrueBBAResults, waitForAllBBAResults, waitForTruthyRBCOutputs:
		return true
	default:
		return false
	}
}

func (r *traceRecorder) snapshot() ACSTrace {
	if r == nil || !r.enabled {
		return ACSTrace{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.trace
	result.RBC = make(map[uint64]RBCTrace, len(r.trace.RBC))
	for id, trace := range r.trace.RBC {
		result.RBC[id] = trace
	}
	result.BBA = make(map[uint64]BBATrace, len(r.trace.BBA))
	for id, trace := range r.trace.BBA {
		result.BBA[id] = trace
	}
	result.Messages = make(map[ACSMessageSubtype]ACSMessageTrace, len(r.trace.Messages))
	for subtype, trace := range r.trace.Messages {
		result.Messages[subtype] = trace
	}
	return result
}
