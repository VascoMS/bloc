package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"btd/be"
)

type corpusProvenance struct {
	SchemaVersion           string            `json:"schema_version"`
	CiphertextWireVersion   string            `json:"ciphertext_wire_version"`
	PublicConfigID          string            `json:"public_config_id"`
	PlaintextMasterCorpusID string            `json:"plaintext_master_corpus_id"`
	PlaintextPrefixSetIDs   map[string]string `json:"plaintext_prefix_set_ids"`
	EncryptedCorpusID       string            `json:"encrypted_corpus_id"`
	EncryptedPrefixSetIDs   map[string]string `json:"encrypted_prefix_set_ids"`
	BMax                    int               `json:"bmax"`
	AvailableCount          int               `json:"available_count"`
}

type runCorpusIdentity struct {
	CiphertextWireVersion   string `json:"ciphertext_wire_version"`
	PublicConfigID          string `json:"public_config_id"`
	PlaintextMasterCorpusID string `json:"plaintext_master_corpus_id"`
	PlaintextPrefixID       string `json:"plaintext_prefix_id"`
	EncryptedCorpusID       string `json:"encrypted_corpus_id"`
	EncryptedPrefixID       string `json:"encrypted_prefix_id"`
	RequestedCount          int    `json:"requested_count"`
}

func readCorpusProvenance(path string) (corpusProvenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpusProvenance{}, err
	}
	var provenance corpusProvenance
	if err := json.Unmarshal(data, &provenance); err != nil {
		return corpusProvenance{}, fmt.Errorf("decode encrypted corpus manifest %s: %w", path, err)
	}
	return provenance, nil
}

func validateCorpusProvenance(provenance corpusProvenance, bMax, count int) error {
	if provenance.SchemaVersion != "bloc-encrypted-corpus-v1" {
		return fmt.Errorf("encrypted corpus schema must be bloc-encrypted-corpus-v1")
	}
	if provenance.CiphertextWireVersion != be.LibraryVersion {
		return fmt.Errorf("encrypted corpus wire version must be %s", be.LibraryVersion)
	}
	if provenance.BMax != bMax {
		return fmt.Errorf("encrypted corpus BMax %d does not match evaluator BMax %d", provenance.BMax, bMax)
	}
	if count <= 0 || count > provenance.AvailableCount {
		return fmt.Errorf("requested count %d exceeds encrypted corpus availability %d", count, provenance.AvailableCount)
	}
	if provenance.PublicConfigID == "" || provenance.PlaintextMasterCorpusID == "" || provenance.EncryptedCorpusID == "" {
		return fmt.Errorf("encrypted corpus provenance is missing a required identity")
	}
	key := strconv.Itoa(count)
	if provenance.PlaintextPrefixSetIDs[key] == "" || provenance.EncryptedPrefixSetIDs[key] == "" {
		return fmt.Errorf("encrypted corpus provenance is missing prefix identities for %d", count)
	}
	return nil
}

func corpusIdentityForCount(provenance corpusProvenance, count int) runCorpusIdentity {
	key := strconv.Itoa(count)
	return runCorpusIdentity{
		CiphertextWireVersion:   provenance.CiphertextWireVersion,
		PublicConfigID:          provenance.PublicConfigID,
		PlaintextMasterCorpusID: provenance.PlaintextMasterCorpusID,
		PlaintextPrefixID:       provenance.PlaintextPrefixSetIDs[key],
		EncryptedCorpusID:       provenance.EncryptedCorpusID,
		EncryptedPrefixID:       provenance.EncryptedPrefixSetIDs[key],
		RequestedCount:          count,
	}
}

func parseCorpusManifestPaths(raw string) (map[int]string, error) {
	paths := map[int]string{}
	if strings.TrimSpace(raw) == "" {
		return paths, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("encrypted corpus manifests must use node-count=path entries")
		}
		nodes, err := strconv.Atoi(parts[0])
		if err != nil || nodes <= 0 {
			return nil, fmt.Errorf("invalid encrypted corpus node count %q", parts[0])
		}
		if _, exists := paths[nodes]; exists {
			return nil, fmt.Errorf("duplicate encrypted corpus manifest for n=%d", nodes)
		}
		paths[nodes] = strings.TrimSpace(parts[1])
	}
	return paths, nil
}

func validateTxSource(source, mempoolURL string) error {
	switch source {
	case "", "synthetic":
		return nil
	case "mock-placeholder", "mock-encrypted-corpus":
		if mempoolURL == "" {
			return fmt.Errorf("tx-source=%s requires --mempool-url", source)
		}
		return nil
	default:
		return fmt.Errorf("tx-source must be synthetic, mock-placeholder, or mock-encrypted-corpus")
	}
}

func validateFinalCampaignTxSource(source, mempoolURL string) error {
	if source != "mock-encrypted-corpus" {
		return fmt.Errorf("final campaign requires tx-source=mock-encrypted-corpus")
	}
	return validateTxSource(source, mempoolURL)
}

func usesMempoolSource(source string) bool {
	return source == "mock-placeholder" || source == "mock-encrypted-corpus"
}

func txSourceManifestMeta(source, mempoolURL string) map[string]any {
	if source == "" {
		source = "synthetic"
	}
	meta := map[string]any{
		"source": source,
	}
	if usesMempoolSource(source) {
		meta["mempool_url"] = mempoolURL
	}
	if source == "mock-placeholder" {
		meta["model"] = "development placeholder source"
	}
	if source == "mock-encrypted-corpus" {
		meta["model"] = "immutable cluster-specific encrypted corpus prefixes"
	}
	return meta
}
