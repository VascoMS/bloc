package hbbft

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlotACSCommonSubset(t *testing.T) {
	const slot = uint64(7)

	inputs := map[int][]byte{
		0: []byte("batch-a"),
		1: []byte("batch-b"),
		2: []byte("batch-c"),
		3: []byte("batch-d"),
	}

	type slotResult struct {
		nodeID uint64
		output *SlotOutput
	}

	nodes := make([]*SlotACS, 4)
	for i := range nodes {
		nodes[i] = NewSlotACS(SlotConfig{
			Config: Config{
				N:     4,
				ID:    uint64(i),
				Nodes: makeids(4),
			},
			Slot: slot,
		})
	}

	resultCh := make(chan slotResult, len(nodes))
	messageCh := make(chan testMsg, 128)

	go func() {
		for msg := range messageCh {
			engine := nodes[msg.msg.To]
			err := engine.HandleMessage(msg.from, msg.msg.Payload.(*SlotMessage))
			require.NoError(t, err)

			for _, queued := range engine.Messages() {
				messageCh <- testMsg{from: engine.ID, msg: queued}
			}
			if output := engine.Output(); output != nil {
				resultCh <- slotResult{nodeID: engine.ID, output: output}
			}
		}
	}()

	for nodeID, batch := range inputs {
		require.NoError(t, nodes[nodeID].InputBatch(batch))
		for _, msg := range nodes[nodeID].Messages() {
			messageCh <- testMsg{from: uint64(nodeID), msg: msg}
			time.Sleep(time.Millisecond)
		}
	}

	var firstBlock []byte
	for i := 0; i < len(nodes); i++ {
		result := <-resultCh
		require.NotNil(t, result.output)
		assert.Equal(t, slot, result.output.Slot)
		assert.True(t, len(result.output.CommonSubset) >= len(nodes)-1)
		assert.Equal(t, len(result.output.CommonSubset), len(result.output.OrderedBatches))

		if firstBlock == nil {
			firstBlock = result.output.BlockBody
		} else {
			assert.Equal(t, firstBlock, result.output.BlockBody)
		}

		var decoded SlotBlockBody
		require.NoError(t, gob.NewDecoder(bytes.NewReader(result.output.BlockBody)).Decode(&decoded))
		assert.Equal(t, slot, decoded.Slot)
		for idx := 1; idx < len(decoded.Batches); idx++ {
			assert.True(t, decoded.Batches[idx-1].ProposerID < decoded.Batches[idx].ProposerID)
		}
		for _, batch := range decoded.Batches {
			assert.Equal(t, inputs[int(batch.ProposerID)], batch.Batch)
		}
	}
}

func TestSlotACSCloseIsIdempotent(t *testing.T) {
	slot := NewSlotACS(SlotConfig{Config: Config{N: 4, ID: 0, Nodes: makeids(4)}, Slot: 1})
	slot.Close()
	slot.Close()
}

func TestSlotACSBeginTraceUsesOneRecorderAcrossChildren(t *testing.T) {
	base := time.Unix(200, 0)
	now := base
	slot := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: makeids(4)},
		Slot:   9,
		Trace:  TraceOptions{Enabled: true, Now: func() time.Time { return now }},
	})
	t.Cleanup(slot.Close)

	slot.BeginTrace(base)
	if slot.trace == nil || slot.acs.trace != slot.trace ||
		slot.acs.rbcInstances[0].trace != slot.trace ||
		slot.acs.bbaInstances[0].trace != slot.trace {
		t.Fatal("ACS children do not share the slot recorder")
	}
	now = base.Add(12 * time.Microsecond)
	slot.trace.recordAggregate(traceACSInputStarted)
	if got := slot.Trace().Aggregate.InputStarted.OffsetUS; got != 12 {
		t.Fatalf("shared recorder offset = %d", got)
	}
}

func TestSlotACSRejectsWrongSlotMessage(t *testing.T) {
	engine := NewSlotACS(SlotConfig{
		Config: Config{
			N:     4,
			ID:    0,
			Nodes: makeids(4),
		},
		Slot: 9,
	})

	err := engine.HandleMessage(1, &SlotMessage{
		Slot:    8,
		Payload: &ACSMessage{ProposerID: 1, Payload: &BroadcastMessage{}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 9")
}

func TestSlotACSProgressReportsInputState(t *testing.T) {
	engine := NewSlotACS(SlotConfig{
		Config: Config{
			N:     4,
			ID:    0,
			Nodes: makeids(4),
		},
		Slot: 11,
	})
	require.NoError(t, engine.InputBatch([]byte("batch")))
	progress := engine.Progress()
	assert.Equal(t, uint64(11), progress.Slot)
	assert.False(t, progress.ACS.Decided)
	if progress.ACS.QueuedMessages <= 0 {
		t.Fatalf("queued messages = %d, want > 0", progress.ACS.QueuedMessages)
	}
	assert.True(t, progress.ACS.RBC[0].EchoSent)
}

func TestSlotACSPostAgreementHook(t *testing.T) {
	engine := NewSlotACS(SlotConfig{
		Config: Config{
			N:     4,
			ID:    0,
			Nodes: makeids(4),
		},
		Slot: 3,
		PostAgreement: func(output *SlotOutput) (interface{}, error) {
			return string(output.BlockBody), nil
		},
	})

	encodedBatch, err := encodeCandidateBatch([]byte("single-batch"))
	require.NoError(t, err)
	engine.acs.output = map[uint64][]byte{0: encodedBatch}
	require.NoError(t, engine.maybeBuildOutput())
	output := engine.Output()
	require.NotNil(t, output)
	assert.NotNil(t, output.DecryptionResult)
}

func TestSlotACSTraceSeparatesCoreDecodeAndBlockBuild(t *testing.T) {
	base := time.Unix(900, 0)
	now := base
	engine := NewSlotACS(SlotConfig{
		Config: Config{N: 4, F: 1, ID: 0, Nodes: makeids(4)},
		Slot:   5,
		Trace:  TraceOptions{Enabled: true, Now: func() time.Time { return now }},
		BlockBuilder: func(slot uint64, batches []AcceptedBatch) ([]byte, error) {
			now = base.Add(30 * time.Microsecond)
			return EncodeSlotBlockBody(slot, batches)
		},
	})
	t.Cleanup(engine.Close)
	engine.BeginTrace(base)

	encodedBatch, err := encodeCandidateBatch([]byte("trace-batch"))
	require.NoError(t, err)
	now = base.Add(10 * time.Microsecond)
	engine.trace.recordAggregate(traceACSCoreDecision)
	engine.trace.recordRBC(0, traceRBCProofAccepted)
	engine.acs.output = map[uint64][]byte{0: encodedBatch}
	now = base.Add(20 * time.Microsecond)
	require.NoError(t, engine.maybeBuildOutput())

	output := engine.Output()
	require.NotNil(t, output)
	got := output.ACSTrace
	if got.Aggregate.CoreDecision.OffsetUS != 10 ||
		got.Adapter.CommonSubsetDecoded.OffsetUS != 20 ||
		got.Adapter.BlockBodyBuilt.OffsetUS != 30 {
		t.Fatalf("slot trace boundaries: %+v", got)
	}
	output.ACSTrace.RBC[0] = RBCTrace{}
	if !engine.Trace().RBC[0].ProofAccepted.Recorded {
		t.Fatal("output trace aliases the live recorder")
	}
	now = base.Add(40 * time.Microsecond)
	engine.MarkNodeOutputReceived()
	if got := engine.Trace().Adapter.NodeOutputReceived.OffsetUS; got != 40 {
		t.Fatalf("node output receipt offset = %d", got)
	}
}
