package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"btd/be"
)

func bindEncryptedCorpus(args []string) error {
	fs := flag.NewFlagSet("bind-encrypted-corpus", flag.ContinueOnError)
	configPath := fs.String("config", "", "existing bloc-cluster-v3 config")
	corpusPath := fs.String("corpus", "", "verified bloc-encrypted-corpus-v1 artifact")
	mempoolURL := fs.String("mempool-url", "", "static mempool-il base URL")
	remoteEvalPath := fs.String("remote-eval", "", "optional remote evaluator config to bind to the same corpus")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*configPath) == "" || strings.TrimSpace(*corpusPath) == "" || strings.TrimSpace(*mempoolURL) == "" {
		return fmt.Errorf("--config, --corpus, and --mempool-url are required")
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	provenance, err := readCorpusProvenance(*corpusPath)
	if err != nil {
		return err
	}
	if err := validateCorpusProvenance(provenance, cfg.BMax, provenance.AvailableCount); err != nil {
		return err
	}
	suite := newSuite()
	pk, err := unmarshalPointHex(suite, cfg.PublicKeyHex)
	if err != nil {
		return err
	}
	publicID, err := be.PublicConfigID(cfg.BMax, cfg.CRSSHA256, pk)
	if err != nil {
		return err
	}
	if err := bindCorpusProvider(&cfg, provenance, publicID, *mempoolURL); err != nil {
		return err
	}
	var remote *remoteEvalConfig
	if *remoteEvalPath != "" {
		data, err := os.ReadFile(*remoteEvalPath)
		if err != nil {
			return err
		}
		var parsed remoteEvalConfig
		if err := json.Unmarshal(data, &parsed); err != nil {
			return err
		}
		if parsed.BMax != cfg.BMax || parsed.NodeCount != cfg.N || parsed.Threshold != cfg.Threshold {
			return fmt.Errorf("remote evaluator config does not match cluster n/t/BMax")
		}
		parsed.Corpus = provenance
		remote = &parsed
	}
	if err := writeJSONFileAtomic(*configPath, cfg, 0644); err != nil {
		return err
	}
	if remote != nil {
		if err := writeJSONFileAtomic(*remoteEvalPath, remote, 0644); err != nil {
			return err
		}
	}
	return nil
}

func bindCorpusProvider(cfg *ConfigFile, provenance corpusProvenance, computedPublicID, mempoolURL string) error {
	if provenance.PublicConfigID != computedPublicID {
		return fmt.Errorf("encrypted corpus public config id %q does not match loaded setup %q", provenance.PublicConfigID, computedPublicID)
	}
	cfg.Provider = ProviderConfig{
		Mode:                          "mempool-http",
		MempoolURL:                    mempoolURL,
		MempoolTimeoutMS:              defaultMempoolTimeoutMS,
		ExpectedPublicConfigID:        provenance.PublicConfigID,
		ExpectedPlaintextMasterID:     provenance.PlaintextMasterCorpusID,
		ExpectedEncryptedCorpusID:     provenance.EncryptedCorpusID,
		ExpectedEncryptedPrefixSetIDs: cloneStringMap(provenance.EncryptedPrefixSetIDs),
		RequireExactCount:             true,
	}
	return validateProviderConfig(cfg.Provider)
}

func cloneStringMap(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
