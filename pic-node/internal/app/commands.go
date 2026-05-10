package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
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
	baseConsensus := fs.Int("base-consensus-port", 9000, "first TCP consensus port")
	baseHTTP := fs.Int("base-http-port", 8000, "first HTTP port")
	clusterID := fs.String("cluster-id", "pic-local", "cluster identifier")
	slot := fs.Uint64("slot", 1, "default slot")
	maxDecryptedGas := fs.Uint64("max-decrypted-gas", 0, "maximum gas to decrypt per slot; 0 means uncapped")
	maxDecryptedTxs := fs.Int("max-decrypted-txs", 0, "maximum transactions to decrypt per slot; 0 means bmax")
	defaultTxGas := fs.Uint64("default-tx-gas", 21000, "default gas assigned to raw/synthetic submissions")
	providerMode := fs.String("provider", "direct", "inclusion-list provider: direct or mempool-http")
	mempoolURL := fs.String("mempool-url", "", "mempool-il base URL for provider=mempool-http")
	out := fs.String("out", "cluster.json", "output config")
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
	suite := newSuite()
	crsSeed := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, crsSeed); err != nil {
		return err
	}
	btd := be.NewBTDFromSeed(suite, *bmax, crsSeed)
	shares, pk := btd.KeyGen(*nodes, *threshold)
	pkHex, err := marshalPointHex(pk)
	if err != nil {
		return err
	}
	cfg := ConfigFile{
		ClusterID:    *clusterID,
		BMax:         *bmax,
		N:            *nodes,
		Threshold:    *threshold,
		Slot:         *slot,
		CRSSeedHex:   hex.EncodeToString(crsSeed),
		PublicKeyHex: pkHex,
		Blockspace: BlockspaceConfig{
			MaxDecryptedGas: *maxDecryptedGas,
			MaxDecryptedTxs: *maxDecryptedTxs,
			DefaultTxGas:    *defaultTxGas,
		},
		Provider: ProviderConfig{Mode: *providerMode, MempoolURL: *mempoolURL},
	}
	for i := 0; i < *nodes; i++ {
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			ID:            uint64(i),
			ConsensusAddr: fmt.Sprintf("127.0.0.1:%d", *baseConsensus+i),
			HTTPAddr:      fmt.Sprintf("127.0.0.1:%d", *baseHTTP+i),
		})
		scalarHex, err := marshalScalarHex(shares[i].V)
		if err != nil {
			return err
		}
		cfg.Shares = append(cfg.Shares, ShareConfig{OperatorID: int(shares[i].I), ScalarHex: scalarHex})
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(data, '\n'), 0644)
}

func runNode(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "cluster config")
	id := fs.Uint64("id", 0, "operator id")
	slot := fs.Uint64("slot", 0, "slot to run; defaults to config slot")
	startAfter := fs.Duration("start-after", 0, "start consensus after delay")
	outPath := fs.String("out", "", "optional result JSON path")
	fault := fs.String("fault", "", "comma-separated fault modes: omit-proposal,withhold-share,corrupt-share")
	delay := fs.Duration("delay", 0, "artificial delay before sending each consensus/share message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	if *slot != 0 {
		cfg.Slot = *slot
	}
	node, err := newNode(cfg, *id, parseFaults(*fault, *delay))
	if err != nil {
		return err
	}
	if err := node.listenConsensus(); err != nil {
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
