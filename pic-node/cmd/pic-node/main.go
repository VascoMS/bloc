package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"btd/be"
	"btd/curves"
	"github.com/anthdm/hbbft"
	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
	"go.dedis.ch/kyber/v4/share"
)

type ConfigFile struct {
	ClusterID    string        `json:"cluster_id"`
	BMax         int           `json:"bmax"`
	N            int           `json:"n"`
	Threshold    int           `json:"threshold"`
	Slot         uint64        `json:"slot"`
	CRSSeedHex   string        `json:"crs_seed_hex"`
	Nodes        []NodeConfig  `json:"nodes"`
	PublicKeyHex string        `json:"public_key_hex"`
	Shares       []ShareConfig `json:"shares"`
}

type NodeConfig struct {
	ID            uint64 `json:"id"`
	ConsensusAddr string `json:"consensus_addr"`
	HTTPAddr      string `json:"http_addr"`
}

type ShareConfig struct {
	OperatorID int    `json:"operator_id"`
	ScalarHex  string `json:"scalar_hex"`
}

type WireEnvelope struct {
	From  uint64
	Kind  string
	Slot  uint64
	ACS   *hbbft.SlotMessage
	Share *WireShare
}

type WireShare struct {
	OperatorID int
	BatchIDHex string
	SubBatchID int
	PointHex   string
}

type WireCiphertext struct {
	Hash string
	Data []byte
}

type Node struct {
	cfg       ConfigFile
	self      NodeConfig
	nodeIDs   []uint64
	peers     map[uint64]NodeConfig
	slot      *hbbft.SlotACS
	cluster   *be.ClusterBTE
	secret    be.SecretShare
	suite     curves.Suite
	startOnce sync.Once
	faults    FaultConfig

	mu          sync.Mutex
	pending     []WireCiphertext
	seenPending map[string]bool
	plan        be.BatchPlan
	planned     bool
	shares      []be.DecryptionShare
	seenShares  map[string]bool
	result      *Result
	metrics     Metrics
}

type Result struct {
	Slot        uint64   `json:"slot"`
	NodeID      uint64   `json:"node_id"`
	BatchID     string   `json:"batch_id"`
	Ciphertexts int      `json:"ciphertexts"`
	Plaintexts  []string `json:"plaintexts_hex"`
	LatencyMS   int64    `json:"latency_ms"`
	Metrics     Metrics  `json:"metrics"`
}

type FaultConfig struct {
	OmitProposal  bool
	WithholdShare bool
	CorruptShare  bool
	Delay         time.Duration
}

type Metrics struct {
	SubmittedTxs        int              `json:"submitted_txs"`
	SubmittedBytes      int              `json:"submitted_bytes"`
	ProposalTxs         int              `json:"proposal_txs"`
	AgreedCiphertexts   int              `json:"agreed_ciphertexts"`
	SubBatches          int              `json:"sub_batches"`
	SharesGenerated     int              `json:"shares_generated"`
	SharesAccepted      int              `json:"shares_accepted"`
	SharesNeededPerSub  int              `json:"shares_needed_per_sub_batch"`
	OutboundMessages    map[string]int   `json:"outbound_messages"`
	InboundMessages     map[string]int   `json:"inbound_messages"`
	OutboundBytes       map[string]int64 `json:"outbound_bytes"`
	InboundBytes        map[string]int64 `json:"inbound_bytes"`
	SlotStartUnixNano   int64            `json:"slot_start_unix_nano"`
	ACSDecisionUnixNano int64            `json:"acs_decision_unix_nano"`
	PlanDoneUnixNano    int64            `json:"plan_done_unix_nano"`
	SharesDoneUnixNano  int64            `json:"shares_done_unix_nano"`
	FirstShareUnixNano  int64            `json:"first_share_unix_nano"`
	ThresholdUnixNano   int64            `json:"threshold_unix_nano"`
	CombineDoneUnixNano int64            `json:"combine_done_unix_nano"`
	ACSMS               int64            `json:"acs_ms"`
	PlanMS              int64            `json:"plan_ms"`
	ShareGenerationMS   int64            `json:"share_generation_ms"`
	CommitToPlaintextMS int64            `json:"commit_to_plaintext_ms"`
	TotalSlotMS         int64            `json:"total_slot_ms"`
}

func main() {
	registerGobTypes()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gen-config":
		if err := genConfig(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "run":
		if err := runNode(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "submit":
		if err := submitTx(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "eval-local":
		if err := evalLocal(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  pic-node gen-config --nodes 4 --threshold 3 --bmax 128 --out cluster.json
  pic-node run --config cluster.json --id 0 --slot 1 --start-after 3s
  pic-node submit --url http://127.0.0.1:8000 --tx 0x010203
  pic-node eval-local --nodes 4 --batch-sizes 8,32 --tx-size 256 --out-dir results
`)
}

func genConfig(args []string) error {
	fs := flag.NewFlagSet("gen-config", flag.ExitOnError)
	nodes := fs.Int("nodes", 4, "number of operators")
	threshold := fs.Int("threshold", 0, "BTE threshold; defaults to 2f+1")
	bmax := fs.Int("bmax", 128, "BTE PRF domain and maximum encrypted batch size")
	baseConsensus := fs.Int("base-consensus-port", 9000, "first TCP consensus port")
	baseHTTP := fs.Int("base-http-port", 8000, "first HTTP port")
	clusterID := fs.String("cluster-id", "pic-local", "cluster identifier")
	slot := fs.Uint64("slot", 1, "default slot")
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
				log.Printf("start consensus: %v", err)
			}
		})
	}
	select {}
}

func submitTx(args []string) error {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8000", "node HTTP URL")
	tx := fs.String("tx", "", "raw transaction bytes as hex, with or without 0x")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := decodeHexMaybe(*tx)
	if err != nil {
		return err
	}
	resp, err := http.Post(strings.TrimRight(*url, "/")+"/tx", "application/octet-stream", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("submit failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Print(string(body))
	return nil
}

type EvalRun struct {
	RunID      string            `json:"run_id"`
	Nodes      int               `json:"nodes"`
	Threshold  int               `json:"threshold"`
	BMax       int               `json:"bmax"`
	BatchSize  int               `json:"batch_size"`
	TxSize     int               `json:"tx_size"`
	Faults     map[uint64]string `json:"faults,omitempty"`
	Success    bool              `json:"success"`
	Consistent bool              `json:"consistent"`
	Error      string            `json:"error,omitempty"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Results    []Result          `json:"results"`
}

func evalLocal(args []string) error {
	fs := flag.NewFlagSet("eval-local", flag.ExitOnError)
	nodes := fs.Int("nodes", 4, "number of operator processes")
	threshold := fs.Int("threshold", 0, "BTE threshold; defaults to 2f+1")
	bmax := fs.Int("bmax", 32, "BTE maximum batch size")
	batchSizesRaw := fs.String("batch-sizes", "8", "comma-separated transaction batch sizes")
	txSize := fs.Int("tx-size", 256, "raw transaction byte size")
	outDir := fs.String("out-dir", "results", "directory for run artifacts")
	basePort := fs.Int("base-port", 21000, "base port; consensus uses base, HTTP uses base+1000")
	timeout := fs.Duration("timeout", 20*time.Second, "per-run timeout")
	faultRaw := fs.String("fault", "", "optional node fault as id:mode, e.g. 3:withhold-share or 0:omit-proposal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	batchSizes, err := parseIntList(*batchSizesRaw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	var runs []EvalRun
	for idx, batchSize := range batchSizes {
		runID := fmt.Sprintf("n%d-b%d-tx%d-%d", *nodes, batchSize, *txSize, time.Now().UnixNano())
		runDir := filepath.Join(*outDir, runID)
		if err := os.MkdirAll(runDir, 0755); err != nil {
			return err
		}
		run, err := runLocalExperiment(self, runDir, runID, *nodes, *threshold, *bmax, batchSize, *txSize, *basePort+(idx*2000), *timeout, *faultRaw)
		if err != nil {
			run.Error = err.Error()
		}
		runs = append(runs, run)
	}
	if err := writeEvalOutputs(*outDir, runs); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(runs, "", "  ")
	fmt.Println(string(encoded))
	return nil
}

func runLocalExperiment(self, runDir, runID string, nodes, threshold, bmax, batchSize, txSize, basePort int, timeout time.Duration, faultRaw string) (EvalRun, error) {
	run := EvalRun{
		RunID:     runID,
		Nodes:     nodes,
		Threshold: threshold,
		BMax:      bmax,
		BatchSize: batchSize,
		TxSize:    txSize,
		Faults:    make(map[uint64]string),
		StartedAt: time.Now(),
	}
	if threshold == 0 {
		f := (nodes - 1) / 3
		run.Threshold = 2*f + 1
	}
	faults, err := parseEvalFault(faultRaw)
	if err != nil {
		return run, err
	}
	run.Faults = faults
	configPath := filepath.Join(runDir, "cluster.json")
	args := []string{"gen-config", "--nodes", strconv.Itoa(nodes), "--threshold", strconv.Itoa(run.Threshold), "--bmax", strconv.Itoa(bmax), "--base-consensus-port", strconv.Itoa(basePort), "--base-http-port", strconv.Itoa(basePort + 1000), "--out", configPath}
	if out, err := exec.Command(self, args...).CombinedOutput(); err != nil {
		return run, fmt.Errorf("gen-config: %w: %s", err, strings.TrimSpace(string(out)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	procs := make([]*exec.Cmd, 0, nodes)
	for id := 0; id < nodes; id++ {
		nodeArgs := []string{"run", "--config", configPath, "--id", strconv.Itoa(id), "--out", filepath.Join(runDir, fmt.Sprintf("node-%d-result.json", id))}
		if fault, ok := faults[uint64(id)]; ok {
			nodeArgs = append(nodeArgs, "--fault", fault)
		}
		cmd := exec.CommandContext(ctx, self, nodeArgs...)
		logFile, err := os.Create(filepath.Join(runDir, fmt.Sprintf("node-%d.log", id)))
		if err != nil {
			return run, err
		}
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			return run, err
		}
		procs = append(procs, cmd)
	}
	defer func() {
		cancel()
		for _, cmd := range procs {
			_ = cmd.Wait()
		}
	}()
	if err := waitForHTTP(nodes, basePort+1000, 5*time.Second); err != nil {
		return run, err
	}
	for i := 0; i < batchSize; i++ {
		nodeID := i % nodes
		rawTx := syntheticTx(i, txSize)
		if err := postBytes(fmt.Sprintf("http://127.0.0.1:%d/tx", basePort+1000+nodeID), rawTx); err != nil {
			return run, fmt.Errorf("submit tx %d to node %d: %w", i, nodeID, err)
		}
	}
	for id := 0; id < nodes; id++ {
		if err := postBytes(fmt.Sprintf("http://127.0.0.1:%d/start", basePort+1000+id), nil); err != nil {
			return run, fmt.Errorf("start node %d: %w", id, err)
		}
	}
	results, err := pollResults(nodes, basePort+1000, timeout)
	run.FinishedAt = time.Now()
	run.Results = results
	run.Consistent = resultsConsistent(results)
	run.Success = err == nil && run.Consistent
	if err != nil {
		return run, err
	}
	return run, nil
}

func waitForHTTP(nodes, baseHTTP int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ok := true
		for id := 0; id < nodes; id++ {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", baseHTTP+id))
			if err != nil {
				ok = false
				break
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nodes did not become healthy within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pollResults(nodes, baseHTTP int, timeout time.Duration) ([]Result, error) {
	deadline := time.Now().Add(timeout)
	for {
		results := make([]Result, 0, nodes)
		for id := 0; id < nodes; id++ {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/result", baseHTTP+id))
			if err != nil {
				break
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				break
			}
			var result Result
			if err := json.Unmarshal(body, &result); err != nil {
				return results, err
			}
			results = append(results, result)
		}
		if len(results) == nodes {
			return results, nil
		}
		if time.Now().After(deadline) {
			return results, fmt.Errorf("timed out waiting for %d node results; got %d", nodes, len(results))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func postBytes(url string, body []byte) error {
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func syntheticTx(i, size int) []byte {
	if size < 8 {
		size = 8
	}
	out := make([]byte, size)
	for j := range out {
		out[j] = byte((i + j) % 251)
	}
	return out
}

func resultsConsistent(results []Result) bool {
	if len(results) == 0 {
		return false
	}
	batchID := results[0].BatchID
	joined := strings.Join(results[0].Plaintexts, "\n")
	for _, result := range results[1:] {
		if result.BatchID != batchID || strings.Join(result.Plaintexts, "\n") != joined {
			return false
		}
	}
	return true
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

func parseEvalFault(raw string) (map[uint64]string, error) {
	out := make(map[uint64]string)
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("fault must be id:mode, got %q", entry)
		}
		id, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return nil, err
		}
		out[id] = parts[1]
	}
	return out, nil
}

func writeEvalOutputs(outDir string, runs []EvalRun) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(outDir, "summary.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"run_id", "nodes", "threshold", "bmax", "batch_size", "tx_size", "success", "consistent", "node_id", "ciphertexts", "slot_ms", "acs_ms", "commit_to_plaintext_ms", "outbound_acs_bytes", "outbound_share_bytes"}); err != nil {
		return err
	}
	for _, run := range runs {
		for _, result := range run.Results {
			record := []string{
				run.RunID,
				strconv.Itoa(run.Nodes),
				strconv.Itoa(run.Threshold),
				strconv.Itoa(run.BMax),
				strconv.Itoa(run.BatchSize),
				strconv.Itoa(run.TxSize),
				strconv.FormatBool(run.Success),
				strconv.FormatBool(run.Consistent),
				strconv.FormatUint(result.NodeID, 10),
				strconv.Itoa(result.Ciphertexts),
				strconv.FormatInt(result.Metrics.TotalSlotMS, 10),
				strconv.FormatInt(result.Metrics.ACSMS, 10),
				strconv.FormatInt(result.Metrics.CommitToPlaintextMS, 10),
				strconv.FormatInt(result.Metrics.OutboundBytes["acs"], 10),
				strconv.FormatInt(result.Metrics.OutboundBytes["share"], 10),
			}
			if err := w.Write(record); err != nil {
				return err
			}
		}
	}
	return nil
}

func newNode(cfg ConfigFile, id uint64, faults FaultConfig) (*Node, error) {
	var self NodeConfig
	peers := make(map[uint64]NodeConfig)
	var ids []uint64
	for _, n := range cfg.Nodes {
		ids = append(ids, n.ID)
		peers[n.ID] = n
		if n.ID == id {
			self = n
		}
	}
	if self.ConsensusAddr == "" {
		return nil, fmt.Errorf("node id %d not found in config", id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	suite := newSuite()
	crsSeed, err := hex.DecodeString(strings.TrimPrefix(cfg.CRSSeedHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode crs seed: %w", err)
	}
	btd := be.NewBTDFromSeed(suite, cfg.BMax, crsSeed)
	pk, err := unmarshalPointHex(suite, cfg.PublicKeyHex)
	if err != nil {
		return nil, err
	}
	var secret be.SecretShare
	foundShare := false
	for _, s := range cfg.Shares {
		if s.OperatorID == int(id) {
			scalar, err := unmarshalScalarHex(suite, s.ScalarHex)
			if err != nil {
				return nil, err
			}
			secret = be.SecretShare{OperatorID: s.OperatorID, Share: &share.PriShare{I: uint32(s.OperatorID), V: scalar}}
			foundShare = true
			break
		}
	}
	if !foundShare {
		return nil, fmt.Errorf("secret share for node %d not found", id)
	}
	cluster := be.NewNode(btd, pk, secret, cfg.N, cfg.Threshold)
	node := &Node{
		cfg:         cfg,
		self:        self,
		nodeIDs:     ids,
		peers:       peers,
		cluster:     cluster,
		secret:      secret,
		suite:       suite,
		faults:      faults,
		seenPending: make(map[string]bool),
		seenShares:  make(map[string]bool),
		metrics: Metrics{
			SharesNeededPerSub: cfg.Threshold,
			OutboundMessages:   make(map[string]int),
			InboundMessages:    make(map[string]int),
			OutboundBytes:      make(map[string]int64),
			InboundBytes:       make(map[string]int64),
		},
	}
	node.slot = hbbft.NewSlotACS(hbbft.SlotConfig{
		Config: hbbft.Config{N: cfg.N, F: (cfg.N - 1) / 3, ID: id, Nodes: ids, BatchSize: cfg.BMax},
		Slot:   cfg.Slot,
	})
	return node, nil
}

func parseFaults(raw string, delay time.Duration) FaultConfig {
	faults := FaultConfig{Delay: delay}
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "":
		case "omit-proposal":
			faults.OmitProposal = true
		case "withhold-share":
			faults.WithholdShare = true
		case "corrupt-share":
			faults.CorruptShare = true
		default:
			log.Printf("ignoring unknown fault mode %q", part)
		}
	}
	return faults
}

func (n *Node) listenConsensus() error {
	ln, err := net.Listen("tcp", n.self.ConsensusAddr)
	if err != nil {
		return err
	}
	log.Printf("node %d consensus listening on %s", n.self.ID, n.self.ConsensusAddr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("accept: %v", err)
				continue
			}
			go n.handleConn(conn)
		}
	}()
	return nil
}

func (n *Node) listenHTTP(outPath string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": n.self.ID})
	})
	mux.HandleFunc("/tx", n.handleSubmitTx)
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		if err := n.startConsensus(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
	})
	mux.HandleFunc("/result", func(w http.ResponseWriter, _ *http.Request) {
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.result == nil {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		writeJSON(w, http.StatusOK, n.result)
	})
	server := &http.Server{Addr: n.self.HTTPAddr, Handler: mux}
	log.Printf("node %d http listening on http://%s", n.self.ID, n.self.HTTPAddr)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
		}
	}()
	if outPath != "" {
		go n.writeResultWhenReady(outPath)
	}
	return nil
}

func (n *Node) handleSubmitTx(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
		var req struct {
			RawTx string `json:"raw_tx"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		raw, err = decodeHexMaybe(req.RawTx)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	n.mu.Lock()
	index := len(n.pending) % n.cfg.BMax
	n.mu.Unlock()
	ct, err := n.cluster.EncryptTx(raw, index, n.cfg.ClusterID, n.cfg.Slot)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	encoded, err := ct.MarshalBinary()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hash := hashHex(encoded)
	n.mu.Lock()
	if !n.seenPending[hash] {
		n.pending = append(n.pending, WireCiphertext{Hash: hash, Data: encoded})
		n.seenPending[hash] = true
		n.metrics.SubmittedTxs++
		n.metrics.SubmittedBytes += len(raw)
	}
	n.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash, "index": index, "ciphertext_bytes": len(encoded)})
}

func (n *Node) startConsensus() error {
	var err error
	n.startOnce.Do(func() {
		start := time.Now()
		n.mu.Lock()
		batch := append([]WireCiphertext(nil), n.pending...)
		if n.faults.OmitProposal {
			batch = nil
		}
		n.metrics.SlotStartUnixNano = start.UnixNano()
		n.metrics.ProposalTxs = len(batch)
		n.mu.Unlock()
		var buf bytes.Buffer
		err = gob.NewEncoder(&buf).Encode(batch)
		if err != nil {
			return
		}
		log.Printf("node %d starting slot %d with %d encrypted txs", n.self.ID, n.cfg.Slot, len(batch))
		err = n.slot.InputBatch(buf.Bytes())
		if err != nil {
			return
		}
		n.drainACSMessages()
		n.tryOutput()
	})
	return err
}

func (n *Node) handleConn(conn net.Conn) {
	defer conn.Close()
	data, err := io.ReadAll(conn)
	if err != nil {
		log.Printf("read envelope: %v", err)
		return
	}
	var env WireEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&env); err != nil {
		log.Printf("decode envelope: %v", err)
		return
	}
	n.recordInbound(env.Kind, len(data))
	switch env.Kind {
	case "acs":
		if env.ACS == nil {
			log.Printf("nil acs message from %d", env.From)
			return
		}
		if err := n.slot.HandleMessage(env.From, env.ACS); err != nil {
			if isBenignDuplicate(err) {
				return
			}
			log.Printf("handle acs from %d: %v", env.From, err)
			return
		}
		n.drainACSMessages()
		n.tryOutput()
	case "share":
		if env.Share == nil {
			log.Printf("nil share from %d", env.From)
			return
		}
		if err := n.addWireShare(*env.Share); err != nil {
			log.Printf("share from %d: %v", env.From, err)
			return
		}
		n.tryCombine()
	default:
		log.Printf("unknown envelope kind %q from %d", env.Kind, env.From)
	}
}

func isBenignDuplicate(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "received multiple readys") ||
		strings.Contains(msg, "received multiple echos")
}

func (n *Node) drainACSMessages() {
	for _, msg := range n.slot.Messages() {
		slotMsg, ok := msg.Payload.(*hbbft.SlotMessage)
		if !ok {
			log.Printf("unexpected slot payload %T", msg.Payload)
			continue
		}
		n.sendEnvelope(msg.To, WireEnvelope{From: n.self.ID, Kind: "acs", Slot: n.cfg.Slot, ACS: slotMsg})
	}
}

func (n *Node) tryOutput() {
	out := n.slot.Output()
	if out == nil {
		return
	}
	decisionAt := time.Now()
	var encrypted []be.Ciphertext
	seen := make(map[string]bool)
	for _, accepted := range out.OrderedBatches {
		var batch []WireCiphertext
		if err := gob.NewDecoder(bytes.NewReader(accepted.Batch)).Decode(&batch); err != nil {
			log.Printf("decode accepted batch from %d: %v", accepted.ProposerID, err)
			return
		}
		for _, item := range batch {
			if seen[item.Hash] {
				continue
			}
			seen[item.Hash] = true
			ct, err := n.cluster.UnmarshalCiphertext(item.Data)
			if err != nil {
				log.Printf("decode ciphertext %s: %v", item.Hash, err)
				return
			}
			encrypted = append(encrypted, ct)
		}
	}
	if len(encrypted) == 0 {
		log.Printf("node %d agreed on empty encrypted batch", n.self.ID)
		return
	}
	plan, err := n.cluster.PlanBatch(encrypted)
	if err != nil {
		log.Printf("plan batch: %v", err)
		return
	}
	planAt := time.Now()
	n.mu.Lock()
	if n.planned {
		n.mu.Unlock()
		return
	}
	n.plan = plan
	n.planned = true
	n.metrics.ACSDecisionUnixNano = decisionAt.UnixNano()
	n.metrics.PlanDoneUnixNano = planAt.UnixNano()
	n.metrics.AgreedCiphertexts = len(encrypted)
	n.metrics.SubBatches = len(plan.SubBatches)
	if n.metrics.SlotStartUnixNano != 0 {
		n.metrics.ACSMS = decisionAt.Sub(time.Unix(0, n.metrics.SlotStartUnixNano)).Milliseconds()
	}
	n.metrics.PlanMS = planAt.Sub(decisionAt).Milliseconds()
	n.mu.Unlock()
	log.Printf("node %d ACS decided %d ciphertexts; batch %s has %d sub-batches", n.self.ID, len(encrypted), hex.EncodeToString(plan.BatchID[:]), len(plan.SubBatches))
	shareStart := time.Now()
	for subBatchID := range plan.SubBatches {
		if n.faults.WithholdShare {
			continue
		}
		d, err := n.cluster.MakeShare(n.secret, plan, subBatchID)
		if err != nil {
			log.Printf("make share %d: %v", subBatchID, err)
			return
		}
		if err := n.addShare(d); err != nil {
			log.Printf("add own share: %v", err)
			return
		}
		wire, err := n.marshalShare(d)
		if err != nil {
			log.Printf("marshal share: %v", err)
			return
		}
		if n.faults.CorruptShare {
			wire.PointHex = corruptHex(wire.PointHex)
		}
		for _, peer := range n.nodeIDs {
			if peer != n.self.ID {
				n.sendEnvelope(peer, WireEnvelope{From: n.self.ID, Kind: "share", Slot: n.cfg.Slot, Share: &wire})
			}
		}
	}
	n.mu.Lock()
	n.metrics.SharesDoneUnixNano = time.Now().UnixNano()
	n.metrics.ShareGenerationMS = time.Since(shareStart).Milliseconds()
	n.mu.Unlock()
	n.tryCombine()
}

func (n *Node) tryCombine() {
	n.mu.Lock()
	if n.result != nil || !n.planned {
		n.mu.Unlock()
		return
	}
	plan := n.plan
	shares := append([]be.DecryptionShare(nil), n.shares...)
	n.mu.Unlock()
	if !hasThresholdPerSubBatch(shares, plan, n.cfg.Threshold) {
		return
	}
	thresholdAt := time.Now()
	results, err := n.cluster.CombineShares(plan, shares)
	if err != nil {
		log.Printf("combine shares: %v", err)
		return
	}
	combineAt := time.Now()
	plaintexts := make([]string, len(results))
	for i, r := range results {
		if r.Err != nil || !r.HashOK {
			plaintexts[i] = "ERROR:" + r.Err.Error()
			continue
		}
		plaintexts[i] = "0x" + hex.EncodeToString(r.RawTx)
	}
	result := &Result{
		Slot:        n.cfg.Slot,
		NodeID:      n.self.ID,
		BatchID:     hex.EncodeToString(plan.BatchID[:]),
		Ciphertexts: len(results),
		Plaintexts:  plaintexts,
	}
	n.mu.Lock()
	if n.result == nil {
		n.metrics.ThresholdUnixNano = thresholdAt.UnixNano()
		n.metrics.CombineDoneUnixNano = combineAt.UnixNano()
		if n.metrics.ACSDecisionUnixNano != 0 {
			n.metrics.CommitToPlaintextMS = combineAt.Sub(time.Unix(0, n.metrics.ACSDecisionUnixNano)).Milliseconds()
		}
		if n.metrics.SlotStartUnixNano != 0 {
			n.metrics.TotalSlotMS = combineAt.Sub(time.Unix(0, n.metrics.SlotStartUnixNano)).Milliseconds()
		}
		result.LatencyMS = n.metrics.TotalSlotMS
		result.Metrics = n.metrics.snapshot()
		n.result = result
		log.Printf("node %d decrypted batch %s with %d plaintext txs", n.self.ID, result.BatchID, len(result.Plaintexts))
	}
	n.mu.Unlock()
}

func (n *Node) addWireShare(w WireShare) error {
	batchID, err := hex.DecodeString(w.BatchIDHex)
	if err != nil {
		return err
	}
	if len(batchID) != 32 {
		return fmt.Errorf("batch id has %d bytes", len(batchID))
	}
	point, err := unmarshalPointHex(n.suite, w.PointHex)
	if err != nil {
		return err
	}
	var id [32]byte
	copy(id[:], batchID)
	return n.addShare(be.DecryptionShare{
		OperatorID: w.OperatorID,
		BatchID:    id,
		SubBatchID: w.SubBatchID,
		Share:      &share.PubShare{I: uint32(w.OperatorID), V: point},
	})
}

func (n *Node) addShare(d be.DecryptionShare) error {
	key := fmt.Sprintf("%x/%d/%d", d.BatchID, d.SubBatchID, d.OperatorID)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seenShares[key] {
		return nil
	}
	n.seenShares[key] = true
	n.shares = append(n.shares, d)
	n.metrics.SharesAccepted++
	if d.OperatorID == int(n.self.ID) {
		n.metrics.SharesGenerated++
	}
	if n.metrics.FirstShareUnixNano == 0 {
		n.metrics.FirstShareUnixNano = time.Now().UnixNano()
	}
	return nil
}

func (n *Node) marshalShare(d be.DecryptionShare) (WireShare, error) {
	pointHex, err := marshalPointHex(d.Share.V)
	if err != nil {
		return WireShare{}, err
	}
	return WireShare{
		OperatorID: d.OperatorID,
		BatchIDHex: hex.EncodeToString(d.BatchID[:]),
		SubBatchID: d.SubBatchID,
		PointHex:   pointHex,
	}, nil
}

func (n *Node) sendEnvelope(to uint64, env WireEnvelope) {
	peer, ok := n.peers[to]
	if !ok {
		log.Printf("unknown peer %d", to)
		return
	}
	go func() {
		if n.faults.Delay > 0 {
			time.Sleep(n.faults.Delay)
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(env); err != nil {
			log.Printf("encode %s to %d failed: %v", env.Kind, to, err)
			return
		}
		data := buf.Bytes()
		var lastErr error
		for attempt := 0; attempt < 20; attempt++ {
			conn, err := net.DialTimeout("tcp", peer.ConsensusAddr, 500*time.Millisecond)
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				_, err = conn.Write(data)
				_ = conn.Close()
				if err == nil {
					n.recordOutbound(env.Kind, len(data))
					return
				}
			}
			lastErr = err
			time.Sleep(100 * time.Millisecond)
		}
		log.Printf("send %s to %d failed: %v", env.Kind, to, lastErr)
	}()
}

func (n *Node) recordInbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.InboundMessages[kind]++
	n.metrics.InboundBytes[kind] += int64(size)
}

func (n *Node) recordOutbound(kind string, size int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.metrics.OutboundMessages[kind]++
	n.metrics.OutboundBytes[kind] += int64(size)
}

func (m Metrics) snapshot() Metrics {
	out := m
	out.OutboundMessages = cloneIntMap(m.OutboundMessages)
	out.InboundMessages = cloneIntMap(m.InboundMessages)
	out.OutboundBytes = cloneInt64Map(m.OutboundBytes)
	out.InboundBytes = cloneInt64Map(m.InboundBytes)
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func corruptHex(s string) string {
	if s == "" {
		return s
	}
	if s[0] == '0' {
		return "1" + s[1:]
	}
	return "0" + s[1:]
}

func (n *Node) writeResultWhenReady(path string) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		n.mu.Lock()
		result := n.result
		n.mu.Unlock()
		if result == nil {
			continue
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Printf("marshal result: %v", err)
			return
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			log.Printf("write result: %v", err)
		}
		return
	}
}

func hasThresholdPerSubBatch(shares []be.DecryptionShare, plan be.BatchPlan, threshold int) bool {
	counts := make(map[int]map[int]bool)
	for _, d := range shares {
		if d.BatchID != plan.BatchID {
			continue
		}
		if counts[d.SubBatchID] == nil {
			counts[d.SubBatchID] = make(map[int]bool)
		}
		counts[d.SubBatchID][d.OperatorID] = true
	}
	for i := range plan.SubBatches {
		if len(counts[i]) < threshold {
			return false
		}
	}
	return true
}

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
	return cfg, nil
}

func newSuite() curves.Suite {
	return curves.NewSuite(kilic.NewBLS12381Suite())
}

func marshalPointHex(p kyber.Point) (string, error) {
	b, err := p.MarshalBinary()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func unmarshalPointHex(suite curves.Suite, h string) (kyber.Point, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil {
		return nil, err
	}
	p := suite.G1().Point()
	if err := p.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return p, nil
}

func marshalScalarHex(s kyber.Scalar) (string, error) {
	b, err := s.MarshalBinary()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func unmarshalScalarHex(suite curves.Suite, h string) (kyber.Scalar, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil {
		return nil, err
	}
	s := suite.G1().Scalar()
	if err := s.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return s, nil
}

func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func decodeHexMaybe(s string) ([]byte, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "0x"))
	if s == "" {
		return nil, fmt.Errorf("empty tx")
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	if _, err := strconv.ParseUint(s[:1], 16, 8); err != nil {
		return []byte(s), nil
	}
	return hex.DecodeString(s)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func registerGobTypes() {
	gob.Register(&hbbft.SlotMessage{})
	gob.Register(&hbbft.ACSMessage{})
	gob.Register(&hbbft.BroadcastMessage{})
	gob.Register(&hbbft.ProofRequest{})
	gob.Register(&hbbft.EchoRequest{})
	gob.Register(&hbbft.ReadyRequest{})
	gob.Register(&hbbft.AgreementMessage{})
	gob.Register(&hbbft.BvalRequest{})
	gob.Register(&hbbft.AuxRequest{})
}
