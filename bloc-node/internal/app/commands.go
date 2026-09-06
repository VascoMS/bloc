package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"btd/be"
)

func genConfig(args []string) error {
	fs := flag.NewFlagSet("gen-config", flag.ExitOnError)
	nodes := fs.Int("nodes", 4, "number of operators")
	threshold := fs.Int("threshold", 0, "BTE threshold; defaults to 2f+1")
	bmax := fs.Int("bmax", 128, "BTE PRF domain and maximum encrypted batch size")
	baseHTTP := fs.Int("base-http-port", 8000, "first HTTP port")
	clusterID := fs.String("cluster-id", "bloc-local", "cluster identifier")
	slot := fs.Uint64("slot", 1, "default slot")
	maxDecryptedGas := fs.Uint64("max-decrypted-gas", 0, "maximum gas to decrypt per slot; 0 means uncapped")
	maxDecryptedTxs := fs.Int("max-decrypted-txs", 0, "maximum transactions to decrypt per slot; 0 means bmax")
	defaultTxGas := fs.Uint64("default-tx-gas", 21000, "default gas assigned to raw/synthetic submissions")
	maxProposalBytes := fs.Int("max-proposal-bytes", defaultMaxProposalBytes, "maximum encoded inclusion-list proposal bytes")
	maxEnvelopeBytes := fs.Int("max-envelope-bytes", defaultMaxEnvelopeBytes, "maximum protobuf envelope bytes")
	maxCombineAttempts := fs.Int("max-combine-attempts-per-sub-batch", defaultMaxCombineAttemptsPerSubBatch, "cumulative threshold-subset attempts per sub-batch")
	acsTrace := fs.Bool("acs-trace", false, "enable bounded ACS diagnostic tracing")
	streamMode := fs.String("stream-mode", streamModeFresh, "libp2p envelope streams: fresh, persistent, or persistent-lanes")
	providerMode := fs.String("provider", "direct", "inclusion-list provider: direct or mempool-http")
	mempoolURL := fs.String("mempool-url", "", "mempool-il base URL for provider=mempool-http")
	mempoolTimeoutMS := fs.Int64("mempool-timeout-ms", defaultMempoolTimeoutMS, "mempool-il request timeout in milliseconds; 0 uses the 2000 ms default")
	baseP2P := fs.Int("base-p2p-port", 10000, "first libp2p listen port")
	addressMode := fs.String("address-mode", "local", "address preset: local, container, kubernetes")
	httpListenTemplate := fs.String("http-listen-template", "", "HTTP listen template; supports {id}, {http_port}, {p2p_port}")
	httpAdvertiseTemplate := fs.String("http-advertise-template", "", "HTTP advertised URL template for remote evaluators")
	p2pListenTemplate := fs.String("p2p-listen-template", "", "libp2p listen multiaddr template")
	p2pAdvertiseTemplate := fs.String("p2p-advertise-template", "", "libp2p advertised multiaddr template for peer dialing")
	out := fs.String("out", "cluster.json", "public cluster config output")
	crsOut := fs.String("crs-out", "", "public CRS output; defaults to cluster.crs beside --out")
	secretsDir := fs.String("secrets-dir", "", "operator secret directory; defaults to secrets beside --out")
	secretPathTemplate := fs.String("secret-path-template", "", "optional operator secret path template containing {id}")
	secretUID := fs.Int("secret-uid", -1, "optional numeric owner UID for generated secret files")
	secretGID := fs.Int("secret-gid", -1, "optional numeric owner GID for generated secret files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *nodes < 4 {
		return fmt.Errorf("nodes must be >= 4 for f >= 1 ACS")
	}
	f := (*nodes - 1) / 3
	if *threshold == 0 {
		*threshold = 2*f + 1
	}
	if *threshold < 1 || *threshold > *nodes {
		return fmt.Errorf("threshold must be in [1,%d]", *nodes)
	}
	limits := ResourceLimits{
		MaxProposalBytes:              *maxProposalBytes,
		MaxEnvelopeBytes:              *maxEnvelopeBytes,
		MaxCombineAttemptsPerSubBatch: *maxCombineAttempts,
	}
	if err := validateResourceLimits(limits); err != nil {
		return err
	}
	provider := ProviderConfig{Mode: *providerMode, MempoolURL: *mempoolURL, MempoolTimeoutMS: *mempoolTimeoutMS}
	normalizeProviderConfig(&provider)
	if err := validateProviderConfig(provider); err != nil {
		return err
	}
	network := NetworkConfig{Mode: "libp2p", StreamMode: *streamMode}
	if err := validateNetworkConfig(network); err != nil {
		return err
	}
	if *crsOut == "" {
		*crsOut = filepath.Join(filepath.Dir(*out), "cluster.crs")
	}
	if *secretsDir == "" {
		*secretsDir = filepath.Join(filepath.Dir(*out), "secrets")
	}
	if *secretPathTemplate != "" && !strings.Contains(*secretPathTemplate, "{id}") {
		return fmt.Errorf("secret-path-template must contain {id}")
	}
	suite := newSuite()
	crs, err := be.GeneratePublicCRS(suite, *bmax)
	if err != nil {
		return err
	}
	btd, err := be.NewBTDFromPublicCRS(suite, *bmax, crs)
	if err != nil {
		return err
	}
	shares, pk := btd.KeyGen(*nodes, *threshold)
	pkHex, err := marshalPointHex(pk)
	if err != nil {
		return err
	}
	crsRelative, err := filepath.Rel(filepath.Dir(*out), *crsOut)
	if err != nil {
		return fmt.Errorf("make CRS path relative to cluster config: %w", err)
	}
	cfg := ConfigFile{
		Version:      clusterConfigVersion,
		ClusterID:    *clusterID,
		BMax:         *bmax,
		N:            *nodes,
		Threshold:    *threshold,
		Slot:         *slot,
		CRSFile:      filepath.ToSlash(crsRelative),
		CRSSHA256:    hashHex(crs),
		PublicKeyHex: pkHex,
		Blockspace: BlockspaceConfig{
			MaxDecryptedGas: *maxDecryptedGas,
			MaxDecryptedTxs: *maxDecryptedTxs,
			DefaultTxGas:    *defaultTxGas,
		},
		Provider: provider,
		Network:  network,
		Limits:   limits,
		Diagnostics: DiagnosticsConfig{
			ACSTrace: *acsTrace,
		},
	}
	secrets := make([]NodeSecretConfig, 0, *nodes)
	templates, err := resolveAddressTemplates(*addressMode, *httpListenTemplate, *httpAdvertiseTemplate, *p2pListenTemplate, *p2pAdvertiseTemplate)
	if err != nil {
		return err
	}
	for i := 0; i < *nodes; i++ {
		p2pPrivHex, p2pPeerID, err := generateLibP2PIdentity()
		if err != nil {
			return err
		}
		httpPort := *baseHTTP + i
		p2pPort := *baseP2P + i
		httpListen := renderAddressTemplate(templates.httpListen, i, httpPort, p2pPort)
		httpAdvertise := renderAddressTemplate(templates.httpAdvertise, i, httpPort, p2pPort)
		p2pListen := renderAddressTemplate(templates.p2pListen, i, httpPort, p2pPort)
		p2pAdvertise := renderAddressTemplate(templates.p2pAdvertise, i, httpPort, p2pPort)
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			ID:               uint64(i),
			HTTPAddr:         legacyHTTPAddr(httpAdvertise, httpListen),
			HTTPListenAddr:   httpListen,
			HTTPAdvertiseURL: httpAdvertise,
			P2PAddr:          p2pAdvertise,
			P2PListenAddr:    p2pListen,
			P2PAdvertiseAddr: p2pAdvertise,
			P2PPeerID:        p2pPeerID,
		})
		scalarHex, err := marshalScalarHex(shares[i].V)
		if err != nil {
			return err
		}
		secrets = append(secrets, NodeSecretConfig{
			Version:           nodeSecretVersion,
			ClusterID:         *clusterID,
			OperatorID:        uint64(shares[i].I),
			BTEShareScalarHex: scalarHex,
			P2PPrivateKeyHex:  p2pPrivHex,
		})
	}
	if err := writeFileAtomic(*crsOut, crs, 0644); err != nil {
		return fmt.Errorf("write public CRS: %w", err)
	}
	if err := writeJSONFileAtomic(*out, cfg, 0644); err != nil {
		return fmt.Errorf("write public cluster config: %w", err)
	}
	for _, secret := range secrets {
		path := filepath.Join(*secretsDir, fmt.Sprintf("operator-%d.json", secret.OperatorID))
		if *secretPathTemplate != "" {
			path = strings.ReplaceAll(*secretPathTemplate, "{id}", strconv.FormatUint(secret.OperatorID, 10))
		}
		if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
			return fmt.Errorf("create operator %d secret directory: %w", secret.OperatorID, err)
		}
		if err := writeJSONFileAtomic(path, secret, 0600); err != nil {
			return fmt.Errorf("write operator %d secrets: %w", secret.OperatorID, err)
		}
		if *secretUID >= 0 || *secretGID >= 0 {
			if err := os.Chown(filepath.Dir(path), *secretUID, *secretGID); err != nil {
				return fmt.Errorf("set operator %d secret directory owner: %w", secret.OperatorID, err)
			}
			if err := os.Chown(path, *secretUID, *secretGID); err != nil {
				return fmt.Errorf("set operator %d secret owner: %w", secret.OperatorID, err)
			}
		}
	}
	return nil
}

func runNode(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "cluster config")
	secretsPath := fs.String("secrets", "", "operator-local secret config")
	idRaw := fs.String("id", "", "operator id; defaults to NODE_ID when unset")
	slot := fs.Uint64("slot", 0, "slot to run; defaults to config slot")
	startAfter := fs.Duration("start-after", 0, "start consensus after delay")
	outPath := fs.String("out", "", "optional result JSON path")
	fault := fs.String("fault", "", "comma-separated fault modes: omit-proposal,withhold-share,corrupt-share")
	delay := fs.Duration("delay", 0, "artificial delay before sending each consensus/share message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *idRaw == "" {
		*idRaw = os.Getenv("NODE_ID")
	}
	if *idRaw == "" {
		return fmt.Errorf("operator id is required via --id or NODE_ID")
	}
	id, err := strconv.ParseUint(*idRaw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid operator id %q: %w", *idRaw, err)
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	if *slot != 0 {
		cfg.Slot = *slot
	}
	secrets, err := readNodeSecrets(*secretsPath)
	if err != nil {
		return err
	}
	node, err := newNode(cfg, secrets, id, parseFaults(*fault, *delay))
	if err != nil {
		return err
	}
	if err := node.startTransport(); err != nil {
		return err
	}
	if err := node.listenHTTP(*outPath); err != nil {
		return err
	}
	if *startAfter > 0 {
		time.AfterFunc(*startAfter, func() {
			if err := node.startConsensus(); err != nil {
				fmt.Fprintf(os.Stderr, "start consensus: %v\n", err)
			}
		})
	}
	select {}
}

func submitTx(args []string) error {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8000", "node HTTP URL")
	tx := fs.String("tx", "", "raw transaction bytes as hex, with or without 0x")
	gas := fs.Uint64("gas", 21000, "transaction gas metadata")
	feeWei := fs.String("fee-wei", "0", "effective fee per gas in wei")
	from := fs.String("from", "", "sender metadata")
	nonce := fs.Uint64("nonce", 0, "nonce metadata")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := SubmitTxRequest{RawTx: *tx, Gas: *gas, EffectiveFeePerGasWei: *feeWei, From: *from, Nonce: *nonce}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := http.Post(strings.TrimRight(*url, "/")+"/tx", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("submit failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	fmt.Print(string(respBody))
	return nil
}

type addressTemplates struct {
	httpListen    string
	httpAdvertise string
	p2pListen     string
	p2pAdvertise  string
}

func resolveAddressTemplates(mode, httpListen, httpAdvertise, p2pListen, p2pAdvertise string) (addressTemplates, error) {
	switch mode {
	case "", "local":
		return fillAddressTemplateDefaults(addressTemplates{
			httpListen:    httpListen,
			httpAdvertise: httpAdvertise,
			p2pListen:     p2pListen,
			p2pAdvertise:  p2pAdvertise,
		}, addressTemplates{
			httpListen:    "127.0.0.1:{http_port}",
			httpAdvertise: "http://127.0.0.1:{http_port}",
			p2pListen:     "/ip4/127.0.0.1/tcp/{p2p_port}",
			p2pAdvertise:  "/ip4/127.0.0.1/tcp/{p2p_port}",
		}), nil
	case "container":
		return fillAddressTemplateDefaults(addressTemplates{
			httpListen:    httpListen,
			httpAdvertise: httpAdvertise,
			p2pListen:     p2pListen,
			p2pAdvertise:  p2pAdvertise,
		}, addressTemplates{
			httpListen:    "0.0.0.0:{http_port}",
			httpAdvertise: "http://bloc-node-{id}:{http_port}",
			p2pListen:     "/ip4/0.0.0.0/tcp/{p2p_port}",
			p2pAdvertise:  "/dns4/bloc-node-{id}/tcp/{p2p_port}",
		}), nil
	case "kubernetes":
		return fillAddressTemplateDefaults(addressTemplates{
			httpListen:    httpListen,
			httpAdvertise: httpAdvertise,
			p2pListen:     p2pListen,
			p2pAdvertise:  p2pAdvertise,
		}, addressTemplates{
			httpListen:    "0.0.0.0:8000",
			httpAdvertise: "http://bloc-node-{id}.bloc-node-headless.bloc.svc.cluster.local:8000",
			p2pListen:     "/ip4/0.0.0.0/tcp/9000",
			p2pAdvertise:  "/dns4/bloc-node-{id}.bloc-node-headless.bloc.svc.cluster.local/tcp/9000",
		}), nil
	default:
		return addressTemplates{}, fmt.Errorf("unknown address-mode %q", mode)
	}
}

func fillAddressTemplateDefaults(value, defaults addressTemplates) addressTemplates {
	if value.httpListen == "" {
		value.httpListen = defaults.httpListen
	}
	if value.httpAdvertise == "" {
		value.httpAdvertise = defaults.httpAdvertise
	}
	if value.p2pListen == "" {
		value.p2pListen = defaults.p2pListen
	}
	if value.p2pAdvertise == "" {
		value.p2pAdvertise = defaults.p2pAdvertise
	}
	return value
}

func renderAddressTemplate(tmpl string, id, httpPort, p2pPort int) string {
	replacer := strings.NewReplacer(
		"{id}", strconv.Itoa(id),
		"{http_port}", strconv.Itoa(httpPort),
		"{p2p_port}", strconv.Itoa(p2pPort),
	)
	return replacer.Replace(tmpl)
}

func legacyHTTPAddr(httpAdvertiseURL, httpListenAddr string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(httpAdvertiseURL, "http://"), "https://")
	if trimmed != "" {
		return trimmed
	}
	return httpListenAddr
}

func parseIntList(raw string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty integer list")
	}
	return out, nil
}
