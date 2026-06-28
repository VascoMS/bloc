package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"bloc-node/internal/app/ethdemo"
)

type persistentCluster struct {
	self          string
	root          string
	configPath    string
	nodes         int
	threshold     int
	basePort      int
	generation    int
	currentSlot   uint64
	cancel        context.CancelFunc
	processes     []*exec.Cmd
	logFiles      []*os.File
	client        *http.Client
	clientBackend *http.Transport
}

type persistentRunSpec struct {
	scenario  evalScenario
	phase     string
	iteration int
}

type evalSubmission struct {
	nodeID  int
	request SubmitTxRequest
}

func runPersistentSuite(self, outDir string, options suiteOptions, scenarios []evalScenario, record func(EvalRun) error) ([]EvalRun, []clusterMeasurement, error) {
	groups := make(map[int][]evalScenario)
	var nodeCounts []int
	for _, scenario := range scenarios {
		if _, ok := groups[scenario.Nodes]; !ok {
			nodeCounts = append(nodeCounts, scenario.Nodes)
		}
		groups[scenario.Nodes] = append(groups[scenario.Nodes], scenario)
	}
	sort.Ints(nodeCounts)
	corpora := make(map[string][]evalSubmission, len(scenarios))
	for _, scenario := range scenarios {
		items, err := buildSubmissionCorpus(scenario, options)
		if err != nil {
			return nil, nil, err
		}
		corpora[scenario.ID] = items
	}

	var allRuns []EvalRun
	var measurements []clusterMeasurement
	var nextSlot uint64 = 1
	actualOrder := 0
	for _, nodes := range nodeCounts {
		group := groups[nodes]
		specs := persistentSpecs(group, options, nodes)
		generation := 0
		consecutiveFailures := 0
		needsRecovery := false
		var cluster *persistentCluster

		startCluster := func(reason string) error {
			generation++
			started, measurement, err := startPersistentCluster(self, outDir, options, group[0], generation, nextSlot, reason)
			measurements = append(measurements, measurement)
			if err != nil {
				consecutiveFailures++
				return err
			}
			cluster = started
			return nil
		}

		for _, spec := range specs {
			for cluster == nil {
				reason := "initial"
				if needsRecovery {
					reason = "recovery"
				}
				if err := startCluster(reason); err != nil {
					if consecutiveFailures >= options.MaxRestarts {
						return allRuns, measurements, fmt.Errorf("persistent n%d cluster failed to start after %d attempts: %w", nodes, consecutiveFailures, err)
					}
					continue
				}
				if needsRecovery {
					recoveryOK := true
					for recoveryIndex, scenario := range group {
						actualOrder++
						runID := fmt.Sprintf("recovery-g%03d-%02d-%s", generation, recoveryIndex+1, scenario.ID)
						run, runErr := cluster.runSlot(outDir, runID, scenario, "recovery", recoveryIndex+1, actualOrder, nextSlot, corpora[scenario.ID], options)
						nextSlot++
						if runErr != nil {
							run.Error = runErr.Error()
							run.Success = false
						}
						allRuns = append(allRuns, run)
						if err := record(run); err != nil {
							cluster.close()
							return allRuns, measurements, err
						}
						if !run.Success || !run.Consistent {
							recoveryOK = false
							break
						}
					}
					if !recoveryOK {
						cluster.close()
						cluster = nil
						consecutiveFailures++
						if consecutiveFailures >= options.MaxRestarts {
							return allRuns, measurements, fmt.Errorf("persistent n%d recovery failed after %d generations", nodes, consecutiveFailures)
						}
						continue
					}
					needsRecovery = false
					consecutiveFailures = 0
				}
			}

			actualOrder++
			runID := fmt.Sprintf("%s-r%03d-%s", spec.phase, spec.iteration, spec.scenario.ID)
			run, runErr := cluster.runSlot(outDir, runID, spec.scenario, spec.phase, spec.iteration, actualOrder, nextSlot, corpora[spec.scenario.ID], options)
			nextSlot++
			if runErr != nil {
				run.Error = runErr.Error()
				run.Success = false
			}
			allRuns = append(allRuns, run)
			if err := record(run); err != nil {
				cluster.close()
				return allRuns, measurements, err
			}
			fmt.Printf("%s iteration=%d scenario=%s slot=%d generation=%d success=%t critical_node=%d total_us=%d\n", spec.phase, spec.iteration, spec.scenario.ID, run.Slot, run.ClusterGeneration, run.Success, run.CriticalNodeID, criticalTotalUS(run))
			if !run.Success || !run.Consistent {
				cluster.close()
				cluster = nil
				needsRecovery = true
				consecutiveFailures++
				if consecutiveFailures >= options.MaxRestarts {
					return allRuns, measurements, fmt.Errorf("persistent n%d cluster failed in %d consecutive generations", nodes, consecutiveFailures)
				}
			} else {
				consecutiveFailures = 0
			}
		}
		if cluster != nil {
			cluster.close()
		}
	}
	return allRuns, measurements, nil
}

func persistentSpecs(group []evalScenario, options suiteOptions, nodes int) []persistentRunSpec {
	rng := rand.New(rand.NewSource(options.Seed + int64(nodes)*1_000_003))
	var specs []persistentRunSpec
	for _, phase := range []struct {
		name  string
		count int
	}{{"warmup", options.Warmups}, {"measured", options.Repetitions}} {
		for iteration := 1; iteration <= phase.count; iteration++ {
			order := append([]evalScenario(nil), group...)
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			for _, scenario := range order {
				specs = append(specs, persistentRunSpec{scenario: scenario, phase: phase.name, iteration: iteration})
			}
		}
	}
	return specs
}

func buildSubmissionCorpus(scenario evalScenario, options suiteOptions) ([]evalSubmission, error) {
	items := make([]evalSubmission, scenario.BatchSize)
	for i := 0; i < scenario.BatchSize; i++ {
		nodeID := i % scenario.Nodes
		feeWei := strconv.FormatUint(options.FeeStart+uint64(i)*options.FeeStep, 10)
		rawTx, txSummary, err := ethdemo.Generate(i, options.TxSize, options.TxGas, feeWei, nodeID, uint64(i/scenario.Nodes))
		if err != nil {
			return nil, fmt.Errorf("generate ethereum tx %d for %s: %w", i, scenario.ID, err)
		}
		items[i] = evalSubmission{nodeID: nodeID, request: SubmitTxRequest{
			RawTx: "0x" + fmt.Sprintf("%x", rawTx), Gas: txSummary.Gas,
			EffectiveFeePerGasWei: txSummary.EffectiveFeePerGasWei,
			From:                  txSummary.From, Nonce: txSummary.Nonce, Kind: "placeholder",
		}}
	}
	return items, nil
}

func startPersistentCluster(self, outDir string, options suiteOptions, scenario evalScenario, generation int, initialSlot uint64, reason string) (*persistentCluster, clusterMeasurement, error) {
	measurement := clusterMeasurement{Nodes: scenario.Nodes, Threshold: scenario.Threshold, Generation: generation, Reason: reason, StartedAt: time.Now().UTC()}
	clusterRoot := filepath.Join(outDir, "clusters", fmt.Sprintf("n%d", scenario.Nodes))
	configPath := filepath.Join(clusterRoot, "cluster.json")
	if err := os.MkdirAll(clusterRoot, 0755); err != nil {
		measurement.Error = err.Error()
		return nil, measurement, err
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		args := []string{"gen-config", "--nodes", strconv.Itoa(scenario.Nodes), "--threshold", strconv.Itoa(scenario.Threshold), "--bmax", strconv.Itoa(options.BMax), "--slot", strconv.FormatUint(initialSlot, 10), "--base-http-port", strconv.Itoa(options.BasePort + 1000), "--base-p2p-port", strconv.Itoa(options.BasePort + 2000), "--default-tx-gas", strconv.FormatUint(options.TxGas, 10), "--cluster-id", fmt.Sprintf("%s-n%d", options.ExperimentID, scenario.Nodes), "--out", configPath}
		if out, genErr := exec.Command(self, args...).CombinedOutput(); genErr != nil {
			err = fmt.Errorf("gen-config: %w: %s", genErr, string(out))
			measurement.Error = err.Error()
			return nil, measurement, err
		}
	} else if err != nil {
		measurement.Error = err.Error()
		return nil, measurement, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cluster := &persistentCluster{self: self, root: clusterRoot, configPath: configPath, nodes: scenario.Nodes, threshold: scenario.Threshold, basePort: options.BasePort, generation: generation, currentSlot: initialSlot, cancel: cancel}
	cluster.client, cluster.clientBackend = newEvalHTTPClient()
	generationDir := filepath.Join(clusterRoot, fmt.Sprintf("generation-%03d", generation))
	if err := os.MkdirAll(generationDir, 0755); err != nil {
		cancel()
		measurement.Error = err.Error()
		return nil, measurement, err
	}
	for id := 0; id < scenario.Nodes; id++ {
		logFile, err := os.Create(filepath.Join(generationDir, fmt.Sprintf("node-%d.log", id)))
		if err != nil {
			cluster.close()
			measurement.Error = err.Error()
			return nil, measurement, err
		}
		cluster.logFiles = append(cluster.logFiles, logFile)
		cmd := exec.CommandContext(ctx, self, "run", "--config", configPath, "--id", strconv.Itoa(id), "--slot", strconv.FormatUint(initialSlot, 10))
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			cluster.close()
			measurement.Error = err.Error()
			return nil, measurement, err
		}
		cluster.processes = append(cluster.processes, cmd)
	}
	readinessTimeout := options.Timeout
	if readinessTimeout < 15*time.Second {
		readinessTimeout = 15 * time.Second
	}
	if err := waitForHTTP(cluster.client, scenario.Nodes, options.BasePort+1000, readinessTimeout); err != nil {
		cluster.close()
		measurement.Error = err.Error()
		measurement.StartupUS = time.Since(measurement.StartedAt).Microseconds()
		return nil, measurement, err
	}
	measurement.ReadyAt = time.Now().UTC()
	measurement.StartupUS = measurement.ReadyAt.Sub(measurement.StartedAt).Microseconds()
	measurement.Success = true
	return cluster, measurement, nil
}

func (c *persistentCluster) runSlot(outDir, runID string, scenario evalScenario, phase string, iteration, orderIndex int, slotID uint64, corpus []evalSubmission, options suiteOptions) (EvalRun, error) {
	run := EvalRun{RunID: runID, ScenarioID: scenario.ID, Phase: phase, Iteration: iteration, OrderIndex: orderIndex, Slot: slotID, ClusterGeneration: c.generation, Nodes: scenario.Nodes, Threshold: scenario.Threshold, BMax: options.BMax, BatchSize: scenario.BatchSize, TxSize: options.TxSize, TxGas: options.TxGas, Network: scenario.Network, StartedAt: time.Now(), Results: []Result{}}
	prepareStarted := time.Now()
	if slotID != c.currentSlot {
		if err := parallelNodes(c.nodes, func(id int) error {
			return postJSON(c.client, fmt.Sprintf("http://127.0.0.1:%d/slot/prepare", c.basePort+1000+id), prepareSlotRequest{Slot: slotID})
		}); err != nil {
			run.PrepareUS = time.Since(prepareStarted).Microseconds()
			run.FinishedAt = time.Now()
			return run, fmt.Errorf("prepare slot %d: %w", slotID, err)
		}
		c.currentSlot = slotID
	}
	run.PrepareUS = time.Since(prepareStarted).Microseconds()

	submitStarted := time.Now()
	byNode := make([][]SubmitTxRequest, c.nodes)
	for _, item := range corpus {
		byNode[item.nodeID] = append(byNode[item.nodeID], item.request)
	}
	if err := parallelNodes(c.nodes, func(id int) error {
		for _, request := range byNode[id] {
			url := fmt.Sprintf("http://127.0.0.1:%d/tx?slot=%d", c.basePort+1000+id, slotID)
			if err := postJSON(c.client, url, request); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		run.SubmissionUS = time.Since(submitStarted).Microseconds()
		run.FinishedAt = time.Now()
		return run, fmt.Errorf("submit transactions: %w", err)
	}
	run.SubmissionUS = time.Since(submitStarted).Microseconds()

	harnessStarted := time.Now()
	if err := startNodesForSlot(c.client, c.nodes, c.basePort+1000, slotID); err != nil {
		run.HarnessWallUS = time.Since(harnessStarted).Microseconds()
		run.FinishedAt = time.Now()
		return run, err
	}
	results, err := pollResultsForSlot(c.client, c.nodes, c.basePort+1000, slotID, options.Timeout)
	run.FinishedAt = time.Now()
	run.HarnessWallUS = time.Since(harnessStarted).Microseconds()
	run.Results = results
	run.StartSkewUS = resultStartSkewUS(results)
	run.CriticalNodeID = criticalNodeID(results)
	run.Consistent = resultsConsistent(results)
	run.Success = err == nil && run.Consistent
	c.writeRunArtifacts(outDir, run)
	if err != nil {
		c.captureStatuses(outDir, runID, slotID)
		return run, err
	}
	return run, nil
}

func parallelNodes(nodes int, fn func(int) error) error {
	var wg sync.WaitGroup
	errs := make(chan error, nodes)
	for id := 0; id < nodes; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := fn(id); err != nil {
				errs <- fmt.Errorf("node %d: %w", id, err)
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
	}
	return first
}

func startNodesForSlot(client *http.Client, nodes, baseHTTP int, slotID uint64) error {
	return parallelNodes(nodes, func(id int) error {
		return postBytes(client, fmt.Sprintf("http://127.0.0.1:%d/start?slot=%d", baseHTTP+id, slotID), nil)
	})
}

func pollResultsForSlot(client *http.Client, nodes, baseHTTP int, slotID uint64, timeout time.Duration) ([]Result, error) {
	return pollResultsForSlotWithURL(client, nodes, slotID, timeout, func(id int) string {
		return fmt.Sprintf("http://127.0.0.1:%d/result?slot=%d", baseHTTP+id, slotID)
	})
}

func pollResultsForSlotWithURL(client *http.Client, nodes int, slotID uint64, timeout time.Duration, urlFor func(int) string) ([]Result, error) {
	deadline := time.Now().Add(timeout)
	for {
		results, missing, err := pollResultsOnce(client, nodes, slotID, urlFor)
		if err != nil {
			return results, err
		}
		if len(results) == nodes {
			return results, nil
		}
		if time.Now().After(deadline) {
			return results, fmt.Errorf("timed out waiting for %d node results for slot %d; got %d; missing nodes: %v", nodes, slotID, len(results), missing)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pollResultsOnce(client *http.Client, nodes int, slotID uint64, urlFor func(int) string) ([]Result, []int, error) {
	results := make([]Result, 0, nodes)
	missing := make([]int, 0, nodes)
	for id := 0; id < nodes; id++ {
		resp, err := client.Get(urlFor(id))
		if err != nil {
			missing = append(missing, id)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			missing = append(missing, id)
			continue
		}
		var result Result
		if err := json.Unmarshal(body, &result); err != nil {
			return results, missing, err
		}
		if result.Slot != slotID || !result.Metrics.MetricsFinalized {
			missing = append(missing, id)
			continue
		}
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].NodeID < results[j].NodeID })
	return results, missing, nil
}

func (c *persistentCluster) writeRunArtifacts(outDir string, run EvalRun) {
	runDir := filepath.Join(outDir, "runs", run.RunID)
	_ = os.MkdirAll(runDir, 0755)
	for _, result := range run.Results {
		_ = writeJSONFile(filepath.Join(runDir, fmt.Sprintf("node-%d-result.json", result.NodeID)), result)
	}
}

func (c *persistentCluster) captureStatuses(outDir, runID string, slotID uint64) {
	statuses := make(map[string]json.RawMessage)
	for id := 0; id < c.nodes; id++ {
		resp, err := c.client.Get(fmt.Sprintf("http://127.0.0.1:%d/slot/status?slot=%d", c.basePort+1000+id, slotID))
		if err != nil {
			statuses[strconv.Itoa(id)] = json.RawMessage(fmt.Sprintf("%q", err.Error()))
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		statuses[strconv.Itoa(id)] = append(json.RawMessage(nil), body...)
	}
	_ = writeJSONFile(filepath.Join(outDir, "runs", runID, "slot-status.json"), statuses)
}

func (c *persistentCluster) close() {
	if c == nil {
		return
	}
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	for _, cmd := range c.processes {
		_ = cmd.Wait()
	}
	c.processes = nil
	if c.clientBackend != nil {
		c.clientBackend.CloseIdleConnections()
	}
	for _, file := range c.logFiles {
		_ = file.Close()
	}
	c.logFiles = nil
}

func writeClusterMeasurements(path string, measurements []clusterMeasurement) error {
	header := []string{"nodes", "threshold", "generation", "reason", "started_at", "ready_at", "startup_us", "success", "error"}
	return writeCSV(path, header, func(w *csv.Writer) error {
		for _, m := range measurements {
			record := []string{strconv.Itoa(m.Nodes), strconv.Itoa(m.Threshold), strconv.Itoa(m.Generation), m.Reason, m.StartedAt.Format(time.RFC3339Nano), m.ReadyAt.Format(time.RFC3339Nano), strconv.FormatInt(m.StartupUS, 10), strconv.FormatBool(m.Success), m.Error}
			if err := w.Write(record); err != nil {
				return err
			}
		}
		return nil
	})
}
