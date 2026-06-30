package mempool

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type staticRoundTripper struct {
	status int
	body   string
}

func (s staticRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func TestPublicRPCClientFetch(t *testing.T) {
	body := `{
  "jsonrpc":"2.0",
  "id":1,
  "result":{
    "transactions":[
      {
        "hash":"0xabc",
        "from":"0x111",
        "to":"0x222",
        "nonce":"0x1",
        "gas":"0x5208",
        "input":"0x",
        "gasPrice":"0x64"
      },
      {
        "hash":"0xdef",
        "from":"0x333",
        "to":"0x444",
        "nonce":"0x2",
        "gas":"0x186a0",
        "input":"0x70686c640123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0000000000000000000000000000000000000000000000000000000000000064deadbeef",
        "maxFeePerGas":"0xc8"
      }
    ]
  }
}`
	client := NewPublicRPCClient("http://example", &http.Client{Transport: staticRoundTripper{status: 200, body: body}})

	txs, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected 2 txs, got %d", len(txs))
	}
	if txs[0].Kind != TxKindPlaintext {
		t.Fatalf("expected plaintext first tx")
	}
	if txs[1].Kind != TxKindPlaceholder {
		t.Fatalf("expected placeholder second tx")
	}
	if txs[1].Placeholder == nil || txs[1].Placeholder.RequestedGas != 100 {
		t.Fatalf("expected parsed placeholder gas=100")
	}
}

func TestPublicRPCClientFetchNilPendingBlock(t *testing.T) {
	client := NewPublicRPCClient("http://example", &http.Client{Transport: staticRoundTripper{status: 200, body: `{"jsonrpc":"2.0","id":1,"result":null}`}})

	txs, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("expected empty tx list")
	}
}
