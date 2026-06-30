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
	for i := range cfg.Nodes {
		normalizeNodeConfig(&cfg.Nodes[i])
	}
}

func normalizeNodeConfig(node *NodeConfig) {
	if node.HTTPListenAddr == "" {
		node.HTTPListenAddr = node.HTTPAddr
	}
	if node.HTTPAddr == "" {
		node.HTTPAddr = node.HTTPListenAddr
	}
	if node.HTTPAdvertiseURL == "" && node.HTTPAddr != "" {
		node.HTTPAdvertiseURL = "http://" + node.HTTPAddr
	}
	if node.P2PListenAddr == "" {
		node.P2PListenAddr = node.P2PAddr
	}
	if node.P2PAddr == "" {
		node.P2PAddr = node.P2PListenAddr
	}
	if node.P2PAdvertiseAddr == "" {
		node.P2PAdvertiseAddr = node.P2PAddr
	}
}

func (node NodeConfig) httpListenAddr() string {
	if node.HTTPListenAddr != "" {
		return node.HTTPListenAddr
	}
	return node.HTTPAddr
}

func (node NodeConfig) httpAdvertiseURL() string {
	if node.HTTPAdvertiseURL != "" {
		return node.HTTPAdvertiseURL
	}
	if node.HTTPAddr == "" {
		return ""
	}
	return "http://" + node.HTTPAddr
}

func (node NodeConfig) p2pListenAddr() string {
	if node.P2PListenAddr != "" {
		return node.P2PListenAddr
	}
	return node.P2PAddr
}

func (node NodeConfig) p2pAdvertiseAddr() string {
	if node.P2PAdvertiseAddr != "" {
		return node.P2PAdvertiseAddr
	}
	return node.P2PAddr
}
