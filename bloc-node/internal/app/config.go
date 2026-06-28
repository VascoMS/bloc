package app

import (
	"encoding/json"
	"os"
)

// readConfig loads a cluster JSON file and applies runtime defaults.
func readConfig(path string) (ConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigFile{}, err
	}
	var cfg ConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ConfigFile{}, err
	}
	if cfg.Slot == 0 {
		cfg.Slot = 1
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

// normalizeConfig fills defaults for fields added after the first prototype
// configs so older local configs remain usable.
func normalizeConfig(cfg *ConfigFile) {
	if cfg.Blockspace.DefaultTxGas == 0 {
		cfg.Blockspace.DefaultTxGas = 21000
	}
	if cfg.Provider.Mode == "" {
		cfg.Provider.Mode = "direct"
	}
	if cfg.Network.Mode == "" {
		cfg.Network.Mode = "libp2p"
	}
}
