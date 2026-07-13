package main

import (
	"btd/be"
	"btd/curves"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
	"go.dedis.ch/kyber/v4/share"
)

const attributionSchemaVersion = "bloc-bte-attribution/v1"

type runOptions struct {
	BatchSizes  []int
	Warmups     int
	Repetitions int
	TxSize      int
	BMax        int
	OutDir      string
	HostLabel   string
	Instance    string
	Zone        string
	GitCommit   string
	ImageTag    string
	Seed        int64
	Variants    []string
	CPUProfile  string
}

type variantMeta struct {
	Name       string `json:"name"`
	Topology   string `json:"topology"`
	BatchSize  int    `json:"batch_size"`
	NodeCount  int    `json:"node_count"`
	Threshold  int    `json:"threshold"`
	BMax       int    `json:"bmax"`
	Alpha      int    `json:"alpha"`
	Verify     bool   `json:"verify"`
	Parallel   bool   `json:"parallel"`
	Hybrid     bool   `json:"hybrid"`
	IndexShape string `json:"index_shape"`
	Planner    string `json:"planner"`
}

type preparedVariant struct {
	meta variantMeta
	run  func() error
}

type measurement struct {
	SchemaVersion string `json:"schema_version"`
	HostLabel     string `json:"host_label"`
	InstanceType  string `json:"instance_type"`
	Zone          string `json:"zone"`
	Variant       string `json:"variant"`
	Topology      string `json:"topology"`
	BatchSize     int    `json:"batch_size"`
	NodeCount     int    `json:"node_count"`
	Threshold     int    `json:"threshold"`
	BMax          int    `json:"bmax"`
	Alpha         int    `json:"alpha"`
	Verify        bool   `json:"verify"`
	Parallel      bool   `json:"parallel"`
	Hybrid        bool   `json:"hybrid"`
	IndexShape    string `json:"index_shape"`
	Planner       string `json:"planner"`
	Phase         string `json:"phase"`
	Iteration     int    `json:"iteration"`
	OrderIndex    int    `json:"order_index"`
	DurationUS    int64  `json:"duration_us"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

type runManifest struct {
	SchemaVersion string        `json:"schema_version"`
	Status        string        `json:"status"`
	InvalidReason string        `json:"invalid_reason,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at"`
	Command       []string      `json:"command"`
	GitCommit     string        `json:"git_commit,omitempty"`
	ImageTag      string        `json:"image_tag,omitempty"`
	HostLabel     string        `json:"host_label"`
	InstanceType  string        `json:"instance_type,omitempty"`
	Zone          string        `json:"zone,omitempty"`
	CPUModel      string        `json:"cpu_model"`
	GoVersion     string        `json:"go_version"`
	GOOS          string        `json:"goos"`
	GOARCH        string        `json:"goarch"`
	NumCPU        int           `json:"num_cpu"`
	GOMAXPROCS    int           `json:"gomaxprocs"`
	BatchSizes    []int         `json:"batch_sizes"`
	Warmups       int           `json:"warmups"`
	Repetitions   int           `json:"repetitions"`
	TxSize        int           `json:"tx_size"`
	BMax          int           `json:"bmax"`
	ScheduleSeed  int64         `json:"schedule_seed"`
	VariantFilter []string      `json:"variant_filter,omitempty"`
	CPUProfile    string        `json:"cpu_profile,omitempty"`
	Variants      []variantMeta `json:"variants"`
}

func runAttribution(args []string) error {
	options, err := parseRunOptions(args)
	if err != nil {
		return err
	}
	if err := prepareOutputDir(options.OutDir); err != nil {
		return err
	}
	started := time.Now().UTC()
	variants, err := buildVariants(options)
	if err != nil {
		return fmt.Errorf("prepare variants: %w", err)
	}
	manifest := runManifest{
		SchemaVersion: attributionSchemaVersion,
		Status:        "running",
		StartedAt:     started,
		Command:       append([]string{"run"}, args...),
		GitCommit:     options.GitCommit,
		ImageTag:      options.ImageTag,
		HostLabel:     options.HostLabel,
		InstanceType:  options.Instance,
		Zone:          options.Zone,
		CPUModel:      cpuModel(),
		GoVersion:     runtime.Version(),
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		NumCPU:        runtime.NumCPU(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		BatchSizes:    options.BatchSizes,
		Warmups:       options.Warmups,
		Repetitions:   options.Repetitions,
		TxSize:        options.TxSize,
		BMax:          options.BMax,
		ScheduleSeed:  options.Seed,
		VariantFilter: options.Variants,
		CPUProfile:    options.CPUProfile,
	}
	for _, variant := range variants {
		manifest.Variants = append(manifest.Variants, variant.meta)
	}
	manifestPath := filepath.Join(options.OutDir, "manifest.json")
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}

	stopProfile, err := startCPUProfile(options.CPUProfile)
	if err != nil {
		return err
	}
	measurements, runErr := executeVariants(options, variants)
	stopProfile()
	if err := writeMeasurements(filepath.Join(options.OutDir, "measurements.csv"), measurements); err != nil {
		return err
	}
	summaries := summarizeMeasurements(measurements)
	if err := writeSummaries(options.OutDir, summaries); err != nil {
		return err
	}
	manifest.FinishedAt = time.Now().UTC()
	if runErr != nil {
		manifest.Status = "invalid"
		manifest.InvalidReason = runErr.Error()
	} else {
		manifest.Status = "complete"
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}
	return runErr
}

func parseRunOptions(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("bte-attribution run", flag.ContinueOnError)
	batchRaw := fs.String("batch-sizes", "8,32,128", "comma-separated batch sizes")
	variantRaw := fs.String("variants", "", "optional comma-separated exact variant names")
	options := runOptions{}
	fs.IntVar(&options.Warmups, "warmups", 5, "warmup repetitions per variant")
	fs.IntVar(&options.Repetitions, "repetitions", 30, "measured repetitions per variant")
	fs.IntVar(&options.TxSize, "tx-size", 256, "hybrid plaintext size in bytes")
	fs.IntVar(&options.BMax, "bmax", 128, "BLOC maximum batch size")
	fs.StringVar(&options.OutDir, "out-dir", "", "output directory")
	fs.StringVar(&options.HostLabel, "host-label", hostname(), "stable host label")
	fs.StringVar(&options.Instance, "instance-type", "", "instance type metadata")
	fs.StringVar(&options.Zone, "zone", "", "availability zone metadata")
	fs.StringVar(&options.GitCommit, "git-commit", "", "git commit metadata")
	fs.StringVar(&options.ImageTag, "image-tag", "", "container image metadata")
	fs.Int64Var(&options.Seed, "schedule-seed", 20260713, "deterministic variant schedule seed")
	fs.StringVar(&options.CPUProfile, "cpu-profile", "", "optional CPU profile output path")
	if err := fs.Parse(args); err != nil {
		return options, err
	}
	batches, err := parseIntList(*batchRaw)
	if err != nil {
		return options, err
	}
	options.BatchSizes = batches
	options.Variants = parseStringList(*variantRaw)
	if options.OutDir == "" {
		return options, errors.New("--out-dir is required")
	}
	if options.Warmups < 0 || options.Repetitions < 1 || options.TxSize < 1 || options.BMax < 1 {
		return options, errors.New("warmups must be non-negative and repetitions, tx-size, and bmax must be positive")
	}
	for _, batch := range options.BatchSizes {
		if batch < 1 || batch > options.BMax {
			return options, fmt.Errorf("batch size %d outside 1..bmax=%d", batch, options.BMax)
		}
	}
	return options, nil
}

func buildVariants(options runOptions) ([]preparedVariant, error) {
	var variants []preparedVariant
	for _, batch := range options.BatchSizes {
		paper, err := newRawFixture(batch, batch, 10, 2, uniqueIndices(batch), paperSubBatches)
		if err != nil {
			return nil, err
		}
		variants = append(variants,
			paper.variant("paper-opt2-sequential-t2", "paper", false, false, "unique", "floor-2sqrt"),
			paper.variant("paper-opt2-parallel-t2", "paper", false, true, "unique", "floor-2sqrt"),
		)

		blocUnique, err := newRawFixture(batch, options.BMax, 10, 2, uniqueIndices(batch), blocSubBatches)
		if err != nil {
			return nil, err
		}
		variants = append(variants, blocUnique.variant("bloc-core-unique-t2-unverified", "bloc-config", false, false, "unique", "bloc-plan"))

		for _, topology := range []struct {
			name      string
			nodes     int
			threshold int
		}{{"n4", 4, 3}, {"n7", 7, 5}} {
			indices := repeatedIndices(batch, topology.nodes)
			thresholdTwo, err := newRawFixture(batch, options.BMax, topology.nodes, 2, indices, blocSubBatches)
			if err != nil {
				return nil, err
			}
			variants = append(variants, thresholdTwo.variant("bloc-core-"+topology.name+"-t2-unverified", topology.name, false, false, "round-robin-repeated", "bloc-plan"))

			actual, err := newRawFixture(batch, options.BMax, topology.nodes, topology.threshold, indices, blocSubBatches)
			if err != nil {
				return nil, err
			}
			variants = append(variants,
				actual.variant(fmt.Sprintf("bloc-core-%s-t%d-unverified", topology.name, topology.threshold), topology.name, false, false, "round-robin-repeated", "bloc-plan"),
				actual.variant(fmt.Sprintf("bloc-core-%s-t%d-verified", topology.name, topology.threshold), topology.name, true, false, "round-robin-repeated", "bloc-plan"),
			)

			hybrid, err := newHybridFixture(batch, options.BMax, topology.nodes, topology.threshold, indices, options.TxSize)
			if err != nil {
				return nil, err
			}
			variants = append(variants, hybrid.variant(fmt.Sprintf("bloc-hybrid-%s-t%d-verified", topology.name, topology.threshold), topology.name))
		}
	}
	if len(options.Variants) == 0 {
		return variants, nil
	}
	wanted := make(map[string]bool, len(options.Variants))
	for _, name := range options.Variants {
		wanted[name] = true
	}
	filtered := variants[:0]
	for _, variant := range variants {
		if wanted[variant.meta.Name] {
			filtered = append(filtered, variant)
			delete(wanted, variant.meta.Name)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for name := range wanted {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown variant(s): %s", strings.Join(missing, ", "))
	}
	return filtered, nil
}

type rawSubBatch struct {
	cts      []be.CT
	messages []kyber.Point
	shares   []*share.PubShare
}

type rawFixture struct {
	batchSize int
	bmax      int
	nodes     int
	threshold int
	alpha     int
	btd       *be.BTD
	subs      []rawSubBatch
}

type subBatchPlanner func([]be.CT, []kyber.Point) ([]rawSubBatch, error)

func newRawFixture(batch, bmax, nodes, threshold int, indices []int, planner subBatchPlanner) (*rawFixture, error) {
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	btd := be.NewBTD(suite, bmax)
	_, pk := btd.KeyGen(nodes, threshold)
	cts := make([]be.CT, batch)
	messages := make([]kyber.Point, batch)
	for i := 0; i < batch; i++ {
		messages[i] = suite.PickGT()
		ct, err := btd.Enc(pk, indices[i], messages[i])
		if err != nil {
			return nil, err
		}
		cts[i] = ct
	}
	subs, err := planner(cts, messages)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		subs[i].shares = make([]*share.PubShare, threshold)
		for operator := 0; operator < threshold; operator++ {
			decShare, err := btd.BatchDec(subs[i].cts, operator, false)
			if err != nil {
				return nil, err
			}
			subs[i].shares[operator] = decShare
		}
	}
	return &rawFixture{batchSize: batch, bmax: bmax, nodes: nodes, threshold: threshold, alpha: len(subs), btd: btd, subs: subs}, nil
}

func (f *rawFixture) variant(name, topology string, verify, parallel bool, indexShape, planner string) preparedVariant {
	meta := variantMeta{Name: name, Topology: topology, BatchSize: f.batchSize, NodeCount: f.nodes, Threshold: f.threshold, BMax: f.bmax, Alpha: f.alpha, Verify: verify, Parallel: parallel, IndexShape: indexShape, Planner: planner}
	return preparedVariant{meta: meta, run: func() error { return f.combine(verify, parallel) }}
}

func (f *rawFixture) combine(verify, parallel bool) error {
	combine := func(sub rawSubBatch) error {
		messages, _, err := f.btd.BatchCombineMessages(sub.cts, sub.shares, verify)
		if err != nil {
			return err
		}
		if len(messages) != len(sub.messages) {
			return fmt.Errorf("got %d messages, want %d", len(messages), len(sub.messages))
		}
		for i := range messages {
			if !messages[i].Equal(sub.messages[i]) {
				return fmt.Errorf("message %d mismatch", i)
			}
		}
		return nil
	}
	if !parallel {
		for _, sub := range f.subs {
			if err := combine(sub); err != nil {
				return err
			}
		}
		return nil
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(f.subs))
	for _, sub := range f.subs {
		sub := sub
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := combine(sub); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return err
	}
	return nil
}

type hybridFixture struct {
	batchSize int
	bmax      int
	nodes     int
	threshold int
	plan      be.BatchPlan
	shares    []be.DecryptionShare
	cluster   *be.ClusterBTE
	raw       [][]byte
}

func newHybridFixture(batch, bmax, nodes, threshold int, indices []int, txSize int) (*hybridFixture, error) {
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	btd := be.NewBTD(suite, bmax)
	secretShares, pk := btd.KeyGen(nodes, threshold)
	cluster := be.NewClusterBTE(btd, pk, secretShares)
	ciphertexts := make([]be.Ciphertext, batch)
	raw := make([][]byte, batch)
	for i := 0; i < batch; i++ {
		raw[i] = make([]byte, txSize)
		for j := range raw[i] {
			raw[i][j] = byte((i + j) % 251)
		}
		ct, err := cluster.EncryptTx(raw[i], indices[i], "bte-attribution", 1)
		if err != nil {
			return nil, err
		}
		ciphertexts[i] = ct
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	if err != nil {
		return nil, err
	}
	var decShares []be.DecryptionShare
	for _, secretShare := range cluster.Shares[:threshold] {
		for subBatchID := range plan.SubBatches {
			decShare, err := cluster.MakeShare(secretShare, plan, subBatchID)
			if err != nil {
				return nil, err
			}
			decShares = append(decShares, decShare)
		}
	}
	return &hybridFixture{batchSize: batch, bmax: bmax, nodes: nodes, threshold: threshold, plan: plan, shares: decShares, cluster: cluster, raw: raw}, nil
}

func (f *hybridFixture) variant(name, topology string) preparedVariant {
	meta := variantMeta{Name: name, Topology: topology, BatchSize: f.batchSize, NodeCount: f.nodes, Threshold: f.threshold, BMax: f.bmax, Alpha: f.plan.Alpha, Verify: true, Hybrid: true, IndexShape: "round-robin-repeated", Planner: "bloc-plan"}
	return preparedVariant{meta: meta, run: func() error {
		results, err := f.cluster.CombineShares(f.plan, f.shares)
		if err != nil {
			return err
		}
		if len(results) != len(f.raw) {
			return fmt.Errorf("got %d plaintexts, want %d", len(results), len(f.raw))
		}
		for i := range results {
			if results[i].Err != nil || !results[i].HashOK || string(results[i].RawTx) != string(f.raw[i]) {
				return fmt.Errorf("plaintext %d failed validation", i)
			}
		}
		return nil
	}}
}

func paperSubBatches(cts []be.CT, messages []kyber.Point) ([]rawSubBatch, error) {
	alpha := int(math.Floor(2 * math.Sqrt(float64(len(cts)))))
	if alpha < 1 {
		alpha = 1
	}
	length := float64(len(cts)) / float64(alpha)
	subs := make([]rawSubBatch, alpha)
	for i := 0; i < alpha; i++ {
		start := int(math.Round(float64(i) * length))
		end := int(math.Round(float64(i+1) * length))
		if i == alpha-1 {
			end = len(cts)
		}
		subs[i] = rawSubBatch{cts: cts[start:end], messages: messages[start:end]}
	}
	return subs, nil
}

func blocSubBatches(cts []be.CT, messages []kyber.Point) ([]rawSubBatch, error) {
	counts := make(map[int]int)
	for _, ct := range cts {
		counts[ct.I]++
	}
	alpha := int(math.Ceil(2 * math.Sqrt(float64(len(cts)))))
	for _, count := range counts {
		if count > alpha {
			alpha = count
		}
	}
	if alpha > len(cts) {
		alpha = len(cts)
	}
	type item struct {
		position int
		ct       be.CT
		message  kyber.Point
	}
	items := make([]item, len(cts))
	for i := range cts {
		items[i] = item{position: i, ct: cts[i], message: messages[i]}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := counts[items[i].ct.I], counts[items[j].ct.I]
		if left != right {
			return left > right
		}
		return items[i].position < items[j].position
	})
	subs := make([]rawSubBatch, alpha)
	seen := make([]map[int]bool, alpha)
	for i := range seen {
		seen[i] = make(map[int]bool)
	}
	for i, item := range items {
		subID := i % alpha
		if seen[subID][item.ct.I] {
			return nil, fmt.Errorf("duplicate index %d in sub-batch %d", item.ct.I, subID)
		}
		seen[subID][item.ct.I] = true
		subs[subID].cts = append(subs[subID].cts, item.ct)
		subs[subID].messages = append(subs[subID].messages, item.message)
	}
	return subs, nil
}

func executeVariants(options runOptions, variants []preparedVariant) ([]measurement, error) {
	rng := rand.New(rand.NewSource(options.Seed))
	var out []measurement
	var failures []string
	orderIndex := 0
	for _, phase := range []struct {
		name  string
		count int
	}{{"warmup", options.Warmups}, {"measured", options.Repetitions}} {
		for iteration := 1; iteration <= phase.count; iteration++ {
			order := rng.Perm(len(variants))
			for _, variantIndex := range order {
				variant := variants[variantIndex]
				orderIndex++
				started := time.Now()
				err := variant.run()
				duration := time.Since(started).Microseconds()
				row := measurement{SchemaVersion: attributionSchemaVersion, HostLabel: options.HostLabel, InstanceType: options.Instance, Zone: options.Zone, Variant: variant.meta.Name, Topology: variant.meta.Topology, BatchSize: variant.meta.BatchSize, NodeCount: variant.meta.NodeCount, Threshold: variant.meta.Threshold, BMax: variant.meta.BMax, Alpha: variant.meta.Alpha, Verify: variant.meta.Verify, Parallel: variant.meta.Parallel, Hybrid: variant.meta.Hybrid, IndexShape: variant.meta.IndexShape, Planner: variant.meta.Planner, Phase: phase.name, Iteration: iteration, OrderIndex: orderIndex, DurationUS: duration, Success: err == nil}
				if err != nil {
					row.Error = err.Error()
					failures = append(failures, fmt.Sprintf("%s/%s/%d: %v", phase.name, variant.meta.Name, iteration, err))
				}
				out = append(out, row)
				fmt.Printf("phase=%s iteration=%d variant=%s batch=%d duration_us=%d success=%t\n", phase.name, iteration, variant.meta.Name, variant.meta.BatchSize, duration, err == nil)
			}
		}
	}
	if len(failures) > 0 {
		return out, fmt.Errorf("%d attribution operation(s) failed; first: %s", len(failures), failures[0])
	}
	return out, nil
}

func uniqueIndices(batch int) []int {
	indices := make([]int, batch)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

func repeatedIndices(batch, nodes int) []int {
	indices := make([]int, batch)
	for i := range indices {
		indices[i] = i / nodes
	}
	return indices
}

func parseIntList(raw string) ([]int, error) {
	var out []int
	seen := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 1 {
			return nil, fmt.Errorf("invalid positive integer %q", part)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one batch size is required")
	}
	return out, nil
}

func parseStringList(raw string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create CPU profile directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		_ = file.Close()
	}, nil
}

func prepareOutputDir(path string) error {
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return fmt.Errorf("output directory %s is not empty", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func writeMeasurements(path string, rows []measurement) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	defer w.Flush()
	header := []string{"schema_version", "host_label", "instance_type", "zone", "variant", "topology", "batch_size", "node_count", "threshold", "bmax", "alpha", "verify", "parallel", "hybrid", "index_shape", "planner", "phase", "iteration", "order_index", "duration_us", "success", "error"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		values := []string{row.SchemaVersion, row.HostLabel, row.InstanceType, row.Zone, row.Variant, row.Topology, strconv.Itoa(row.BatchSize), strconv.Itoa(row.NodeCount), strconv.Itoa(row.Threshold), strconv.Itoa(row.BMax), strconv.Itoa(row.Alpha), strconv.FormatBool(row.Verify), strconv.FormatBool(row.Parallel), strconv.FormatBool(row.Hybrid), row.IndexShape, row.Planner, row.Phase, strconv.Itoa(row.Iteration), strconv.Itoa(row.OrderIndex), strconv.FormatInt(row.DurationUS, 10), strconv.FormatBool(row.Success), row.Error}
		if err := w.Write(values); err != nil {
			return err
		}
	}
	return w.Error()
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

func cpuModel() string {
	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.ToLower(line), "model name") {
				if _, value, ok := strings.Cut(line, ":"); ok {
					return strings.TrimSpace(value)
				}
			}
		}
	}
	if value := os.Getenv("PROCESSOR_IDENTIFIER"); value != "" {
		return value
	}
	return "unknown"
}
