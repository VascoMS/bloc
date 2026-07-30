package app

import (
	"strings"
	"testing"
)

func TestBindCorpusProviderRequiresLoadedPublicIdentity(t *testing.T) {
	cfg := ConfigFile{}
	provenance := corpusProvenance{
		PublicConfigID:          "public",
		PlaintextMasterCorpusID: "plain",
		EncryptedCorpusID:       "encrypted",
		EncryptedPrefixSetIDs:   map[string]string{"8": "prefix"},
	}
	if err := bindCorpusProvider(&cfg, provenance, "other", "http://mempool:8080"); err == nil || !strings.Contains(err.Error(), "loaded setup") {
		t.Fatalf("public mismatch error = %v", err)
	}
	if err := bindCorpusProvider(&cfg, provenance, "public", "http://mempool:8080"); err != nil {
		t.Fatalf("bind provider: %v", err)
	}
	if !cfg.Provider.RequireExactCount || cfg.Provider.ExpectedEncryptedPrefixSetIDs["8"] != "prefix" {
		t.Fatalf("provider not bound exactly: %+v", cfg.Provider)
	}
	provenance.EncryptedPrefixSetIDs["8"] = "mutated"
	if cfg.Provider.ExpectedEncryptedPrefixSetIDs["8"] != "prefix" {
		t.Fatal("provider retained mutable manifest map")
	}
}
