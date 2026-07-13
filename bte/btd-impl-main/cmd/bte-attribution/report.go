package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var paperOpt2US = map[int]float64{8: 15400, 32: 74200, 128: 500800}

type reportOptions struct {
	CampaignDir string
	SameAZData  string
	CrossAZData string
	Out         string
}

type placementAggregate struct {
	Instance   string
	Variant    string
	Topology   string
	BatchSize  int
	HostP50    []float64
	Placements map[string]float64
}

type sidecarAggregate struct {
	Label        string
	Nodes        int
	BatchSize    int
	AllNodeMean  float64
	AllNodeP50   float64
	CriticalMean float64
	CriticalP50  float64
}

func reportAttribution(args []string) error {
	options, err := parseReportOptions(args)
	if err != nil {
		return err
	}
	summaries, err := readCampaignSummaries(options.CampaignDir)
	if err != nil {
		return err
	}
	aggregates := aggregatePlacements(summaries)
	var sidecars []sidecarAggregate
	for _, dataset := range []struct{ label, path string }{{"same-AZ", options.SameAZData}, {"cross-AZ", options.CrossAZData}} {
		if dataset.path == "" {
			continue
		}
		rows, err := readSidecarPath(dataset.label, dataset.path)
		if err != nil {
			return err
		}
		sidecars = append(sidecars, rows...)
	}
	report := renderReport(aggregates, sidecars)
	if err := os.MkdirAll(filepath.Dir(options.Out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(options.Out, []byte(report), 0o644)
}

func readSidecarPath(label, path string) ([]sidecarAggregate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return readSidecarMeasurements(label, path)
	}
	var paths []string
	err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "scenarios" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "node_measurements.csv" {
			paths = append(paths, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no node_measurements.csv files under %s", path)
	}
	sort.Strings(paths)
	var out []sidecarAggregate
	for _, candidate := range paths {
		rows, err := readSidecarMeasurements(label, candidate)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func parseReportOptions(args []string) (reportOptions, error) {
	fs := flag.NewFlagSet("bte-attribution report", flag.ContinueOnError)
	options := reportOptions{}
	fs.StringVar(&options.CampaignDir, "campaign-dir", "", "campaign artifact directory")
	fs.StringVar(&options.SameAZData, "same-az-data", "", "same-AZ node_measurements.csv")
	fs.StringVar(&options.CrossAZData, "cross-az-data", "", "cross-AZ node_measurements.csv")
	fs.StringVar(&options.Out, "out", "", "Markdown report output")
	if err := fs.Parse(args); err != nil {
		return options, err
	}
	if options.CampaignDir == "" || options.Out == "" {
		return options, fmt.Errorf("--campaign-dir and --out are required")
	}
	return options, nil
}

func readCampaignSummaries(root string) ([]scenarioSummary, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "scenario_summary.csv" && filepath.Base(filepath.Dir(path)) == "timed" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		rootSummary := filepath.Join(root, "scenario_summary.csv")
		if _, err := os.Stat(rootSummary); err != nil {
			return nil, fmt.Errorf("no scenario_summary.csv files under %s", root)
		}
		paths = append(paths, rootSummary)
	}
	var out []scenarioSummary
	for _, path := range paths {
		rows, err := readScenarioSummaryCSV(path)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func readScenarioSummaryCSV(path string) ([]scenarioSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("summary %s has no data", path)
	}
	header := csvHeader(rows[0])
	var out []scenarioSummary
	for _, row := range rows[1:] {
		batch, err := csvInt(row, header, "batch_size")
		if err != nil {
			return nil, err
		}
		threshold, _ := csvInt(row, header, "threshold")
		alpha, _ := csvInt(row, header, "alpha")
		runs, _ := csvInt(row, header, "runs")
		p50, err := csvFloat(row, header, "p50_us")
		if err != nil {
			return nil, err
		}
		out = append(out, scenarioSummary{HostLabel: csvValue(row, header, "host_label"), InstanceType: csvValue(row, header, "instance_type"), Zone: csvValue(row, header, "zone"), Variant: csvValue(row, header, "variant"), Topology: csvValue(row, header, "topology"), BatchSize: batch, Threshold: threshold, Alpha: alpha, Runs: runs, P50US: p50})
	}
	return out, nil
}

func aggregatePlacements(rows []scenarioSummary) []placementAggregate {
	type key struct {
		instance, variant, topology string
		batch                       int
	}
	groups := make(map[key][]float64)
	placements := make(map[key]map[string]float64)
	for _, row := range rows {
		k := key{row.InstanceType, row.Variant, row.Topology, row.BatchSize}
		groups[k] = append(groups[k], row.P50US)
		if placements[k] == nil {
			placements[k] = make(map[string]float64)
		}
		placements[k][row.HostLabel] = row.P50US
	}
	var out []placementAggregate
	for k, values := range groups {
		sort.Float64s(values)
		out = append(out, placementAggregate{Instance: k.instance, Variant: k.variant, Topology: k.topology, BatchSize: k.batch, HostP50: values, Placements: placements[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		if out[i].BatchSize != out[j].BatchSize {
			return out[i].BatchSize < out[j].BatchSize
		}
		return out[i].Variant < out[j].Variant
	})
	return out
}

func readSidecarMeasurements(label, path string) ([]sidecarAggregate, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("sidecar data %s has no rows", path)
	}
	header := csvHeader(rows[0])
	type key struct{ nodes, batch int }
	type values struct{ all, critical []float64 }
	groups := make(map[key]*values)
	for _, row := range rows[1:] {
		if csvValue(row, header, "phase") != "measured" || csvValue(row, header, "success") != "true" || csvValue(row, header, "consistent") != "true" {
			continue
		}
		scenario := csvValue(row, header, "scenario_id")
		nodes, batch, ok := parseScenarioID(scenario)
		if !ok {
			continue
		}
		duration, err := csvFloat(row, header, "combine_us")
		if err != nil {
			return nil, err
		}
		k := key{nodes, batch}
		if groups[k] == nil {
			groups[k] = &values{}
		}
		groups[k].all = append(groups[k].all, duration)
		if csvValue(row, header, "critical_node") == "true" {
			groups[k].critical = append(groups[k].critical, duration)
		}
	}
	var out []sidecarAggregate
	for k, values := range groups {
		sort.Float64s(values.all)
		sort.Float64s(values.critical)
		out = append(out, sidecarAggregate{Label: label, Nodes: k.nodes, BatchSize: k.batch, AllNodeMean: mean(values.all), AllNodeP50: percentileType7(values.all, 0.5), CriticalMean: mean(values.critical), CriticalP50: percentileType7(values.critical, 0.5)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		if out[i].Nodes != out[j].Nodes {
			return out[i].Nodes < out[j].Nodes
		}
		return out[i].BatchSize < out[j].BatchSize
	})
	return out, nil
}

func renderReport(aggregates []placementAggregate, sidecars []sidecarAggregate) string {
	instances := uniqueInstances(aggregates)
	var b strings.Builder
	b.WriteString("# BTE Combine Discrepancy Attribution Report\n\n")
	b.WriteString("## Methodology\n\n")
	b.WriteString("The published baseline is BEAT-MEV Figure 5's sequential Opt-2 combine on an AMD Ryzen 7 5800X: 15.4 ms at B=8, 74.2 ms at B=32, and 500.8 ms at B=128. The inherited benchmark uses n=10, t=2, unique indices, floor(2*sqrt(B)) contiguous sub-batches, and verification disabled. The controlled ladder preserves that configuration first, then changes BMax/planning, index distribution, threshold, proof verification, and the hybrid wrapper one dimension at a time. Values are placement-median p50 durations; ranges show the minimum and maximum host p50. Factor ratios are paired comparisons and are not additive.\n\n")
	b.WriteString("## Paper-Equivalent Hardware Comparison\n\n")
	b.WriteString("| Instance | Batch | Paper | Measured p50 | Placement range | Ratio |\n|---|---:|---:|---:|---:|---:|\n")
	for _, instance := range instances {
		for _, batch := range []int{8, 32, 128} {
			measured, ok := findAggregate(aggregates, instance, "paper-opt2-sequential-t2", batch)
			paper, hasPaper := paperOpt2US[batch]
			if !ok || !hasPaper {
				continue
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %.2f ms | %.2f ms | %.2f--%.2f ms | %.2fx |\n", instance, batch, paper/1000, placementMedian(measured)/1000, measured[0]/1000, measured[len(measured)-1]/1000, placementMedian(measured)/paper))
		}
	}
	b.WriteString("\n## Paired Attribution Ladder\n\n")
	b.WriteString("| Instance | Topology | Batch | Comparison | From p50 | To p50 | Median ratio | Placement ratios | Direction agreement | Classification |\n|---|---|---:|---|---:|---:|---:|---:|---|---|\n")
	for _, instance := range instances {
		for _, topology := range []struct {
			name      string
			threshold int
		}{{"n4", 3}, {"n7", 5}} {
			for _, batch := range []int{8, 32, 128} {
				steps := [][3]string{
					{"configuration", "paper-opt2-sequential-t2", "bloc-core-unique-t2-unverified"},
					{"index distribution", "bloc-core-unique-t2-unverified", "bloc-core-" + topology.name + "-t2-unverified"},
					{"threshold", "bloc-core-" + topology.name + "-t2-unverified", fmt.Sprintf("bloc-core-%s-t%d-unverified", topology.name, topology.threshold)},
					{"proof verification", fmt.Sprintf("bloc-core-%s-t%d-unverified", topology.name, topology.threshold), fmt.Sprintf("bloc-core-%s-t%d-verified", topology.name, topology.threshold)},
					{"hybrid wrapper", fmt.Sprintf("bloc-core-%s-t%d-verified", topology.name, topology.threshold), fmt.Sprintf("bloc-hybrid-%s-t%d-verified", topology.name, topology.threshold)},
				}
				for _, step := range steps {
					fromRow, okFrom := findAggregateRow(aggregates, instance, step[1], batch)
					toRow, okTo := findAggregateRow(aggregates, instance, step[2], batch)
					if !okFrom || !okTo {
						continue
					}
					left, right := placementMedian(fromRow.HostP50), placementMedian(toRow.HostP50)
					ratio := right / left
					paired := pairedRatios(fromRow, toRow)
					b.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %.2f ms | %.2f ms | %.2fx | %s | %s | %s |\n", instance, topology.name, batch, step[0], left/1000, right/1000, ratio, formatRatios(paired), directionAgreement(paired), classifyRatio(ratio)))
				}
			}
		}
	}
	if len(sidecars) > 0 {
		b.WriteString("\n## Existing Integrated Sidecar Context\n\n")
		b.WriteString("| Dataset | Nodes | Batch | All-node mean | All-node p50 | Critical mean | Critical p50 |\n|---|---:|---:|---:|---:|---:|---:|\n")
		for _, row := range sidecars {
			b.WriteString(fmt.Sprintf("| %s | %d | %d | %.2f ms | %.2f ms | %.2f ms | %.2f ms |\n", row.Label, row.Nodes, row.BatchSize, row.AllNodeMean/1000, row.AllNodeP50/1000, row.CriticalMean/1000, row.CriticalP50/1000))
		}
	}
	b.WriteString("\n## Interpretation Rule\n\nA factor is labelled negligible below 5%, secondary from 5% through 15%, and material above 15%. A final causal claim should also agree in direction across both placements of an instance class.\n")
	return b.String()
}

func findAggregate(rows []placementAggregate, instance, variant string, batch int) ([]float64, bool) {
	row, ok := findAggregateRow(rows, instance, variant, batch)
	return row.HostP50, ok
}

func findAggregateRow(rows []placementAggregate, instance, variant string, batch int) (placementAggregate, bool) {
	for _, row := range rows {
		if row.Instance == instance && row.Variant == variant && row.BatchSize == batch {
			return row, len(row.HostP50) > 0
		}
	}
	return placementAggregate{}, false
}

func pairedRatios(from, to placementAggregate) []float64 {
	var out []float64
	for host, left := range from.Placements {
		if right, ok := to.Placements[host]; ok && left > 0 {
			out = append(out, right/left)
		}
	}
	sort.Float64s(out)
	return out
}

func formatRatios(ratios []float64) string {
	if len(ratios) == 0 {
		return "n/a"
	}
	parts := make([]string, len(ratios))
	for i, ratio := range ratios {
		parts[i] = fmt.Sprintf("%.2fx", ratio)
	}
	return strings.Join(parts, ", ")
}

func directionAgreement(ratios []float64) string {
	if len(ratios) < 2 {
		return "insufficient"
	}
	direction := 0
	for _, ratio := range ratios {
		current := -1
		if ratio >= 1 {
			current = 1
		}
		if direction != 0 && current != direction {
			return "mixed"
		}
		direction = current
	}
	return "yes"
}

func placementMedian(values []float64) float64 {
	return percentileType7(values, 0.5)
}

func classifyRatio(ratio float64) string {
	delta := ratio - 1
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta < 0.05:
		return "negligible"
	case delta <= 0.15:
		return "secondary"
	default:
		return "material"
	}
}

func uniqueInstances(rows []placementAggregate) []string {
	seen := make(map[string]bool)
	var out []string
	for _, row := range rows {
		if row.Instance != "" && !seen[row.Instance] {
			seen[row.Instance] = true
			out = append(out, row.Instance)
		}
	}
	sort.Strings(out)
	return out
}

func parseScenarioID(value string) (nodes, batch int, ok bool) {
	parts := strings.Split(value, "-")
	for _, part := range parts {
		if strings.HasPrefix(part, "n") {
			nodes, _ = strconv.Atoi(strings.TrimPrefix(part, "n"))
		}
		if strings.HasPrefix(part, "b") {
			batch, _ = strconv.Atoi(strings.TrimPrefix(part, "b"))
		}
	}
	return nodes, batch, nodes > 0 && batch > 0
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func csvHeader(row []string) map[string]int {
	out := make(map[string]int, len(row))
	for i, value := range row {
		out[value] = i
	}
	return out
}

func csvValue(row []string, header map[string]int, name string) string {
	index, ok := header[name]
	if !ok || index >= len(row) {
		return ""
	}
	return row[index]
}

func csvInt(row []string, header map[string]int, name string) (int, error) {
	value := csvValue(row, header, name)
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q", name, value)
	}
	return parsed, nil
}

func csvFloat(row []string, header map[string]int, name string) (float64, error) {
	value := csvValue(row, header, name)
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q", name, value)
	}
	return parsed, nil
}
