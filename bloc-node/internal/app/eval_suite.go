package app

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const suiteSchemaVersion = "bloc-eval-suite/v3"
const p99ContractedSamples = 1000

type evalScenario struct {
	ID        string `json:"id"`
	Nodes     int    `json:"nodes"`
	Threshold int    `json:"threshold"`
	BatchSize int    `json:"batch_size"`
	Network   string `json:"network"`
}

type suiteManifest struct {
	SchemaVersion       string            `json:"schema_version"`
	ExperimentID        string            `json:"experiment_id"`
	Profile             string            `json:"profile,omitempty"`
	Status              string            `json:"status"`
	Valid               bool              `json:"valid"`
	InvalidReason       string            `json:"invalid_reason,omitempty"`
	StartedAt           time.Time         `json:"started_at"`
	FinishedAt          time.Time         `json:"finished_at,omitempty"`
	Command             []string          `json:"command"`
	Seed                int64             `json:"seed"`
	Warmups             int               `json:"warmups"`
	Repetitions         int               `json:"repetitions"`
	RepetitionBlocks    int               `json:"repetition_blocks"`
	PlannedRuns         int               `json:"planned_runs"`
	PlannedScenarioRuns map[string]int    `json:"planned_scenario_runs"`
	BMax                int               `json:"bmax"`
	TxSize              int               `json:"tx_size"`
	TxGas               uint64            `json:"tx_gas"`
	TxSource            string            `json:"tx_source"`
	TxSourceMeta        map[string]any    `json:"tx_source_metadata,omitempty"`
	FeeStartWei         uint64            `json:"fee_start_wei"`
	FeeStepWei          uint64            `json:"fee_step_wei"`
	Timeout             string            `json:"timeout"`
	Deadline            string            `json:"deadline"`
	Scenarios           []evalScenario    `json:"scenarios"`
	RunOrder            []string          `json:"run_order"`
	ExecutionMode       string            `json:"execution_mode"`
	Schedule            string            `json:"schedule"`
	ClusterStartups     int               `json:"cluster_startups"`
	RecoveryRuns        int               `json:"recovery_runs"`
	Deployment          map[string]string `json:"deployment,omitempty"`
	RemoteEndpoints     []remoteEvalNode  `json:"remote_endpoints,omitempty"`
	ImageTag            string            `json:"image_tag,omitempty"`
	GitCommit           string            `json:"git_commit,omitempty"`
}

type suiteOptions struct {
	Profile          string
	NodeCountsRaw    string
	BatchSizesRaw    string
	BMax             int
	TxSize           int
	TxGas            uint64
	TxSource         string
	MempoolURL       string
	FeeStart         uint64
	FeeStep          uint64
	Warmups          int
	Repetitions      int
	RepetitionBlocks int
	Seed             int64
	BasePort         int
	Timeout          time.Duration
	Deadline         time.Duration
	OutDir           string
	ExperimentID     string
	ExecutionMode    string
	MaxRestarts      int
}

type clusterMeasurement struct {
	Nodes      int       `json:"nodes"`
	Threshold  int       `json:"threshold"`
	Generation int       `json:"generation"`
	Reason     string    `json:"reason"`
	StartedAt  time.Time `json:"started_at"`
	ReadyAt    time.Time `json:"ready_at,omitempty"`
	StartupUS  int64     `json:"startup_us"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

type metricSummary struct {
	Count       int      `json:"count"`
	Min         float64  `json:"min"`
	Mean        float64  `json:"mean"`
	StdDev      float64  `json:"sample_stddev"`
	P50         float64  `json:"p50"`
	P95         float64  `json:"p95"`
	P99         *float64 `json:"p99,omitempty"`
	P99Eligible bool     `json:"p99_eligible"`
	Max         float64  `json:"max"`
}

type scenarioSummary struct {
	ScenarioID               string                   `json:"scenario_id"`
	Nodes                    int                      `json:"nodes"`
	BatchSize                int                      `json:"batch_size"`
	Network                  string                   `json:"network"`
	Attempted                int                      `json:"attempted"`
	Completed                int                      `json:"completed"`
	Succeeded                int                      `json:"succeeded"`
	ConsistentWithinDeadline int                      `json:"consistent_within_deadline"`
	Failed                   int                      `json:"failed"`
	TimedOut                 int                      `json:"timed_out"`
	Metrics                  map[string]metricSummary `json:"metrics"`
}

var latencyMetricNames = []string{
	"total_slot_us",
	"proposal_preparation_us",
	"acs_us",
	"merge_plan_us",
	"acs_output_decode_us",
	"agreed_set_us",
	"merge_us",
	"ciphertext_decode_us",
	"batch_plan_us",
	"share_generation_us",
	"threshold_wait_us",
	"combine_us",
	"materialization_us",
	"commit_to_plaintext_us",
	"harness_wall_us",
	"start_skew_us",
}

func evalSuite(args []string) error {
	options, err := parseEvalSuiteOptions(args)
	if err != nil {
		return err
	}
	if options.Warmups < 0 || options.Repetitions < 1 {
		return fmt.Errorf("warmups must be >= 0 and repetitions must be >= 1")
	}
	if options.RepetitionBlocks < 1 || options.Repetitions%options.RepetitionBlocks != 0 {
		return fmt.Errorf("repetitions must be divisible by repetition-blocks >= 1")
	}
	if options.Deadline <= 0 {
		return fmt.Errorf("deadline must be positive")
	}
	if options.BMax < 1 || options.TxSize < 1 {
		return fmt.Errorf("bmax and tx-size must be positive")
	}
	if err := validateTxSource(options.TxSource, options.MempoolURL); err != nil {
		return err
	}
	nodeCounts, err := parseIntList(options.NodeCountsRaw)
	if err != nil {
		return fmt.Errorf("node-counts: %w", err)
	}
	batchSizes, err := parseIntList(options.BatchSizesRaw)
	if err != nil {
		return fmt.Errorf("batch-sizes: %w", err)
	}
	scenarios, err := buildScenarios(nodeCounts, batchSizes, options.BMax)
	if err != nil {
		return err
	}
	plannedRuns := len(scenarios) * (options.Warmups + options.Repetitions)
	plannedScenarioRuns := make(map[string]int, len(scenarios))
	for _, scenario := range scenarios {
		plannedScenarioRuns[scenario.ID] = options.Repetitions
	}
	printSuitePlan(options, scenarios, plannedRuns)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if options.ExperimentID == "" {
		options.ExperimentID = "m1-local-" + stamp
	}
	if options.OutDir == "" {
		options.OutDir = filepath.Join("results", "m1-local", options.ExperimentID)
	}
	if err := prepareSuiteOutputDir(options.OutDir); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	manifest := suiteManifest{
		SchemaVersion:       suiteSchemaVersion,
		ExperimentID:        options.ExperimentID,
		Profile:             options.Profile,
		Status:              "running",
		StartedAt:           time.Now().UTC(),
		Command:             append([]string{"eval-suite"}, args...),
		Seed:                options.Seed,
		Warmups:             options.Warmups,
		Repetitions:         options.Repetitions,
		RepetitionBlocks:    options.RepetitionBlocks,
		PlannedRuns:         plannedRuns,
		PlannedScenarioRuns: plannedScenarioRuns,
		BMax:                options.BMax,
		TxSize:              options.TxSize,
		TxGas:               options.TxGas,
		TxSource:            options.TxSource,
		TxSourceMeta:        txSourceManifestMeta(options.TxSource, options.MempoolURL),
		FeeStartWei:         options.FeeStart,
		FeeStepWei:          options.FeeStep,
		Timeout:             options.Timeout.String(),
		Deadline:            options.Deadline.String(),
		Scenarios:           scenarios,
		ExecutionMode:       options.ExecutionMode,
		Schedule:            "seeded-global-interleave",
	}
	if options.ExecutionMode == "persistent" {
		manifest.Schedule = "sequential-by-node-count-seeded-batch-interleave"
	}
	if err := writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest); err != nil {
		return err
	}
	runsPath := filepath.Join(options.OutDir, "runs.jsonl")
	runsFile, err := os.Create(runsPath)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(runsFile)
	allRuns := make([]EvalRun, 0, plannedRuns)
	recordRun := func(run EvalRun) error {
		manifest.RunOrder = append(manifest.RunOrder, fmt.Sprintf("%s/block-%d/block-iteration-%d/%s/slot-%d/generation-%d", run.Phase, run.MeasurementBlock, run.BlockIteration, run.ScenarioID, run.Slot, run.ClusterGeneration))
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
		return writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest)
	}
	var campaignErr error
	if options.ExecutionMode == "persistent" {
		var measurements []clusterMeasurement
		allRuns, measurements, campaignErr = runPersistentSuite(self, options.OutDir, options, scenarios, recordRun)
		manifest.ClusterStartups = len(measurements)
		for _, run := range allRuns {
			if run.Phase == "recovery" {
				manifest.RecoveryRuns++
			}
		}
		if err := writeClusterMeasurements(filepath.Join(options.OutDir, "cluster_measurements.csv"), measurements); err != nil {
			_ = runsFile.Close()
			return err
		}
	} else {
		rng := rand.New(rand.NewSource(options.Seed))
		orderIndex := 0
		for _, phase := range []struct {
			name  string
			count int
		}{{"warmup", options.Warmups}, {"measured", options.Repetitions}} {
			for iteration := 1; iteration <= phase.count; iteration++ {
				order := append([]evalScenario(nil), scenarios...)
				rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
				for _, scenario := range order {
					orderIndex++
					runID := fmt.Sprintf("%s-r%03d-%s", phase.name, iteration, scenario.ID)
					runDir := filepath.Join(options.OutDir, "runs", runID)
					run, runErr := runLocalExperiment(self, runDir, runID, scenario.Nodes, scenario.Threshold, options.BMax, scenario.BatchSize, options.TxSize, options.TxGas, options.FeeStart, options.FeeStep, 0, 0, options.BasePort, options.Timeout, "")
					run.ScenarioID, run.Phase, run.Iteration, run.OrderIndex = scenario.ID, phase.name, iteration, orderIndex
					run.ScheduleSeed = options.Seed
					run.PlannedScenarioRuns = options.Repetitions
					run.BlockIteration = iteration
					if phase.name == "measured" {
						perBlock := options.Repetitions / options.RepetitionBlocks
						run.MeasurementBlock = (iteration-1)/perBlock + 1
						run.BlockIteration = (iteration-1)%perBlock + 1
					}
					if runErr != nil {
						run.Error, run.Success = runErr.Error(), false
					}
					classifyRunOutcome(&run, runErr, options.Deadline)
					allRuns = append(allRuns, run)
					if err := recordRun(run); err != nil {
						_ = runsFile.Close()
						return err
					}
					fmt.Printf("%s iteration=%d scenario=%s success=%t critical_node=%d total_us=%d\n", phase.name, iteration, scenario.ID, run.Success, run.CriticalNodeID, criticalTotalUS(run))
				}
			}
		}
	}
	if err := runsFile.Close(); err != nil {
		return err
	}
	if err := writeSuiteOutputs(options.OutDir, scenarios, allRuns); err != nil {
		return err
	}
	collectionComplete := campaignErr == nil && suiteCollectionComplete(plannedRuns, allRuns)
	manifest.Valid = collectionComplete
	manifest.FinishedAt = time.Now().UTC()
	if collectionComplete {
		manifest.Status = "complete"
	} else {
		manifest.Status = "invalid"
		manifest.InvalidReason = "the planned warmup and measured schedule was not fully retained"
		if campaignErr != nil {
			manifest.InvalidReason = campaignErr.Error()
		}
	}
	if err := writeJSONFile(filepath.Join(options.OutDir, "manifest.json"), manifest); err != nil {
		return err
	}
	if !collectionComplete {
		return fmt.Errorf("suite failed: %s", manifest.InvalidReason)
	}
	return nil
}

func suiteCollectionComplete(plannedRuns int, runs []EvalRun) bool {
	retained := 0
	for _, run := range runs {
		if run.Phase == "warmup" || run.Phase == "measured" {
			retained++
		}
	}
	return retained == plannedRuns
}

func parseEvalSuiteOptions(args []string) (suiteOptions, error) {
	options := suiteOptions{}
	fs := flag.NewFlagSet("eval-suite", flag.ContinueOnError)
	fs.StringVar(&options.Profile, "profile", "", "named experiment profile, e.g. m1-baseline")
	fs.StringVar(&options.NodeCountsRaw, "node-counts", "4,7", "comma-separated operator counts")
	fs.StringVar(&options.BatchSizesRaw, "batch-sizes", "8,32,128", "comma-separated transaction batch sizes")
	fs.IntVar(&options.BMax, "bmax", 128, "BTE maximum batch size")
	fs.IntVar(&options.TxSize, "tx-size", 256, "minimum signed Ethereum transaction byte size")
	fs.Uint64Var(&options.TxGas, "tx-gas", 21000, "minimum gas limit used in generated transactions")
	fs.StringVar(&options.TxSource, "tx-source", "synthetic", "transaction source: synthetic or mock-placeholder")
	fs.StringVar(&options.MempoolURL, "mempool-url", "", "mempool-il base URL for tx-source=mock-placeholder")
	fs.Uint64Var(&options.FeeStart, "fee-start-wei", 1000, "first generated effective fee per gas")
	fs.Uint64Var(&options.FeeStep, "fee-step-wei", 1, "generated fee increment")
	fs.IntVar(&options.Warmups, "warmups", 5, "warmup runs per scenario")
	fs.IntVar(&options.Repetitions, "repetitions", 30, "measured runs per scenario")
	fs.IntVar(&options.RepetitionBlocks, "repetition-blocks", 1, "balanced measured repetition blocks")
	fs.Int64Var(&options.Seed, "seed", 20260621, "scenario-order shuffle seed")
	fs.IntVar(&options.BasePort, "base-port", 24000, "base port reused by sequential runs")
	fs.DurationVar(&options.Timeout, "timeout", 30*time.Second, "per-run timeout")
	fs.DurationVar(&options.Deadline, "deadline", 12*time.Second, "successful consistent timing deadline")
	fs.StringVar(&options.OutDir, "out-dir", "", "experiment directory; defaults below results/m1-local")
	fs.StringVar(&options.ExperimentID, "experiment-id", "", "stable experiment label")
	fs.StringVar(&options.ExecutionMode, "execution-mode", "isolated", "cluster lifecycle: isolated or persistent")
	fs.IntVar(&options.MaxRestarts, "max-restarts", 3, "maximum consecutive persistent-cluster recovery attempts")
	if err := fs.Parse(args); err != nil {
		return suiteOptions{}, err
	}
	explicit := make(map[string]bool)
	fs.Visit(func(item *flag.Flag) { explicit[item.Name] = true })
	if options.Profile != "" {
		if options.Profile != "m1-baseline" {
			return suiteOptions{}, fmt.Errorf("unknown eval-suite profile %q", options.Profile)
		}
		if !explicit["node-counts"] {
			options.NodeCountsRaw = "4,7,10"
		}
		if !explicit["batch-sizes"] {
			options.BatchSizesRaw = "8,32,128"
		}
		if !explicit["bmax"] {
			options.BMax = 128
		}
		if !explicit["tx-size"] {
			options.TxSize = 256
		}
		if !explicit["tx-gas"] {
			options.TxGas = 21000
		}
		if !explicit["fee-start-wei"] {
			options.FeeStart = 1000
		}
		if !explicit["fee-step-wei"] {
			options.FeeStep = 1
		}
		if !explicit["warmups"] {
			options.Warmups = 5
		}
		if !explicit["repetitions"] {
			options.Repetitions = 30
		}
		if !explicit["seed"] {
			options.Seed = 20260621
		}
		if !explicit["base-port"] {
			options.BasePort = 24000
		}
		if !explicit["timeout"] {
			options.Timeout = 30 * time.Second
		}
		if !explicit["execution-mode"] {
			options.ExecutionMode = "persistent"
		}
	}
	if options.ExecutionMode != "isolated" && options.ExecutionMode != "persistent" {
		return suiteOptions{}, fmt.Errorf("execution-mode must be isolated or persistent")
	}
	if options.MaxRestarts < 1 {
		return suiteOptions{}, fmt.Errorf("max-restarts must be >= 1")
	}
	return options, nil
}

func printSuitePlan(options suiteOptions, scenarios []evalScenario, plannedRuns int) {
	profile := options.Profile
	if profile == "" {
		profile = "custom"
	}
	fmt.Printf("profile=%s execution_mode=%s scenarios=%d warmups=%d repetitions=%d planned_runs=%d\n", profile, options.ExecutionMode, len(scenarios), options.Warmups, options.Repetitions, plannedRuns)
	for _, scenario := range scenarios {
		fmt.Printf("  scenario=%s nodes=%d threshold=%d batch_size=%d network=%s bmax=%d tx_size=%d\n", scenario.ID, scenario.Nodes, scenario.Threshold, scenario.BatchSize, scenario.Network, options.BMax, options.TxSize)
	}
}

func prepareSuiteOutputDir(path string) error {
	entries, err := os.ReadDir(path)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("output directory %s is not empty", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Join(path, "runs"), 0755)
}

func buildScenarios(nodes, batches []int, bmax int) ([]evalScenario, error) {
	var scenarios []evalScenario
	for _, n := range nodes {
		if n != 4 && n != 7 && n != 10 {
			return nil, fmt.Errorf("node count %d must be one of 4, 7, or 10", n)
		}
		f := (n - 1) / 3
		threshold := 2*f + 1
		for _, batch := range batches {
			if batch != 8 && batch != 32 && batch != 128 && batch != 512 {
				return nil, fmt.Errorf("batch size %d must be one of 8, 32, 128, or 512", batch)
			}
			if batch > bmax {
				return nil, fmt.Errorf("batch size %d exceeds BMax %d", batch, bmax)
			}
			scenarios = append(scenarios, evalScenario{
				ID: fmt.Sprintf("n%d-b%d-libp2p", n, batch), Nodes: n,
				Threshold: threshold, BatchSize: batch, Network: "libp2p",
			})
		}
	}
	return scenarios, nil
}

func writeSuiteOutputs(outDir string, scenarios []evalScenario, runs []EvalRun) error {
	if err := writeNodeMeasurements(filepath.Join(outDir, "node_measurements.csv"), runs); err != nil {
		return err
	}
	if err := writeRunMeasurements(filepath.Join(outDir, "run_measurements.csv"), runs); err != nil {
		return err
	}
	summaries := summarizeScenarios(scenarios, runs)
	if err := writeJSONFile(filepath.Join(outDir, "scenario_summary.json"), summaries); err != nil {
		return err
	}
	return writeScenarioSummaryCSV(filepath.Join(outDir, "scenario_summary.csv"), summaries)
}

func metricValues(run EvalRun, result Result) map[string]float64 {
	m := result.Metrics
	return map[string]float64{
		"total_slot_us": float64(m.TotalSlotUS), "proposal_preparation_us": float64(m.ProposalPreparationUS),
		"acs_us": float64(m.ACSUS), "merge_plan_us": float64(m.MergePlanUS),
		"acs_output_decode_us": float64(m.ACSOutputDecodeUS), "agreed_set_us": float64(m.AgreedSetUS),
		"merge_us": float64(m.MergeUS), "ciphertext_decode_us": float64(m.CiphertextDecodeUS),
		"batch_plan_us":       float64(m.BatchPlanUS),
		"share_generation_us": float64(m.ShareGenerationUS), "threshold_wait_us": float64(m.ThresholdWaitUS),
		"combine_us": float64(m.CombineUS), "materialization_us": float64(m.MaterializationUS),
		"commit_to_plaintext_us": float64(m.CommitToPlaintextUS),
		"harness_wall_us":        float64(run.HarnessWallUS),
		"start_skew_us":          float64(run.StartSkewUS),
	}
}

func criticalResult(run EvalRun) (Result, bool) {
	for _, result := range run.Results {
		if result.NodeID == run.CriticalNodeID {
			return result, true
		}
	}
	return Result{}, false
}

func criticalTotalUS(run EvalRun) int64 {
	result, ok := criticalResult(run)
	if !ok {
		return 0
	}
	return result.Metrics.TotalSlotUS
}

func summarizeScenarios(scenarios []evalScenario, runs []EvalRun) []scenarioSummary {
	values := make(map[string]map[string][]float64)
	counts := make(map[string]scenarioSummary)
	for _, run := range runs {
		if run.Phase != "measured" {
			continue
		}
		count := counts[run.ScenarioID]
		count.Attempted++
		result, ok := criticalResult(run)
		if run.Success && run.Consistent && ok {
			count.Completed++
			count.Succeeded++
			if run.DeadlineMet {
				count.ConsistentWithinDeadline++
			}
			if values[run.ScenarioID] == nil {
				values[run.ScenarioID] = make(map[string][]float64)
			}
			for name, value := range metricValues(run, result) {
				values[run.ScenarioID][name] = append(values[run.ScenarioID][name], value)
			}
		} else if run.TimedOut || run.Outcome == "timed_out" {
			count.TimedOut++
		} else {
			count.Failed++
		}
		counts[run.ScenarioID] = count
	}
	output := make([]scenarioSummary, 0, len(scenarios))
	for _, scenario := range scenarios {
		count := counts[scenario.ID]
		summary := scenarioSummary{
			ScenarioID: scenario.ID, Nodes: scenario.Nodes, BatchSize: scenario.BatchSize,
			Network: scenario.Network, Attempted: count.Attempted, Completed: count.Completed,
			Succeeded: count.Succeeded, ConsistentWithinDeadline: count.ConsistentWithinDeadline,
			Failed: count.Failed, TimedOut: count.TimedOut, Metrics: make(map[string]metricSummary),
		}
		for _, name := range latencyMetricNames {
			summary.Metrics[name] = summarizeMetric(values[scenario.ID][name])
		}
		output = append(output, summary)
	}
	return output
}

func summarizeMetric(values []float64) metricSummary {
	if len(values) == 0 {
		return metricSummary{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	mean := sum / float64(len(sorted))
	var variance float64
	if len(sorted) > 1 {
		for _, value := range sorted {
			delta := value - mean
			variance += delta * delta
		}
		variance /= float64(len(sorted) - 1)
	}
	summary := metricSummary{Count: len(sorted), Min: sorted[0], Mean: mean, StdDev: math.Sqrt(variance), P50: percentileType7(sorted, 0.50), P95: percentileType7(sorted, 0.95), Max: sorted[len(sorted)-1]}
	if len(sorted) >= p99ContractedSamples {
		p99 := percentileType7(sorted, 0.99)
		summary.P99 = &p99
		summary.P99Eligible = true
	}
	return summary
}

func classifyRunOutcome(run *EvalRun, runErr error, deadline time.Duration) {
	if run.Success && run.Consistent {
		run.Outcome = "completed"
		totalUS := criticalTotalUS(*run)
		run.DeadlineMet = totalUS > 0 && totalUS <= deadline.Microseconds()
		run.TimedOut = false
		return
	}
	message := strings.ToLower(run.Error)
	if runErr != nil && message == "" {
		message = strings.ToLower(runErr.Error())
	}
	run.TimedOut = strings.Contains(message, "timed out") ||
		strings.Contains(message, "timeout") ||
		strings.Contains(message, "deadline exceeded")
	if run.TimedOut {
		run.Outcome = "timed_out"
	} else {
		run.Outcome = "failed"
	}
	run.DeadlineMet = false
}

func shouldRecoverPersistentCluster(run EvalRun, runErr error) bool {
	if runErr == nil {
		return false
	}
	message := strings.ToLower(runErr.Error())
	return !run.TimedOut && !strings.Contains(message, "reported terminal failure")
}

func percentileType7(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	h := float64(len(sorted)-1) * p
	lower := int(math.Floor(h))
	upper := int(math.Ceil(h))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower] + (h-float64(lower))*(sorted[upper]-sorted[lower])
}

func writeNodeMeasurements(path string, runs []EvalRun) error {
	header := []string{"run_id", "scenario_id", "phase", "iteration", "order_index", "measurement_block", "block_iteration", "schedule_seed", "planned_scenario_runs", "success", "consistent", "outcome", "deadline_met", "timed_out", "node_id", "critical_node", "total_slot_us", "proposal_preparation_us", "acs_us", "merge_plan_us", "selected_ciphertexts", "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us", "share_generation_us", "threshold_wait_us", "combine_us", "combine_attempts", "materialization_us", "commit_to_plaintext_us", "metrics_finalized"}
	return writeCSV(path, header, func(w *csv.Writer) error {
		for _, run := range runs {
			for _, result := range run.Results {
				m := result.Metrics
				record := []string{run.RunID, run.ScenarioID, run.Phase, strconv.Itoa(run.Iteration), strconv.Itoa(run.OrderIndex), strconv.Itoa(run.MeasurementBlock), strconv.Itoa(run.BlockIteration), strconv.FormatInt(run.ScheduleSeed, 10), strconv.Itoa(run.PlannedScenarioRuns), strconv.FormatBool(run.Success), strconv.FormatBool(run.Consistent), run.Outcome, strconv.FormatBool(run.DeadlineMet), strconv.FormatBool(run.TimedOut), strconv.FormatUint(result.NodeID, 10), strconv.FormatBool(result.NodeID == run.CriticalNodeID), strconv.FormatInt(m.TotalSlotUS, 10), strconv.FormatInt(m.ProposalPreparationUS, 10), strconv.FormatInt(m.ACSUS, 10), strconv.FormatInt(m.MergePlanUS, 10), strconv.Itoa(m.SelectedCiphertexts), strconv.FormatInt(m.ACSOutputDecodeUS, 10), strconv.FormatInt(m.AgreedSetUS, 10), strconv.FormatInt(m.MergeUS, 10), strconv.FormatInt(m.CiphertextDecodeUS, 10), strconv.FormatInt(m.BatchPlanUS, 10), strconv.FormatInt(m.ShareGenerationUS, 10), strconv.FormatInt(m.ThresholdWaitUS, 10), strconv.FormatInt(m.CombineUS, 10), strconv.Itoa(m.CombineAttempts), strconv.FormatInt(m.MaterializationUS, 10), strconv.FormatInt(m.CommitToPlaintextUS, 10), strconv.FormatBool(m.MetricsFinalized)}
				if err := w.Write(record); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func writeRunMeasurements(path string, runs []EvalRun) error {
	header := []string{"run_id", "scenario_id", "phase", "iteration", "order_index", "measurement_block", "block_iteration", "schedule_seed", "planned_scenario_runs", "slot", "cluster_generation", "nodes", "threshold", "batch_size", "network", "bmax", "tx_size", "tx_gas", "success", "consistent", "outcome", "deadline_met", "timed_out", "error", "critical_node_id", "total_slot_us", "proposal_preparation_us", "acs_us", "merge_plan_us", "selected_ciphertexts", "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us", "share_generation_us", "threshold_wait_us", "combine_us", "combine_attempts", "materialization_us", "commit_to_plaintext_us", "prepare_us", "submission_us", "harness_wall_us", "start_skew_us"}
	return writeCSV(path, header, func(w *csv.Writer) error {
		for _, run := range runs {
			result, _ := criticalResult(run)
			m := result.Metrics
			record := []string{run.RunID, run.ScenarioID, run.Phase, strconv.Itoa(run.Iteration), strconv.Itoa(run.OrderIndex), strconv.Itoa(run.MeasurementBlock), strconv.Itoa(run.BlockIteration), strconv.FormatInt(run.ScheduleSeed, 10), strconv.Itoa(run.PlannedScenarioRuns), strconv.FormatUint(run.Slot, 10), strconv.Itoa(run.ClusterGeneration), strconv.Itoa(run.Nodes), strconv.Itoa(run.Threshold), strconv.Itoa(run.BatchSize), run.Network, strconv.Itoa(run.BMax), strconv.Itoa(run.TxSize), strconv.FormatUint(run.TxGas, 10), strconv.FormatBool(run.Success), strconv.FormatBool(run.Consistent), run.Outcome, strconv.FormatBool(run.DeadlineMet), strconv.FormatBool(run.TimedOut), run.Error, strconv.FormatUint(run.CriticalNodeID, 10), strconv.FormatInt(m.TotalSlotUS, 10), strconv.FormatInt(m.ProposalPreparationUS, 10), strconv.FormatInt(m.ACSUS, 10), strconv.FormatInt(m.MergePlanUS, 10), strconv.Itoa(m.SelectedCiphertexts), strconv.FormatInt(m.ACSOutputDecodeUS, 10), strconv.FormatInt(m.AgreedSetUS, 10), strconv.FormatInt(m.MergeUS, 10), strconv.FormatInt(m.CiphertextDecodeUS, 10), strconv.FormatInt(m.BatchPlanUS, 10), strconv.FormatInt(m.ShareGenerationUS, 10), strconv.FormatInt(m.ThresholdWaitUS, 10), strconv.FormatInt(m.CombineUS, 10), strconv.Itoa(m.CombineAttempts), strconv.FormatInt(m.MaterializationUS, 10), strconv.FormatInt(m.CommitToPlaintextUS, 10), strconv.FormatInt(run.PrepareUS, 10), strconv.FormatInt(run.SubmissionUS, 10), strconv.FormatInt(run.HarnessWallUS, 10), strconv.FormatInt(run.StartSkewUS, 10)}
			if err := w.Write(record); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeScenarioSummaryCSV(path string, summaries []scenarioSummary) error {
	header := []string{"scenario_id", "nodes", "batch_size", "network", "attempted", "completed", "succeeded", "consistent_within_deadline", "failed", "timed_out", "metric", "count", "min", "mean", "sample_stddev", "p50", "p95", "p99", "p99_eligible", "max"}
	return writeCSV(path, header, func(w *csv.Writer) error {
		for _, scenario := range summaries {
			for _, name := range latencyMetricNames {
				m := scenario.Metrics[name]
				p99 := ""
				if m.P99 != nil {
					p99 = formatFloat(*m.P99)
				}
				record := []string{scenario.ScenarioID, strconv.Itoa(scenario.Nodes), strconv.Itoa(scenario.BatchSize), scenario.Network, strconv.Itoa(scenario.Attempted), strconv.Itoa(scenario.Completed), strconv.Itoa(scenario.Succeeded), strconv.Itoa(scenario.ConsistentWithinDeadline), strconv.Itoa(scenario.Failed), strconv.Itoa(scenario.TimedOut), name, strconv.Itoa(m.Count), formatFloat(m.Min), formatFloat(m.Mean), formatFloat(m.StdDev), formatFloat(m.P50), formatFloat(m.P95), p99, strconv.FormatBool(m.P99Eligible), formatFloat(m.Max)}
				if err := w.Write(record); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func writeCSV(path string, header []string, rows func(*csv.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		_ = f.Close()
		return err
	}
	if err := rows(w); err != nil {
		_ = f.Close()
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
