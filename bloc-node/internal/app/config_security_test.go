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
	var legacyV2 map[string]any
	if err := json.Unmarshal(publicJSON, &legacyV2); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacyV2["limits"]; !ok {
		t.Fatal("generated public config omitted explicit resource limits")
	}
	delete(legacyV2, "limits")
	legacyV2["provider"] = map[string]any{"mode": "direct"}
	legacyJSON, err := json.Marshal(legacyV2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clusterPath, legacyJSON, 0644); err != nil {
		t.Fatal(err)
	}
	legacyCfg, err := readConfig(clusterPath)
	if err != nil {
		t.Fatalf("read v2 config without limits: %v", err)
	}
	if legacyCfg.Limits != defaultResourceLimits() {
		t.Fatalf("legacy v2 defaults = %+v, want %+v", legacyCfg.Limits, defaultResourceLimits())
	}
	if legacyCfg.Provider.MempoolTimeoutMS != defaultMempoolTimeoutMS {
		t.Fatalf("legacy v2 mempool timeout = %d ms, want %d", legacyCfg.Provider.MempoolTimeoutMS, defaultMempoolTimeoutMS)
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
