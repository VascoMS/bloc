package app

import (
	"bytes"
	"strings"
	"testing"

	"bloc-node/internal/app/ethdemo"
	"bloc-node/internal/app/inclusion"
	"btd/be"
	"github.com/anthdm/hbbft"
)

func TestGenerateDemoEthereumTxParsesAndIsDeterministic(t *testing.T) {
	rawA, summaryA, err := ethdemo.Generate(3, 180, 50000, "12345", 1, 7)
	if err != nil {
		t.Fatalf("generate first tx: %v", err)
	}
	rawB, summaryB, err := ethdemo.Generate(3, 180, 50000, "12345", 1, 7)
	if err != nil {
		t.Fatalf("generate second tx: %v", err)
	}
	if !bytes.Equal(rawA, rawB) {
		t.Fatalf("demo tx generation is not deterministic")
	}
	if len(rawA) < 180 {
		t.Fatalf("raw tx has %d bytes, want at least 180", len(rawA))
	}
	if summaryA.Hash != summaryB.Hash {
		t.Fatalf("hash changed across deterministic generation: %s != %s", summaryA.Hash, summaryB.Hash)
	}
	if summaryA.Nonce != 7 || summaryA.Gas != 50000 || summaryA.EffectiveFeePerGasWei != "12345" {
		t.Fatalf("unexpected summary: %+v", summaryA)
	}
	if !strings.HasPrefix(summaryA.From, "0x") || len(summaryA.From) != 42 {
		t.Fatalf("invalid sender address: %s", summaryA.From)
	}
}

func TestParseEthereumTxRejectsMalformedPayload(t *testing.T) {
	if _, err := ethdemo.Parse([]byte{0x01, 0x02, 0x03}); err == nil {
		t.Fatalf("expected malformed payload to fail")
	}
}

func TestNormalizeConfigDefaultsToLibP2P(t *testing.T) {
	cfg := ConfigFile{}
	normalizeConfig(&cfg)
	if cfg.Network.Mode != "libp2p" {
		t.Fatalf("network mode = %q, want libp2p", cfg.Network.Mode)
	}
}

func TestNewNodeRejectsLegacyTCPMode(t *testing.T) {
	_, err := newNode(ConfigFile{
		Network: NetworkConfig{Mode: "tcp"},
		Nodes:   []NodeConfig{{ID: 0, HTTPAddr: "127.0.0.1:1"}},
	}, NodeSecretConfig{}, 0, FaultConfig{})
	if err == nil || !strings.Contains(err.Error(), "only libp2p is supported") {
		t.Fatalf("newNode error = %v, want unsupported network mode", err)
	}
}

func TestProtoEnvelopeCodecRoundTrip(t *testing.T) {
	codec := ProtoEnvelopeCodec{}
	in := WireEnvelope{From: 1, To: 2, Direct: true, Kind: "share", Slot: 9, Share: &WireShare{
		OperatorID: 1,
		BatchIDHex: "abcd",
		SubBatchID: 3,
		PointHex:   "ef01",
	}}

	data, err := codec.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.From != in.From || out.To != in.To || out.Direct != in.Direct || out.Kind != in.Kind || out.Slot != in.Slot {
		t.Fatalf("decoded envelope header mismatch: %+v", out)
	}
	if out.Share == nil || out.Share.OperatorID != in.Share.OperatorID || out.Share.BatchIDHex != in.Share.BatchIDHex || out.Share.SubBatchID != in.Share.SubBatchID || out.Share.PointHex != in.Share.PointHex {
		t.Fatalf("decoded share mismatch: %+v", out.Share)
	}
}

func TestProtoEnvelopeCodecRoundTripsACSPayloads(t *testing.T) {
	tests := []*hbbft.SlotMessage{
		{
			Slot: 11,
			Payload: &hbbft.ACSMessage{ProposerID: 2, Payload: &hbbft.AgreementMessage{
				Epoch:   3,
				Message: &hbbft.BvalRequest{Value: true},
			}},
		},
		{
			Slot: 11,
			Payload: &hbbft.ACSMessage{ProposerID: 2, Payload: &hbbft.AgreementMessage{
				Epoch:   4,
				Message: &hbbft.AuxRequest{Value: false},
			}},
		},
		{
			Slot: 11,
			Payload: &hbbft.ACSMessage{ProposerID: 2, Payload: &hbbft.BroadcastMessage{Payload: &hbbft.ProofRequest{
				RootHash: []byte{1, 2, 3},
				Proof:    [][]byte{{4, 5}, {6, 7}},
				Index:    1,
				Leaves:   2,
			}}},
		},
		{
			Slot: 11,
			Payload: &hbbft.ACSMessage{ProposerID: 2, Payload: &hbbft.BroadcastMessage{Payload: &hbbft.EchoRequest{ProofRequest: hbbft.ProofRequest{
				RootHash: []byte{8, 9},
				Proof:    [][]byte{{10}},
				Index:    0,
				Leaves:   1,
			}}}},
		},
		{
			Slot: 11,
			Payload: &hbbft.ACSMessage{ProposerID: 2, Payload: &hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{
				RootHash: []byte{11, 12},
			}}},
		},
	}
	codec := ProtoEnvelopeCodec{}
	for _, acs := range tests {
		in := WireEnvelope{From: 1, To: 3, Direct: true, Kind: "acs", Slot: 11, ACS: acs}
		data, err := codec.Encode(in)
		if err != nil {
			t.Fatalf("encode %T: %v", acs.Payload.Payload, err)
		}
		out, err := codec.Decode(data)
		if err != nil {
			t.Fatalf("decode %T: %v", acs.Payload.Payload, err)
		}
		if out.ACS == nil || out.ACS.Slot != acs.Slot || out.ACS.Payload.ProposerID != acs.Payload.ProposerID {
			t.Fatalf("decoded acs header mismatch: %+v", out.ACS)
		}
		if out.ACS.Payload.Payload == nil {
			t.Fatalf("decoded nil acs payload for %T", acs.Payload.Payload)
		}
	}
}

func TestInclusionListProtoRoundTrip(t *testing.T) {
	list := InclusionList{Slot: 9, OperatorID: 2, Items: []EncryptedPlaceholder{
		testPlaceholder("one", 21000, "100", "0xabc", 0),
		testPlaceholder("two", 30000, "90", "0xdef", 1),
	}}
	list.Hash = inclusion.HashInclusionList(list)
	data, err := inclusion.EncodeList(list)
	if err != nil {
		t.Fatalf("encode inclusion list: %v", err)
	}
	out, err := inclusion.DecodeList(data)
	if err != nil {
		t.Fatalf("decode inclusion list: %v", err)
	}
	if out.Hash != "" || inclusion.HashInclusionList(out) != list.Hash || len(out.Items) != len(list.Items) {
		t.Fatalf("decoded list mismatch: %+v", out)
	}
}

func TestMergeInclusionListsDeterministicAcrossListOrder(t *testing.T) {
	a := testPlaceholder("a", 21000, "10", "0xbbb", 1)
	b := testPlaceholder("b", 21000, "20", "0xaaa", 0)
	c := testPlaceholder("c", 21000, "20", "0xaaa", 1)

	left := []InclusionList{
		{Slot: 1, OperatorID: 2, Items: []EncryptedPlaceholder{a, b}},
		{Slot: 1, OperatorID: 1, Items: []EncryptedPlaceholder{c}},
	}
	right := []InclusionList{
		{Slot: 1, OperatorID: 1, Items: []EncryptedPlaceholder{c}},
		{Slot: 1, OperatorID: 2, Items: []EncryptedPlaceholder{b, a}},
	}

	mergedLeft := inclusion.Merge(1, left, BlockspaceConfig{}, 10)
	mergedRight := inclusion.Merge(1, right, BlockspaceConfig{}, 10)

	if mergedLeft.Hash != mergedRight.Hash {
		t.Fatalf("merge hash differs: %s != %s", mergedLeft.Hash, mergedRight.Hash)
	}
	if len(mergedLeft.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(mergedLeft.Items))
	}
	got := []string{mergedLeft.Items[0].Hash, mergedLeft.Items[1].Hash, mergedLeft.Items[2].Hash}
	want := []string{b.Hash, c.Hash, a.Hash}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d hash = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestMergeInclusionListsAppliesGasTxAndBMaxCaps(t *testing.T) {
	items := []EncryptedPlaceholder{
		testPlaceholder("a", 30000, "30", "0x1", 0),
		testPlaceholder("b", 30000, "20", "0x2", 0),
		testPlaceholder("c", 30000, "10", "0x3", 0),
	}
	lists := []InclusionList{{Slot: 7, OperatorID: 0, Items: items}}

	merged := inclusion.Merge(7, lists, BlockspaceConfig{MaxDecryptedGas: 60000, MaxDecryptedTxs: 3}, 2)

	if len(merged.Items) != 2 {
		t.Fatalf("selected %d items, want 2", len(merged.Items))
	}
	if merged.SelectedGas != 60000 {
		t.Fatalf("selected gas = %d, want 60000", merged.SelectedGas)
	}
	if merged.SkippedItems != 1 {
		t.Fatalf("skipped items = %d, want 1", merged.SkippedItems)
	}
}

func TestMergeInclusionListsDeduplicatesAndSkipsInvalid(t *testing.T) {
	valid := testPlaceholder("valid", 21000, "1", "0x1", 0)
	duplicate := valid
	invalidGas := testPlaceholder("invalid-gas", 0, "2", "0x2", 0)
	invalidHash := testPlaceholder("invalid-hash", 21000, "3", "0x3", 0)
	invalidHash.Hash = "deadbeef"
	invalidFee := testPlaceholder("invalid-fee", 21000, "not-a-number", "0x4", 0)

	merged := inclusion.Merge(1, []InclusionList{
		{Slot: 1, OperatorID: 0, Items: []EncryptedPlaceholder{valid, invalidGas}},
		{Slot: 1, OperatorID: 1, Items: []EncryptedPlaceholder{duplicate, invalidHash, invalidFee}},
	}, BlockspaceConfig{}, 10)

	if len(merged.Items) != 1 {
		t.Fatalf("selected %d items, want 1", len(merged.Items))
	}
	if merged.Items[0].Hash != valid.Hash {
		t.Fatalf("selected hash = %s, want %s", merged.Items[0].Hash, valid.Hash)
	}
}

func TestMergeInclusionListsConflictingDuplicateKeepsFirstWinner(t *testing.T) {
	first := testPlaceholder("shared", 21000, "10", "0x1", 0)
	conflicting := first
	conflicting.Gas = 42000

	merged := inclusion.Merge(1, []InclusionList{
		{Slot: 1, OperatorID: 0, Items: []EncryptedPlaceholder{first}},
		{Slot: 1, OperatorID: 1, Items: []EncryptedPlaceholder{conflicting}},
	}, BlockspaceConfig{}, 10)

	if len(merged.Items) != 1 || merged.Items[0].Gas != first.Gas {
		t.Fatalf("conflicting duplicate changed first-winner semantics: %+v", merged.Items)
	}
}

func TestDecodeAcceptedListsHashesOnlyDuringCanonicalization(t *testing.T) {
	list := InclusionList{Slot: 3, OperatorID: 2, Items: []EncryptedPlaceholder{
		testPlaceholder("one", 21000, "7", "0x1", 0),
	}}
	encoded, err := inclusion.EncodeList(list)
	if err != nil {
		t.Fatal(err)
	}
	lists, err := decodeAcceptedLists(3, []hbbft.AcceptedBatch{{ProposerID: 2, Batch: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	if lists[0].Hash != "" {
		t.Fatalf("decoded list was hashed before agreed-set construction: %s", lists[0].Hash)
	}
	agreed := inclusion.NewAgreedSet(3, lists)
	want := inclusion.HashInclusionList(list)
	if agreed.Lists[0].Hash != want {
		t.Fatalf("canonical hash = %s, want %s", agreed.Lists[0].Hash, want)
	}
}

func TestDecodeAcceptedListsRejectsWrongSlotAndOperator(t *testing.T) {
	tests := []struct {
		name       string
		list       InclusionList
		proposerID uint64
		want       string
	}{
		{
			name:       "slot",
			list:       InclusionList{Slot: 4, OperatorID: 2, Items: []EncryptedPlaceholder{testPlaceholder("slot", 21000, "7", "0x1", 0)}},
			proposerID: 2,
			want:       "slot 4, expected 3",
		},
		{
			name:       "operator",
			list:       InclusionList{Slot: 3, OperatorID: 1, Items: []EncryptedPlaceholder{testPlaceholder("operator", 21000, "7", "0x1", 0)}},
			proposerID: 2,
			want:       "proposer 2 claims operator 1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := inclusion.EncodeList(test.list)
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeAcceptedLists(3, []hbbft.AcceptedBatch{{ProposerID: test.proposerID, Batch: encoded}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHandleACSOutputRejectsWrongSlot(t *testing.T) {
	node := &Node{slotState: &slotState{id: 7}}
	node.handleACSOutput(&hbbft.SlotOutput{Slot: 6})
	if node.failedSlots != 1 {
		t.Fatalf("failed slots = %d, want 1", node.failedSlots)
	}
	if node.planned {
		t.Fatal("wrong-slot ACS output was planned")
	}
}

func TestMergeInclusionListsAllowsEmptySelectionUnderGasCap(t *testing.T) {
	item := testPlaceholder("too-large", 21000, "1", "0x1", 0)

	merged := inclusion.Merge(1, []InclusionList{
		{Slot: 1, OperatorID: 0, Items: []EncryptedPlaceholder{item}},
	}, BlockspaceConfig{MaxDecryptedGas: 1}, 10)

	if len(merged.Items) != 0 {
		t.Fatalf("selected %d items, want 0", len(merged.Items))
	}
	if merged.SkippedItems != 1 {
		t.Fatalf("skipped items = %d, want 1", merged.SkippedItems)
	}
}

func TestMatchingSharesForPlanIgnoresOtherBatchShares(t *testing.T) {
	var batchID [32]byte
	batchID[0] = 1
	var otherBatchID [32]byte
	otherBatchID[0] = 2
	plan := be.BatchPlan{
		BatchID: batchID,
		SubBatches: [][]be.BatchItem{
			{{OriginalPosition: 0}},
			{{OriginalPosition: 1}},
		},
	}
	shares := []be.DecryptionShare{
		{OperatorID: 0, BatchID: batchID, SubBatchID: 0},
		{OperatorID: 1, BatchID: batchID, SubBatchID: 0},
		{OperatorID: 2, BatchID: batchID, SubBatchID: 0},
		{OperatorID: 0, BatchID: batchID, SubBatchID: 1},
		{OperatorID: 1, BatchID: batchID, SubBatchID: 1},
		{OperatorID: 2, BatchID: batchID, SubBatchID: 1},
		{OperatorID: 3, BatchID: otherBatchID, SubBatchID: 0},
		{OperatorID: 3, BatchID: batchID, SubBatchID: 2},
	}

	matching, rejected := matchingSharesForPlan(shares, plan)

	if rejected != 2 {
		t.Fatalf("rejected %d shares, want 2", rejected)
	}
	if len(matching) != 6 {
		t.Fatalf("matched %d shares, want 6", len(matching))
	}
	if !hasThresholdPerSubBatch(matching, plan, 3) {
		t.Fatalf("matching shares should satisfy threshold")
	}
}

func testPlaceholder(seed string, gas uint64, fee, from string, nonce uint64) EncryptedPlaceholder {
	payload := bytes.Repeat([]byte(seed), 4)
	return EncryptedPlaceholder{
		Hash:                  hashHex(payload),
		Ciphertext:            payload,
		Gas:                   gas,
		EffectiveFeePerGasWei: fee,
		From:                  from,
		Nonce:                 nonce,
		Kind:                  "placeholder",
	}
}
