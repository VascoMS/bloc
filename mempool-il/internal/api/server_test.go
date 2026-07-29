package api

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"mempool-il/internal/inclusion"
	"mempool-il/internal/mempool"
)

type staticSlotSource struct{}

func (staticSlotSource) FetchSlot(_ context.Context, slot uint64, limit int) (mempool.SlotPage, error) {
	first := mempool.Transaction{
		Hash: "0x01", From: "0xabc", Nonce: 1, Gas: 21_000,
		Kind: mempool.TxKindPlaceholder, EncryptedPayloadHex: "0x6274652d74782d7632",
		EffectiveFeePerGasW: big.NewInt(1),
	}
	second := first
	second.Hash = "0x02"
	second.Nonce = 2
	second.EffectiveFeePerGasW = big.NewInt(10)
	transactions := []mempool.Transaction{first, second}
	if limit < len(transactions) {
		transactions = transactions[:limit]
	}
	return mempool.SlotPage{
		SchemaVersion: "bloc-encrypted-corpus-v1", CiphertextWireVersion: "bte-tx-v2",
		PublicConfigID: "public", PlaintextMasterCorpusID: "plaintext",
		EncryptedCorpusID: "encrypted", EncryptedPrefixSetID: "prefix",
		Slot: slot, RequestedCount: limit, AvailableCount: 2, ReturnedCount: len(transactions),
		Transactions: transactions,
	}, nil
}

func TestInclusionListPreservesStaticCorpusOrder(t *testing.T) {
	server := NewServerWithSlotSource(
		mempool.NewStore(),
		inclusion.NewBuilder(inclusion.Config{MaxTransactions: 8, MaxGas: 1_000_000}),
		staticSlotSource{},
	)
	request := httptest.NewRequest(http.MethodGet, "/inclusion-list?slot=7&limit=2", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	var body struct {
		Items []struct {
			Hash string `json:"hash"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].Hash != "0x01" || body.Items[1].Hash != "0x02" {
		t.Fatalf("static order changed: %+v", body.Items)
	}
}

func TestInclusionListRequiresLimitForStaticCorpus(t *testing.T) {
	server := NewServerWithSlotSource(
		mempool.NewStore(),
		inclusion.NewBuilder(inclusion.Config{MaxTransactions: 8, MaxGas: 1_000_000}),
		staticSlotSource{},
	)
	request := httptest.NewRequest(http.MethodGet, "/inclusion-list?slot=7", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestInclusionListReturnsStaticCorpusProvenance(t *testing.T) {
	server := NewServerWithSlotSource(
		mempool.NewStore(),
		inclusion.NewBuilder(inclusion.Config{MaxTransactions: 8, MaxGas: 1_000_000}),
		staticSlotSource{},
	)
	request := httptest.NewRequest(http.MethodGet, "/inclusion-list?slot=7&limit=1", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"schema_version": "bloc-encrypted-corpus-v1", "ciphertext_wire_version": "bte-tx-v2",
		"public_config_id": "public", "encrypted_corpus_id": "encrypted",
		"encrypted_prefix_set_id": "prefix", "slot": float64(7),
		"requested_count": float64(1), "available_count": float64(2), "returned_count": float64(1),
	} {
		if body[field] != want {
			t.Fatalf("%s = %v, want %v", field, body[field], want)
		}
	}
}
