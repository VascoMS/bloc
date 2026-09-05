package hbbft

import (
	"log"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/reedsolomon"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRBCTraceRecordsThresholdAndReconstructionMilestones(t *testing.T) {
	base := time.Unix(500, 0)
	now := base
	recorder := newTraceRecorder(makeids(4), true, func() time.Time { return now })
	recorder.begin(base)
	rbc := newRBC(Config{ID: 3, N: 4, F: 1, Nodes: makeids(4)}, 0, recorder)
	t.Cleanup(rbc.stop)

	value := []byte("traceable-rbc-payload!")
	shards, err := makeShards(rbc.enc, value)
	require.NoError(t, err)
	proofs, err := makeProofRequests(shards)
	require.NoError(t, err)

	now = base.Add(10 * time.Microsecond)
	require.NoError(t, rbc.HandleMessage(0, &BroadcastMessage{Payload: proofs[0]}))
	now = base.Add(15 * time.Microsecond)
	require.Error(t, rbc.HandleMessage(0, &BroadcastMessage{Payload: proofs[0]}))
	now = base.Add(20 * time.Microsecond)
	require.NoError(t, rbc.HandleMessage(1, &BroadcastMessage{Payload: &EchoRequest{ProofRequest: *proofs[1]}}))
	now = base.Add(30 * time.Microsecond)
	require.NoError(t, rbc.HandleMessage(2, &BroadcastMessage{Payload: &EchoRequest{ProofRequest: *proofs[2]}}))
	now = base.Add(40 * time.Microsecond)
	require.NoError(t, rbc.HandleMessage(1, &BroadcastMessage{Payload: &ReadyRequest{RootHash: proofs[0].RootHash}}))
	if got := rbc.trace.snapshot().RBC[rbc.proposerID]; got.DecodeEligible.Recorded ||
		got.ReconstructionStarted.Recorded || got.ReconstructionFinished.Recorded ||
		got.OutputStored.Recorded {
		t.Fatalf("RBC decoded too early: %+v", got)
	}
	assert.False(t, rbc.outputDecoded)
	now = base.Add(50 * time.Microsecond)
	require.NoError(t, rbc.HandleMessage(2, &BroadcastMessage{Payload: &ReadyRequest{RootHash: proofs[0].RootHash}}))

	got := rbc.trace.snapshot().RBC[rbc.proposerID]
	if !got.ProofAccepted.Recorded || !got.EchoSent.Recorded ||
		!got.ReadySent.Recorded || !got.DecodeEligible.Recorded ||
		!got.ReconstructionStarted.Recorded ||
		!got.ReconstructionFinished.Recorded || !got.OutputStored.Recorded {
		t.Fatalf("incomplete RBC trace: %+v", got)
	}
	if got.ProofAccepted.OffsetUS != 10 || got.EchoSent.OffsetUS != 10 || got.ReadySent.OffsetUS != 30 {
		t.Fatalf("RBC threshold offsets: %+v", got)
	}
	if got.ReconstructionStarted.OffsetUS > got.ReconstructionFinished.OffsetUS ||
		got.ReconstructionFinished.OffsetUS > got.OutputStored.OffsetUS {
		t.Fatalf("RBC reconstruction order: %+v", got)
	}
	require.Equal(t, value, rbc.Output())
	if got.ReadyTrigger != RBCReadyTriggerEchoQuorum ||
		got.ReadyTriggerEchoCount != 3 || got.ReadyTriggerReadyCount != 0 {
		t.Fatalf("ECHO-quorum READY trigger = %+v", got)
	}
}

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

func readyRelayFixture(t *testing.T) (*RBC, []byte, []*ProofRequest) {
	t.Helper()
	rbc := NewRBC(Config{ID: 0, N: 4, F: 1, Nodes: makeids(4)}, 0)
	value := []byte("traceable-rbc-payload!")
	shards, err := makeShards(rbc.enc, value)
	require.NoError(t, err)
	proofs, err := makeProofRequests(shards)
	require.NoError(t, err)
	return rbc, value, proofs
}

func handleRBCEcho(t *testing.T, rbc *RBC, senderID uint64, proof *ProofRequest) {
	t.Helper()
	require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
		Payload: &EchoRequest{ProofRequest: *proof},
	}))
}

func handleRBCReady(t *testing.T, rbc *RBC, senderID uint64, root []byte) {
	t.Helper()
	require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
		Payload: &ReadyRequest{RootHash: root},
	}))
}

func TestRBCReadyRelayAdmitsLocalReady(t *testing.T) {
	rbc, value, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	// Two distinct ECHOs provide the N-2F shards needed for reconstruction,
	// but remain below the N-F ECHO threshold that directly emits READY.
	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCEcho(t, rbc, 2, proofs[2])
	require.Empty(t, rbc.Messages())

	handleRBCReady(t, rbc, 1, root)
	assert.False(t, rbc.readySent)
	assert.NotContains(t, rbc.recvReadys, rbc.ID)
	assert.False(t, rbc.outputDecoded)
	assert.Empty(t, rbc.Messages())
	handleRBCReady(t, rbc, 2, root)

	assert.Equal(t, 3, rbc.countReadys(root))
	assert.Equal(t, root, rbc.recvReadys[rbc.ID])
	require.Equal(t, value, rbc.Output())

	messages := rbc.Messages()
	require.Len(t, messages, 1)
	ready, ok := messages[0].Payload.(*ReadyRequest)
	require.True(t, ok)
	assert.Equal(t, root, ready.RootHash)
}

func TestRBCReadyRelayStillWaitsForEnoughMatchingEchos(t *testing.T) {
	rbc, value, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCReady(t, rbc, 1, root)
	handleRBCReady(t, rbc, 2, root)

	assert.Equal(t, 3, rbc.countReadys(root))
	assert.Nil(t, rbc.Output())
	require.Len(t, rbc.Messages(), 1)

	// Reconstruction becomes eligible when a second matching shard arrives.
	handleRBCEcho(t, rbc, 2, proofs[2])
	require.Equal(t, value, rbc.Output())
	assert.Empty(t, rbc.Messages())
}

func TestRBCReadyEmissionRemainsExactlyOnceAfterEchoQuorum(t *testing.T) {
	rbc, _, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	handleRBCReady(t, rbc, 1, root)
	handleRBCReady(t, rbc, 2, root)
	require.Len(t, rbc.Messages(), 1)

	handleRBCEcho(t, rbc, 1, proofs[1])
	handleRBCEcho(t, rbc, 2, proofs[2])
	handleRBCEcho(t, rbc, 3, proofs[3])

	assert.Empty(t, rbc.Messages())
	assert.Equal(t, root, rbc.recvReadys[rbc.ID])
	assert.Equal(t, 3, rbc.countReadys(root))
}

func TestRBCEmitReadyRejectsPreAdmittedLocalReady(t *testing.T) {
	rbc, _, proofs := readyRelayFixture(t)
	t.Cleanup(rbc.stop)
	root := proofs[0].RootHash

	rbc.recvReadys[rbc.ID] = root

	err := rbc.emitReady(root, RBCReadyTriggerRelay)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local ready already admitted")
	assert.False(t, rbc.readySent)
	assert.Empty(t, rbc.Messages())
	assert.Equal(t, 1, rbc.countReadys(root))
}

// Test RBC where 1 node will not provide its value. We use 4 nodes that will
// tolerate 1 faulty node. The 3 good nodes should be able te reconstruct the
// proposed value just fine.
func TestRBC1FaultyNode(t *testing.T) {
	var (
		n          = 4
		pid        = 0
		resCh      = make(chan bcResult, 3)
		value      = []byte("not a normal looking payload")
		faultyNode = uint64(3)
	)
	nodes := makeRBCNodes(n, pid, resCh)

	var wg sync.WaitGroup
	wg.Add(n - 1)
	// Start a routine that will collect the results from all the good nodes.
	// In this case 3 results should be equal to the proposed value.
	go func() {
		for {
			res := <-resCh
			// Test that we really have a faulty node.
			assert.NotEqual(t, res.nodeID, faultyNode)
			assert.Equal(t, value, res.value)
			wg.Done()
		}
	}()

	// Faulty node will not provide its input.
	nodes[faultyNode].faulty = true
	assert.Nil(t, nodes[pid].inputValue(value))
	wg.Wait()
}

// Test RBC with 4 good nodes in the network. We expect all 4 nodes to output
// the proposed value.
func TestRBC4GoodNodes(t *testing.T) {
	var (
		n     = 4
		pid   = 0
		resCh = make(chan bcResult)
		value = []byte("not a normal looking payload")
	)
	nodes := makeRBCNodes(n, pid, resCh)

	var wg sync.WaitGroup
	wg.Add(n)
	// Start a routine that will collect the results from all the nodes. In this
	// case we expect all 4 nodes to ouput the proposed value.
	go func() {
		for {
			res := <-resCh
			assert.Equal(t, value, res.value)
			wg.Done()
		}
	}()

	assert.Nil(t, nodes[pid].inputValue(value))
	wg.Wait()
}

func TestRBCInputValue(t *testing.T) {
	rbc := NewRBC(Config{
		N: 4,
	}, 0)
	reqs, err := rbc.InputValue([]byte("this is a test string"))
	assert.Nil(t, err)
	assert.Equal(t, rbc.N-1, len(reqs))
	assert.Equal(t, 1, len(rbc.Messages()))
	assert.Equal(t, 0, len(rbc.messages))
}

func TestNewReliableBroadcast(t *testing.T) {
	assertState := func(t *testing.T, rb *RBC, cfg Config) {
		assert.NotNil(t, rb.enc)
		assert.NotNil(t, rb.recvEchos)
		assert.NotNil(t, rb.recvReadys)
		assert.Equal(t, rb.numParityShards, cfg.F*2)
		assert.Equal(t, rb.numDataShards, cfg.N-rb.numParityShards)
		assert.Equal(t, 0, len(rb.messages))
	}

	cfg := Config{N: 4, F: 1}
	rb := NewRBC(cfg, 0)
	assertState(t, rb, cfg)

	cfg = Config{N: 18, F: 4}
	rb = NewRBC(cfg, 0)
	assertState(t, rb, cfg)

	cfg = Config{N: 100, F: 10}
	rb = NewRBC(cfg, 0)
	assertState(t, rb, cfg)
}

func TestRBCOutputIsNilAfterConsuming(t *testing.T) {
	rbc := NewRBC(Config{N: 4}, 0)
	output := []byte("a")
	rbc.output = output
	assert.Equal(t, output, rbc.Output())
	assert.Nil(t, rbc.Output())
}

func TestRBCMessagesIsEmptyAfterConsuming(t *testing.T) {
	rbc := NewRBC(Config{N: 4}, 0)
	rbc.messages = []*BroadcastMessage{&BroadcastMessage{}}
	assert.Equal(t, 1, len(rbc.Messages()))
	assert.Equal(t, 0, len(rbc.Messages()))
}

func TestMakeShards(t *testing.T) {
	var (
		data = []byte("this is a very normal string.")
		nP   = 2
		nD   = 4
	)
	for i := 0; i < 10; i++ {
		nP += i
		nD += i
		enc, err := reedsolomon.New(nD, nP)
		assert.Nil(t, err)
		shards, err := makeShards(enc, data)
		assert.Nil(t, err)
		assert.Equal(t, nP+nD, len(shards))
	}
}

func TestMakeProofRequests(t *testing.T) {
	var (
		data = []byte("this is a very normal string.")
		nP   = 2
		nD   = 4
	)
	enc, err := reedsolomon.New(nD, nP)
	assert.Nil(t, err)
	shards, err := makeShards(enc, data)
	assert.Nil(t, err)
	assert.Equal(t, nP+nD, len(shards))
	reqs, err := makeProofRequests(shards)
	assert.Nil(t, err)
	assert.Equal(t, nP+nD, len(reqs))
	for _, r := range reqs {
		assert.True(t, validateProof(r))
	}
}

func TestRBCRejectsMixedRootReconstruction(t *testing.T) {
	base := time.Unix(600, 0)
	now := base
	recorder := newTraceRecorder(makeids(4), true, func() time.Time { return now })
	recorder.begin(base)
	rbc := newRBC(Config{ID: 3, N: 4, F: 1, Nodes: makeids(4)}, 0, recorder)
	t.Cleanup(rbc.stop)

	valueA := []byte("root-A-payload!!")
	valueB := []byte("root-B-payload!!")

	shardsA, err := makeShards(rbc.enc, valueA)
	require.NoError(t, err)
	proofsA, err := makeProofRequests(shardsA)
	require.NoError(t, err)

	shardsB, err := makeShards(rbc.enc, valueB)
	require.NoError(t, err)
	proofsB, err := makeProofRequests(shardsB)
	require.NoError(t, err)

	handleEcho := func(senderID uint64, proof *ProofRequest) {
		t.Helper()
		require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
			Payload: &EchoRequest{ProofRequest: *proof},
		}))
	}
	handleReady := func(senderID uint64, root []byte) {
		t.Helper()
		require.NoError(t, rbc.HandleMessage(senderID, &BroadcastMessage{
			Payload: &ReadyRequest{RootHash: root},
		}))
	}

	// Two senders echo the same root-A shard, so root A has the required
	// sender count but only one distinct shard. A root-B shard must not be used
	// to make that reconstruction possible.
	handleEcho(0, proofsA[2])
	handleEcho(1, proofsA[2])
	handleEcho(2, proofsB[0])
	handleReady(0, proofsA[0].RootHash)
	handleReady(1, proofsA[0].RootHash)
	now = base.Add(20 * time.Microsecond)
	handleReady(2, proofsA[0].RootHash)

	assert.Nil(t, rbc.Output())
	assert.False(t, rbc.progress().OutputDecoded)
	incomplete := rbc.trace.snapshot().RBC[rbc.proposerID]
	assert.True(t, incomplete.DecodeEligible.Recorded)
	assert.True(t, incomplete.ReconstructionStarted.Recorded)
	assert.False(t, incomplete.ReconstructionFinished.Recorded)
	assert.False(t, incomplete.OutputStored.Recorded)

	// A second distinct root-A shard makes reconstruction valid and must still
	// be accepted after the earlier incomplete attempt.
	now = base.Add(40 * time.Microsecond)
	handleEcho(3, proofsA[3])
	assert.Equal(t, valueA, rbc.Output())
	assert.True(t, rbc.progress().OutputDecoded)
	completed := rbc.trace.snapshot().RBC[rbc.proposerID]
	assert.Equal(t, int64(0), completed.ReconstructionStarted.OffsetUS)
	assert.Equal(t, int64(40), completed.ReconstructionFinished.OffsetUS)
	assert.Equal(t, int64(40), completed.OutputStored.OffsetUS)
}

func TestRBCRejectsReconstructionWithWrongRoot(t *testing.T) {
	rbc := NewRBC(Config{ID: 0, N: 4, F: 1}, 0)
	t.Cleanup(rbc.stop)

	shardsA, err := makeShards(rbc.enc, []byte("root-A-payload!!"))
	require.NoError(t, err)
	proofsA, err := makeProofRequests(shardsA)
	require.NoError(t, err)

	shardsB, err := makeShards(rbc.enc, []byte("root-B-payload!!"))
	require.NoError(t, err)

	rootA := proofsA[0].RootHash
	for senderID := uint64(0); senderID < 2; senderID++ {
		index := int(senderID)
		proof := proofsA[index]
		rbc.recvEchos[senderID] = &EchoRequest{ProofRequest: ProofRequest{
			RootHash: rootA,
			Proof:    [][]byte{shardsB[index]},
			Index:    proof.Index,
			Leaves:   proof.Leaves,
		}}
	}
	for senderID := uint64(0); senderID < 3; senderID++ {
		rbc.recvReadys[senderID] = rootA
	}

	require.NoError(t, rbc.tryDecodeValue(rootA))
	assert.Nil(t, rbc.Output())
	assert.False(t, rbc.progress().OutputDecoded)
}

type bcResult struct {
	nodeID uint64
	value  []byte
}

// simple engine to test RBC independently.
type testRBCEngine struct {
	faulty    bool
	rbc       *RBC
	rpcCh     <-chan RPC
	resCh     chan bcResult
	transport Transport
}

func newTestRBCEngine(resCh chan bcResult, rbc *RBC, tr Transport) *testRBCEngine {
	return &testRBCEngine{
		rbc:       rbc,
		rpcCh:     tr.Consume(),
		resCh:     resCh,
		transport: tr,
	}
}

func (e *testRBCEngine) run() {
	for {
		select {
		case rpc := <-e.rpcCh:
			err := e.rbc.HandleMessage(rpc.NodeID, rpc.Payload.(*BroadcastMessage))
			if err != nil {
				log.Println(err)
				continue
			}
			for _, msg := range e.rbc.Messages() {
				e.transport.Broadcast(e.rbc.ID, msg)
			}
			if output := e.rbc.Output(); output != nil {
				// Faulty node will refuse to send its produced output, causing
				// potential disturb of conensus liveness.
				if e.faulty {
					continue
				}
				e.resCh <- bcResult{
					nodeID: e.rbc.ID,
					value:  output,
				}
			}
		}
	}
}

func (e *testRBCEngine) inputValue(data []byte) error {
	reqs, err := e.rbc.InputValue(data)
	if err != nil {
		return err
	}
	msgs := make([]interface{}, len(reqs))
	for i := 0; i < len(reqs); i++ {
		msgs[i] = reqs[i]
	}
	e.transport.SendProofMessages(e.rbc.ID, msgs)
	return nil
}

func makeRBCNodes(n, pid int, resCh chan bcResult) []*testRBCEngine {
	transports := makeTransports(n)
	connectTransports(transports)
	nodes := make([]*testRBCEngine, len(transports))

	for i, tr := range transports {
		cfg := Config{
			ID: uint64(i),
			N:  len(transports),
		}
		nodes[i] = newTestRBCEngine(resCh, NewRBC(cfg, uint64(pid)), tr)
		go nodes[i].run()
	}
	return nodes
}

func connectTransports(tt []Transport) {
	for i := 0; i < len(tt); i++ {
		for ii := 0; ii < len(tt); ii++ {
			if ii == i {
				continue
			}
			tt[i].Connect(tt[ii].Addr(), tt[ii])
		}
	}
}
