package hbbft

import (
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Test ACS with 4 good nodes. The result should be that at least the output
// of (N - f) nodes has been provided.
func TestACSGoodNodes(t *testing.T) {
	inputs := map[int][]byte{
		0: []byte("AAAAAA"),
		1: []byte("BBBBBB"),
		2: []byte("CCCCCC"),
		3: []byte("DDDDDD"),
	}
	testCommonSubset(t, inputs)
}

func testCommonSubset(t *testing.T, inputs map[int][]byte) {
	type acsResult struct {
		nodeID  uint64
		results map[uint64][]byte
	}
	var (
		resultCh = make(chan acsResult, 4)
		nodes    = makeACSNetwork(4)
		messages = make(chan testMsg)
	)

	go func() {
		for {
			select {
			case msg := <-messages:
				acs := nodes[msg.msg.To]
				err := acs.HandleMessage(msg.from, msg.msg.Payload.(*ACSMessage))
				if err != nil {
					t.Fatal(err)
				}
				for _, msg := range acs.messageQue.messages() {
					go func(msg MessageTuple) {
						messages <- testMsg{acs.ID, msg}
					}(msg)
				}
				if output := acs.Output(); output != nil {
					resultCh <- acsResult{acs.ID, output}
				}
			}
		}
	}()

	for nodeID, value := range inputs {
		assert.Nil(t, nodes[nodeID].InputValue(value))
		for _, msg := range nodes[nodeID].messageQue.messages() {
			messages <- testMsg{uint64(nodeID), msg}
			time.Sleep(1 * time.Millisecond)
		}
	}

	count := 0
	for res := range resultCh {
		assert.True(t, len(res.results) >= len(nodes)-1)
		for id, result := range res.results {
			assert.Equal(t, inputs[int(id)], result)
		}
		count++
		if count == 4 {
			break
		}
	}
}

func TestNewACS(t *testing.T) {
	var (
		id    = uint64(0)
		nodes = []uint64{0, 1, 2, 3}
		acs   = NewACS(Config{
			N:     len(nodes),
			ID:    id,
			Nodes: nodes,
		})
	)
	assert.Equal(t, len(nodes), len(acs.bbaInstances))
	assert.Equal(t, len(nodes), len(acs.rbcInstances))

	for i := range acs.rbcInstances {
		_, ok := acs.bbaInstances[i]
		assert.True(t, ok)
	}
	for i := range acs.bbaInstances {
		_, ok := acs.bbaInstances[i]
		assert.True(t, ok)
	}
	assert.Equal(t, id, acs.ID)
}

func TestNewACSPreservesTraceDisabledCompatibility(t *testing.T) {
	acs := NewACS(Config{N: 4, ID: 0, Nodes: makeids(4)})
	t.Cleanup(acs.stop)
	if acs.trace == nil {
		t.Fatal("legacy constructor did not supply a disabled recorder")
	}
	if got := acs.trace.snapshot(); got.Enabled {
		t.Fatalf("legacy constructor enabled tracing: %+v", got)
	}
	for id, rbc := range acs.rbcInstances {
		if rbc.trace != acs.trace {
			t.Fatalf("RBC %d does not share the ACS recorder", id)
		}
	}
	if acs.bbaInstances[0].trace != acs.trace {
		t.Fatal("BBA does not share the ACS recorder")
	}
}

func TestACSOutputIsNilAfterConsuming(t *testing.T) {
	acs := NewACS(Config{N: 4})
	output := map[uint64][]byte{
		1: []byte("this is it"),
	}
	acs.output = output
	assert.Equal(t, output, acs.Output())
	assert.Nil(t, acs.Output())
}

func TestACSWaitsForEveryTruthyRBCResult(t *testing.T) {
	acs := &ACS{
		Config:     Config{N: 4, F: 1},
		bbaResults: map[uint64]bool{0: true, 1: true, 2: true, 3: false},
		rbcResults: map[uint64][]byte{0: []byte("zero"), 1: []byte("one")},
	}

	acs.tryCompleteAgreement()
	if acs.decided || acs.output != nil {
		t.Fatal("ACS decided before every truthy RBC result was available")
	}

	acs.rbcResults[2] = []byte("two")
	acs.tryCompleteAgreement()
	if !acs.decided {
		t.Fatal("ACS did not decide after every truthy RBC result became available")
	}
	assert.Equal(t, map[uint64][]byte{
		0: []byte("zero"),
		1: []byte("one"),
		2: []byte("two"),
	}, acs.output)
}

func TestACSWaitsForAllBBAResultsDespiteAllReliableBroadcastsOutput(t *testing.T) {
	acs := &ACS{
		Config:     Config{N: 4, F: 1},
		bbaResults: map[uint64]bool{0: true, 1: true, 2: true},
		rbcResults: map[uint64][]byte{
			0: []byte("zero"),
			1: []byte("one"),
			2: []byte("two"),
			3: []byte("three"),
		},
	}

	acs.tryCompleteAgreement()
	if acs.decided || acs.output != nil {
		t.Fatal("ACS decided before every BBA instance produced a result")
	}

	acs.bbaResults[3] = false
	acs.tryCompleteAgreement()
	if !acs.decided {
		t.Fatal("ACS did not decide after every BBA instance produced a result")
	}
	assert.Equal(t, map[uint64][]byte{
		0: []byte("zero"),
		1: []byte("one"),
		2: []byte("two"),
	}, acs.output)
}

func TestACSProgressExplainsIncompleteAgreement(t *testing.T) {
	acs := &ACS{
		Config:       Config{N: 4, F: 1},
		messageQue:   newMessageQue(),
		bbaInstances: map[uint64]*BBA{},
		rbcInstances: map[uint64]*RBC{},
		bbaResults:   map[uint64]bool{2: true, 0: true, 1: true},
		rbcResults: map[uint64][]byte{
			3: []byte("three"),
			1: []byte("one"),
			0: []byte("zero"),
			2: []byte("two"),
		},
	}

	progress := acs.progress()
	assert.Equal(t, 3, progress.BBAResultCount)
	assert.Equal(t, map[uint64]bool{0: true, 1: true, 2: true}, progress.BBAResults)
	assert.Equal(t, []uint64{0, 1, 2}, progress.TruthyBBAProposerIDs)
	assert.Equal(t, []uint64{0, 1, 2, 3}, progress.RBCOutputIDs)
	assert.Equal(t, "waiting_for_all_bba_results", progress.WaitingReason)
}

func TestACSCommonSubsetAcrossReorderedDeliverySchedules(t *testing.T) {
	const schedules = 1000
	for seed := int64(0); seed < schedules; seed++ {
		outputs := runReorderedSlotSchedule(t, seed)
		first := outputs[0]
		if len(first) < 3 {
			t.Fatalf("seed %d: common subset size = %d, want at least 3", seed, len(first))
		}
		for nodeID := 1; nodeID < 4; nodeID++ {
			if !reflect.DeepEqual(first, outputs[nodeID]) {
				t.Fatalf("seed %d: node 0 subset %v differs from node %d subset %v", seed, first, nodeID, outputs[nodeID])
			}
		}
	}
}

type scheduledSlotMessage struct {
	from uint64
	msg  MessageTuple
}

func runReorderedSlotSchedule(t *testing.T, seed int64) map[int]map[uint64][]byte {
	t.Helper()
	const nodeCount = 4
	nodes := make([]*SlotACS, nodeCount)
	for i := range nodes {
		nodes[i] = NewSlotACS(SlotConfig{
			Config: Config{N: nodeCount, ID: uint64(i), Nodes: makeids(nodeCount)},
			Slot:   uint64(seed + 1),
		})
		defer nodes[i].Close()
	}

	queue := make([]scheduledSlotMessage, 0, 256)
	for nodeID, node := range nodes {
		if err := node.InputBatch([]byte{byte('A' + nodeID)}); err != nil {
			t.Fatalf("seed %d: node %d input: %v", seed, nodeID, err)
		}
		for _, msg := range node.Messages() {
			queue = append(queue, scheduledSlotMessage{from: uint64(nodeID), msg: msg})
		}
	}

	rng := rand.New(rand.NewSource(seed))
	outputs := make(map[int]map[uint64][]byte, nodeCount)
	for len(queue) > 0 && len(outputs) < nodeCount {
		idx := rng.Intn(len(queue))
		event := queue[idx]
		queue[idx] = queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		target := nodes[event.msg.To]
		if err := target.HandleMessage(event.from, event.msg.Payload.(*SlotMessage)); err != nil {
			t.Fatalf("seed %d: deliver %d -> %d: %v", seed, event.from, event.msg.To, err)
		}
		for _, msg := range target.Messages() {
			queue = append(queue, scheduledSlotMessage{from: target.ID, msg: msg})
		}
		if output := target.Output(); output != nil {
			outputs[int(target.ID)] = output.CommonSubset
		}
	}
	if len(outputs) != nodeCount {
		t.Fatalf("seed %d: %d/%d nodes completed with %d messages pending", seed, len(outputs), nodeCount, len(queue))
	}
	return outputs
}

type testMsg struct {
	from uint64
	msg  MessageTuple
}

func makeACSNetwork(n int) []*ACS {
	network := make([]*ACS, n)
	for i := 0; i < n; i++ {
		network[i] = NewACS(Config{N: n, ID: uint64(i), Nodes: makeids(n)})
	}
	return network
}

func makeids(n int) []uint64 {
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		ids[i] = uint64(i)
	}
	return ids
}

// makeTransports is a test helper function for making n number of transports.
func makeTransports(n int) []Transport {
	transports := make([]Transport, n)
	for i := 0; i < n; i++ {
		transports[i] = NewLocalTransport(uint64(i))
	}
	return transports
}
