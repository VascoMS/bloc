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
	if err := validateResourceLimits(cfg.Limits); err != nil {
		return ConfigFile{}, err
	}
	if err := validateProviderConfig(cfg.Provider); err != nil {
		return ConfigFile{}, err
	}
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

// UnmarshalJSON distinguishes an omitted optional limit from an explicit zero,
// which is invalid rather than a request for the default.
func (limits *ResourceLimits) UnmarshalJSON(data []byte) error {
	type wireLimits struct {
		MaxProposalBytes              *int `json:"max_proposal_bytes"`
		MaxEnvelopeBytes              *int `json:"max_envelope_bytes"`
		MaxCombineAttemptsPerSubBatch *int `json:"max_combine_attempts_per_sub_batch"`
	}
	var wire wireLimits
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.MaxProposalBytes != nil {
		limits.MaxProposalBytes = *wire.MaxProposalBytes
		limits.explicitZeroProposal = *wire.MaxProposalBytes == 0
	}
	if wire.MaxEnvelopeBytes != nil {
		limits.MaxEnvelopeBytes = *wire.MaxEnvelopeBytes
		limits.explicitZeroEnvelope = *wire.MaxEnvelopeBytes == 0
	}
	if wire.MaxCombineAttemptsPerSubBatch != nil {
		limits.MaxCombineAttemptsPerSubBatch = *wire.MaxCombineAttemptsPerSubBatch
		limits.explicitZeroCombineAttempts = *wire.MaxCombineAttemptsPerSubBatch == 0
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
	normalizeProviderConfig(&cfg.Provider)
	if cfg.Network.Mode == "" {
		cfg.Network.Mode = "libp2p"
	}
	defaults := defaultResourceLimits()
	if cfg.Limits.MaxProposalBytes == 0 && !cfg.Limits.explicitZeroProposal {
		cfg.Limits.MaxProposalBytes = defaults.MaxProposalBytes
	}
	if cfg.Limits.MaxEnvelopeBytes == 0 && !cfg.Limits.explicitZeroEnvelope {
		cfg.Limits.MaxEnvelopeBytes = defaults.MaxEnvelopeBytes
	}
	if cfg.Limits.MaxCombineAttemptsPerSubBatch == 0 && !cfg.Limits.explicitZeroCombineAttempts {
		cfg.Limits.MaxCombineAttemptsPerSubBatch = defaults.MaxCombineAttemptsPerSubBatch
	}
	for i := range cfg.Nodes {
		normalizeNodeConfig(&cfg.Nodes[i])
	}
}

func normalizeProviderConfig(provider *ProviderConfig) {
	if provider.Mode == "" {
		provider.Mode = "direct"
	}
	if provider.MempoolTimeoutMS == 0 {
		provider.MempoolTimeoutMS = defaultMempoolTimeoutMS
	}
}

func validateProviderConfig(provider ProviderConfig) error {
	if provider.MempoolTimeoutMS < 0 {
		return fmt.Errorf("provider.mempool_timeout_ms must be non-negative")
	}
	if provider.MempoolTimeoutMS > maximumMempoolTimeoutMS {
		return fmt.Errorf("provider.mempool_timeout_ms must be at most %d", maximumMempoolTimeoutMS)
	}
	return nil
}

func validateResourceLimits(limits ResourceLimits) error {
	if limits.MaxProposalBytes <= 0 || limits.MaxProposalBytes > absoluteMaxProposalBytes {
		return fmt.Errorf("limits.max_proposal_bytes must be in [1,%d]", absoluteMaxProposalBytes)
	}
	if limits.MaxEnvelopeBytes <= 0 || limits.MaxEnvelopeBytes > absoluteMaxEnvelopeBytes {
		return fmt.Errorf("limits.max_envelope_bytes must be in [1,%d]", absoluteMaxEnvelopeBytes)
	}
	if limits.MaxEnvelopeBytes < limits.MaxProposalBytes+minimumEnvelopeHeadroomBytes {
		return fmt.Errorf("limits.max_envelope_bytes must be at least max_proposal_bytes + %d", minimumEnvelopeHeadroomBytes)
	}
	if limits.MaxCombineAttemptsPerSubBatch < 1 || limits.MaxCombineAttemptsPerSubBatch > absoluteMaxCombineAttemptsPerSubBatch {
		return fmt.Errorf("limits.max_combine_attempts_per_sub_batch must be in [1,%d]", absoluteMaxCombineAttemptsPerSubBatch)
	}
	return nil
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
