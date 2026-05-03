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
