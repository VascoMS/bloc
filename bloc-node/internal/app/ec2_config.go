package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"btd/be"
)

type ec2ConfigOptions struct {
	InventoryPath    string
	ClusterOut       string
	CRSOut           string
	SecretsDir       string
	RemoteEvalOut    string
	ClusterID        string
	Nodes            int
	Threshold        int
	BMax             int
	Slot             uint64
	HTTPPort         int
	P2PPort          int
	HTTPHostMode     string
	P2PHostMode      string
	ProviderMode     string
	MempoolURL       string
	MempoolTimeoutMS int64
	MaxDecryptedGas  uint64
	MaxDecryptedTxs  int
	DefaultTxGas     uint64
	Limits           ResourceLimits
	PrometheusURL    string
	GrafanaURL       string
	ControllerURL    string
}

type ec2Inventory struct {
	Deployment map[string]string  `json:"deployment,omitempty"`
	Controller *ec2InventoryHost  `json:"controller,omitempty"`
	Nodes      []ec2InventoryHost `json:"nodes"`
}

type ec2InventoryHost struct {
	ID           int    `json:"id"`
	Label        string `json:"label,omitempty"`
	PrivateIP    string `json:"private_ip,omitempty"`
	PrivateDNS   string `json:"private_dns,omitempty"`
	PublicIP     string `json:"public_ip,omitempty"`
	PublicDNS    string `json:"public_dns,omitempty"`
	Region       string `json:"region,omitempty"`
	Zone         string `json:"zone,omitempty"`
	InstanceType string `json:"instance_type,omitempty"`
}

func genEC2Config(args []string) error {
	options, err := parseEC2ConfigOptions(args)
	if err != nil {
		return err
	}
	inventory, err := readEC2Inventory(options.InventoryPath)
	if err != nil {
		return err
	}
	cluster, crs, secrets, remote, err := buildEC2Configs(inventory, options)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(options.CRSOut, crs, 0644); err != nil {
		return err
	}
	if err := writeJSONFileAtomic(options.ClusterOut, cluster, 0644); err != nil {
		return err
	}
	for _, secret := range secrets {
		path := filepath.Join(options.SecretsDir, fmt.Sprintf("operator-%d.json", secret.OperatorID))
		if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
			return err
		}
		if err := writeJSONFileAtomic(path, secret, 0600); err != nil {
			return err
		}
	}
	if err := writeJSONFileAtomic(options.RemoteEvalOut, remote, 0644); err != nil {
		return err
	}
	return nil
}

func parseEC2ConfigOptions(args []string) (ec2ConfigOptions, error) {
	options := ec2ConfigOptions{}
	fs := flag.NewFlagSet("gen-ec2-config", flag.ContinueOnError)
	fs.StringVar(&options.InventoryPath, "inventory", "deploy/ec2/inventory.json", "EC2 inventory JSON from Terraform or scripts")
	fs.StringVar(&options.ClusterOut, "cluster-out", "cluster.ec2.json", "output sidecar cluster config")
	fs.StringVar(&options.CRSOut, "crs-out", "", "output public CRS artifact; defaults beside cluster config")
	fs.StringVar(&options.SecretsDir, "secrets-dir", "", "output directory for per-operator secrets; defaults beside cluster config")
	fs.StringVar(&options.RemoteEvalOut, "remote-eval-out", "remote-eval.ec2.json", "output remote evaluator config")
	fs.StringVar(&options.ClusterID, "cluster-id", "bloc-ec2", "cluster identifier")
	fs.IntVar(&options.Nodes, "nodes", 0, "expected operator count; defaults to inventory node count")
	fs.IntVar(&options.Threshold, "threshold", 0, "BTE threshold; defaults to 2f+1")
	fs.IntVar(&options.BMax, "bmax", 128, "BTE PRF domain and maximum encrypted batch size")
	fs.Uint64Var(&options.Slot, "slot", 1, "initial slot")
	fs.IntVar(&options.HTTPPort, "http-port", 8000, "sidecar HTTP port on every EC2 operator")
	fs.IntVar(&options.P2PPort, "p2p-port", 9000, "sidecar libp2p port on every EC2 operator")
	fs.StringVar(&options.HTTPHostMode, "http-host-mode", "private-ip", "HTTP advertised host: private-ip, private-dns, public-ip, or public-dns")
	fs.StringVar(&options.P2PHostMode, "p2p-host-mode", "private-ip", "libp2p advertised host: private-ip, private-dns, public-ip, or public-dns")
	fs.StringVar(&options.ProviderMode, "provider", "direct", "inclusion-list provider: direct or mempool-http")
	fs.StringVar(&options.MempoolURL, "mempool-url", "", "mempool-il base URL for provider=mempool-http")
	fs.Int64Var(&options.MempoolTimeoutMS, "mempool-timeout-ms", defaultMempoolTimeoutMS, "mempool-il request timeout in milliseconds; 0 uses the 2000 ms default")
	fs.Uint64Var(&options.MaxDecryptedGas, "max-decrypted-gas", 0, "maximum gas to decrypt per slot; 0 means uncapped")
	fs.IntVar(&options.MaxDecryptedTxs, "max-decrypted-txs", 0, "maximum transactions to decrypt per slot; 0 means bmax")
	fs.Uint64Var(&options.DefaultTxGas, "default-tx-gas", 21000, "default gas assigned to raw/synthetic submissions")
	fs.IntVar(&options.Limits.MaxProposalBytes, "max-proposal-bytes", defaultMaxProposalBytes, "maximum encoded inclusion-list proposal bytes")
	fs.IntVar(&options.Limits.MaxEnvelopeBytes, "max-envelope-bytes", defaultMaxEnvelopeBytes, "maximum protobuf envelope bytes")
	fs.IntVar(&options.Limits.MaxCombineAttemptsPerSubBatch, "max-combine-attempts-per-sub-batch", defaultMaxCombineAttemptsPerSubBatch, "cumulative threshold-subset attempts per sub-batch")
	fs.StringVar(&options.PrometheusURL, "prometheus-url", "http://127.0.0.1:9090", "Prometheus URL to record in remote evaluator metadata")
	fs.StringVar(&options.GrafanaURL, "grafana-url", "http://127.0.0.1:3000", "Grafana URL to record in remote evaluator metadata")
	fs.StringVar(&options.ControllerURL, "controller-url", "", "optional controller URL or host label to record in metadata")
	if err := fs.Parse(args); err != nil {
		return ec2ConfigOptions{}, err
	}
	if options.CRSOut == "" {
		options.CRSOut = filepath.Join(filepath.Dir(options.ClusterOut), "cluster.ec2.crs")
	}
	if options.SecretsDir == "" {
		options.SecretsDir = filepath.Join(filepath.Dir(options.ClusterOut), "secrets.ec2")
	}
	if options.BMax < 1 || options.HTTPPort < 1 || options.P2PPort < 1 {
		return ec2ConfigOptions{}, fmt.Errorf("bmax, http-port, and p2p-port must be positive")
	}
	if err := validateResourceLimits(options.Limits); err != nil {
		return ec2ConfigOptions{}, err
	}
	if err := validateProviderConfig(ProviderConfig{MempoolTimeoutMS: options.MempoolTimeoutMS}); err != nil {
		return ec2ConfigOptions{}, err
	}
	if _, err := validateEC2HostMode(options.HTTPHostMode); err != nil {
		return ec2ConfigOptions{}, fmt.Errorf("http-host-mode: %w", err)
	}
	if _, err := validateEC2HostMode(options.P2PHostMode); err != nil {
		return ec2ConfigOptions{}, fmt.Errorf("p2p-host-mode: %w", err)
	}
	return options, nil
}

func readEC2Inventory(path string) (ec2Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ec2Inventory{}, err
	}
	var inventory ec2Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return ec2Inventory{}, err
	}
	sort.Slice(inventory.Nodes, func(i, j int) bool { return inventory.Nodes[i].ID < inventory.Nodes[j].ID })
	return inventory, nil
}

func buildEC2Configs(inventory ec2Inventory, options ec2ConfigOptions) (ConfigFile, []byte, []NodeSecretConfig, remoteEvalConfig, error) {
	if options.Limits == (ResourceLimits{}) {
		options.Limits = defaultResourceLimits()
	}
	if err := validateResourceLimits(options.Limits); err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}
	provider := ProviderConfig{
		Mode:             options.ProviderMode,
		MempoolURL:       options.MempoolURL,
		MempoolTimeoutMS: options.MempoolTimeoutMS,
	}
	normalizeProviderConfig(&provider)
	if err := validateProviderConfig(provider); err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}
	nodes := len(inventory.Nodes)
	if options.Nodes != 0 && options.Nodes != nodes {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("nodes=%d does not match %d inventory nodes", options.Nodes, nodes)
	}
	if nodes < 4 {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("inventory requires at least 4 operator nodes")
	}
	seen := make(map[int]bool, nodes)
	for _, node := range inventory.Nodes {
		if node.ID < 0 || node.ID >= nodes {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("node id %d must be in [0,%d]", node.ID, nodes-1)
		}
		if seen[node.ID] {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("duplicate node id %d", node.ID)
		}
		seen[node.ID] = true
	}
	threshold := options.Threshold
	if threshold == 0 {
		f := (nodes - 1) / 3
		threshold = 2*f + 1
	}
	if threshold < 1 || threshold > nodes {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("threshold must be in [1,%d]", nodes)
	}

	suite := newSuite()
	crs, err := be.GeneratePublicCRS(suite, options.BMax)
	if err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}
	btd, err := be.NewBTDFromPublicCRS(suite, options.BMax, crs)
	if err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}
	shares, pk := btd.KeyGen(nodes, threshold)
	pkHex, err := marshalPointHex(pk)
	if err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}
	crsRelative, err := filepath.Rel(filepath.Dir(options.ClusterOut), options.CRSOut)
	if err != nil {
		return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
	}

	cluster := ConfigFile{
		Version:      clusterConfigVersion,
		ClusterID:    options.ClusterID,
		BMax:         options.BMax,
		N:            nodes,
		Threshold:    threshold,
		Slot:         options.Slot,
		CRSFile:      filepath.ToSlash(crsRelative),
		CRSSHA256:    hashHex(crs),
		PublicKeyHex: pkHex,
		Blockspace: BlockspaceConfig{
			MaxDecryptedGas: options.MaxDecryptedGas,
			MaxDecryptedTxs: options.MaxDecryptedTxs,
			DefaultTxGas:    options.DefaultTxGas,
		},
		Provider: provider,
		Network:  NetworkConfig{Mode: "libp2p"},
		Limits:   options.Limits,
	}
	secrets := make([]NodeSecretConfig, 0, nodes)
	remote := remoteEvalConfig{
		NodeCount:   nodes,
		Threshold:   threshold,
		BMax:        options.BMax,
		Network:     "libp2p",
		InitialSlot: options.Slot,
		Deployment: map[string]string{
			"environment": "ec2",
			"prometheus":  options.PrometheusURL,
			"grafana":     options.GrafanaURL,
		},
	}
	for k, v := range inventory.Deployment {
		remote.Deployment[k] = v
	}
	if options.ControllerURL != "" {
		remote.Deployment["controller"] = options.ControllerURL
	} else if inventory.Controller != nil {
		remote.Deployment["controller"] = firstNonEmpty(inventory.Controller.Label, inventory.Controller.PrivateDNS, inventory.Controller.PrivateIP, inventory.Controller.PublicDNS, inventory.Controller.PublicIP)
	}

	for _, host := range inventory.Nodes {
		httpHost, err := hostValue(host, options.HTTPHostMode)
		if err != nil {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("node %d http host: %w", host.ID, err)
		}
		p2pHost, err := hostValue(host, options.P2PHostMode)
		if err != nil {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("node %d p2p host: %w", host.ID, err)
		}
		p2pPrivHex, p2pPeerID, err := generateLibP2PIdentity()
		if err != nil {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
		}
		httpAdvertise := fmt.Sprintf("http://%s:%d", httpHost, options.HTTPPort)
		p2pAdvertise, err := p2pMultiaddr(p2pHost, options.P2PPort)
		if err != nil {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, fmt.Errorf("node %d p2p advertise: %w", host.ID, err)
		}
		cluster.Nodes = append(cluster.Nodes, NodeConfig{
			ID:               uint64(host.ID),
			HTTPAddr:         strings.TrimPrefix(httpAdvertise, "http://"),
			HTTPListenAddr:   fmt.Sprintf("0.0.0.0:%d", options.HTTPPort),
			HTTPAdvertiseURL: httpAdvertise,
			P2PAddr:          p2pAdvertise,
			P2PListenAddr:    fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", options.P2PPort),
			P2PAdvertiseAddr: p2pAdvertise,
			P2PPeerID:        p2pPeerID,
		})
		scalarHex, err := marshalScalarHex(shares[host.ID].V)
		if err != nil {
			return ConfigFile{}, nil, nil, remoteEvalConfig{}, err
		}
		secrets = append(secrets, NodeSecretConfig{
			Version:           nodeSecretVersion,
			ClusterID:         options.ClusterID,
			OperatorID:        uint64(host.ID),
			BTEShareScalarHex: scalarHex,
			P2PPrivateKeyHex:  p2pPrivHex,
		})
		remote.Nodes = append(remote.Nodes, remoteEvalNode{
			ID:     host.ID,
			URL:    httpAdvertise,
			Label:  firstNonEmpty(host.Label, fmt.Sprintf("operator-%d", host.ID)),
			Region: host.Region,
			Zone:   host.Zone,
		})
	}
	return cluster, crs, secrets, remote, nil
}

func validateEC2HostMode(mode string) (string, error) {
	switch mode {
	case "private-ip", "private-dns", "public-ip", "public-dns":
		return mode, nil
	default:
		return "", fmt.Errorf("must be private-ip, private-dns, public-ip, or public-dns")
	}
}

func hostValue(host ec2InventoryHost, mode string) (string, error) {
	switch mode {
	case "private-ip":
		return requiredHostValue(host.PrivateIP, mode)
	case "private-dns":
		return requiredHostValue(host.PrivateDNS, mode)
	case "public-ip":
		return requiredHostValue(host.PublicIP, mode)
	case "public-dns":
		return requiredHostValue(host.PublicDNS, mode)
	default:
		return "", fmt.Errorf("unsupported host mode %q", mode)
	}
}

func requiredHostValue(value, mode string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("inventory is missing %s", mode)
	}
	return value, nil
}

func p2pMultiaddr(host string, port int) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return fmt.Sprintf("/ip4/%s/tcp/%d", host, port), nil
		}
		return fmt.Sprintf("/ip6/%s/tcp/%d", host, port), nil
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("empty host")
	}
	return fmt.Sprintf("/dns4/%s/tcp/%d", host, port), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
