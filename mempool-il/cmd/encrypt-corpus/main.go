package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"mempool-il/internal/mempool"
)

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStringFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("secret path must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	var secretPaths repeatedStringFlag
	plaintextPath := flag.String("plaintext-corpus", "", "canonical 512-entry plaintext corpus")
	clusterPath := flag.String("cluster-config", "", "bloc-cluster-v3 public configuration")
	campaignIdentityPath := flag.String("campaign-identity", "", "network-independent bloc-campaign-identity-v1 public configuration")
	limit := flag.Int("limit", 0, "number of ordered corpus entries to encrypt")
	outputPath := flag.String("out", "", "new encrypted-corpus artifact path")
	flag.Var(&secretPaths, "operator-secret", "operator secret used only for generator self-check (repeat at least threshold times)")
	flag.Parse()

	manifest, err := mempool.GenerateEncryptedCorpus(mempool.EncryptedCorpusOptions{
		PlaintextPath:        *plaintextPath,
		ClusterConfigPath:    *clusterPath,
		CampaignIdentityPath: *campaignIdentityPath,
		SecretPaths:          secretPaths,
		Limit:                *limit,
		OutputPath:           *outputPath,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf(
		"encrypted corpus written: public_config_id=%s plaintext_master_corpus_id=%s encrypted_corpus_id=%s count=%d",
		manifest.PublicConfigID,
		manifest.PlaintextMasterCorpusID,
		manifest.EncryptedCorpusID,
		manifest.AvailableCount,
	)
}
