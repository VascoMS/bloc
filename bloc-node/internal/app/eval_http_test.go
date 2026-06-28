package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEvalHTTPClientReusesDrainedConnections(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	client, transport := newEvalHTTPClient()
	defer transport.CloseIdleConnections()
	for i := 0; i < 20; i++ {
		if err := postJSON(client, server.URL, map[string]int{"iteration": i}); err != nil {
			t.Fatal(err)
		}
	}
	if got := connections.Load(); got > 2 {
		t.Fatalf("opened %d HTTP connections for sequential requests, want at most 2", got)
	}
}
