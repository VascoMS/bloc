package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

type scenarioSummary struct {
	HostLabel    string  `json:"host_label"`
	InstanceType string  `json:"instance_type"`
	Zone         string  `json:"zone"`
	Variant      string  `json:"variant"`
	Topology     string  `json:"topology"`
	BatchSize    int     `json:"batch_size"`
	Threshold    int     `json:"threshold"`
	Alpha        int     `json:"alpha"`
	Runs         int     `json:"runs"`
	MeanUS       float64 `json:"mean_us"`
	P50US        float64 `json:"p50_us"`
	P95US        float64 `json:"p95_us"`
	MinUS        float64 `json:"min_us"`
	MaxUS        float64 `json:"max_us"`
}

func summarizeMeasurements(rows []measurement) []scenarioSummary {
	type key struct {
		host, instance, zone, variant, topology string
		batch, threshold, alpha                 int
	}
	groups := make(map[key][]float64)
	for _, row := range rows {
		if row.Phase != "measured" || !row.Success {
			continue
		}
		k := key{row.HostLabel, row.InstanceType, row.Zone, row.Variant, row.Topology, row.BatchSize, row.Threshold, row.Alpha}
		groups[k] = append(groups[k], float64(row.DurationUS))
	}
	var out []scenarioSummary
	for k, values := range groups {
		sort.Float64s(values)
		var total float64
		for _, value := range values {
			total += value
		}
		out = append(out, scenarioSummary{HostLabel: k.host, InstanceType: k.instance, Zone: k.zone, Variant: k.variant, Topology: k.topology, BatchSize: k.batch, Threshold: k.threshold, Alpha: k.alpha, Runs: len(values), MeanUS: total / float64(len(values)), P50US: percentileType7(values, 0.50), P95US: percentileType7(values, 0.95), MinUS: values[0], MaxUS: values[len(values)-1]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BatchSize != out[j].BatchSize {
			return out[i].BatchSize < out[j].BatchSize
		}
		if out[i].Variant != out[j].Variant {
			return out[i].Variant < out[j].Variant
		}
		return out[i].HostLabel < out[j].HostLabel
	})
	return out
}

func percentileType7(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	h := float64(len(sorted)-1) * p
	lo, hi := int(math.Floor(h)), int(math.Ceil(h))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (h-float64(lo))*(sorted[hi]-sorted[lo])
}

func writeSummaries(outDir string, rows []scenarioSummary) error {
	if err := writeJSON(filepath.Join(outDir, "scenario_summary.json"), rows); err != nil {
		return err
	}
	file, err := os.Create(filepath.Join(outDir, "scenario_summary.csv"))
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	defer w.Flush()
	if err := w.Write([]string{"host_label", "instance_type", "zone", "variant", "topology", "batch_size", "threshold", "alpha", "runs", "mean_us", "p50_us", "p95_us", "min_us", "max_us"}); err != nil {
		return err
	}
	for _, row := range rows {
		values := []string{row.HostLabel, row.InstanceType, row.Zone, row.Variant, row.Topology, strconv.Itoa(row.BatchSize), strconv.Itoa(row.Threshold), strconv.Itoa(row.Alpha), strconv.Itoa(row.Runs), formatFloat(row.MeanUS), formatFloat(row.P50US), formatFloat(row.P95US), formatFloat(row.MinUS), formatFloat(row.MaxUS)}
		if err := w.Write(values); err != nil {
			return err
		}
	}
	return w.Error()
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}
