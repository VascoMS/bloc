package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bloc-node/internal/app/ethdemo"
)

// EvalRun is one evaluator measurement row before it is expanded into JSON and
// CSV artifacts. It records both experiment inputs and every node result.
type EvalRun struct {
	RunID             string            `json:"run_id"`
	ScenarioID        string            `json:"scenario_id,omitempty"`
	Phase             string            `json:"phase,omitempty"`
	Iteration         int               `json:"iteration,omitempty"`
	OrderIndex        int               `json:"order_index,omitempty"`
	Slot              uint64            `json:"slot,omitempty"`
	ClusterGeneration int               `json:"cluster_generation,omitempty"`
	Nodes             int               `json:"nodes"`
	Threshold         int               `json:"threshold"`
	BMax              int               `json:"bmax"`
	BatchSize         int               `json:"batch_size"`
	TxSize            int               `json:"tx_size"`
	TxGas             uint64            `json:"tx_gas"`
	MaxDecryptedGas   uint64            `json:"max_decrypted_gas"`
	MaxDecryptedTxs   int               `json:"max_decrypted_txs"`
	Network           string            `json:"network"`
	Faults            map[uint64]string `json:"faults,omitempty"`
	Success           bool              `json:"success"`
	Consistent        bool              `json:"consistent"`
	Error             string            `json:"error,omitempty"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        time.Time         `json:"finished_at"`
	HarnessWallUS     int64             `json:"harness_wall_us,omitempty"`
	PrepareUS         int64             `json:"prepare_us,omitempty"`
	SubmissionUS      int64             `json:"submission_us,omitempty"`
	StartSkewUS       int64             `json:"start_skew_us,omitempty"`
	CriticalNodeID    uint64            `json:"critical_node_id,omitempty"`
	Results           []Result          `json:"results"`
}

func evalLocal(args []string) error {
	fs := flag.NewFlagSet("eval-local", flag.ExitOnError)
	nodes := fs.Int("nodes", 4, "number of operator processes")
	threshold := fs.Int("threshold", 0, "BTE threshold; defaults to 2f+1")
	bmax := fs.Int("bmax", 32, "BTE maximum batch size")
	batchSizesRaw := fs.String("batch-sizes", "8", "comma-separated transaction batch sizes")
	txSize := fs.Int("tx-size", 256, "minimum signed Ethereum transaction byte size")
	txGas := fs.Uint64("tx-gas", 21000, "minimum gas limit used in generated Ethereum transactions")
	feeStart := fs.Uint64("fee-start-wei", 1000, "first generated effective fee per gas in wei")
	feeStep := fs.Uint64("fee-step-wei", 1, "generated fee increment per transaction")
	maxDecryptedGas := fs.Uint64("max-decrypted-gas", 0, "maximum gas to decrypt per slot; 0 means uncapped")
	maxDecryptedTxs := fs.Int("max-decrypted-txs", 0, "maximum transactions to decrypt per slot; 0 means bmax")
	outDir := fs.String("out-dir", "results", "directory for run artifacts")
	basePort := fs.Int("base-port", 21000, "base port; HTTP uses base+1000 and libp2p uses base+2000")
	timeout := fs.Duration("timeout", 20*time.Second, "per-run timeout")
	faultRaw := fs.String("fault", "", "optional node fault as id:mode, e.g. 3:withhold-share or 0:omit-proposal")
	printMode := fs.String("print", "json", "stdout format: json or summary")
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
		run, err := runLocalExperiment(self, runDir, runID, *nodes, *threshold, *bmax, batchSize, *txSize, *txGas, *feeStart, *feeStep, *maxDecryptedGas, *maxDecryptedTxs, *basePort+(idx*2000), *timeout, *faultRaw)
		if err != nil {
			run.Error = err.Error()
		}
		runs = append(runs, run)
	}
	if err := writeEvalOutputs(*outDir, runs); err != nil {
		return err
	}
	switch *printMode {
	case "summary":
		printEvalSummary(runs, *outDir)
	case "json":
		encoded, _ := json.MarshalIndent(runs, "", "  ")
		fmt.Println(string(encoded))
	default:
		return fmt.Errorf("unknown print mode %q", *printMode)
	}
	return nil
}

func runLocalExperiment(self, runDir, runID string, nodes, threshold, bmax, batchSize, txSize int, txGas, feeStart, feeStep, maxDecryptedGas uint64, maxDecryptedTxs int, basePort int, timeout time.Duration, faultRaw string) (EvalRun, error) {
	run := EvalRun{
		RunID:           runID,
		Nodes:           nodes,
		Threshold:       threshold,
		BMax:            bmax,
		BatchSize:       batchSize,
		TxSize:          txSize,
		TxGas:           txGas,
		MaxDecryptedGas: maxDecryptedGas,
		MaxDecryptedTxs: maxDecryptedTxs,
		Network:         "libp2p",
		Faults:          make(map[uint64]string),
		StartedAt:       time.Now(),
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return run, err
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
	args := []string{"gen-config", "--nodes", strconv.Itoa(nodes), "--threshold", strconv.Itoa(run.Threshold), "--bmax", strconv.Itoa(bmax), "--base-http-port", strconv.Itoa(basePort + 1000), "--base-p2p-port", strconv.Itoa(basePort + 2000), "--max-decrypted-gas", strconv.FormatUint(maxDecryptedGas, 10), "--max-decrypted-txs", strconv.Itoa(maxDecryptedTxs), "--default-tx-gas", strconv.FormatUint(txGas, 10), "--out", configPath}
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
	client, clientTransport := newEvalHTTPClient()
	defer clientTransport.CloseIdleConnections()
	readinessTimeout := timeout
	if readinessTimeout < 15*time.Second {
		readinessTimeout = 15 * time.Second
	}
	if err := waitForHTTP(client, nodes, basePort+1000, readinessTimeout); err != nil {
		return run, err
	}
	for i := 0; i < batchSize; i++ {
		nodeID := i % nodes
		feeWei := strconv.FormatUint(feeStart+uint64(i)*feeStep, 10)
		rawTx, txSummary, err := ethdemo.Generate(i, txSize, txGas, feeWei, nodeID, uint64(i/nodes))
		if err != nil {
			return run, fmt.Errorf("generate ethereum tx %d: %w", i, err)
		}
		submit := SubmitTxRequest{
			RawTx:                 "0x" + fmt.Sprintf("%x", rawTx),
			Gas:                   txSummary.Gas,
			EffectiveFeePerGasWei: txSummary.EffectiveFeePerGasWei,
			From:                  txSummary.From,
			Nonce:                 txSummary.Nonce,
			Kind:                  "placeholder",
		}
		if err := postJSON(client, fmt.Sprintf("http://127.0.0.1:%d/tx", basePort+1000+nodeID), submit); err != nil {
			return run, fmt.Errorf("submit tx %d to node %d: %w", i, nodeID, err)
		}
	}
	harnessStart := time.Now()
	if err := startNodes(client, nodes, basePort+1000); err != nil {
		return run, err
	}
	results, err := pollResults(client, nodes, basePort+1000, timeout)
	run.FinishedAt = time.Now()
	run.HarnessWallUS = time.Since(harnessStart).Microseconds()
	run.Results = results
	run.StartSkewUS = resultStartSkewUS(results)
	run.CriticalNodeID = criticalNodeID(results)
	run.Consistent = resultsConsistent(results)
	run.Success = err == nil && run.Consistent
	if err != nil {
		return run, err
	}
	return run, nil
}

func startNodes(client *http.Client, nodes, baseHTTP int) error {
	type startResult struct {
		id  int
		err error
	}
	results := make(chan startResult, nodes)
	for id := 0; id < nodes; id++ {
		go func(id int) {
			results <- startResult{id: id, err: postBytes(client, fmt.Sprintf("http://127.0.0.1:%d/start", baseHTTP+id), nil)}
		}(id)
	}
	var failures []string
	for range nodes {
		result := <-results
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("node %d: %v", result.id, result.err))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("start nodes: %s", strings.Join(failures, "; "))
	}
	return nil
}

func waitForHTTP(client *http.Client, nodes, baseHTTP int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ok := true
		for id := 0; id < nodes; id++ {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", baseHTTP+id))
			if err != nil {
				ok = false
				break
			}
			drainAndClose(resp.Body)
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

func pollResults(client *http.Client, nodes, baseHTTP int, timeout time.Duration) ([]Result, error) {
	deadline := time.Now().Add(timeout)
	for {
		results := make([]Result, 0, nodes)
		for id := 0; id < nodes; id++ {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/result", baseHTTP+id))
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
			if !result.Metrics.MetricsFinalized {
				break
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

func resultStartSkewUS(results []Result) int64 {
	if len(results) < 2 {
		return 0
	}
	min, max := results[0].Metrics.SlotStartUnixNano, results[0].Metrics.SlotStartUnixNano
	for _, result := range results[1:] {
		stamp := result.Metrics.SlotStartUnixNano
		if stamp < min {
			min = stamp
		}
		if stamp > max {
			max = stamp
		}
	}
	return (max - min) / 1000
}

func criticalNodeID(results []Result) uint64 {
	var id uint64
	var max int64 = -1
	for _, result := range results {
		if result.Metrics.TotalSlotUS > max || (result.Metrics.TotalSlotUS == max && result.NodeID < id) {
			id = result.NodeID
			max = result.Metrics.TotalSlotUS
		}
	}
	return id
}

func postBytes(client *http.Client, url string, body []byte) error {
	resp, err := client.Post(url, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drainAndClose(resp.Body)
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func postJSON(client *http.Client, url string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drainAndClose(resp.Body)
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func newEvalHTTPClient() (*http.Client, *http.Transport) {
	transport := &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     10 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, transport
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

func resultsConsistent(results []Result) bool {
	if len(results) == 0 {
		return false
	}
	first := results[0]
	batchID := first.BatchID
	joined := strings.Join(first.Plaintexts, "\n")
	mergedSetHash := first.Materialized.MergedSetHash
	selectedGas := first.Materialized.SelectedGas
	plainHashes := strings.Join(first.Materialized.PlaintextHashes, "\n")
	ethHashes := strings.Join(first.Materialized.EthereumTxHashes, "\n")
	for _, result := range results[1:] {
		if result.BatchID != batchID ||
			strings.Join(result.Plaintexts, "\n") != joined ||
			result.Materialized.MergedSetHash != mergedSetHash ||
			result.Materialized.SelectedGas != selectedGas ||
			strings.Join(result.Materialized.PlaintextHashes, "\n") != plainHashes ||
			strings.Join(result.Materialized.EthereumTxHashes, "\n") != ethHashes {
			return false
		}
	}
	return true
}

func printEvalSummary(runs []EvalRun, outDir string) {
	for _, run := range runs {
		fmt.Printf("scenario=%s success=%t consistent=%t network=%s nodes=%d batch_size=%d\n", run.RunID, run.Success, run.Consistent, run.Network, run.Nodes, run.BatchSize)
		if run.Error != "" {
			fmt.Printf("  error=%s\n", run.Error)
			fmt.Printf("  output_dir=%s\n", outDir)
			continue
		}
		if len(run.Results) == 0 {
			fmt.Printf("  output_dir=%s\n", outDir)
			continue
		}
		result := run.Results[0]
		fmt.Printf("  batch_id=%s\n", result.BatchID)
		fmt.Printf("  agreed_lists=%d selected_txs=%d selected_gas=%d skipped=%d\n", result.Metrics.AgreedLists, result.Metrics.SelectedCiphertexts, result.Metrics.SelectedGas, result.Metrics.SkippedCiphertexts)
		fmt.Printf("  merged_set_hash=%s\n", result.Materialized.MergedSetHash)
		fmt.Printf("  ethereum_tx_hashes=%s\n", strings.Join(result.Materialized.EthereumTxHashes, ","))
		fmt.Printf("  total_slot_us=%d acs_us=%d merge_plan_us=%d share_generation_us=%d commit_to_plaintext_us=%d\n", result.Metrics.TotalSlotUS, result.Metrics.ACSUS, result.Metrics.MergePlanUS, result.Metrics.ShareGenerationUS, result.Metrics.CommitToPlaintextUS)
		fmt.Printf("  output_dir=%s\n", outDir)
	}
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
	if err := w.Write([]string{"run_id", "network", "nodes", "threshold", "bmax", "batch_size", "tx_size", "tx_gas", "max_decrypted_gas", "max_decrypted_txs", "success", "consistent", "node_id", "agreed_lists", "agreed_ciphertexts", "selected_ciphertexts", "skipped_ciphertexts", "selected_gas", "ciphertexts", "slot_ms", "acs_ms", "commit_to_plaintext_ms", "total_slot_us", "proposal_preparation_us", "acs_us", "merge_plan_us", "share_generation_us", "threshold_wait_us", "combine_us", "combine_attempts", "materialization_us", "commit_to_plaintext_us", "metrics_finalized", "outbound_acs_bytes", "outbound_share_bytes"}); err != nil {
		return err
	}
	for _, run := range runs {
		for _, result := range run.Results {
			record := []string{
				run.RunID,
				run.Network,
				strconv.Itoa(run.Nodes),
				strconv.Itoa(run.Threshold),
				strconv.Itoa(run.BMax),
				strconv.Itoa(run.BatchSize),
				strconv.Itoa(run.TxSize),
				strconv.FormatUint(run.TxGas, 10),
				strconv.FormatUint(run.MaxDecryptedGas, 10),
				strconv.Itoa(run.MaxDecryptedTxs),
				strconv.FormatBool(run.Success),
				strconv.FormatBool(run.Consistent),
				strconv.FormatUint(result.NodeID, 10),
				strconv.Itoa(result.Metrics.AgreedLists),
				strconv.Itoa(result.Metrics.AgreedCiphertexts),
				strconv.Itoa(result.Metrics.SelectedCiphertexts),
				strconv.Itoa(result.Metrics.SkippedCiphertexts),
				strconv.FormatUint(result.Metrics.SelectedGas, 10),
				strconv.Itoa(result.Ciphertexts),
				strconv.FormatInt(result.Metrics.TotalSlotMS, 10),
				strconv.FormatInt(result.Metrics.ACSMS, 10),
				strconv.FormatInt(result.Metrics.CommitToPlaintextMS, 10),
				strconv.FormatInt(result.Metrics.TotalSlotUS, 10),
				strconv.FormatInt(result.Metrics.ProposalPreparationUS, 10),
				strconv.FormatInt(result.Metrics.ACSUS, 10),
				strconv.FormatInt(result.Metrics.MergePlanUS, 10),
				strconv.FormatInt(result.Metrics.ShareGenerationUS, 10),
				strconv.FormatInt(result.Metrics.ThresholdWaitUS, 10),
				strconv.FormatInt(result.Metrics.CombineUS, 10),
				strconv.Itoa(result.Metrics.CombineAttempts),
				strconv.FormatInt(result.Metrics.MaterializationUS, 10),
				strconv.FormatInt(result.Metrics.CommitToPlaintextUS, 10),
				strconv.FormatBool(result.Metrics.MetricsFinalized),
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
