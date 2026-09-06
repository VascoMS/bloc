package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenConfigSeparatesPublicAndOperatorSecrets(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{
		"--nodes", "4",
		"--threshold", "3",
		"--bmax", "4",
		"--out", clusterPath,
	}); err != nil {
		t.Fatalf("gen config: %v", err)
	}

	publicJSON, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"crs_seed_hex", "bte_share_scalar_hex", "p2p_private_key_hex", `"shares"`} {
		if strings.Contains(string(publicJSON), forbidden) {
			t.Fatalf("public cluster config contains secret field %q", forbidden)
		}
	}
	cfg, err := readConfig(clusterPath)
	if err != nil {
		t.Fatalf("read public config: %v", err)
	}
	if len(cfg.CRSBytes) == 0 || cfg.CRSFile != "cluster.crs" {
		t.Fatalf("public CRS was not loaded: file=%q bytes=%d", cfg.CRSFile, len(cfg.CRSBytes))
	}
	if cfg.Limits != defaultResourceLimits() {
		t.Fatalf("generated resource limits = %+v, want %+v", cfg.Limits, defaultResourceLimits())
	}
	if cfg.Provider.MempoolTimeoutMS != defaultMempoolTimeoutMS {
		t.Fatalf("generated mempool timeout = %d ms, want %d", cfg.Provider.MempoolTimeoutMS, defaultMempoolTimeoutMS)
	}
	var configWithoutLimits map[string]any
	if err := json.Unmarshal(publicJSON, &configWithoutLimits); err != nil {
		t.Fatal(err)
	}
	if _, ok := configWithoutLimits["limits"]; !ok {
		t.Fatal("generated public config omitted explicit resource limits")
	}
	delete(configWithoutLimits, "limits")
	configWithoutLimits["provider"] = map[string]any{"mode": "direct"}
	configJSON, err := json.Marshal(configWithoutLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clusterPath, configJSON, 0644); err != nil {
		t.Fatal(err)
	}
	defaultedCfg, err := readConfig(clusterPath)
	if err != nil {
		t.Fatalf("read v3 config without limits: %v", err)
	}
	if defaultedCfg.Limits != defaultResourceLimits() {
		t.Fatalf("v3 defaults = %+v, want %+v", defaultedCfg.Limits, defaultResourceLimits())
	}
	if defaultedCfg.Provider.MempoolTimeoutMS != defaultMempoolTimeoutMS {
		t.Fatalf("v3 mempool timeout = %d ms, want %d", defaultedCfg.Provider.MempoolTimeoutMS, defaultMempoolTimeoutMS)
	}

	secretPath := filepath.Join(dir, "secrets", "operator-2.json")
	secret, err := readNodeSecrets(secretPath)
	if err != nil {
		t.Fatalf("read operator secret: %v", err)
	}
	if secret.OperatorID != 2 || secret.ClusterID != cfg.ClusterID {
		t.Fatalf("unexpected operator secret: %+v", secret)
	}
	node, err := newNode(cfg, secret, 2, FaultConfig{})
	if err != nil {
		t.Fatalf("construct node from split config: %v", err)
	}
	if node.mempoolClient == nil || node.mempoolClient.Timeout != 2*time.Second {
		t.Fatalf("node mempool client timeout = %v, want 2s", node.mempoolClient)
	}

	secret.OperatorID = 1
	if _, err := newNode(cfg, secret, 2, FaultConfig{}); err == nil || !strings.Contains(err.Error(), "does not match requested operator") {
		t.Fatalf("wrong operator secret error = %v", err)
	}
	foreignSecret, err := readNodeSecrets(filepath.Join(dir, "secrets", "operator-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	foreignSecret.OperatorID = 2
	if _, err := newNode(cfg, foreignSecret, 2, FaultConfig{}); err == nil || !strings.Contains(err.Error(), "derives peer id") {
		t.Fatalf("wrong libp2p identity error = %v", err)
	}

	crsPath := filepath.Join(dir, "cluster.crs")
	crs, err := os.ReadFile(crsPath)
	if err != nil {
		t.Fatal(err)
	}
	crs[len(crs)-1] ^= 1
	if err := os.WriteFile(crsPath, crs, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(clusterPath); err == nil || !strings.Contains(err.Error(), "public CRS hash mismatch") {
		t.Fatalf("tampered CRS error = %v", err)
	}
}

func TestReadConfigRejectsV2CiphertextContract(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{
		"--nodes", "4",
		"--threshold", "3",
		"--bmax", "8",
		"--out", clusterPath,
	}); err != nil {
		t.Fatalf("gen config: %v", err)
	}

	data, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["version"] = "bloc-cluster-v2"
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clusterPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = readConfig(clusterPath)
	if err == nil || !strings.Contains(err.Error(), `unsupported cluster config version "bloc-cluster-v2"`) {
		t.Fatalf("v2 config error = %v", err)
	}
}

func TestConfigAcceptsOptionalACSTraceDiagnostics(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{
		"--nodes", "4",
		"--threshold", "3",
		"--bmax", "8",
		"--out", clusterPath,
	}); err != nil {
		t.Fatalf("gen config: %v", err)
	}

	legacy, err := readConfig(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Diagnostics.ACSTrace {
		t.Fatal("omitted diagnostics enabled ACS tracing")
	}
	raw, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["diagnostics"] = map[string]any{"acs_trace": true}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clusterPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Diagnostics.ACSTrace {
		t.Fatal("ACS trace diagnostics were not enabled")
	}
}

func TestGenConfigEnablesACSTraceOnlyWhenRequested(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{
		"--nodes", "4", "--threshold", "3", "--bmax", "8",
		"--acs-trace", "--out", clusterPath,
	}); err != nil {
		t.Fatalf("gen config: %v", err)
	}
	cfg, err := readConfig(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Diagnostics.ACSTrace {
		t.Fatal("generated config did not enable ACS tracing")
	}
}

func TestGenConfigRetainsRequestedStreamMode(t *testing.T) {
	for _, mode := range []string{streamModePersistent, streamModePersistentLanes} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			clusterPath := filepath.Join(dir, "cluster.json")
			if err := genConfig([]string{
				"--nodes", "4", "--threshold", "3", "--bmax", "8",
				"--stream-mode", mode, "--out", clusterPath,
			}); err != nil {
				t.Fatalf("gen config: %v", err)
			}
			cfg, err := readConfig(clusterPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Network.StreamMode != mode {
				t.Fatalf("generated stream mode = %q, want %q", cfg.Network.StreamMode, mode)
			}
		})
	}
}

func TestReadConfigRejectsUnknownStreamMode(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := genConfig([]string{
		"--nodes", "4", "--threshold", "3", "--bmax", "8",
		"--out", clusterPath,
	}); err != nil {
		t.Fatalf("gen config: %v", err)
	}
	raw, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["network"] = map[string]any{"mode": "libp2p", "stream_mode": "reuse"}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clusterPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(clusterPath); err == nil || !strings.Contains(err.Error(), "network.stream_mode") {
		t.Fatalf("unknown stream mode error = %v", err)
	}
}

func TestGenConfigRejectsNegativeMempoolTimeout(t *testing.T) {
	err := genConfig([]string{
		"--nodes", "4",
		"--mempool-timeout-ms", "-1",
		"--out", filepath.Join(t.TempDir(), "cluster.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "provider.mempool_timeout_ms") {
		t.Fatalf("negative mempool timeout error = %v, want field-specific rejection", err)
	}
}

func TestReadConfigRejectsLegacyCombinedSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.json")
	if err := os.WriteFile(path, []byte(`{"crs_seed_hex":"seed","shares":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(path); err == nil || !strings.Contains(err.Error(), "legacy combined cluster config") {
		t.Fatalf("legacy config error = %v", err)
	}
}

func TestValidateAuthenticatedEnvelopeRejectsSpoofing(t *testing.T) {
	valid := WireEnvelope{From: 1, To: 2, Direct: true, Share: &WireShare{OperatorID: 1}}
	if err := validateAuthenticatedEnvelope(1, 2, valid); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	tests := map[string]WireEnvelope{
		"spoofed from":   {From: 3, To: 2, Direct: true},
		"wrong receiver": {From: 1, To: 3, Direct: true},
		"not direct":     {From: 1, To: 2, Direct: false},
		"spoofed share":  {From: 1, To: 2, Direct: true, Share: &WireShare{OperatorID: 3}},
	}
	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateAuthenticatedEnvelope(1, 2, envelope); err == nil {
				t.Fatal("expected authenticated envelope validation to fail")
			}
		})
	}
}

func TestAuthenticatedOutboundEnvelopeOverwritesCallerClaims(t *testing.T) {
	envelope := authenticatedOutboundEnvelope(1, 2, WireEnvelope{From: 99, To: 98, Direct: false})
	if envelope.From != 1 || envelope.To != 2 || !envelope.Direct {
		t.Fatalf("outbound envelope retained caller routing: %+v", envelope)
	}
}
