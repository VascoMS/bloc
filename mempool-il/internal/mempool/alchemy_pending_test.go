package mempool

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type methodRouterRoundTripper struct {
	mu        sync.Mutex
	responses map[string][]string
}

func (m *methodRouterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	method := extractMethod(body)
	m.mu.Lock()
	defer m.mu.Unlock()

	queue := m.responses[method]
	if len(queue) == 0 {
		queue = []string{`{"jsonrpc":"2.0","id":1,"result":null}`}
	}
	respBody := queue[0]
	if len(queue) > 1 {
		m.responses[method] = queue[1:]
	}

	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(respBody)),
	}, nil
}

func extractMethod(body []byte) string {
	asText := string(body)
	idx := strings.Index(asText, `"method":"`)
	if idx == -1 {
		return ""
	}
	start := idx + len(`"method":"`)
	end := strings.Index(asText[start:], `"`)
	if end == -1 {
		return ""
	}
	return asText[start : start+end]
}

func TestAlchemyPendingFetchReplacementAndTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rt := &methodRouterRoundTripper{responses: map[string][]string{
		"eth_newPendingTransactionFilter": {
			`{"jsonrpc":"2.0","id":1,"result":"0xabc"}`,
		},
		"eth_getFilterChanges": {
			`{"jsonrpc":"2.0","id":1,"result":["0x1","0x2"]}`,
			`{"jsonrpc":"2.0","id":1,"result":[]}`,
			`{"jsonrpc":"2.0","id":1,"result":[]}`,
		},
		"eth_getTransactionByHash": {
			`{"jsonrpc":"2.0","id":1,"result":{"hash":"0x1","from":"0xaa","to":"0xbb","nonce":"0x1","gas":"0x5208","input":"0x","gasPrice":"0x64"}}`,
			`{"jsonrpc":"2.0","id":1,"result":{"hash":"0x2","from":"0xaa","to":"0xbb","nonce":"0x1","gas":"0x5208","input":"0x","gasPrice":"0xc8"}}`,
		},
	}}

	client := NewAlchemyPendingClient("http://example", &http.Client{Transport: rt}, 2*time.Minute)
	client.now = func() time.Time { return now }

	first, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected fetch error: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 tx after replacement, got %d", len(first))
	}
	if first[0].Hash != "0x2" {
		t.Fatalf("expected higher fee replacement to remain, got %s", first[0].Hash)
	}

	now = now.Add(1 * time.Minute)
	second, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected second fetch error: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected tx retained before ttl expiry")
	}

	now = now.Add(3 * time.Minute)
	third, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected third fetch error: %v", err)
	}
	if len(third) != 0 {
		t.Fatalf("expected tx expired by ttl")
	}
}
