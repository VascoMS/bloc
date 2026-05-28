package app

import (
	"bytes"
	"testing"
)

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

	mergedLeft := mergeInclusionLists(1, left, BlockspaceConfig{}, 10)
	mergedRight := mergeInclusionLists(1, right, BlockspaceConfig{}, 10)

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

	merged := mergeInclusionLists(7, lists, BlockspaceConfig{MaxDecryptedGas: 60000, MaxDecryptedTxs: 3}, 2)

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

	merged := mergeInclusionLists(1, []InclusionList{
		{Slot: 1, OperatorID: 0, Items: []EncryptedPlaceholder{valid, invalidGas}},
		{Slot: 1, OperatorID: 1, Items: []EncryptedPlaceholder{duplicate, invalidHash}},
	}, BlockspaceConfig{}, 10)

	if len(merged.Items) != 1 {
		t.Fatalf("selected %d items, want 1", len(merged.Items))
	}
	if merged.Items[0].Hash != valid.Hash {
		t.Fatalf("selected hash = %s, want %s", merged.Items[0].Hash, valid.Hash)
	}
}

func TestMergeInclusionListsAllowsEmptySelectionUnderGasCap(t *testing.T) {
	item := testPlaceholder("too-large", 21000, "1", "0x1", 0)

	merged := mergeInclusionLists(1, []InclusionList{
		{Slot: 1, OperatorID: 0, Items: []EncryptedPlaceholder{item}},
	}, BlockspaceConfig{MaxDecryptedGas: 1}, 10)

	if len(merged.Items) != 0 {
		t.Fatalf("selected %d items, want 0", len(merged.Items))
	}
	if merged.SkippedItems != 1 {
		t.Fatalf("skipped items = %d, want 1", merged.SkippedItems)
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
