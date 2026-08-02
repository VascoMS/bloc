package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCampaignIdentityContainsNoDeploymentAddresses(t *testing.T) {
	identity, _, secrets, err := buildCampaignIdentity(campaignIdentityOptions{
		ClusterID: "final-n4",
		N:         4,
		Threshold: 3,
		BMax:      128,
		Blockspace: BlockspaceConfig{
			MaxDecryptedTxs: 128,
			DefaultTxGas:    21000,
		},
		Limits: defaultResourceLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"private_ip", "public_ip", "region", "zone", "controller",
		"http_addr", "p2p_addr", "mempool_url",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("campaign identity contains deployment field %q: %s", forbidden, encoded)
		}
	}
	if identity.Version != campaignIdentityVersion || identity.N != 4 || identity.Threshold != 3 || identity.BMax != 128 {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if len(identity.Operators) != 4 || len(secrets) != 4 {
		t.Fatalf("operator counts = %d/%d, want 4/4", len(identity.Operators), len(secrets))
	}
	seen := map[string]bool{}
	for i, operator := range identity.Operators {
		if operator.ID != uint64(i) || operator.P2PPeerID == "" || seen[operator.P2PPeerID] {
			t.Fatalf("invalid operator identity %d: %+v", i, operator)
		}
		seen[operator.P2PPeerID] = true
	}
}

func TestGenCampaignIdentityWritesPrivateSecretsAndRefusesOverwrite(t *testing.T) {
	root, identityPath, crsPath, secretDir := generateCampaignIdentityFixture(t)
	for path, wantMode := range map[string]os.FileMode{
		identityPath: 0644,
		crsPath:      0644,
		secretDir:    0700,
		filepath.Join(secretDir, "operator-0.json"): 0600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Fatalf("mode %s = %04o, want %04o", path, got, wantMode)
		}
	}
	if err := genCampaignIdentity(campaignIdentityArgs(root)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
}

func TestVerifyCampaignSecretsRejectsWrongP2PIdentity(t *testing.T) {
	_, identityPath, _, secretDir := generateCampaignIdentityFixture(t)
	identity, crs, err := readCampaignIdentity(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(secretDir, "operator-0.json")
	secret, err := readNodeSecrets(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	differentPrivateKey, _, err := generateLibP2PIdentity()
	if err != nil {
		t.Fatal(err)
	}
	secret.P2PPrivateKeyHex = differentPrivateKey
	writeCampaignIdentityJSON(t, secretPath, secret, 0600)
	if err := verifyCampaignSecrets(identity, crs, secretDir); err == nil || !strings.Contains(err.Error(), "peer id") {
		t.Fatalf("wrong peer identity error = %v", err)
	}
}

func TestVerifyCampaignSecretsRejectsWrongShareSet(t *testing.T) {
	_, identityPath, _, secretDir := generateCampaignIdentityFixture(t)
	identity, crs, err := readCampaignIdentity(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	secret0Path := filepath.Join(secretDir, "operator-0.json")
	secret1Path := filepath.Join(secretDir, "operator-1.json")
	secret0, err := readNodeSecrets(secret0Path)
	if err != nil {
		t.Fatal(err)
	}
	secret1, err := readNodeSecrets(secret1Path)
	if err != nil {
		t.Fatal(err)
	}
	secret0.BTEShareScalarHex = secret1.BTEShareScalarHex
	writeCampaignIdentityJSON(t, secret0Path, secret0, 0600)
	if err := verifyCampaignSecrets(identity, crs, secretDir); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Fatalf("wrong share-set error = %v", err)
	}
}

func generateCampaignIdentityFixture(t *testing.T) (root, identityPath, crsPath, secretDir string) {
	t.Helper()
	root = t.TempDir()
	identityPath = filepath.Join(root, "cluster-identity.json")
	crsPath = filepath.Join(root, "cluster.crs")
	secretDir = filepath.Join(root, "secrets")
	if err := genCampaignIdentity(campaignIdentityArgs(root)); err != nil {
		t.Fatalf("generate campaign identity: %v", err)
	}
	return root, identityPath, crsPath, secretDir
}

func campaignIdentityArgs(root string) []string {
	return []string{
		"--cluster-id", "final-n4",
		"--nodes", "4",
		"--threshold", "3",
		"--bmax", "128",
		"--identity-out", filepath.Join(root, "cluster-identity.json"),
		"--crs-out", filepath.Join(root, "cluster.crs"),
		"--secrets-dir", filepath.Join(root, "secrets"),
	}
}

func writeCampaignIdentityJSON(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
