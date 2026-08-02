package mempool

import (
	"context"
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
	clusterPath, _, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
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

func TestGenerateEncryptedCorpusFromCampaignIdentity(t *testing.T) {
	_, identityPath, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
	plaintextPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	outputPath := filepath.Join(t.TempDir(), "encrypted-corpus.json")

	manifest, err := GenerateEncryptedCorpus(EncryptedCorpusOptions{
		PlaintextPath:        plaintextPath,
		CampaignIdentityPath: identityPath,
		SecretPaths:          secretPaths,
		Limit:                8,
		OutputPath:           outputPath,
	})
	if err != nil {
		t.Fatalf("generate encrypted corpus from campaign identity: %v", err)
	}
	if manifest.BMax != 8 || manifest.AvailableCount != 8 {
		t.Fatalf("unexpected artifact bounds: %+v", manifest)
	}
}

func TestGenerateEncryptedCorpusRequiresExactlyOnePublicConfiguration(t *testing.T) {
	clusterPath, identityPath, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
	plaintextPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	for _, test := range []struct {
		name         string
		clusterPath  string
		identityPath string
	}{
		{name: "neither"},
		{name: "both", clusterPath: clusterPath, identityPath: identityPath},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := GenerateEncryptedCorpus(EncryptedCorpusOptions{
				PlaintextPath:        plaintextPath,
				ClusterConfigPath:    test.clusterPath,
				CampaignIdentityPath: test.identityPath,
				SecretPaths:          secretPaths,
				Limit:                8,
				OutputPath:           filepath.Join(t.TempDir(), "encrypted-corpus.json"),
			})
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("configuration error = %v", err)
			}
		})
	}
}

func TestGenerateEncryptedCorpusRejectsInvalidCampaignIdentity(t *testing.T) {
	_, identityPath, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
	plaintextPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	var identity map[string]any
	if err := json.Unmarshal(data, &identity); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "version", mutate: func(value map[string]any) { value["version"] = "bloc-campaign-identity-v0" }, want: "version"},
		{name: "threshold", mutate: func(value map[string]any) { value["threshold"] = float64(5) }, want: "threshold"},
		{name: "crs hash", mutate: func(value map[string]any) { value["crs_sha256"] = strings.Repeat("0", 64) }, want: "hash mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copyValue := make(map[string]any, len(identity))
			for key, value := range identity {
				copyValue[key] = value
			}
			test.mutate(copyValue)
			mutated, err := json.Marshal(copyValue)
			if err != nil {
				t.Fatal(err)
			}
			mutatedPath := filepath.Join(filepath.Dir(identityPath), strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(mutatedPath, mutated, 0644); err != nil {
				t.Fatal(err)
			}
			_, err = GenerateEncryptedCorpus(EncryptedCorpusOptions{
				PlaintextPath:        plaintextPath,
				CampaignIdentityPath: mutatedPath,
				SecretPaths:          secretPaths,
				Limit:                8,
				OutputPath:           filepath.Join(t.TempDir(), "encrypted-corpus.json"),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("identity error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadEncryptedCorpusRejectsMutation(t *testing.T) {
	clusterPath, _, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
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

func TestEncryptedCorpusSourceReturnsImmutablePrefixesAcrossSlots(t *testing.T) {
	clusterPath, _, secretPaths := writeEncryptedCorpusTestCluster(t, 8, 4, 3)
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
	source, err := NewEncryptedCorpusSource(outputPath)
	if err != nil {
		t.Fatalf("load source: %v", err)
	}

	first, err := source.FetchSlot(context.Background(), 11, 8)
	if err != nil {
		t.Fatalf("fetch first slot: %v", err)
	}
	second, err := source.FetchSlot(context.Background(), 12, 8)
	if err != nil {
		t.Fatalf("fetch second slot: %v", err)
	}
	if first.Slot != 11 || second.Slot != 12 || first.EncryptedPrefixSetID != second.EncryptedPrefixSetID {
		t.Fatalf("unexpected slot correlation: first=%+v second=%+v", first, second)
	}
	for i := range first.Transactions {
		if first.Transactions[i].EncryptedPayloadHex != second.Transactions[i].EncryptedPayloadHex {
			t.Fatalf("slot changed ciphertext %d", i)
		}
	}
	first.Transactions[0].EncryptedPayloadHex = "mutated"
	third, err := source.FetchSlot(context.Background(), 13, 8)
	if err != nil {
		t.Fatalf("fetch third slot: %v", err)
	}
	if third.Transactions[0].EncryptedPayloadHex == "mutated" {
		t.Fatal("caller mutation changed static source")
	}
	if _, err := source.FetchSlot(context.Background(), 13, 0); err == nil {
		t.Fatal("zero limit accepted")
	}
	if _, err := source.FetchSlot(context.Background(), 13, 9); err == nil {
		t.Fatal("limit above BMax accepted")
	}
}

func writeEncryptedCorpusTestCluster(t *testing.T, bmax, n, threshold int) (string, string, []string) {
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
	operators := make([]map[string]any, n)
	for i := range operators {
		operators[i] = map[string]any{"id": i, "p2p_peer_id": "fixture-peer-" + string(rune('0'+i))}
	}
	identity := map[string]any{
		"version": "bloc-campaign-identity-v1", "cluster_id": "encrypted-corpus-test",
		"bmax": bmax, "n": n, "threshold": threshold,
		"crs_file": "cluster.crs", "crs_sha256": hashHex(crs),
		"public_key_hex": hex.EncodeToString(pkBytes),
		"blockspace":     map[string]any{"max_decrypted_txs": bmax, "default_tx_gas": 21000},
		"limits": map[string]any{
			"max_proposal_bytes":                 8388608,
			"max_envelope_bytes":                 16777216,
			"max_combine_attempts_per_sub_batch": 256,
		},
		"operators": operators,
	}
	identityBytes, _ := json.Marshal(identity)
	identityPath := filepath.Join(dir, "campaign-identity.json")
	if err := os.WriteFile(identityPath, identityBytes, 0644); err != nil {
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
	return clusterPath, identityPath, secretPaths
}
