package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// readConfig loads a cluster JSON file and applies runtime defaults.
func readConfig(path string) (ConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigFile{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ConfigFile{}, err
	}
	if _, legacySeed := fields["crs_seed_hex"]; legacySeed {
		return ConfigFile{}, fmt.Errorf("legacy combined cluster config is unsupported; regenerate public config, CRS, and per-operator secrets")
	}
	if _, legacyShares := fields["shares"]; legacyShares {
		return ConfigFile{}, fmt.Errorf("legacy combined cluster config is unsupported; regenerate public config, CRS, and per-operator secrets")
	}
	var cfg ConfigFile
	if err := decodeStrictJSON(data, &cfg); err != nil {
		return ConfigFile{}, err
	}
	if cfg.Version != clusterConfigVersion {
		return ConfigFile{}, fmt.Errorf("unsupported cluster config version %q", cfg.Version)
	}
	if strings.TrimSpace(cfg.CRSFile) == "" || strings.TrimSpace(cfg.CRSSHA256) == "" {
		return ConfigFile{}, fmt.Errorf("cluster config requires crs_file and crs_sha256")
	}
	crsPath := cfg.CRSFile
	if !filepath.IsAbs(crsPath) {
		crsPath = filepath.Join(filepath.Dir(path), crsPath)
	}
	crs, err := os.ReadFile(crsPath)
	if err != nil {
		return ConfigFile{}, fmt.Errorf("read public CRS %s: %w", crsPath, err)
	}
	if got := hashHex(crs); !strings.EqualFold(got, strings.TrimSpace(cfg.CRSSHA256)) {
		return ConfigFile{}, fmt.Errorf("public CRS hash mismatch: got %s, expected %s", got, cfg.CRSSHA256)
	}
	cfg.CRSBytes = crs
	if cfg.Slot == 0 {
		cfg.Slot = 1
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

func readNodeSecrets(path string) (NodeSecretConfig, error) {
	if strings.TrimSpace(path) == "" {
		return NodeSecretConfig{}, fmt.Errorf("operator secrets path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NodeSecretConfig{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return NodeSecretConfig{}, err
	}
	for _, required := range []string{"version", "cluster_id", "operator_id", "bte_share_scalar_hex", "p2p_private_key_hex"} {
		if _, ok := fields[required]; !ok {
			return NodeSecretConfig{}, fmt.Errorf("node secret config is missing %s", required)
		}
	}
	var secrets NodeSecretConfig
	if err := decodeStrictJSON(data, &secrets); err != nil {
		return NodeSecretConfig{}, err
	}
	if secrets.Version != nodeSecretVersion {
		return NodeSecretConfig{}, fmt.Errorf("unsupported node secret version %q", secrets.Version)
	}
	if secrets.ClusterID == "" || secrets.BTEShareScalarHex == "" || secrets.P2PPrivateKeyHex == "" {
		return NodeSecretConfig{}, fmt.Errorf("node secret config requires cluster_id, bte_share_scalar_hex, and p2p_private_key_hex")
	}
	return secrets, nil
}

func decodeStrictJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func writeJSONFileAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), mode)
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".bloc-config-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return nil
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
