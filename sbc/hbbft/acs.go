package hbbft

import (
	"fmt"
	"sort"
	"sync"
)

const (
	waitForTrueBBAResults   = "waiting_for_n_minus_f_true_bba_results"
	waitForAllBBAResults    = "waiting_for_all_bba_results"
	waitForTruthyRBCOutputs = "waiting_for_truthy_rbc_outputs"
)

// ACSMessage represents a message sent between nodes in the ACS protocol.
type ACSMessage struct {
	// Unique identifier of the "proposing" node.
	ProposerID uint64
	// Actual payload beeing sent.
	Payload interface{}
}

// ACS implements the Asynchronous Common Subset protocol.
// ACS assumes a network of N nodes that send signed messages to each other.
// There can be f faulty nodes where (3 * f < N).
// Each participating node proposes an element for inlcusion. The protocol
// guarantees that all of the good nodes output the same set, consisting of
// at least (N -f) of the proposed values.
//
// Algorithm:
// ACS creates a Broadcast algorithm for each of the participating nodes.
// At least (N -f) of these will eventually output the element proposed by that
// node. ACS will also create and BBA instance for each participating node, to
// decide whether that node's proposed element should be inlcuded in common set.
// Whenever an element is received via broadcast, we imput "true" into the
// corresponding BBA instance. When (N-f) BBA instances have decided true we
// input false into the remaining ones, where we haven't provided input yet.
// Once all BBA instances have decided, ACS returns the set of all proposed
// values for which the decision was truthy.
type ACS struct {
	// Config holds the ACS configuration.
	Config
	// Mapping of node ids and their rbc instance.
	rbcInstances map[uint64]*RBC
	// Mapping of node ids and their bba instance.
	bbaInstances map[uint64]*BBA
	// Results of the Reliable Broadcast.
	rbcResults map[uint64][]byte
	// Results of the Binary Byzantine Agreement.
	bbaResults map[uint64]bool
	// Final output of the ACS.
	output map[uint64][]byte
	// Que of ACSMessages that need to be broadcasted after each received
	// and processed a message.
	messageQue *messageQue
	// Whether this ACS instance has already has decided output or not.
	decided bool

	// control flow tuples for internal channel communication.
	closeCh    chan struct{}
	inputCh    chan acsInputTuple
	messageCh  chan acsMessageTuple
	progressCh chan acsProgressTuple
	closeOnce  sync.Once
	trace      *traceRecorder
}

// Control flow structure for internal channel communication. Allowing us to
// avoid the use of mutexes and eliminates race conditions.
type (
	acsMessageTuple struct {
		senderID uint64
		msg      *ACSMessage
		err      chan error
	}

	acsInputResponse struct {
		rbcMessages []*BroadcastMessage
		acsMessages []*ACSMessage
		err         error
	}

	acsInputTuple struct {
		value    []byte
		response chan acsInputResponse
	}

	acsProgressTuple struct {
		response chan ACSProgress
	}
)

// ACSProgress is a read-only diagnostic snapshot of ACS/RBC/BBA state.
type ACSProgress struct {
	Decided              bool                   `json:"decided"`
	QueuedMessages       int                    `json:"queued_messages"`
	RBCOutputs           int                    `json:"rbc_outputs"`
	RBCOutputIDs         []uint64               `json:"rbc_output_ids,omitempty"`
	BBAResultCount       int                    `json:"bba_results"`
	BBAResults           map[uint64]bool        `json:"bba_result_map,omitempty"`
	TruthyBBAResults     int                    `json:"truthy_bba_results"`
	TruthyBBAProposerIDs []uint64               `json:"truthy_bba_proposer_ids,omitempty"`
	WaitingReason        string                 `json:"waiting_reason,omitempty"`
	RBC                  map[uint64]RBCProgress `json:"rbc,omitempty"`
	BBA                  map[uint64]BBAProgress `json:"bba,omitempty"`
}

// RBCProgress is a compact diagnostic snapshot for one reliable-broadcast
// instance inside ACS.
type RBCProgress struct {
	Echos         int  `json:"echos"`
	Readys        int  `json:"readys"`
	EchoSent      bool `json:"echo_sent"`
	ReadySent     bool `json:"ready_sent"`
	OutputDecoded bool `json:"output_decoded"`
	OutputStored  bool `json:"output_stored"`
}

// BBAProgress is a compact diagnostic snapshot for one binary-agreement
// instance inside ACS.
type BBAProgress struct {
	Epoch           uint32 `json:"epoch"`
	BinValues       int    `json:"bin_values"`
	SentBvals       int    `json:"sent_bvals"`
	ReceivedBvals   int    `json:"received_bvals"`
	ReceivedAux     int    `json:"received_aux"`
	DelayedMessages int    `json:"delayed_messages"`
	Done            bool   `json:"done"`
	HasOutput       bool   `json:"has_output"`
	HasDecision     bool   `json:"has_decision"`
}

// NewACS returns a new ACS instance configured with the given Config and node
// ids.
func NewACS(cfg Config) *ACS {
	return newACS(cfg, newTraceRecorder(cfg.Nodes, false, nil))
}

func newACS(cfg Config, trace *traceRecorder) *ACS {
	if cfg.F == 0 {
		cfg.F = (cfg.N - 1) / 3
	}
	if trace == nil {
		trace = newTraceRecorder(cfg.Nodes, false, nil)
	}
	acs := &ACS{
		Config:       cfg,
		rbcInstances: make(map[uint64]*RBC),
		bbaInstances: make(map[uint64]*BBA),
		rbcResults:   make(map[uint64][]byte),
		bbaResults:   make(map[uint64]bool),
		messageQue:   newMessageQue(),
		closeCh:      make(chan struct{}),
		inputCh:      make(chan acsInputTuple),
		messageCh:    make(chan acsMessageTuple),
		progressCh:   make(chan acsProgressTuple),
		trace:        trace,
	}
	// Create all the instances for the participating nodes
	for _, id := range cfg.Nodes {
		acs.rbcInstances[id] = newRBC(cfg, id, trace)
		acs.bbaInstances[id] = newBBA(cfg, id, trace)
	}
	go acs.run()
	return acs
}

// InputValue sets the input value for broadcast and returns an initial set of
// Broadcast and ACS Messages to be broadcasted in the network.
func (a *ACS) InputValue(val []byte) error {
	t := acsInputTuple{
		value:    val,
		response: make(chan acsInputResponse),
	}
	a.inputCh <- t
	resp := <-t.response
	return resp.err
}

// HandleMessage handles incoming messages to ACS and redirects them to the
// appropriate sub(protocol) instance.
func (a *ACS) HandleMessage(senderID uint64, msg *ACSMessage) error {
	t := acsMessageTuple{
		senderID: senderID,
		msg:      msg,
		err:      make(chan error),
	}
	a.messageCh <- t
	return <-t.err
}

// handleMessage handles incoming messages to ACS and redirects them to the
// appropriate sub(protocol) instance.
func (a *ACS) handleMessage(senderID uint64, msg *ACSMessage) error {
	switch t := msg.Payload.(type) {
	case *AgreementMessage:
		return a.handleAgreement(senderID, msg.ProposerID, t)
	case *BroadcastMessage:
		return a.handleBroadcast(senderID, msg.ProposerID, t)
	default:
		return fmt.Errorf("received unknown message (%v)", t)
	}
}

// Output will return the output of the ACS instance. If the output was not nil
// then it will return the output else nil. Note that after consuming the output
// its will be set to nil forever.
func (a *ACS) Output() map[uint64][]byte {
	if a.output != nil {
		out := a.output
		a.output = nil
		return out
	}
	return nil
}

// Progress returns a read-only snapshot from the ACS event loop.
func (a *ACS) Progress() ACSProgress {
	if a == nil {
		return ACSProgress{}
	}
	t := acsProgressTuple{response: make(chan ACSProgress, 1)}
	select {
	case a.progressCh <- t:
		return <-t.response
	case <-a.closeCh:
		return ACSProgress{}
	}
}

// Done returns true whether ACS has completed its agreements and cleared its
// messageQue.
func (a *ACS) Done() bool {
	agreementsDone := true
	for _, bba := range a.bbaInstances {
		if !bba.done {
			agreementsDone = false
		}
	}
	return agreementsDone && a.messageQue.len() == 0
}

// inputValue sets the input value for broadcast and returns an initial set of
// Broadcast and ACS Messages to be broadcasted in the network.
func (a *ACS) inputValue(data []byte) error {
	a.trace.recordAggregate(traceACSInputStarted)
	a.trace.transitionWait(waitForTrueBBAResults)
	rbc, ok := a.rbcInstances[a.ID]
	if !ok {
		return fmt.Errorf("could not find rbc instance (%d)", a.ID)
	}
	reqs, err := rbc.InputValue(data)
	if err != nil {
		return err
	}
	if len(reqs) != a.N-1 {
		return fmt.Errorf("expecting (%d) proof messages got (%d)", a.N, len(reqs))
	}
	for i, id := range uint64sWithout(a.Nodes, a.ID) {
		a.messageQue.addMessage(&ACSMessage{a.ID, reqs[i]}, id)
	}
	for _, msg := range rbc.Messages() {
		a.addMessage(a.ID, msg)
	}
	if output := rbc.Output(); output != nil {
		a.rbcResults[a.ID] = output
		a.recordTraceState()
		a.processAgreement(a.ID, func(bba *BBA) error {
			if bba.AcceptInput() {
				return bba.InputValue(true)
			}
			return nil
		})
	}
	return nil
}

func (a *ACS) stop() {
	a.closeOnce.Do(func() {
		close(a.closeCh)
		for _, rbc := range a.rbcInstances {
			rbc.stop()
		}
		for _, bba := range a.bbaInstances {
			bba.stop()
		}
	})
}

func (a *ACS) run() {
	for {
		select {
		case <-a.closeCh:
			return
		case t := <-a.inputCh:
			err := a.inputValue(t.value)
			t.response <- acsInputResponse{err: err}
		case t := <-a.messageCh:
			t.err <- a.handleMessage(t.senderID, t.msg)
		case t := <-a.progressCh:
			t.response <- a.progress()
		}
	}
}

func (a *ACS) progress() ACSProgress {
	p := ACSProgress{
		Decided:          a.decided,
		QueuedMessages:   a.messageQue.len(),
		RBCOutputs:       len(a.rbcResults),
		BBAResultCount:   len(a.bbaResults),
		BBAResults:       make(map[uint64]bool, len(a.bbaResults)),
		TruthyBBAResults: a.countTruthyAgreements(),
		WaitingReason:    a.waitingReason(),
		RBC:              make(map[uint64]RBCProgress, len(a.rbcInstances)),
		BBA:              make(map[uint64]BBAProgress, len(a.bbaInstances)),
	}
	for id := range a.rbcResults {
		p.RBCOutputIDs = append(p.RBCOutputIDs, id)
	}
	sort.Slice(p.RBCOutputIDs, func(i, j int) bool { return p.RBCOutputIDs[i] < p.RBCOutputIDs[j] })
	for id, result := range a.bbaResults {
		p.BBAResults[id] = result
		if result {
			p.TruthyBBAProposerIDs = append(p.TruthyBBAProposerIDs, id)
		}
	}
	sort.Slice(p.TruthyBBAProposerIDs, func(i, j int) bool { return p.TruthyBBAProposerIDs[i] < p.TruthyBBAProposerIDs[j] })
	for id, rbc := range a.rbcInstances {
		p.RBC[id] = rbc.progress()
		if _, ok := a.rbcResults[id]; ok {
			entry := p.RBC[id]
			entry.OutputStored = true
			p.RBC[id] = entry
		}
	}
	for id, bba := range a.bbaInstances {
		p.BBA[id] = bba.progress()
	}
	return p
}

// handleAgreement processes the received AgreementMessage from sender (sid)
// for a value proposed by the proposing node (pid).
func (a *ACS) handleAgreement(sid, pid uint64, msg *AgreementMessage) error {
	return a.processAgreement(pid, func(bba *BBA) error {
		return bba.HandleMessage(sid, msg)
	})
}

// handleBroadcast processes the received BroadcastMessage.
func (a *ACS) handleBroadcast(sid, pid uint64, msg *BroadcastMessage) error {
	return a.processBroadcast(pid, func(rbc *RBC) error {
		return rbc.HandleMessage(sid, msg)
	})
}

func (a *ACS) processBroadcast(pid uint64, fun func(rbc *RBC) error) error {
	rbc, ok := a.rbcInstances[pid]
	if !ok {
		return fmt.Errorf("could not find rbc instance for (%d)", pid)
	}
	if err := fun(rbc); err != nil {
		return err
	}
	for _, msg := range rbc.Messages() {
		a.addMessage(pid, msg)
	}
	if output := rbc.Output(); output != nil {
		a.rbcResults[pid] = output
		a.recordTraceState()
		err := a.processAgreement(pid, func(bba *BBA) error {
			if bba.AcceptInput() {
				return bba.InputValue(true)
			}
			return nil
		})
		if err != nil {
			return err
		}
		a.tryCompleteAgreement()
	}
	return nil
}

func (a *ACS) processAgreement(pid uint64, fun func(bba *BBA) error) error {
	bba, ok := a.bbaInstances[pid]
	if !ok {
		return fmt.Errorf("could not find bba instance for (%d)", pid)
	}
	if bba.done {
		return nil
	}
	if err := fun(bba); err != nil {
		return err
	}
	for _, msg := range bba.Messages() {
		a.addMessage(pid, msg)
	}
	// Check if we got an output.
	if output := bba.Output(); output != nil {
		if _, ok := a.bbaResults[pid]; ok {
			return fmt.Errorf("multiple bba results for (%d)", pid)
		}
		a.bbaResults[pid] = output.(bool)
		a.recordTraceState()
		// When received 1 from at least (N - f) instances of BA, provide input 0.
		// to each other instance of BBA that has not provided his input yet.
		if output.(bool) && a.countTruthyAgreements() == a.N-a.F {
			falseInputRecorded := false
			for id, bba := range a.bbaInstances {
				if bba.AcceptInput() {
					if !falseInputRecorded {
						a.trace.recordAggregate(traceACSFalseInputInjected)
						falseInputRecorded = true
					}
					if err := bba.InputValue(false); err != nil {
						return err
					}
					for _, msg := range bba.Messages() {
						a.addMessage(id, msg)
					}
					if output := bba.Output(); output != nil {
						a.bbaResults[id] = output.(bool)
						a.recordTraceState()
					}
				}
			}
		}
		a.tryCompleteAgreement()
	}
	return nil
}

func (a *ACS) tryCompleteAgreement() {
	a.recordTraceState()
	a.trace.transitionWait(a.waitingReason())
	if a.decided || a.countTruthyAgreements() < a.N-a.F {
		return
	}
	if len(a.bbaResults) < a.N {
		return
	}
	// At this point all bba instances have provided their output.
	nodesThatProvidedTrue := []uint64{}
	for id, ok := range a.bbaResults {
		if ok {
			nodesThatProvidedTrue = append(nodesThatProvidedTrue, id)
		}
	}
	sort.Slice(nodesThatProvidedTrue, func(i, j int) bool { return nodesThatProvidedTrue[i] < nodesThatProvidedTrue[j] })
	bcResults := make(map[uint64][]byte)
	for _, id := range nodesThatProvidedTrue {
		val, ok := a.rbcResults[id]
		if !ok {
			return
		}
		bcResults[id] = val
	}
	a.output = bcResults
	a.decided = true
	a.trace.recordAggregate(traceACSCoreDecision)
	a.trace.finishWait()
}

func (a *ACS) recordTraceState() {
	if len(a.rbcResults) > 0 {
		a.trace.recordAggregate(traceACSFirstRBCOutput)
	}
	if len(a.rbcResults) >= a.N-a.F {
		a.trace.recordAggregate(traceACSRBCOutputQuorum)
	}
	truthy := a.countTruthyAgreements()
	if truthy > 0 {
		a.trace.recordAggregate(traceACSFirstTrueBBA)
	}
	if truthy >= a.N-a.F {
		a.trace.recordAggregate(traceACSTrueBBAQuorum)
	}
	if len(a.bbaResults) == a.N {
		a.trace.recordAggregate(traceACSAllBBADecided)
	}
	if truthy < a.N-a.F || len(a.bbaResults) < a.N {
		return
	}
	for id, result := range a.bbaResults {
		if result {
			if _, ok := a.rbcResults[id]; !ok {
				return
			}
		}
	}
	a.trace.recordAggregate(traceACSTruthyRBCReady)
}

func (a *ACS) waitingReason() string {
	if a.decided {
		return ""
	}
	if a.countTruthyAgreements() < a.N-a.F {
		return waitForTrueBBAResults
	}
	if len(a.bbaResults) < a.N {
		return waitForAllBBAResults
	}
	for id, result := range a.bbaResults {
		if result {
			if _, ok := a.rbcResults[id]; !ok {
				return waitForTruthyRBCOutputs
			}
		}
	}
	return "ready_to_complete"
}

func (a *ACS) addMessage(from uint64, msg interface{}) {
	for _, id := range uint64sWithout(a.Nodes, a.ID) {
		a.messageQue.addMessage(&ACSMessage{from, msg}, id)
	}
}

// countTruthyAgreements returns the number of truthy received agreement messages.
func (a *ACS) countTruthyAgreements() int {
	n := 0
	for _, ok := range a.bbaResults {
		if ok {
			n++
		}
	}
	return n
}

func uint64sWithout(s []uint64, val uint64) []uint64 {
	dest := []uint64{}
	for i := 0; i < len(s); i++ {
		if s[i] != val {
			dest = append(dest, s[i])
		}
	}
	return dest
}
