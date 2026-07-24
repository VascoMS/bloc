package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newMempoolProviderTestNode(serverURL string, timeout time.Duration) *Node {
	return &Node{
		cfg: ConfigFile{Provider: ProviderConfig{
			Mode:             "mempool-http",
			MempoolURL:       serverURL,
			MempoolTimeoutMS: timeout.Milliseconds(),
		}},
		self:          NodeConfig{ID: 2},
		mempoolClient: &http.Client{Timeout: timeout},
		slotState:     &slotState{id: 9},
	}
}

func TestMempoolProviderConsumesEncryptedPayloadHex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"hash":"0xplaceholder","kind":"placeholder","encrypted_payload_hex":"0x010203","placeholder_envelope_hex":"0x70686c64","gas":21000,"effective_fee_per_gas_wei":"10","from":"0xabc","nonce":1}]}`))
	}))
	defer server.Close()

	node := newMempoolProviderTestNode(server.URL, time.Second)
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

	node := newMempoolProviderTestNode(server.URL, time.Second)
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

	node := newMempoolProviderTestNode(server.URL, time.Second)
	node.cfg.BMax = 1
	node.cfg.Limits = defaultResourceLimits()
	_, err := node.buildInclusionList()
	if err == nil || !strings.Contains(err.Error(), "BMax") {
		t.Fatalf("error = %v, want provider BMax rejection", err)
	}
}

func TestMempoolProviderReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	node := newMempoolProviderTestNode(server.URL, time.Second)
	_, err := node.fetchMempoolInclusionList()
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("error = %v, want compatible HTTP status and body", err)
	}
}

func TestMempoolProviderHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	node := newMempoolProviderTestNode(server.URL, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.fetchMempoolInclusionListContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestMempoolProviderBlockingRequestTimesOut(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	const timeout = 25 * time.Millisecond
	node := newMempoolProviderTestNode(server.URL, timeout)
	before := time.Now()
	_, err := node.fetchMempoolInclusionList()
	elapsed := time.Since(before)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("request returned after %s, want bounded near %s", elapsed, timeout)
	}
	select {
	case <-started:
	default:
		t.Fatal("provider request never reached the blocking upstream")
	}
}

func TestProviderConfigDefaultsAndRejectsNegativeTimeout(t *testing.T) {
	cfg := ConfigFile{}
	normalizeConfig(&cfg)
	if cfg.Provider.MempoolTimeoutMS != 2000 {
		t.Fatalf("default mempool timeout = %d ms, want 2000", cfg.Provider.MempoolTimeoutMS)
	}
	if err := validateProviderConfig(cfg.Provider); err != nil {
		t.Fatalf("default provider config rejected: %v", err)
	}
	if err := validateProviderConfig(ProviderConfig{MempoolTimeoutMS: -1}); err == nil ||
		!strings.Contains(err.Error(), "mempool_timeout_ms") {
		t.Fatalf("negative timeout error = %v, want field-specific rejection", err)
	}
	if err := validateProviderConfig(ProviderConfig{MempoolTimeoutMS: 1<<63 - 1}); err == nil ||
		!strings.Contains(err.Error(), "at most") {
		t.Fatalf("overflowing timeout error = %v, want representable-duration rejection", err)
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
