package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	manifest := suiteManifest{Profile: "m1-baseline", PlannedRuns: 315, BMax: 128, TxSize: 256, TxGas: 21000, FeeStartWei: 1000, FeeStepWei: 1, Timeout: "30s"}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"profile": "m1-baseline", "planned_runs": float64(315), "bmax": float64(128), "tx_size": float64(256), "tx_gas": float64(21000), "timeout": "30s"} {
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
		BMax: 128, TxSize: 256, TxGas: 21000, CriticalNodeID: 2,
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
	for name, want := range map[string]string{"bmax": "128", "tx_size": "256", "tx_gas": "21000", "combine_attempts": "1"} {
		if got := records[1][index[name]]; got != want {
			t.Fatalf("%s = %s, want %s", name, got, want)
		}
	}
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
