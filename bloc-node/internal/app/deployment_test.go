package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeNodeConfigPreservesLegacyAddresses(t *testing.T) {
	cfg := ConfigFile{Nodes: []NodeConfig{{ID: 0, HTTPAddr: "127.0.0.1:8000", P2PAddr: "/ip4/127.0.0.1/tcp/9000"}}}
	normalizeConfig(&cfg)
	node := cfg.Nodes[0]
	if node.HTTPListenAddr != "127.0.0.1:8000" || node.HTTPAdvertiseURL != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected HTTP defaults: %+v", node)
	}
	if node.P2PListenAddr != "/ip4/127.0.0.1/tcp/9000" || node.P2PAdvertiseAddr != "/ip4/127.0.0.1/tcp/9000" {
		t.Fatalf("unexpected p2p defaults: %+v", node)
	}
}

func TestAddressTemplatesSupportContainerAndKubernetesModes(t *testing.T) {
	container, err := resolveAddressTemplates("container", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := renderAddressTemplate(container.httpListen, 2, 8080, 9000); got != "0.0.0.0:8080" {
		t.Fatalf("container http listen = %q", got)
	}
	if got := renderAddressTemplate(container.p2pAdvertise, 2, 8080, 9000); got != "/dns4/bloc-node-2/tcp/9000" {
		t.Fatalf("container p2p advertise = %q", got)
	}
	k8s, err := resolveAddressTemplates("kubernetes", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := renderAddressTemplate(k8s.p2pAdvertise, 1, 8001, 9001); !strings.Contains(got, "bloc-node-1.bloc-node-headless.bloc.svc.cluster.local") {
		t.Fatalf("kubernetes p2p advertise = %q", got)
	}
}

func TestPrometheusMetricsExposeSlotStateAndCounters(t *testing.T) {
	node := &Node{
		cfg:           ConfigFile{ClusterID: "cluster"},
		self:          NodeConfig{ID: 2, HTTPAddr: "127.0.0.1:8002", P2PPeerID: "peer"},
		observability: newNodeMetrics("cluster", 2),
		slotState: &slotState{
			id:     9,
			phase:  slotCompleted,
			result: &Result{},
			metrics: Metrics{
				SelectedCiphertexts: 3,
				SelectedGas:         63000,
				TotalSlotUS:         1200,
				OutboundMessages:    map[string]int{"acs": 7},
				InboundMessages:     map[string]int{"share": 5},
				OutboundBytes:       map[string]int64{"acs": 100},
				InboundBytes:        map[string]int64{"share": 80},
			},
		},
		completedSlots: 1,
	}
	node.observability.setCurrentSlot(9)
	node.observability.slotCompleted(node.metrics.snapshot())
	body := scrapeNodeMetrics(t, node)
	for _, want := range []string{
		"bloc_node_info",
		`bloc_slot_phase{cluster_id="cluster",node_id="2",phase="completed"} 1`,
		`bloc_slot_completed_total{cluster_id="cluster",node_id="2"} 1`,
		`bloc_slot_result_available{cluster_id="cluster",node_id="2"} 1`,
		`bloc_slot_selected_transactions{cluster_id="cluster",node_id="2"} 3`,
		`bloc_slot_stage_duration_seconds_bucket`,
		`bloc_slot_stage_duration_seconds_sum`,
		`bloc_slot_stage_duration_seconds_count`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestPrometheusMetricsUseBoundedSchemaAndBaseUnits(t *testing.T) {
	node := &Node{cfg: ConfigFile{ClusterID: "cluster"}, self: NodeConfig{ID: 1}, observability: newNodeMetrics("cluster", 1)}
	node.observability.slotStarted()
	node.observability.recordProtocol("outbound", "acs", 128)
	node.observability.recordProtocol("inbound", "share", 64)
	node.observability.slotFailed("arbitrary dynamic error")
	node.observability.slotCompleted(Metrics{TotalSlotUS: 2000, ACSUS: 1000, SelectedCiphertexts: 8, SelectedGas: 168000})
	body := scrapeNodeMetrics(t, node)
	for _, want := range []string{
		"bloc_slot_stage_duration_seconds",
		"bloc_protocol_message_bytes_total",
		"bloc_protocol_messages_total",
		`reason="unknown"`,
		`kind="acs"`,
		`kind="share"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{
		"bloc_slot_latency_us",
		"bloc_slot_latency_ms",
		"batch_id",
		"tx_hash",
		"peer_id",
		"http_advertise_url",
		"arbitrary dynamic error",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics body contains forbidden token %q:\n%s", forbidden, body)
		}
	}
}

func TestHTTPInstrumentationUsesNormalizedLabels(t *testing.T) {
	node := &Node{cfg: ConfigFile{ClusterID: "cluster"}, self: NodeConfig{ID: 3}, observability: newNodeMetrics("cluster", 3)}
	handler := node.instrumentHTTP("slot_status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/slot/status?slot=123", nil))
	body := scrapeNodeMetrics(t, node)
	for _, want := range []string{
		`bloc_http_requests_total{cluster_id="cluster",code="202",handler="slot_status",method="GET",node_id="3"} 1`,
		`bloc_http_request_duration_seconds_bucket`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "slot=123") || strings.Contains(body, "/slot/status") {
		t.Fatalf("HTTP metrics leaked raw path/query:\n%s", body)
	}
}

func scrapeNodeMetrics(t *testing.T, node *Node) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	node.handleMetrics(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	resp := recorder.Result()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	contentType := resp.Header.Get("content-type")
	if !strings.Contains(contentType, "text/plain") {
		t.Fatalf("content-type = %q, want prometheus text/plain", contentType)
	}
	return string(body)
}

func TestReadRemoteEvalConfigSupportsEndpointShortcut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	if err := os.WriteFile(path, []byte(`{"endpoints":["http://node-b/","http://node-a"],"threshold":3,"bmax":16}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := readRemoteEvalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 2 || cfg.Nodes[0].URL != "http://node-b" || cfg.Nodes[1].URL != "http://node-a" {
		t.Fatalf("unexpected nodes: %+v", cfg.Nodes)
	}
	if cfg.Threshold != 3 || cfg.BMax != 16 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestWaitForRemoteHTTPReportsMissingNodes(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}))
	defer ready.Close()
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "starting"})
	}))
	defer missing.Close()
	err := waitForRemoteHTTP(http.DefaultClient, []remoteEvalNode{{ID: 0, URL: ready.URL}, {ID: 1, URL: missing.URL}}, time.Nanosecond)
	if err == nil || !strings.Contains(err.Error(), "missing nodes: [1]") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoteManifestRecordsDeploymentFields(t *testing.T) {
	manifest := suiteManifest{
		ExperimentID:    "distributed-smoke",
		ExecutionMode:   "remote",
		Deployment:      map[string]string{"environment": "compose"},
		RemoteEndpoints: []remoteEvalNode{{ID: 0, URL: "http://node-0:8000", Region: "local"}},
		ImageTag:        "bloc:test",
		GitCommit:       "abc123",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{`"experiment_id":"distributed-smoke"`, `"execution_mode":"remote"`, `"remote_endpoints"`, `"image_tag":"bloc:test"`, `"git_commit":"abc123"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("manifest missing %s: %s", want, body)
		}
	}
}
