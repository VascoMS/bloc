package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"btd/be"
	"go.dedis.ch/kyber/v4/share"
)

func TestResourceLimitsDefaultsAndValidation(t *testing.T) {
	cfg := ConfigFile{}
	normalizeConfig(&cfg)
	if cfg.Limits != defaultResourceLimits() {
		t.Fatalf("defaults = %+v, want %+v", cfg.Limits, defaultResourceLimits())
	}
	if err := validateResourceLimits(cfg.Limits); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	tests := []ResourceLimits{
		{MaxProposalBytes: -1, MaxEnvelopeBytes: defaultMaxEnvelopeBytes, MaxCombineAttemptsPerSubBatch: 1},
		{MaxProposalBytes: absoluteMaxProposalBytes + 1, MaxEnvelopeBytes: absoluteMaxEnvelopeBytes, MaxCombineAttemptsPerSubBatch: 1},
		{MaxProposalBytes: defaultMaxProposalBytes, MaxEnvelopeBytes: defaultMaxProposalBytes, MaxCombineAttemptsPerSubBatch: 1},
		{MaxProposalBytes: defaultMaxProposalBytes, MaxEnvelopeBytes: absoluteMaxEnvelopeBytes + 1, MaxCombineAttemptsPerSubBatch: 1},
		{MaxProposalBytes: defaultMaxProposalBytes, MaxEnvelopeBytes: defaultMaxEnvelopeBytes, MaxCombineAttemptsPerSubBatch: absoluteMaxCombineAttemptsPerSubBatch + 1},
	}
	for _, limits := range tests {
		if err := validateResourceLimits(limits); err == nil {
			t.Fatalf("invalid limits accepted: %+v", limits)
		}
	}
}

func TestResourceLimitsRejectExplicitJSONZero(t *testing.T) {
	var limits ResourceLimits
	if err := json.Unmarshal([]byte(`{"max_proposal_bytes":0}`), &limits); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigFile{Limits: limits}
	normalizeConfig(&cfg)
	if err := validateResourceLimits(cfg.Limits); err == nil {
		t.Fatal("explicit zero proposal limit was replaced by a default")
	}
}

func TestProposalBoundsAcceptExactLimitsAndRejectOverflow(t *testing.T) {
	cfg := ConfigFile{BMax: 4, Limits: ResourceLimits{MaxProposalBytes: 100}}
	if err := validateProposalBounds(4, 100, cfg); err != nil {
		t.Fatalf("exact bounds rejected: %v", err)
	}
	if err := validateProposalBounds(5, 100, cfg); err == nil || !strings.Contains(err.Error(), "BMax") {
		t.Fatalf("item overflow error = %v", err)
	}
	if err := validateProposalBounds(4, 101, cfg); err == nil || !strings.Contains(err.Error(), "maximum 100") {
		t.Fatalf("byte overflow error = %v", err)
	}
}

func TestReadBoundedEnvelope(t *testing.T) {
	data, err := readBoundedEnvelope(bytes.NewReader([]byte("1234")), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("exact read = %q, %v", data, err)
	}
	if _, err := readBoundedEnvelope(bytes.NewReader([]byte("12345")), 4); !errors.Is(err, errEnvelopeTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestTruncatedEnvelopeFailsDecoding(t *testing.T) {
	codec := ProtoEnvelopeCodec{}
	encoded, err := codec.Encode(WireEnvelope{
		From: 1, To: 0, Direct: true, Kind: "share", Slot: 1,
		Share: &WireShare{OperatorID: 1, BatchIDHex: strings.Repeat("00", 32), PointHex: strings.Repeat("00", 48)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(encoded[:len(encoded)/2]); err == nil {
		t.Fatal("truncated protobuf envelope decoded successfully")
	}
}

type fixedEnvelopeCodec struct{ encoded []byte }

func (c fixedEnvelopeCodec) Encode(WireEnvelope) ([]byte, error) { return c.encoded, nil }
func (fixedEnvelopeCodec) Decode([]byte) (WireEnvelope, error)   { return WireEnvelope{}, nil }

func TestOutboundEnvelopeRejectsOversizeBeforeOpeningStream(t *testing.T) {
	node := &Node{
		cfg:   ConfigFile{Limits: ResourceLimits{MaxEnvelopeBytes: 4}},
		self:  NodeConfig{ID: 0},
		peers: map[uint64]NodeConfig{1: {ID: 1}},
	}
	transport := newLibP2PTransport(node, fixedEnvelopeCodec{encoded: []byte("12345")})
	if _, err := transport.Send(t.Context(), 1, WireEnvelope{}); !errors.Is(err, errEnvelopeTooLarge) {
		t.Fatalf("oversize send error = %v", err)
	}
}

func TestShareAdmissionIsBoundedAndPrunedToPlan(t *testing.T) {
	node := resourceSafetyTestNode(4)
	var batchID [32]byte
	batchID[0] = 1
	for operatorID := 0; operatorID < node.cfg.N; operatorID++ {
		for subBatchID := 0; subBatchID < node.cfg.BMax; subBatchID++ {
			if err := node.addShare(testDecryptionShare(node, operatorID, subBatchID, batchID, false)); err != nil {
				t.Fatalf("add operator=%d sub=%d: %v", operatorID, subBatchID, err)
			}
		}
	}
	node.mu.Lock()
	if got, want := node.retainedShareCountLocked(), node.cfg.N*node.cfg.BMax; got != want {
		node.mu.Unlock()
		t.Fatalf("retained shares = %d, want %d", got, want)
	}
	plan := be.BatchPlan{BatchID: batchID, SubBatches: make([][]be.BatchItem, 2)}
	node.plan = plan
	node.planned = true
	node.pruneShareCandidatesLocked(plan)
	got := node.retainedShareCountLocked()
	node.mu.Unlock()
	if want := node.cfg.N * len(plan.SubBatches); got != want {
		t.Fatalf("post-plan retained shares = %d, want %d", got, want)
	}

	if err := node.addShare(testDecryptionShare(node, 0, 0, batchID, false)); err != nil {
		t.Fatalf("identical duplicate rejected: %v", err)
	}
	if err := node.addShare(testDecryptionShare(node, 0, 0, batchID, true)); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
	var otherBatch [32]byte
	otherBatch[0] = 2
	if err := node.addShare(testDecryptionShare(node, 1, 0, otherBatch, false)); err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("wrong batch error = %v", err)
	}
	if err := node.addShare(testDecryptionShare(node, 1, 2, batchID, false)); err == nil || !strings.Contains(err.Error(), "sub-batch") {
		t.Fatalf("wrong sub-batch error = %v", err)
	}
}

func TestShareAdmissionRejectsUnknownOperatorAndIndexMismatch(t *testing.T) {
	node := resourceSafetyTestNode(4)
	var batchID [32]byte
	if err := node.addShare(testDecryptionShare(node, 9, 0, batchID, false)); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown operator error = %v", err)
	}
	candidate := testDecryptionShare(node, 1, 0, batchID, false)
	candidate.Share.I = 2
	if err := node.addShare(candidate); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("index mismatch error = %v", err)
	}
}

func TestWireShareAdmissionRequiresCanonicalLengths(t *testing.T) {
	node := resourceSafetyTestNode(4)
	pointHex, err := marshalPointHex(node.suite.G1().Point().Base())
	if err != nil {
		t.Fatal(err)
	}
	valid := WireShare{
		OperatorID: 0,
		BatchIDHex: strings.Repeat("00", 32),
		SubBatchID: 0,
		PointHex:   pointHex,
	}
	if err := node.addWireShare(valid); err != nil {
		t.Fatalf("canonical share rejected: %v", err)
	}

	tests := map[string]WireShare{
		"prefixed batch": func() WireShare {
			candidate := valid
			candidate.BatchIDHex = "0x" + candidate.BatchIDHex
			return candidate
		}(),
		"short batch": func() WireShare {
			candidate := valid
			candidate.BatchIDHex = candidate.BatchIDHex[1:]
			return candidate
		}(),
		"prefixed point": func() WireShare { candidate := valid; candidate.PointHex = "0x" + candidate.PointHex; return candidate }(),
		"short point":    func() WireShare { candidate := valid; candidate.PointHex = candidate.PointHex[1:]; return candidate }(),
		"bad sub-batch":  func() WireShare { candidate := valid; candidate.SubBatchID = node.cfg.BMax; return candidate }(),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if err := node.addWireShare(candidate); err == nil {
				t.Fatal("non-canonical share accepted")
			}
		})
	}
}

func resourceSafetyTestNode(bmax int) *Node {
	peers := make(map[uint64]NodeConfig, 4)
	ids := make([]uint64, 4)
	for i := range ids {
		ids[i] = uint64(i)
		peers[uint64(i)] = NodeConfig{ID: uint64(i)}
	}
	return &Node{
		cfg:     ConfigFile{N: 4, BMax: bmax, Threshold: 3, Limits: defaultResourceLimits()},
		self:    NodeConfig{ID: 0},
		peers:   peers,
		nodeIDs: ids,
		suite:   newSuite(),
		slotState: &slotState{
			shareCandidates: make(map[int]*operatorShareCandidates),
		},
	}
}

func testDecryptionShare(node *Node, operatorID, subBatchID int, batchID [32]byte, alternate bool) be.DecryptionShare {
	point := node.suite.G1().Point().Base()
	if alternate {
		point = node.suite.G1().Point().Add(point, node.suite.G1().Point().Base())
	}
	return be.DecryptionShare{
		OperatorID: operatorID,
		BatchID:    batchID,
		SubBatchID: subBatchID,
		Share:      &share.PubShare{I: uint32(operatorID), V: point},
	}
}
