package hbbft

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type acsTraceBenchmarkMessage struct {
	from uint64
	msg  MessageTuple
}

type acsTraceBenchmarkOutput struct {
	commonSubset   map[uint64][]byte
	orderedBatches []AcceptedBatch
	blockBody      []byte
}

type acsTraceBenchmarkResult struct {
	outputs  []acsTraceBenchmarkOutput
	messages []acsTraceBenchmarkMessage
}

type acsTraceBenchmarkNetwork struct {
	nodes []*SlotACS
}

var acsTraceBenchmarkSink int

func TestACSTracePreservesLocalOutputsAndMessageSchedule(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 256)
	for _, nodes := range []int{4, 7} {
		off := runACSTraceBenchmarkCase(t, nodes, payload, false)
		on := runACSTraceBenchmarkCase(t, nodes, payload, true)
		if !reflect.DeepEqual(off.outputs, on.outputs) {
			t.Fatalf("n=%d trace changed local ACS outputs", nodes)
		}
		if !reflect.DeepEqual(off.messages, on.messages) {
			t.Fatalf("n=%d trace changed emitted message schedule", nodes)
		}
	}
}

// BenchmarkACSTrace measures the entire local slot-ACS critical path with the
// same payload and FIFO delivery schedule for each trace-off/trace-on pair.
// The literal proposal sizes are the encoded batch-8/32/128 prefixes derived
// from both retained n4/n7 Issue #15 readiness corpora; the sizes are identical
// across those two cluster-specific corpora.
func BenchmarkACSTrace(b *testing.B) {
	cases := []struct {
		batch         int
		proposalBytes int
	}{
		{batch: 8, proposalBytes: 12234},
		{batch: 32, proposalBytes: 50982},
		{batch: 128, proposalBytes: 201622},
	}
	for _, nodes := range []int{4, 7} {
		for _, test := range cases {
			for _, enabled := range []bool{false, true} {
				traceMode := "off"
				if enabled {
					traceMode = "on"
				}
				name := fmt.Sprintf("n%d/batch%d/trace_%s", nodes, test.batch, traceMode)
				b.Run(name, func(b *testing.B) {
					payload := bytes.Repeat([]byte{0x5a}, test.proposalBytes)
					b.ReportAllocs()
					b.ReportMetric(float64(test.proposalBytes), "proposal_B")
					b.ResetTimer()
					for iteration := 0; iteration < b.N; iteration++ {
						b.StopTimer()
						network := newACSTraceBenchmarkNetwork(nodes, enabled)
						b.StartTimer()
						result := network.run(b, payload)
						b.StopTimer()
						network.close()
						acsTraceBenchmarkSink += len(result.messages)
						b.StartTimer()
					}
				})
			}
		}
	}
}

func runACSTraceBenchmarkCase(tb testing.TB, nodeCount int, payload []byte, enabled bool) acsTraceBenchmarkResult {
	tb.Helper()
	network := newACSTraceBenchmarkNetwork(nodeCount, enabled)
	defer network.close()
	return network.run(tb, payload)
}

func newACSTraceBenchmarkNetwork(nodeCount int, enabled bool) *acsTraceBenchmarkNetwork {
	nodes := make([]*SlotACS, nodeCount)
	for id := range nodes {
		nodes[id] = NewSlotACS(SlotConfig{
			Config: Config{N: nodeCount, F: (nodeCount - 1) / 3, ID: uint64(id), Nodes: makeids(nodeCount)},
			Slot:   1,
			Trace:  TraceOptions{Enabled: enabled},
		})
	}
	return &acsTraceBenchmarkNetwork{nodes: nodes}
}

func (network *acsTraceBenchmarkNetwork) close() {
	for _, node := range network.nodes {
		node.Close()
	}
}

func (network *acsTraceBenchmarkNetwork) run(tb testing.TB, payload []byte) acsTraceBenchmarkResult {
	tb.Helper()
	nodeCount := len(network.nodes)
	for _, node := range network.nodes {
		node.BeginTrace(time.Now())
	}

	result := acsTraceBenchmarkResult{
		outputs:  make([]acsTraceBenchmarkOutput, nodeCount),
		messages: make([]acsTraceBenchmarkMessage, 0, nodeCount*nodeCount*16),
	}
	for id, node := range network.nodes {
		if err := node.InputBatch(payload); err != nil {
			tb.Fatalf("n=%d node=%d input: %v", nodeCount, id, err)
		}
		for _, msg := range node.Messages() {
			result.messages = append(result.messages, acsTraceBenchmarkMessage{from: uint64(id), msg: msg})
		}
	}

	completed := 0
	for cursor := 0; cursor < len(result.messages) && completed < nodeCount; cursor++ {
		event := result.messages[cursor]
		target := network.nodes[event.msg.To]
		if err := target.HandleMessage(event.from, event.msg.Payload.(*SlotMessage)); err != nil {
			tb.Fatalf("n=%d deliver %d -> %d: %v", nodeCount, event.from, event.msg.To, err)
		}
		for _, msg := range target.Messages() {
			result.messages = append(result.messages, acsTraceBenchmarkMessage{from: target.ID, msg: msg})
		}
		if output := target.Output(); output != nil {
			result.outputs[target.ID] = acsTraceBenchmarkOutput{
				commonSubset: output.CommonSubset, orderedBatches: output.OrderedBatches, blockBody: output.BlockBody,
			}
			completed++
		}
	}
	if completed != nodeCount {
		tb.Fatalf("n=%d completed %d/%d nodes after %d emitted messages", nodeCount, completed, nodeCount, len(result.messages))
	}
	return result
}
