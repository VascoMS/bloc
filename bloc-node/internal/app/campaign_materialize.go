package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type campaignMaterializeOptions struct {
	BundleRoot    string
	InventoryPath string
	ClusterOut    string
	CRSOut        string
	RemoteEvalOut string
	Topology      string
	MempoolURL    string
	PrometheusURL string
	GrafanaURL    string
	ControllerURL string
	HTTPPort      int
	P2PPort       int
	HTTPHostMode  string
	P2PHostMode   string
	ACSTrace      bool
}

func buildMaterializedCampaignConfigs(bundle campaignBundle, inventory ec2Inventory, options campaignMaterializeOptions) (ConfigFile, []byte, remoteEvalConfig, error) {
	if options.HTTPPort < 1 || options.P2PPort < 1 {
		return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("campaign HTTP and P2P ports must be positive")
	}
	if options.HTTPHostMode == "" {
		options.HTTPHostMode = "private-ip"
	}
	if options.P2PHostMode == "" {
		options.P2PHostMode = "private-ip"
	}
	if _, err := validateEC2HostMode(options.HTTPHostMode); err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	if _, err := validateEC2HostMode(options.P2PHostMode); err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	if strings.TrimSpace(options.MempoolURL) == "" {
		return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("campaign mempool URL is required")
	}
	nodes := append([]ec2InventoryHost(nil), inventory.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	if len(nodes) != bundle.Identity.N {
		return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("campaign inventory node count %d does not match bundle n=%d", len(nodes), bundle.Identity.N)
	}
	seen := map[int]bool{}
	for index, node := range nodes {
		if seen[node.ID] {
			return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("duplicate campaign node id %d", node.ID)
		}
		seen[node.ID] = true
		if node.ID != index {
			return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("campaign node ids must be consecutive from zero")
		}
	}
	if err := validateCampaignInventoryPlacement(options.Topology, inventory.Controller, nodes); err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	crs, err := os.ReadFile(bundle.CRSPath)
	if err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	crsRelative, err := filepath.Rel(filepath.Dir(options.ClusterOut), options.CRSOut)
	if err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	cluster := ConfigFile{
		Version: clusterConfigVersion, ClusterID: bundle.Identity.ClusterID,
		BMax: bundle.Identity.BMax, N: bundle.Identity.N, Threshold: bundle.Identity.Threshold, Slot: 1,
		CRSFile: filepath.ToSlash(crsRelative), CRSSHA256: bundle.Identity.CRSSHA256,
		PublicKeyHex: bundle.Identity.PublicKeyHex, Blockspace: bundle.Identity.Blockspace,
		Limits: bundle.Identity.Limits, Network: NetworkConfig{Mode: "libp2p"},
		Diagnostics: DiagnosticsConfig{ACSTrace: options.ACSTrace},
		Provider: ProviderConfig{
			Mode: "mempool-http", MempoolURL: options.MempoolURL, MempoolTimeoutMS: defaultMempoolTimeoutMS,
			ExpectedPublicConfigID:        bundle.Corpus.PublicConfigID,
			ExpectedPlaintextMasterID:     bundle.Corpus.PlaintextMasterCorpusID,
			ExpectedEncryptedCorpusID:     bundle.Corpus.EncryptedCorpusID,
			ExpectedEncryptedPrefixSetIDs: cloneStringMap(bundle.Corpus.EncryptedPrefixSetIDs), RequireExactCount: true,
		},
	}
	remote := remoteEvalConfig{
		NodeCount: bundle.Identity.N, Threshold: bundle.Identity.Threshold, BMax: bundle.Identity.BMax,
		Network: "libp2p", InitialSlot: 1, Corpus: bundle.Corpus,
		Deployment: map[string]string{
			"environment": "ec2", "topology": options.Topology,
			"prometheus": options.PrometheusURL, "grafana": options.GrafanaURL,
			"source_sha": bundle.Manifest.SourceSHA, "bloc_image": bundle.Manifest.BlocImage,
			"mempool_image": bundle.Manifest.MempoolImage,
		},
	}
	for key, value := range inventory.Deployment {
		remote.Deployment[key] = value
	}
	if options.ControllerURL != "" {
		remote.Deployment["controller"] = options.ControllerURL
	} else if inventory.Controller != nil {
		remote.Deployment["controller"] = firstNonEmpty(inventory.Controller.Label, inventory.Controller.PrivateDNS, inventory.Controller.PrivateIP)
	}
	for _, host := range nodes {
		httpHost, err := hostValue(host, options.HTTPHostMode)
		if err != nil {
			return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("node %d HTTP host: %w", host.ID, err)
		}
		p2pHost, err := hostValue(host, options.P2PHostMode)
		if err != nil {
			return ConfigFile{}, nil, remoteEvalConfig{}, fmt.Errorf("node %d P2P host: %w", host.ID, err)
		}
		httpURL := fmt.Sprintf("http://%s:%d", httpHost, options.HTTPPort)
		p2pAddress, err := p2pMultiaddr(p2pHost, options.P2PPort)
		if err != nil {
			return ConfigFile{}, nil, remoteEvalConfig{}, err
		}
		cluster.Nodes = append(cluster.Nodes, NodeConfig{
			ID: uint64(host.ID), HTTPAddr: strings.TrimPrefix(httpURL, "http://"),
			HTTPListenAddr: fmt.Sprintf("0.0.0.0:%d", options.HTTPPort), HTTPAdvertiseURL: httpURL,
			P2PAddr: p2pAddress, P2PListenAddr: fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", options.P2PPort),
			P2PAdvertiseAddr: p2pAddress, P2PPeerID: bundle.Identity.Operators[host.ID].P2PPeerID,
		})
		remote.Nodes = append(remote.Nodes, remoteEvalNode{
			ID: host.ID, URL: httpURL, Label: firstNonEmpty(host.Label, fmt.Sprintf("operator-%d", host.ID)),
			Region: host.Region, Zone: host.Zone,
		})
	}
	if err := validateProviderConfig(cluster.Provider); err != nil {
		return ConfigFile{}, nil, remoteEvalConfig{}, err
	}
	return cluster, crs, remote, nil
}

func validateCampaignInventoryPlacement(topology string, controller *ec2InventoryHost, nodes []ec2InventoryHost) error {
	if controller == nil {
		return fmt.Errorf("campaign inventory requires a controller")
	}
	if controller.InstanceType != "t3.small" {
		return fmt.Errorf("campaign controller must use t3.small")
	}
	for _, node := range nodes {
		if node.InstanceType != "t3.small" {
			return fmt.Errorf("campaign operator %d must use t3.small", node.ID)
		}
	}
	switch topology {
	case "T0-same-az":
		if controller.Region != "us-east-1" || controller.Zone != "us-east-1a" {
			return fmt.Errorf("same-AZ controller must be in us-east-1a")
		}
		for _, node := range nodes {
			if node.Region != "us-east-1" || node.Zone != "us-east-1a" {
				return fmt.Errorf("same-AZ operator %d must be in us-east-1a", node.ID)
			}
		}
	case "T2-three-region":
		if controller.Region != "us-east-1" {
			return fmt.Errorf("three-region controller must be in us-east-1")
		}
		regions := []string{"us-east-1", "eu-west-1", "eu-central-1"}
		for _, node := range nodes {
			if node.Region != regions[node.ID%3] || !strings.HasPrefix(node.Zone, node.Region) {
				return fmt.Errorf("operator %d violates modulo-three placement", node.ID)
			}
		}
	default:
		return fmt.Errorf("campaign topology must be T0-same-az or T2-three-region")
	}
	return nil
}

func materializeCampaignConfig(args []string) error {
	options := campaignMaterializeOptions{}
	fs := flag.NewFlagSet("materialize-campaign-config", flag.ContinueOnError)
	fs.StringVar(&options.BundleRoot, "bundle-root", "", "verified frozen campaign bundle")
	fs.StringVar(&options.InventoryPath, "inventory", "", "Terraform inventory JSON")
	fs.StringVar(&options.Topology, "topology", "", "T0-same-az or T2-three-region")
	fs.StringVar(&options.ClusterOut, "cluster-out", "", "new topology-specific cluster config")
	fs.StringVar(&options.CRSOut, "crs-out", "", "new copied public CRS path")
	fs.StringVar(&options.RemoteEvalOut, "remote-eval-out", "", "new remote evaluator config")
	fs.StringVar(&options.MempoolURL, "mempool-url", "http://mempool-il:8080", "operator-local mempool URL")
	fs.StringVar(&options.PrometheusURL, "prometheus-url", "http://127.0.0.1:9090", "Prometheus metadata URL")
	fs.StringVar(&options.GrafanaURL, "grafana-url", "http://127.0.0.1:3000", "Grafana metadata URL")
	fs.StringVar(&options.ControllerURL, "controller-url", "", "controller metadata label")
	fs.IntVar(&options.HTTPPort, "http-port", 8000, "operator HTTP port")
	fs.IntVar(&options.P2PPort, "p2p-port", 9000, "operator P2P port")
	fs.StringVar(&options.HTTPHostMode, "http-host-mode", "private-ip", "inventory HTTP host field")
	fs.StringVar(&options.P2PHostMode, "p2p-host-mode", "private-ip", "inventory P2P host field")
	fs.BoolVar(&options.ACSTrace, "acs-trace", false, "enable bounded ACS diagnostic tracing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"bundle-root": options.BundleRoot, "inventory": options.InventoryPath, "topology": options.Topology,
		"cluster-out": options.ClusterOut, "crs-out": options.CRSOut, "remote-eval-out": options.RemoteEvalOut,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}
	for _, path := range []string{options.ClusterOut, options.CRSOut, options.RemoteEvalOut} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("campaign materialization output already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	bundle, err := loadCampaignBundle(options.BundleRoot)
	if err != nil {
		return err
	}
	inventory, err := readEC2Inventory(options.InventoryPath)
	if err != nil {
		return err
	}
	cluster, crs, remote, err := buildMaterializedCampaignConfigs(bundle, inventory, options)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(options.CRSOut, crs, 0644); err != nil {
		return err
	}
	if err := writeJSONFileAtomic(options.ClusterOut, cluster, 0644); err != nil {
		return err
	}
	return writeJSONFileAtomic(options.RemoteEvalOut, remote, 0644)
}
