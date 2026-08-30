package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthdm/hbbft"
)

func TestM1BaselineProfileResolvesCompleteMatrix(t *testing.T) {
	options, err := parseEvalSuiteOptions([]string{"--profile", "m1-baseline"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parseIntList(options.NodeCountsRaw)
	batches, _ := parseIntList(options.BatchSizesRaw)
	scenarios, err := buildScenarios(nodes, batches, options.BMax)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 9 {
		t.Fatalf("scenario count = %d, want 9", len(scenarios))
	}
	thresholds := map[int]int{}
	for _, scenario := range scenarios {
		thresholds[scenario.Nodes] = scenario.Threshold
		if scenario.Network != "libp2p" {
			t.Fatalf("scenario %s network = %s, want libp2p", scenario.ID, scenario.Network)
		}
	}
	if !reflect.DeepEqual(thresholds, map[int]int{4: 3, 7: 5, 10: 7}) {
		t.Fatalf("thresholds = %v", thresholds)
	}
	if options.BMax != 128 || options.TxSize != 256 || options.Warmups != 5 || options.Repetitions != 30 {
		t.Fatalf("unexpected profile options: %+v", options)
	}
	if options.ExecutionMode != "persistent" {
		t.Fatalf("m1 execution mode = %q, want persistent", options.ExecutionMode)
	}
	if planned := len(scenarios) * (options.Warmups + options.Repetitions); planned != 315 {
		t.Fatalf("planned runs = %d, want 315", planned)
	}
}

func TestSuiteScenariosRejectValuesOutsideEvidenceMatrix(t *testing.T) {
	for _, test := range []struct {
		name    string
		nodes   []int
		batches []int
	}{
		{name: "node count", nodes: []int{5}, batches: []int{8}},
		{name: "batch size", nodes: []int{4}, batches: []int{16}},
		{name: "BMax", nodes: []int{4}, batches: []int{512}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildScenarios(test.nodes, test.batches, 128); err == nil {
				t.Fatal("buildScenarios accepted a value outside the evidence matrix")
			}
		})
	}
}

func TestExecutionModeDefaultsAndOverrides(t *testing.T) {
	custom, err := parseEvalSuiteOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if custom.ExecutionMode != "isolated" {
		t.Fatalf("custom execution mode = %q, want isolated", custom.ExecutionMode)
	}
	override, err := parseEvalSuiteOptions([]string{"--profile", "m1-baseline", "--execution-mode", "isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if override.ExecutionMode != "isolated" {
		t.Fatalf("explicit execution mode = %q, want isolated", override.ExecutionMode)
	}
}

func TestEvalSuiteOptionsEnableACSTrace(t *testing.T) {
	options, err := parseEvalSuiteOptions([]string{"--acs-trace"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.ACSTrace {
		t.Fatal("--acs-trace did not enable local diagnostic configs")
	}
}

func TestEvalSuiteStreamModeDefaultsAndOverrides(t *testing.T) {
	defaults, err := parseEvalSuiteOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.StreamMode != streamModeFresh {
		t.Fatalf("default stream mode = %q, want fresh", defaults.StreamMode)
	}
	persistent, err := parseEvalSuiteOptions([]string{"--stream-mode", "persistent"})
	if err != nil {
		t.Fatal(err)
	}
	if persistent.StreamMode != streamModePersistent {
		t.Fatalf("explicit stream mode = %q, want persistent", persistent.StreamMode)
	}
	if _, err := parseEvalSuiteOptions([]string{"--stream-mode", "reuse"}); err == nil {
		t.Fatal("unknown evaluator stream mode was accepted")
	}
}

func TestPersistentScheduleHasExactGroupedM1Samples(t *testing.T) {
	options, err := parseEvalSuiteOptions([]string{"--profile", "m1-baseline"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parseIntList(options.NodeCountsRaw)
	batches, _ := parseIntList(options.BatchSizesRaw)
	scenarios, err := buildScenarios(nodes, batches, options.BMax)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, nodes := range []int{4, 7, 10} {
		var group []evalScenario
		for _, scenario := range scenarios {
			if scenario.Nodes == nodes {
				group = append(group, scenario)
			}
		}
		specs := persistentSpecs(group, options, nodes)
		if len(specs) != 105 {
			t.Fatalf("n%d samples = %d, want 105", nodes, len(specs))
		}
		counts := map[string]int{}
		for _, spec := range specs {
			counts[spec.scenario.ID]++
		}
		for _, scenario := range group {
			if counts[scenario.ID] != 35 {
				t.Fatalf("%s samples = %d, want 35", scenario.ID, counts[scenario.ID])
			}
		}
		total += len(specs)
	}
	if total != 315 {
		t.Fatalf("persistent samples = %d, want 315", total)
	}
}

func TestM1BaselineProfileAllowsExplicitOverrides(t *testing.T) {
	options, err := parseEvalSuiteOptions([]string{"--profile", "m1-baseline", "--node-counts", "4", "--warmups", "0", "--repetitions", "1", "--tx-size", "512", "--timeout", "45s"})
	if err != nil {
		t.Fatal(err)
	}
	if options.NodeCountsRaw != "4" || options.Warmups != 0 || options.Repetitions != 1 || options.TxSize != 512 || options.Timeout != 45*time.Second {
		t.Fatalf("explicit overrides were not preserved: %+v", options)
	}
}

func TestSuiteManifestRecordsResolvedCampaignConfiguration(t *testing.T) {
	manifest := suiteManifest{
		SchemaVersion: suiteSchemaVersion, Profile: "m1-baseline", Seed: 77,
		StreamMode:       streamModePersistent,
		RepetitionBlocks: 10, PlannedRuns: 9090,
		PlannedScenarioRuns: map[string]int{"n4-b8-libp2p": 1000},
		RunOrder:            []string{"measured/block-1/block-iteration-1/n4-b8-libp2p/slot-1/generation-1"},
		BMax:                512, TxSize: 256, TxGas: 21000, FeeStartWei: 1000, FeeStepWei: 1,
		Timeout: "30s", Deadline: "12s",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{
		"schema_version": suiteSchemaVersion, "profile": "m1-baseline",
		"seed": float64(77), "repetition_blocks": float64(10),
		"planned_runs": float64(9090), "bmax": float64(512), "tx_size": float64(256),
		"tx_gas": float64(21000), "timeout": "30s", "deadline": "12s",
		"stream_mode": streamModePersistent,
	} {
		if got := decoded[name]; got != want {
			t.Fatalf("manifest %s = %v, want %v", name, got, want)
		}
	}
}

func TestEvalSuiteRejectsUnknownProfile(t *testing.T) {
	if _, err := parseEvalSuiteOptions([]string{"--profile", "unknown"}); err == nil {
		t.Fatal("expected unknown profile to fail")
	}
}

func TestEvalSuiteRejectsRemovedNetworksFlag(t *testing.T) {
	if _, err := parseEvalSuiteOptions([]string{"--networks", "tcp"}); err == nil {
		t.Fatal("expected removed networks flag to fail")
	}
}

func TestRunMeasurementsRecordFixedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.csv")
	runs := []EvalRun{{
		RunID: "run", Nodes: 7, Threshold: 5, BatchSize: 32, Network: "libp2p",
		BMax: 128, TxSize: 256, TxGas: 21000, CriticalNodeID: 2, MeasurementBlock: 4,
		BlockIteration: 9, ScheduleSeed: 77, PlannedScenarioRuns: 1000,
		Outcome: "completed", DeadlineMet: true,
		Results: []Result{{NodeID: 2, Metrics: Metrics{CombineAttempts: 1}}},
	}}
	if err := writeRunMeasurements(path, runs); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	records, err := csv.NewReader(handle).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	index := make(map[string]int)
	for i, name := range records[0] {
		index[name] = i
	}
	for name, want := range map[string]string{
		"bmax": "128", "tx_size": "256", "tx_gas": "21000", "combine_attempts": "1",
		"measurement_block": "4", "block_iteration": "9", "schedule_seed": "77",
		"planned_scenario_runs": "1000", "outcome": "completed", "deadline_met": "true",
		"timed_out": "false",
	} {
		if got := records[1][index[name]]; got != want {
			t.Fatalf("%s = %s, want %s", name, got, want)
		}
	}
}

func TestNodeMeasurementsIncludeACSTraceSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.csv")
	trace := hbbft.ACSTrace{
		SchemaVersion: hbbft.ACSTraceSchemaVersion,
		Enabled:       true,
		Aggregate: hbbft.ACSAggregateTrace{
			InputStarted:       hbbft.TracePoint{Recorded: true, OffsetUS: 0},
			FirstRBCOutput:     hbbft.TracePoint{Recorded: true, OffsetUS: 10},
			RBCOutputQuorum:    hbbft.TracePoint{Recorded: true, OffsetUS: 20},
			FirstTrueBBA:       hbbft.TracePoint{Recorded: true, OffsetUS: 30},
			TrueBBAQuorum:      hbbft.TracePoint{Recorded: true, OffsetUS: 40},
			FalseInputInjected: hbbft.TracePoint{Recorded: true, OffsetUS: 50},
			AllBBADecided:      hbbft.TracePoint{Recorded: true, OffsetUS: 60},
			TruthyRBCReady:     hbbft.TracePoint{Recorded: true, OffsetUS: 70},
			CoreDecision:       hbbft.TracePoint{Recorded: true, OffsetUS: 80},
		},
		Wait: hbbft.ACSWaitTrace{TrueBBAQuorumUS: 11, AllBBAUS: 22, TruthyRBCUS: 33},
		Adapter: hbbft.ACSAdapterTrace{
			CommonSubsetDecoded: hbbft.TracePoint{Recorded: true, OffsetUS: 90},
			BlockBodyBuilt:      hbbft.TracePoint{Recorded: true, OffsetUS: 100},
			NodeOutputReceived:  hbbft.TracePoint{Recorded: true, OffsetUS: 110},
		},
		BBA: map[uint64]hbbft.BBATrace{
			0: {MaxEpoch: 2},
			1: {MaxEpoch: 7},
		},
		Messages: map[hbbft.ACSMessageSubtype]hbbft.ACSMessageTrace{
			hbbft.ACSMessageProof: {
				InboundCount: 2, InboundBytes: 20, OutboundCount: 3, OutboundBytes: 30, SendCount: 4, SendTotalUS: 40, SendMaxUS: 25, SendFailureCount: 1,
				Encode: hbbft.ACSSendPhaseTrace{Count: 4, TotalUS: 40, MaxUS: 25}, QueueWait: hbbft.ACSSendPhaseTrace{Count: 4, TotalUS: 8, MaxUS: 5},
				StreamOpen: hbbft.ACSSendPhaseTrace{Count: 4, TotalUS: 12, MaxUS: 6}, Write: hbbft.ACSSendPhaseTrace{Count: 4, TotalUS: 16, MaxUS: 9},
				Finalize: hbbft.ACSSendPhaseTrace{Count: 4, TotalUS: 20, MaxUS: 11}, StreamOpenCount: 4,
			},
			hbbft.ACSMessageEcho: {
				InboundCount: 5, InboundBytes: 50, OutboundCount: 6, OutboundBytes: 60, SendCount: 7, SendTotalUS: 70, SendMaxUS: 35, SendFailureCount: 2,
				Encode: hbbft.ACSSendPhaseTrace{Count: 7, TotalUS: 70, MaxUS: 35}, QueueWait: hbbft.ACSSendPhaseTrace{Count: 7, TotalUS: 14, MaxUS: 7},
				StreamOpen: hbbft.ACSSendPhaseTrace{Count: 7, TotalUS: 21, MaxUS: 10}, Write: hbbft.ACSSendPhaseTrace{Count: 7, TotalUS: 28, MaxUS: 12},
				Finalize: hbbft.ACSSendPhaseTrace{Count: 7, TotalUS: 35, MaxUS: 15}, StreamOpenCount: 1, StreamReuseCount: 6,
			},
		},
	}
	runs := []EvalRun{{
		RunID: "run", MeasurementBlock: 4, StreamMode: streamModePersistent,
		Results: []Result{{NodeID: 2, ACSTrace: trace}},
	}}

	if err := writeNodeMeasurements(path, runs); err != nil {
		t.Fatal(err)
	}
	records, err := readCSV(path)
	if err != nil {
		t.Fatal(err)
	}
	index := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		index[name] = i
	}
	want := map[string]string{
		"stream_mode":                  streamModePersistent,
		"acs_trace_schema":             hbbft.ACSTraceSchemaVersion,
		"acs_input_started_us":         "0",
		"acs_first_rbc_output_us":      "10",
		"acs_rbc_output_quorum_us":     "20",
		"acs_first_true_bba_us":        "30",
		"acs_true_bba_quorum_us":       "40",
		"acs_false_input_injected_us":  "50",
		"acs_all_bba_decided_us":       "60",
		"acs_truthy_rbc_ready_us":      "70",
		"acs_core_decision_us":         "80",
		"acs_common_subset_decoded_us": "90",
		"acs_block_body_built_us":      "100",
		"acs_node_output_received_us":  "110",
		"acs_wait_true_bba_quorum_us":  "11",
		"acs_wait_all_bba_us":          "22",
		"acs_wait_truthy_rbc_us":       "33",
		"acs_inbound_messages":         "7",
		"acs_inbound_bytes":            "70",
		"acs_outbound_messages":        "9",
		"acs_outbound_bytes":           "90",
		"acs_send_count":               "11",
		"acs_send_total_us":            "110",
		"acs_send_max_us":              "35",
		"acs_send_failures":            "3",
		"acs_encode_total_us":          "110",
		"acs_encode_max_us":            "35",
		"acs_queue_wait_total_us":      "22",
		"acs_queue_wait_max_us":        "7",
		"acs_stream_open_total_us":     "33",
		"acs_stream_open_max_us":       "10",
		"acs_write_total_us":           "44",
		"acs_write_max_us":             "12",
		"acs_finalize_total_us":        "55",
		"acs_finalize_max_us":          "15",
		"acs_stream_open_count":        "5",
		"acs_stream_reuse_count":       "6",
		"acs_max_bba_epoch":            "7",
	}
	for name, expected := range want {
		column, ok := index[name]
		if !ok {
			t.Fatalf("missing ACS trace column %q", name)
		}
		if got := records[1][column]; got != expected {
			t.Fatalf("%s = %q, want %q", name, got, expected)
		}
	}
}

func TestSuiteStreamModeAgreementFailsClosed(t *testing.T) {
	runs := []EvalRun{{RunID: "run", StreamMode: streamModePersistent}}
	if err := validateSuiteStreamMode(suiteManifest{StreamMode: streamModePersistent}, runs); err != nil {
		t.Fatalf("matching stream mode rejected: %v", err)
	}
	runs[0].StreamMode = streamModeFresh
	if err := validateSuiteStreamMode(suiteManifest{StreamMode: streamModePersistent}, runs); err == nil || !strings.Contains(err.Error(), "stream mode") {
		t.Fatalf("mismatched stream mode error = %v", err)
	}
}

func TestPersistentConfigBaseRejectsDifferentStreamMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.json")
	if err := os.WriteFile(path, []byte(`{"network":{"mode":"libp2p","stream_mode":"fresh"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateClusterConfigStreamMode(path, streamModePersistent); err == nil || !strings.Contains(err.Error(), "stream mode") {
		t.Fatalf("config-base mismatch error = %v", err)
	}
	if err := validateClusterConfigStreamMode(path, streamModeFresh); err != nil {
		t.Fatalf("matching config-base mode rejected: %v", err)
	}
}

func readCSV(path string) ([][]string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return csv.NewReader(handle).ReadAll()
}

func TestPrepareSuiteOutputDirRejectsExistingArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := prepareSuiteOutputDir(dir); err == nil {
		t.Fatal("expected non-empty output directory to be rejected")
	}
	fresh := filepath.Join(t.TempDir(), "new-suite")
	if err := prepareSuiteOutputDir(fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fresh, "runs")); err != nil {
		t.Fatalf("runs directory not created: %v", err)
	}
}

func TestRefreshMetricsUsesDefinedEventBoundaries(t *testing.T) {
	base := time.Now()
	node := Node{slotState: &slotState{metricTimes: metricTimes{
		slotStart:            base,
		proposalReady:        base.Add(2 * time.Millisecond),
		acsDecision:          base.Add(12 * time.Millisecond),
		acsOutputDecoded:     base.Add(12*time.Millisecond + 500*time.Microsecond),
		agreedSetDone:        base.Add(13 * time.Millisecond),
		mergeDone:            base.Add(14 * time.Millisecond),
		ciphertextsDecoded:   base.Add(14*time.Millisecond + 500*time.Microsecond),
		planDone:             base.Add(15 * time.Millisecond),
		shareGenerationStart: base.Add(15 * time.Millisecond),
		sharesDone:           base.Add(19 * time.Millisecond),
		threshold:            base.Add(20 * time.Millisecond),
		combineDone:          base.Add(22 * time.Millisecond),
		materialized:         base.Add(23 * time.Millisecond),
	}}}
	node.refreshMetricsLocked()

	m := node.metrics
	if m.ProposalPreparationUS != 2000 || m.ACSUS != 10000 || m.MergePlanUS != 3000 {
		t.Fatalf("unexpected pre-commit timing: %+v", m)
	}
	if m.ACSOutputDecodeUS != 500 || m.AgreedSetUS != 500 || m.MergeUS != 1000 || m.CiphertextDecodeUS != 500 || m.BatchPlanUS != 500 {
		t.Fatalf("unexpected merge-plan attribution: %+v", m)
	}
	if m.ShareGenerationUS != 4000 || m.ThresholdWaitUS != 5000 {
		t.Fatalf("unexpected overlapping timing: %+v", m)
	}
	if m.CombineUS != 2000 || m.MaterializationUS != 1000 || m.CommitToPlaintextUS != 11000 || m.TotalSlotUS != 23000 {
		t.Fatalf("unexpected completion timing: %+v", m)
	}
	if !m.MetricsFinalized || m.TotalSlotMS != 23 || m.ACSMS != 10 {
		t.Fatalf("metrics not finalized or compatibility fields wrong: %+v", m)
	}
}

func TestRefreshMetricsWaitsForShareGenerationFinalization(t *testing.T) {
	base := time.Now()
	node := Node{slotState: &slotState{metricTimes: metricTimes{
		slotStart: base, proposalReady: base, acsDecision: base,
		acsOutputDecoded: base, agreedSetDone: base, mergeDone: base, ciphertextsDecoded: base,
		planDone: base, shareGenerationStart: base, threshold: base,
		combineDone: base, materialized: base.Add(time.Millisecond),
	}}}
	node.refreshMetricsLocked()
	if node.metrics.MetricsFinalized {
		t.Fatal("metrics finalized before local share generation completed")
	}
	node.metricTimes.sharesDone = base.Add(2 * time.Millisecond)
	node.refreshMetricsLocked()
	if !node.metrics.MetricsFinalized {
		t.Fatal("metrics did not finalize after shares completed")
	}
}

func TestPercentileType7(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	if got := percentileType7(values, 0.50); got != 3 {
		t.Fatalf("p50 = %v, want 3", got)
	}
	if got := percentileType7(values, 0.95); got != 4.8 {
		t.Fatalf("p95 = %v, want 4.8", got)
	}
}

func TestP99RequiresContractedSuccessfulSampleCount(t *testing.T) {
	values := make([]float64, 1000)
	for i := range values {
		values[i] = float64(i + 1)
	}

	insufficient := summarizeMetric(values[:999])
	if insufficient.P99Eligible || insufficient.P99 != nil {
		t.Fatalf("999 samples published p99: %+v", insufficient)
	}
	eligible := summarizeMetric(values)
	if !eligible.P99Eligible || eligible.P99 == nil {
		t.Fatalf("1000 samples did not publish p99: %+v", eligible)
	}
	if math.Abs(*eligible.P99-990.01) > 1e-9 {
		t.Fatalf("p99 = %.12f, want 990.01", *eligible.P99)
	}
	if eligible.P50 != 500.5 || math.Abs(eligible.P95-950.05) > 1e-9 {
		t.Fatalf("compatibility quantiles changed: %+v", eligible)
	}
}

func TestScenarioSummarySeparatesOutcomesAndExcludesNonCompletions(t *testing.T) {
	scenario := evalScenario{ID: "n4-b8-libp2p", Nodes: 4, BatchSize: 8, Network: "libp2p"}
	result := func(total int64) []Result {
		return []Result{{NodeID: 0, Metrics: Metrics{TotalSlotUS: total}}}
	}
	runs := []EvalRun{
		{ScenarioID: scenario.ID, Phase: "measured", Success: true, Consistent: true, Outcome: "completed", DeadlineMet: true, Results: result(10_000_000)},
		{ScenarioID: scenario.ID, Phase: "measured", Success: true, Consistent: true, Outcome: "completed", Results: result(13_000_000)},
		{ScenarioID: scenario.ID, Phase: "measured", Outcome: "failed", Results: result(1)},
		{ScenarioID: scenario.ID, Phase: "measured", Outcome: "timed_out", TimedOut: true, Results: result(2)},
	}

	summary := summarizeScenarios([]evalScenario{scenario}, runs)[0]
	if summary.Attempted != 4 || summary.Completed != 2 || summary.Succeeded != 2 ||
		summary.ConsistentWithinDeadline != 1 || summary.Failed != 1 || summary.TimedOut != 1 {
		t.Fatalf("unexpected outcome counts: %+v", summary)
	}
	metric := summary.Metrics["total_slot_us"]
	if metric.Count != 2 || metric.P50 != 11_500_000 || metric.Min != 10_000_000 || metric.Max != 13_000_000 {
		t.Fatalf("non-completion entered latency quantiles: %+v", metric)
	}
}

func TestRunOutcomeClassifiesDeadlineExceededAsTimeout(t *testing.T) {
	run := EvalRun{Error: "poll results: context deadline exceeded"}
	err := fmt.Errorf("%s", run.Error)

	classifyRunOutcome(&run, err, 12*time.Second)

	if run.Outcome != "timed_out" || !run.TimedOut || run.DeadlineMet {
		t.Fatalf("deadline-exceeded outcome = %+v", run)
	}
}

func TestSuiteCompletenessCountsFailedAttemptsButNotRecoveryRuns(t *testing.T) {
	runs := []EvalRun{
		{Phase: "measured", Outcome: "completed"},
		{Phase: "measured", Outcome: "timed_out", TimedOut: true},
		{Phase: "recovery", Outcome: "failed"},
	}
	if !suiteCollectionComplete(2, runs) {
		t.Fatal("a retained timed-out attempt made a complete collection invalid")
	}
	if suiteCollectionComplete(3, runs) {
		t.Fatal("an incomplete planned schedule was accepted")
	}
}

func TestPersistentRecoveryDoesNotReplaceTerminalEvidenceAttempts(t *testing.T) {
	for _, test := range []struct {
		name string
		run  EvalRun
		err  error
		want bool
	}{
		{name: "completed", run: EvalRun{Success: true, Consistent: true}, want: false},
		{name: "terminal failure", run: EvalRun{Outcome: "failed"}, err: fmt.Errorf("node 2 reported terminal failure for slot 9"), want: false},
		{name: "timeout", run: EvalRun{Outcome: "timed_out", TimedOut: true}, err: fmt.Errorf("timed out waiting for results"), want: false},
		{name: "inconsistent", run: EvalRun{Success: true, Consistent: false}, want: false},
		{name: "infrastructure", run: EvalRun{Outcome: "failed"}, err: fmt.Errorf("prepare slot: connection refused"), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldRecoverPersistentCluster(test.run, test.err); got != test.want {
				t.Fatalf("shouldRecoverPersistentCluster() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPersistentScheduleUsesStableBalancedRepetitionBlocks(t *testing.T) {
	scenarios, err := buildScenarios([]int{4}, []int{8, 32, 128}, 128)
	if err != nil {
		t.Fatal(err)
	}
	options := suiteOptions{Warmups: 0, Repetitions: 6, RepetitionBlocks: 3, Seed: 42}
	specs := persistentSpecs(scenarios, options, 4)
	if len(specs) != 18 {
		t.Fatalf("spec count = %d, want 18", len(specs))
	}
	counts := map[int]map[string]int{}
	for _, spec := range specs {
		if counts[spec.measurementBlock] == nil {
			counts[spec.measurementBlock] = map[string]int{}
		}
		counts[spec.measurementBlock][spec.scenario.ID]++
		if spec.blockIteration < 1 || spec.blockIteration > 2 {
			t.Fatalf("invalid block iteration: %+v", spec)
		}
	}
	for block := 1; block <= 3; block++ {
		for _, scenario := range scenarios {
			if counts[block][scenario.ID] != 2 {
				t.Fatalf("block %d scenario %s count = %d, want 2", block, scenario.ID, counts[block][scenario.ID])
			}
		}
	}
	if !reflect.DeepEqual(specs, persistentSpecs(scenarios, options, 4)) {
		t.Fatal("same seed produced different block schedule")
	}
	options.Seed++
	if reflect.DeepEqual(specs, persistentSpecs(scenarios, options, 4)) {
		t.Fatal("different seeds produced identical block schedule")
	}
}

func TestExtendedEvidenceMatrixRequiresBMax512(t *testing.T) {
	if _, err := buildScenarios([]int{4, 7, 10}, []int{8, 32, 128, 512}, 128); err == nil {
		t.Fatal("batch 512 accepted with BMax 128")
	}
	scenarios, err := buildScenarios([]int{4, 7, 10}, []int{8, 32, 128, 512}, 512)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 12 {
		t.Fatalf("scenario count = %d, want 12", len(scenarios))
	}
}

func TestRemoteEvidenceScenarioRequiresSupportedNodesAndBMax(t *testing.T) {
	for _, nodes := range []int{4, 7, 10} {
		cfg := remoteEvalConfig{NodeCount: nodes, BMax: 512}
		if err := validateRemoteEvidenceScenario(cfg, 512); err != nil {
			t.Fatalf("n=%d batch=512 rejected: %v", nodes, err)
		}
	}
	if err := validateRemoteEvidenceScenario(remoteEvalConfig{NodeCount: 4, BMax: 128}, 512); err == nil {
		t.Fatal("remote batch 512 accepted with BMax 128")
	}
	if err := validateRemoteEvidenceScenario(remoteEvalConfig{NodeCount: 5, BMax: 512}, 8); err == nil {
		t.Fatal("unsupported evidence node count accepted")
	}
}

func TestRemoteBlockMetadataRetainsFullScenarioPlan(t *testing.T) {
	options, err := parseRemoteEvalOptions([]string{
		"--repetitions", "100",
		"--measurement-block", "3",
		"--planned-scenario-runs", "1000",
		"--seed", "77",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.MeasurementBlock != 3 || options.PlannedScenarioRuns != 1000 || options.Seed != 77 {
		t.Fatalf("remote block metadata was not retained: %+v", options)
	}
	if _, err := parseRemoteEvalOptions([]string{"--repetitions", "100", "--planned-scenario-runs", "99"}); err == nil {
		t.Fatal("planned scenario count below block repetitions was accepted")
	}
}

func TestCriticalNodeUsesSlowestAndLowestIDOnTie(t *testing.T) {
	results := []Result{
		{NodeID: 2, Metrics: Metrics{TotalSlotUS: 20}},
		{NodeID: 1, Metrics: Metrics{TotalSlotUS: 20}},
		{NodeID: 0, Metrics: Metrics{TotalSlotUS: 10}},
	}
	if got := criticalNodeID(results); got != 1 {
		t.Fatalf("critical node = %d, want 1", got)
	}
}

func TestSeededScenarioShuffleIsRepeatable(t *testing.T) {
	scenarios, err := buildScenarios([]int{4, 7}, []int{8, 32}, 32)
	if err != nil {
		t.Fatal(err)
	}
	shuffle := func(seed int64) []string {
		copyOf := append([]evalScenario(nil), scenarios...)
		rand.New(rand.NewSource(seed)).Shuffle(len(copyOf), func(i, j int) { copyOf[i], copyOf[j] = copyOf[j], copyOf[i] })
		ids := make([]string, len(copyOf))
		for i := range copyOf {
			ids[i] = copyOf[i].ID
		}
		return ids
	}
	if !reflect.DeepEqual(shuffle(42), shuffle(42)) {
		t.Fatal("same seed produced different schedules")
	}
	if reflect.DeepEqual(shuffle(42), shuffle(43)) {
		t.Fatal("different seeds produced identical schedules")
	}
}

func TestScenarioSummaryExcludesWarmupsAndFailures(t *testing.T) {
	scenario := evalScenario{ID: "n4-b8-libp2p", Nodes: 4, BatchSize: 8, Network: "libp2p"}
	result := func(total int64) []Result {
		return []Result{{NodeID: 0, Metrics: Metrics{TotalSlotUS: total}}}
	}
	runs := []EvalRun{
		{ScenarioID: scenario.ID, Phase: "warmup", Success: true, Consistent: true, Results: result(999)},
		{ScenarioID: scenario.ID, Phase: "measured", Success: true, Consistent: true, Results: result(10)},
		{ScenarioID: scenario.ID, Phase: "measured", Success: true, Consistent: true, Results: result(20)},
		{ScenarioID: scenario.ID, Phase: "measured", Success: false, Results: result(1000)},
	}
	summary := summarizeScenarios([]evalScenario{scenario}, runs)[0]
	if summary.Attempted != 3 || summary.Succeeded != 2 || summary.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", summary)
	}
	metric := summary.Metrics["total_slot_us"]
	if metric.Count != 2 || metric.P50 != 15 || metric.Max != 20 {
		t.Fatalf("unexpected total summary: %+v", metric)
	}
}

func TestFinalCampaignRejectsSyntheticBeforeExecution(t *testing.T) {
	_, err := parseRemoteEvalOptions([]string{"--final-campaign", "--tx-source", "synthetic"})
	if err == nil || !strings.Contains(err.Error(), "mock-encrypted-corpus") {
		t.Fatalf("final campaign source error = %v", err)
	}
}

func TestCorpusProvenanceSelectsExactPrefixIdentity(t *testing.T) {
	provenance := corpusProvenance{
		SchemaVersion:           "bloc-encrypted-corpus-v1",
		CiphertextWireVersion:   "bte-tx-v2",
		PublicConfigID:          "public",
		PlaintextMasterCorpusID: "master",
		PlaintextPrefixSetIDs:   map[string]string{"32": "plain-32"},
		EncryptedCorpusID:       "encrypted",
		EncryptedPrefixSetIDs:   map[string]string{"32": "cipher-32"},
		BMax:                    128,
		AvailableCount:          128,
	}
	if err := validateCorpusProvenance(provenance, 128, 32); err != nil {
		t.Fatalf("valid provenance rejected: %v", err)
	}
	identity := corpusIdentityForCount(provenance, 32)
	if identity.RequestedCount != 32 || identity.PlaintextPrefixID != "plain-32" || identity.EncryptedPrefixID != "cipher-32" || identity.PublicConfigID != "public" {
		t.Fatalf("unexpected run identity: %+v", identity)
	}
	if err := validateCorpusProvenance(provenance, 128, 8); err == nil {
		t.Fatal("missing prefix identity accepted")
	}
}

func TestResultsConsistentChecksMaterializedHashes(t *testing.T) {
	base := Result{BatchID: "batch", Plaintexts: []string{"0x01"}, Materialized: MaterializedTransactionSet{
		AgreedSetHash: "agreed", MergedSetHash: "merged", SelectedGas: 21000,
		PlaintextHashes: []string{"plain"}, EthereumTxHashes: []string{"eth"},
	}}
	equivalent := base
	equivalent.Materialized.AgreedSetHash = "different-agreed-subset"
	if !resultsConsistent([]Result{base, equivalent}) {
		t.Fatal("different ACS accepted-list hashes rejected despite identical materialized output")
	}
	other := base
	other.Materialized.MergedSetHash = "different"
	if resultsConsistent([]Result{base, other}) {
		t.Fatal("different merged-set hashes were accepted as consistent")
	}
}

func TestPollResultsReportsNonPrefixCompletions(t *testing.T) {
	muxes := make([]*http.ServeMux, 4)
	servers := make([]*httptest.Server, 4)
	for i := range muxes {
		muxes[i] = http.NewServeMux()
		servers[i] = httptest.NewServer(muxes[i])
		defer servers[i].Close()
	}
	for _, id := range []int{0, 1, 3} {
		id := id
		muxes[id].HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, Result{Slot: 9, NodeID: uint64(id), Metrics: Metrics{MetricsFinalized: true}})
		})
	}
	muxes[2].HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
	})

	results, err := pollResultsForSlotWithURL(http.DefaultClient, 4, 9, time.Nanosecond, func(id int) string {
		return servers[id].URL + "/result"
	})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if got := fmt.Sprint(err); got != "timed out waiting for 4 node results for slot 9; got 3; missing nodes: [2]" {
		t.Fatalf("error = %q", got)
	}
	if results[2].NodeID != 3 {
		t.Fatalf("non-prefix result was not retained: %+v", results)
	}
}

func TestPollResultsStopsOnTerminalFailureAndRetainsReason(t *testing.T) {
	muxes := make([]*http.ServeMux, 3)
	servers := make([]*httptest.Server, 3)
	for i := range muxes {
		muxes[i] = http.NewServeMux()
		servers[i] = httptest.NewServer(muxes[i])
		defer servers[i].Close()
	}
	muxes[0].HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Result{Slot: 12, NodeID: 0, Metrics: Metrics{MetricsFinalized: true}})
	})
	muxes[1].HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnprocessableEntity, struct {
			Status string `json:"status"`
			SlotFailure
		}{Status: "failed", SlotFailure: SlotFailure{Slot: 12, Reason: "share", FailedAtUnixNano: 42}})
	})
	muxes[2].HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
	})

	started := time.Now()
	results, err := pollResultsForSlotWithURL(http.DefaultClient, 3, 12, time.Minute, func(id int) string {
		return servers[id].URL + "/result"
	})
	if err == nil {
		t.Fatal("expected terminal failure")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("terminal failure was treated as pending: %v", time.Since(started))
	}
	if len(results) != 1 || results[0].NodeID != 0 {
		t.Fatalf("completed results were not retained: %+v", results)
	}
	if got := err.Error(); got != "node 1 reported terminal failure for slot 12: share" {
		t.Fatalf("error = %q", got)
	}
}

func TestTerminalFailureRemainsVisibleAndExcludedFromLatencySummary(t *testing.T) {
	scenario := evalScenario{ID: "n4-b128-libp2p", Nodes: 4, BatchSize: 128, Network: "libp2p"}
	run := EvalRun{
		RunID: "measured-r001-n4-b128-libp2p", ScenarioID: scenario.ID, Phase: "measured",
		Slot: 12, Nodes: 4, BatchSize: 128, Network: "libp2p",
		Error: "node 1 reported terminal failure for slot 12: share",
	}
	path := filepath.Join(t.TempDir(), "run_measurements.csv")
	if err := writeRunMeasurements(path, []EvalRun{run}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV rows = %d, want 2", len(rows))
	}
	errorColumn := -1
	for i, name := range rows[0] {
		if name == "error" {
			errorColumn = i
			break
		}
	}
	if errorColumn < 0 || rows[1][errorColumn] != run.Error {
		t.Fatalf("terminal reason missing from CSV: %v", rows)
	}
	localDir := t.TempDir()
	if err := writeEvalOutputs(localDir, []EvalRun{run}); err != nil {
		t.Fatal(err)
	}
	localFile, err := os.Open(filepath.Join(localDir, "summary.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer localFile.Close()
	localRows, err := csv.NewReader(localFile).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(localRows) != 2 {
		t.Fatalf("eval-local CSV rows = %d, want header plus failed run: %v", len(localRows), localRows)
	}
	localErrorColumn, nodeColumn, totalColumn := -1, -1, -1
	for i, name := range localRows[0] {
		switch name {
		case "error":
			localErrorColumn = i
		case "node_id":
			nodeColumn = i
		case "total_slot_us":
			totalColumn = i
		}
	}
	if localErrorColumn < 0 || localRows[1][localErrorColumn] != run.Error {
		t.Fatalf("terminal reason missing from eval-local CSV: %v", localRows)
	}
	if nodeColumn < 0 || totalColumn < 0 || localRows[1][nodeColumn] != "" || localRows[1][totalColumn] != "" {
		t.Fatalf("failed run acquired a synthetic node or latency sample: %v", localRows[1])
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EvalRun
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error != run.Error {
		t.Fatalf("terminal reason missing from JSONL shape: %+v", decoded)
	}
	summary := summarizeScenarios([]evalScenario{scenario}, []EvalRun{run})[0]
	if summary.Attempted != 1 || summary.Succeeded != 0 || summary.Failed != 1 || summary.Metrics["total_slot_us"].Count != 0 {
		t.Fatalf("terminal failure entered latency summary: %+v", summary)
	}
}
