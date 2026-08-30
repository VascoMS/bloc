package app

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type remoteEvalOptions struct {
	ConfigPath          string
	OutDir              string
	ExperimentID        string
	FirstSlot           uint64
	BatchSize           int
	TxSize              int
	TxGas               uint64
	TxSource            string
	MempoolURL          string
	FeeStart            uint64
	FeeStep             uint64
	Warmups             int
	Repetitions         int
	RepetitionBlocks    int
	MeasurementBlock    int
	PlannedScenarioRuns int
	Seed                int64
	Timeout             time.Duration
	Deadline            time.Duration
	ImageTag            string
	GitCommit           string
	FinalCampaign       bool
}

type remoteEvalConfig struct {
	Nodes       []remoteEvalNode  `json:"nodes"`
	Endpoints   []string          `json:"endpoints,omitempty"`
	NodeCount   int               `json:"node_count,omitempty"`
	Threshold   int               `json:"threshold,omitempty"`
	BMax        int               `json:"bmax,omitempty"`
	Network     string            `json:"network,omitempty"`
	StreamMode  string            `json:"stream_mode,omitempty"`
	Deployment  map[string]string `json:"deployment,omitempty"`
	InitialSlot uint64            `json:"initial_slot,omitempty"`
	Corpus      corpusProvenance  `json:"corpus,omitempty"`
}

type remoteEvalNode struct {
	ID     int    `json:"id"`
	URL    string `json:"url"`
	Label  string `json:"label,omitempty"`
	Region string `json:"region,omitempty"`
	Zone   string `json:"zone,omitempty"`
}

func evalRemote(args []string) error {
	options, err := parseRemoteEvalOptions(args)
	if err != nil {
		return err
	}
	cfg, err := readRemoteEvalConfig(options.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.NodeCount == 0 {
		cfg.NodeCount = len(cfg.Nodes)
	}
	if cfg.NodeCount != len(cfg.Nodes) {
		return fmt.Errorf("node_count=%d does not match %d configured nodes", cfg.NodeCount, len(cfg.Nodes))
	}
	if cfg.NodeCount < 4 {
		return fmt.Errorf("node_count must be >= 4")
	}
	if cfg.Threshold == 0 {
		f := (cfg.NodeCount - 1) / 3
		cfg.Threshold = 2*f + 1
	}
	if cfg.BMax == 0 {
		cfg.BMax = options.BatchSize
	}
	if err := validateRemoteEvidenceScenario(cfg, options.BatchSize); err != nil {
		return err
	}
	if options.FinalCampaign {
		if err := validateFinalCampaignTxSource(options.TxSource, options.MempoolURL); err != nil {
			return err
		}
	}
	if options.TxSource == "mock-encrypted-corpus" {
		if err := validateCorpusProvenance(cfg.Corpus, cfg.BMax, options.BatchSize); err != nil {
			return err
		}
	}
	if cfg.Network == "" {
		cfg.Network = "libp2p"
	}
	initialSlot := cfg.InitialSlot
	if initialSlot == 0 {
		initialSlot = 1
	}
	if options.FirstSlot == 0 {
		options.FirstSlot = initialSlot
	}
	if options.OutDir == "" {
		options.OutDir = filepath.Join("results", "distributed", options.ExperimentID)
	}
	if err := prepareSuiteOutputDir(options.OutDir); err != nil {
		return err
	}
	client, transport := newEvalHTTPClient()
	defer transport.CloseIdleConnections()
	if err := waitForRemoteHTTP(client, cfg.Nodes, options.Timeout); err != nil {
		return err
	}
	scenario := evalScenario{ID: fmt.Sprintf("remote-n%d-b%d-libp2p", cfg.NodeCount, options.BatchSize), Nodes: cfg.NodeCount, Threshold: cfg.Threshold, BatchSize: options.BatchSize, Network: cfg.Network}
	plannedRuns := options.Warmups + options.Repetitions
	manifest := suiteManifest{
		SchemaVersion:       suiteSchemaVersion,
		ExperimentID:        options.ExperimentID,
		Profile:             "distributed-remote",
		Status:              "running",
		StartedAt:           time.Now().UTC(),
		Command:             append([]string{"eval-remote"}, args...),
		Warmups:             options.Warmups,
		Repetitions:         options.Repetitions,
		RepetitionBlocks:    options.RepetitionBlocks,
		PlannedRuns:         plannedRuns,
		PlannedScenarioRuns: map[string]int{scenario.ID: options.PlannedScenarioRuns},
		Seed:                options.Seed,
		BMax:                cfg.BMax,
		TxSize:              options.TxSize,
		TxGas:               options.TxGas,
		TxSource:            options.TxSource,
		TxSourceMeta:        txSourceManifestMeta(options.TxSource, options.MempoolURL),
		FeeStartWei:         options.FeeStart,
		FeeStepWei:          options.FeeStep,
		Timeout:             options.Timeout.String(),
		Deadline:            options.Deadline.String(),
		Scenarios:           []evalScenario{scenario},
		ExecutionMode:       "remote",
		StreamMode:          cfg.StreamMode,
		Schedule:            "sequential-remote-slots",
		Deployment:          cfg.Deployment,
		RemoteEndpoints:     cfg.Nodes,
		ImageTag:            options.ImageTag,
		GitCommit:           options.GitCommit,
	}
	if options.TxSource == "mock-encrypted-corpus" {
		identity := corpusIdentityForCount(cfg.Corpus, options.BatchSize)
		manifest.CorpusByScenario = map[string]runCorpusIdentity{scenario.ID: identity}
	}
	if err := writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest); err != nil {
		return err
	}
	runsFile, err := os.Create(filepath.Join(options.OutDir, "runs.jsonl"))
	if err != nil {
		return err
	}
	defer runsFile.Close()
	writer := bufio.NewWriter(runsFile)
	var runs []EvalRun
	orderIndex := 0
	slotID := options.FirstSlot
	for _, phase := range []struct {
		name  string
		count int
	}{{"warmup", options.Warmups}, {"measured", options.Repetitions}} {
		for iteration := 1; iteration <= phase.count; iteration++ {
			orderIndex++
			runID := fmt.Sprintf("%s-r%03d-%s", phase.name, iteration, scenario.ID)
			corpus, err := buildSubmissionCorpusForSource(scenario, suiteOptions{TxSize: options.TxSize, TxGas: options.TxGas, TxSource: options.TxSource, FeeStart: options.FeeStart, FeeStep: options.FeeStep})
			if err != nil {
				return err
			}
			prepare := true
			run, runErr := runRemoteSlot(client, options.OutDir, runID, cfg, scenario, phase.name, iteration, orderIndex, slotID, corpus, options, prepare)
			run.ScheduleSeed = options.Seed
			run.PlannedScenarioRuns = options.PlannedScenarioRuns
			run.BlockIteration = iteration
			if phase.name == "measured" {
				if options.MeasurementBlock > 0 {
					run.MeasurementBlock = options.MeasurementBlock
					run.BlockIteration = iteration
				} else {
					perBlock := options.Repetitions / options.RepetitionBlocks
					run.MeasurementBlock = (iteration-1)/perBlock + 1
					run.BlockIteration = (iteration-1)%perBlock + 1
				}
			}
			if runErr != nil {
				run.Error = runErr.Error()
				run.Success = false
			}
			classifyRunOutcome(&run, runErr, options.Deadline)
			runs = append(runs, run)
			manifest.RunOrder = append(manifest.RunOrder, fmt.Sprintf("%s/block-%d/block-iteration-%d/%s/slot-%d", run.Phase, run.MeasurementBlock, run.BlockIteration, run.ScenarioID, run.Slot))
			encoded, err := json.Marshal(run)
			if err != nil {
				return err
			}
			if _, err := writer.Write(append(encoded, '\n')); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			if err := writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest); err != nil {
				return err
			}
			fmt.Printf("%s iteration=%d scenario=%s slot=%d success=%t critical_node=%d total_us=%d\n", phase.name, iteration, scenario.ID, slotID, run.Success, run.CriticalNodeID, criticalTotalUS(run))
			slotID++
		}
	}
	manifest.ACSTraceSchema, err = acsTraceSchemaForRuns(runs)
	if err != nil {
		return err
	}
	if err := writeSuiteOutputs(options.OutDir, []evalScenario{scenario}, runs, manifest); err != nil {
		return err
	}
	collectionComplete := suiteCollectionComplete(plannedRuns, runs)
	manifest.Valid = collectionComplete
	manifest.FinishedAt = time.Now().UTC()
	if collectionComplete {
		manifest.Status = "complete"
	} else {
		manifest.Status = "invalid"
		manifest.InvalidReason = "the planned remote schedule was not fully retained"
	}
	if err := writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest); err != nil {
		return err
	}
	if !collectionComplete {
		return fmt.Errorf("remote suite failed: %s", manifest.InvalidReason)
	}
	return nil
}

func validateRemoteEvidenceScenario(cfg remoteEvalConfig, batchSize int) error {
	switch cfg.NodeCount {
	case 4, 7, 10:
	default:
		return fmt.Errorf("node_count must be one of 4, 7, or 10")
	}
	switch batchSize {
	case 8, 32, 128, 512:
	default:
		return fmt.Errorf("batch-size must be one of 8, 32, 128, or 512")
	}
	if batchSize > cfg.BMax {
		return fmt.Errorf("batch-size %d exceeds configured BMax %d", batchSize, cfg.BMax)
	}
	return nil
}

func parseRemoteEvalOptions(args []string) (remoteEvalOptions, error) {
	options := remoteEvalOptions{}
	fs := flag.NewFlagSet("eval-remote", flag.ContinueOnError)
	fs.StringVar(&options.ConfigPath, "config", "remote-eval.json", "remote evaluator config with node HTTP endpoints")
	fs.StringVar(&options.OutDir, "out-dir", "", "experiment directory; defaults below results/distributed")
	fs.StringVar(&options.ExperimentID, "experiment-id", "", "stable experiment label")
	fs.Uint64Var(&options.FirstSlot, "first-slot", 0, "first slot id; defaults to config initial_slot or 1")
	fs.IntVar(&options.BatchSize, "batch-size", 8, "transactions per measured slot")
	fs.IntVar(&options.TxSize, "tx-size", 256, "minimum signed Ethereum transaction byte size")
	fs.Uint64Var(&options.TxGas, "tx-gas", 21000, "minimum gas limit used in generated transactions")
	fs.StringVar(&options.TxSource, "tx-source", "synthetic", "transaction source: synthetic, mock-placeholder, or mock-encrypted-corpus")
	fs.StringVar(&options.MempoolURL, "mempool-url", "", "mempool-il base URL for a mock source")
	fs.Uint64Var(&options.FeeStart, "fee-start-wei", 1000, "first generated effective fee per gas")
	fs.Uint64Var(&options.FeeStep, "fee-step-wei", 1, "generated fee increment")
	fs.IntVar(&options.Warmups, "warmups", 0, "warmup remote slots")
	fs.IntVar(&options.Repetitions, "repetitions", 1, "measured remote slots")
	fs.IntVar(&options.RepetitionBlocks, "repetition-blocks", 1, "balanced measured repetition blocks")
	fs.IntVar(&options.MeasurementBlock, "measurement-block", 0, "explicit enclosing campaign block ID")
	fs.IntVar(&options.PlannedScenarioRuns, "planned-scenario-runs", 0, "full planned measured count for this scenario")
	fs.Int64Var(&options.Seed, "seed", 20260621, "scenario-order seed recorded in artifacts")
	fs.DurationVar(&options.Timeout, "timeout", 30*time.Second, "per-run timeout")
	fs.DurationVar(&options.Deadline, "deadline", 12*time.Second, "successful consistent timing deadline")
	fs.StringVar(&options.ImageTag, "image-tag", "", "deployment image tag to record in manifest")
	fs.StringVar(&options.GitCommit, "git-commit", "", "git commit to record in manifest")
	fs.BoolVar(&options.FinalCampaign, "final-campaign", false, "require the immutable encrypted-corpus final-campaign contract")
	if err := fs.Parse(args); err != nil {
		return remoteEvalOptions{}, err
	}
	if options.ExperimentID == "" {
		options.ExperimentID = "distributed-" + time.Now().UTC().Format("20060102T150405Z")
	}
	if options.Warmups < 0 || options.Repetitions < 1 {
		return remoteEvalOptions{}, fmt.Errorf("warmups must be >= 0 and repetitions must be >= 1")
	}
	if options.RepetitionBlocks < 1 || options.Repetitions%options.RepetitionBlocks != 0 {
		return remoteEvalOptions{}, fmt.Errorf("repetitions must be divisible by repetition-blocks >= 1")
	}
	if options.MeasurementBlock < 0 {
		return remoteEvalOptions{}, fmt.Errorf("measurement-block must be non-negative")
	}
	if options.PlannedScenarioRuns == 0 {
		options.PlannedScenarioRuns = options.Repetitions
	}
	if options.PlannedScenarioRuns < options.Repetitions {
		return remoteEvalOptions{}, fmt.Errorf("planned-scenario-runs must be at least repetitions")
	}
	if options.Deadline <= 0 {
		return remoteEvalOptions{}, fmt.Errorf("deadline must be positive")
	}
	if options.BatchSize < 1 || options.TxSize < 1 {
		return remoteEvalOptions{}, fmt.Errorf("batch-size and tx-size must be positive")
	}
	if err := validateTxSource(options.TxSource, options.MempoolURL); err != nil {
		return remoteEvalOptions{}, err
	}
	if options.FinalCampaign {
		if err := validateFinalCampaignTxSource(options.TxSource, options.MempoolURL); err != nil {
			return remoteEvalOptions{}, err
		}
	}
	return options, nil
}

func readRemoteEvalConfig(path string) (remoteEvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return remoteEvalConfig{}, err
	}
	var cfg remoteEvalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return remoteEvalConfig{}, err
	}
	if len(cfg.Nodes) == 0 {
		for id, endpoint := range cfg.Endpoints {
			cfg.Nodes = append(cfg.Nodes, remoteEvalNode{ID: id, URL: endpoint})
		}
	}
	if len(cfg.Nodes) == 0 {
		return remoteEvalConfig{}, fmt.Errorf("remote config requires nodes or endpoints")
	}
	network := NetworkConfig{Mode: cfg.Network, StreamMode: cfg.StreamMode}
	normalizeNetworkConfig(&network)
	if err := validateNetworkConfig(network); err != nil {
		return remoteEvalConfig{}, err
	}
	cfg.Network = network.Mode
	cfg.StreamMode = network.StreamMode
	sort.Slice(cfg.Nodes, func(i, j int) bool { return cfg.Nodes[i].ID < cfg.Nodes[j].ID })
	for i := range cfg.Nodes {
		if cfg.Nodes[i].URL == "" {
			return remoteEvalConfig{}, fmt.Errorf("node %d has empty url", cfg.Nodes[i].ID)
		}
		cfg.Nodes[i].URL = strings.TrimRight(cfg.Nodes[i].URL, "/")
	}
	return cfg, nil
}

func waitForRemoteHTTP(client *http.Client, nodes []remoteEvalNode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var missing []int
		for _, node := range nodes {
			resp, err := client.Get(node.URL + "/healthz")
			if err != nil {
				missing = append(missing, node.ID)
				continue
			}
			drainAndClose(resp.Body)
			if resp.StatusCode != http.StatusOK {
				missing = append(missing, node.ID)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("remote nodes did not become healthy within %s; missing nodes: %v", timeout, missing)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runRemoteSlot(client *http.Client, outDir, runID string, cfg remoteEvalConfig, scenario evalScenario, phase string, iteration, orderIndex int, slotID uint64, corpus []evalSubmission, options remoteEvalOptions, prepare bool) (EvalRun, error) {
	run := EvalRun{RunID: runID, ScenarioID: scenario.ID, Phase: phase, Iteration: iteration, OrderIndex: orderIndex, Slot: slotID, Nodes: scenario.Nodes, Threshold: scenario.Threshold, BMax: cfg.BMax, BatchSize: scenario.BatchSize, TxSize: options.TxSize, TxGas: options.TxGas, TxSource: options.TxSource, Network: scenario.Network, StreamMode: cfg.StreamMode, StartedAt: time.Now(), Results: []Result{}}
	if options.TxSource == "mock-encrypted-corpus" {
		identity := corpusIdentityForCount(cfg.Corpus, scenario.BatchSize)
		run.Corpus = &identity
	}
	prepareStarted := time.Now()
	if prepare {
		if err := parallelNodes(len(cfg.Nodes), func(index int) error {
			return postJSON(client, cfg.Nodes[index].URL+"/slot/prepare", prepareRequestForScenario(slotID, scenario))
		}); err != nil {
			run.PrepareUS = time.Since(prepareStarted).Microseconds()
			run.FinishedAt = time.Now()
			return run, fmt.Errorf("prepare remote slot %d: %w", slotID, err)
		}
	}
	run.PrepareUS = time.Since(prepareStarted).Microseconds()

	submitStarted := time.Now()
	if !usesMempoolSource(options.TxSource) {
		byNode := make(map[int][]SubmitTxRequest)
		for _, item := range corpus {
			byNode[item.nodeID] = append(byNode[item.nodeID], item.request)
		}
		if err := parallelNodes(len(cfg.Nodes), func(index int) error {
			node := cfg.Nodes[index]
			for _, request := range byNode[node.ID] {
				if err := postJSON(client, fmt.Sprintf("%s/tx?slot=%d", node.URL, slotID), request); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			run.SubmissionUS = time.Since(submitStarted).Microseconds()
			run.FinishedAt = time.Now()
			return run, fmt.Errorf("submit remote transactions: %w", err)
		}
	}
	run.SubmissionUS = time.Since(submitStarted).Microseconds()

	harnessStarted := time.Now()
	if err := parallelNodes(len(cfg.Nodes), func(index int) error {
		return postBytes(client, fmt.Sprintf("%s/start?slot=%d", cfg.Nodes[index].URL, slotID), nil)
	}); err != nil {
		run.HarnessWallUS = time.Since(harnessStarted).Microseconds()
		run.FinishedAt = time.Now()
		return run, fmt.Errorf("start remote nodes: %w", err)
	}
	results, err := pollResultsForSlotWithURL(client, len(cfg.Nodes), slotID, options.Timeout, func(index int) string {
		return fmt.Sprintf("%s/result?slot=%d", cfg.Nodes[index].URL, slotID)
	})
	run.FinishedAt = time.Now()
	run.HarnessWallUS = time.Since(harnessStarted).Microseconds()
	run.Results = results
	run.ReceivedTxCount = receivedTxCount(results)
	run.StartSkewUS = resultStartSkewUS(results)
	run.CriticalNodeID = criticalNodeID(results)
	run.Consistent = resultsConsistent(results)
	run.Success = err == nil && run.Consistent
	if err == nil && options.TxSource == "mock-encrypted-corpus" && run.ReceivedTxCount != scenario.BatchSize {
		err = fmt.Errorf("received %d selected ciphertexts, expected exact corpus prefix %d", run.ReceivedTxCount, scenario.BatchSize)
		run.Success = false
	}
	writeRemoteRunArtifacts(outDir, run, cfg.Nodes)
	if err != nil {
		captureRemoteStatuses(client, outDir, runID, slotID, cfg.Nodes)
		return run, err
	}
	return run, nil
}

func receivedTxCount(results []Result) int {
	if len(results) == 0 {
		return 0
	}
	count := results[0].Metrics.SelectedCiphertexts
	for _, result := range results[1:] {
		if result.Metrics.SelectedCiphertexts != count {
			return -1
		}
	}
	return count
}

func writeRemoteRunArtifacts(outDir string, run EvalRun, nodes []remoteEvalNode) {
	runDir := filepath.Join(outDir, "runs", run.RunID)
	_ = os.MkdirAll(runDir, 0755)
	nodeMeta := make(map[string]remoteEvalNode)
	for _, node := range nodes {
		nodeMeta[strconv.Itoa(node.ID)] = node
	}
	_ = writeJSONFile(filepath.Join(runDir, "remote_nodes.json"), nodeMeta)
	for _, result := range run.Results {
		_ = writeJSONFile(filepath.Join(runDir, fmt.Sprintf("node-%d-result.json", result.NodeID)), result)
	}
}

func captureRemoteStatuses(client *http.Client, outDir, runID string, slotID uint64, nodes []remoteEvalNode) {
	statuses := make(map[string]json.RawMessage)
	for _, node := range nodes {
		resp, err := client.Get(fmt.Sprintf("%s/slot/status?slot=%d", node.URL, slotID))
		if err != nil {
			statuses[strconv.Itoa(node.ID)] = json.RawMessage(fmt.Sprintf("%q", err.Error()))
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		statuses[strconv.Itoa(node.ID)] = append(json.RawMessage(nil), body...)
	}
	_ = writeJSONFile(filepath.Join(outDir, "runs", runID, "slot-status.json"), statuses)
}
