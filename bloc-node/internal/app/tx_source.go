package app

import "fmt"

func validateTxSource(source, mempoolURL string) error {
	switch source {
	case "", "synthetic":
		return nil
	case "mock-placeholder":
		if mempoolURL == "" {
			return fmt.Errorf("tx-source=mock-placeholder requires --mempool-url")
		}
		return nil
	default:
		return fmt.Errorf("tx-source must be synthetic or mock-placeholder")
	}
}

func txSourceManifestMeta(source, mempoolURL string) map[string]any {
	if source == "" {
		source = "synthetic"
	}
	meta := map[string]any{
		"source": source,
	}
	if source == "mock-placeholder" {
		meta["mempool_url"] = mempoolURL
		meta["model"] = "real target transactions encrypted once into mock placeholder candidates"
	}
	return meta
}
