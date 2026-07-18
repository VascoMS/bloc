package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMempoolProviderConsumesEncryptedPayloadHex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"hash":"0xplaceholder","kind":"placeholder","encrypted_payload_hex":"0x010203","placeholder_envelope_hex":"0x70686c64","gas":21000,"effective_fee_per_gas_wei":"10","from":"0xabc","nonce":1}]}`))
	}))
	defer server.Close()

	node := &Node{
		cfg:       ConfigFile{Provider: ProviderConfig{Mode: "mempool-http", MempoolURL: server.URL}},
		self:      NodeConfig{ID: 2},
		slotState: &slotState{id: 9},
	}
	list, err := node.fetchMempoolInclusionList()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(list.Items))
	}
	if got := list.Items[0].Ciphertext; string(got) != string([]byte{0x01, 0x02, 0x03}) {
		t.Fatalf("ciphertext = %x", got)
	}
}

func TestMempoolProviderRejectsMalformedEncryptedPayloadHex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"kind":"placeholder","encrypted_payload_hex":"not-hex","gas":21000,"effective_fee_per_gas_wei":"10"}]}`))
	}))
	defer server.Close()

	node := &Node{
		cfg:       ConfigFile{Provider: ProviderConfig{Mode: "mempool-http", MempoolURL: server.URL}},
		self:      NodeConfig{ID: 2},
		slotState: &slotState{id: 9},
	}
	_, err := node.fetchMempoolInclusionList()
	if err == nil || !strings.Contains(err.Error(), "decode mempool item") {
		t.Fatalf("error = %v, want malformed payload rejection", err)
	}
}

func TestMempoolProviderRejectsProposalBeyondBMax(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"kind":"placeholder","encrypted_payload_hex":"01"},{"kind":"placeholder","encrypted_payload_hex":"02"}]}`))
	}))
	defer server.Close()

	node := &Node{
		cfg: ConfigFile{
			BMax:     1,
			Provider: ProviderConfig{Mode: "mempool-http", MempoolURL: server.URL},
			Limits:   defaultResourceLimits(),
		},
		self:      NodeConfig{ID: 2},
		slotState: &slotState{id: 9},
	}
	_, err := node.buildInclusionList()
	if err == nil || !strings.Contains(err.Error(), "BMax") {
		t.Fatalf("error = %v, want provider BMax rejection", err)
	}
}

func TestValidateTxSource(t *testing.T) {
	if err := validateTxSource("synthetic", ""); err != nil {
		t.Fatalf("synthetic rejected: %v", err)
	}
	if err := validateTxSource("mock-placeholder", "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("mock-placeholder rejected: %v", err)
	}
	if err := validateTxSource("mock-placeholder", ""); err == nil {
		t.Fatalf("mock-placeholder without mempool url accepted")
	}
}
