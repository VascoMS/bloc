package mempool

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"btd/be"
	"btd/curves"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
)

func TestGenerateEncryptedCorpusPublishesVerifiedArtifactAtomically(t *testing.T) {
	clusterPath, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
	plaintextPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	outputPath := filepath.Join(t.TempDir(), "encrypted-corpus.json")

	manifest, err := GenerateEncryptedCorpus(EncryptedCorpusOptions{
		PlaintextPath:     plaintextPath,
		ClusterConfigPath: clusterPath,
		SecretPaths:       secretPaths,
		Limit:             8,
		OutputPath:        outputPath,
	})
	if err != nil {
		t.Fatalf("generate encrypted corpus: %v", err)
	}
	if manifest.SchemaVersion != "bloc-encrypted-corpus-v1" || manifest.CiphertextWireVersion != "bte-tx-v2" {
		t.Fatalf("unexpected versions: %+v", manifest)
	}
	if manifest.BMax != 8 || manifest.AvailableCount != 8 || manifest.IndexAssignment != "coordinated-position-v1" {
		t.Fatalf("unexpected artifact bounds: %+v", manifest)
	}
	if manifest.PublicConfigID == "" || manifest.PlaintextMasterCorpusID == "" || manifest.EncryptedCorpusID == "" {
		t.Fatalf("missing artifact identities: %+v", manifest)
	}
	for position, candidate := range manifest.Candidates {
		if candidate.Position != position || candidate.Index != position {
			t.Fatalf("candidate %d position/index = %d/%d", position, candidate.Position, candidate.Index)
		}
		encoded, decodeErr := hex.DecodeString(strings.TrimPrefix(candidate.CiphertextHex, "0x"))
		if decodeErr != nil {
			t.Fatalf("decode candidate %d: %v", position, decodeErr)
		}
		if !strings.HasPrefix(string(encoded), "bte-tx-v2") {
			t.Fatalf("candidate %d is not ciphertext v2", position)
		}
	}

	loaded, err := LoadEncryptedCorpus(outputPath)
	if err != nil {
		t.Fatalf("load encrypted corpus: %v", err)
	}
	if loaded.EncryptedCorpusID != manifest.EncryptedCorpusID {
		t.Fatalf("loaded corpus id = %s, want %s", loaded.EncryptedCorpusID, manifest.EncryptedCorpusID)
	}
	if _, err := GenerateEncryptedCorpus(EncryptedCorpusOptions{
		PlaintextPath:     plaintextPath,
		ClusterConfigPath: clusterPath,
		SecretPaths:       secretPaths,
		Limit:             8,
		OutputPath:        outputPath,
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second generation error = %v", err)
	}
}

func TestLoadEncryptedCorpusRejectsMutation(t *testing.T) {
	clusterPath, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
	plaintextPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	outputPath := filepath.Join(t.TempDir(), "encrypted-corpus.json")
	if _, err := GenerateEncryptedCorpus(EncryptedCorpusOptions{
		PlaintextPath:     plaintextPath,
		ClusterConfigPath: clusterPath,
		SecretPaths:       secretPaths,
		Limit:             8,
		OutputPath:        outputPath,
	}); err != nil {
		t.Fatalf("generate encrypted corpus: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest EncryptedCorpusManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Candidates[0].CiphertextHex = strings.Repeat("00", 32)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mutatedPath := filepath.Join(t.TempDir(), "mutated.json")
	if err := os.WriteFile(mutatedPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEncryptedCorpus(mutatedPath); err == nil {
		t.Fatal("mutated encrypted corpus was accepted")
	}
}

func writeEncryptedCorpusTestCluster(t *testing.T, bmax, n, threshold int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	crs, err := be.GeneratePublicCRS(suite, bmax)
	if err != nil {
		t.Fatalf("generate CRS: %v", err)
	}
	btd, err := be.NewBTDFromPublicCRS(suite, bmax, crs)
	if err != nil {
		t.Fatalf("load CRS: %v", err)
	}
	shares, pk := btd.KeyGen(n, threshold)
	pkBytes, err := pk.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster.crs"), crs, 0644); err != nil {
		t.Fatal(err)
	}
	cluster := map[string]any{
		"version": "bloc-cluster-v3", "cluster_id": "encrypted-corpus-test",
		"bmax": bmax, "n": n, "threshold": threshold,
		"crs_file": "cluster.crs", "crs_sha256": hashHex(crs),
		"public_key_hex": hex.EncodeToString(pkBytes),
	}
	clusterBytes, _ := json.Marshal(cluster)
	clusterPath := filepath.Join(dir, "cluster.json")
	if err := os.WriteFile(clusterPath, clusterBytes, 0644); err != nil {
		t.Fatal(err)
	}
	secretPaths := make([]string, len(shares))
	for i, secret := range shares {
		scalar, marshalErr := secret.V.MarshalBinary()
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		document := map[string]any{
			"version": "bloc-node-secret-v1", "cluster_id": "encrypted-corpus-test",
			"operator_id": secret.I, "bte_share_scalar_hex": hex.EncodeToString(scalar),
		}
		secretBytes, _ := json.Marshal(document)
		secretPaths[i] = filepath.Join(dir, "operator-"+string(rune('0'+i))+".json")
		if err := os.WriteFile(secretPaths[i], secretBytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return clusterPath, secretPaths
}
