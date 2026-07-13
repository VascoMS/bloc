package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepeatedIndicesMatchRoundRobinSubmission(t *testing.T) {
	got := repeatedIndices(10, 4)
	want := []int{0, 0, 0, 0, 1, 1, 1, 1, 2, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestPercentileType7(t *testing.T) {
	values := []float64{1, 2, 3, 4}
	if got := percentileType7(values, 0.5); got != 2.5 {
		t.Fatalf("p50 = %v, want 2.5", got)
	}
	if got := percentileType7(values, 0.95); math.Abs(got-3.85) > 1e-9 {
		t.Fatalf("p95 = %v, want 3.85", got)
	}
}

func TestClassifyRatio(t *testing.T) {
	tests := map[float64]string{1.04: "negligible", 1.10: "secondary", 1.16: "material", 0.80: "material"}
	for ratio, want := range tests {
		if got := classifyRatio(ratio); got != want {
			t.Fatalf("classifyRatio(%v) = %q, want %q", ratio, got, want)
		}
	}
}

func TestParseScenarioID(t *testing.T) {
	nodes, batch, ok := parseScenarioID("remote-n7-b128-libp2p")
	if !ok || nodes != 7 || batch != 128 {
		t.Fatalf("got nodes=%d batch=%d ok=%t", nodes, batch, ok)
	}
}

func TestBuildVariantsFiltersExactNames(t *testing.T) {
	options := runOptions{
		BatchSizes: []int{8},
		BMax:       8,
		TxSize:     16,
		Variants:   []string{"paper-opt2-sequential-t2"},
	}
	variants, err := buildVariants(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 1 || variants[0].meta.Name != options.Variants[0] {
		t.Fatalf("got %d variants, want only %q", len(variants), options.Variants[0])
	}
}

func TestBuildVariantsRejectsUnknownFilter(t *testing.T) {
	options := runOptions{
		BatchSizes: []int{8},
		BMax:       8,
		TxSize:     16,
		Variants:   []string{"does-not-exist"},
	}
	if _, err := buildVariants(options); err == nil {
		t.Fatal("expected unknown variant error")
	}
}

func TestReadSidecarPathFindsNestedDatasets(t *testing.T) {
	root := t.TempDir()
	for _, scenario := range []string{"remote-n4-b8-libp2p", "remote-n7-b32-libp2p"} {
		dir := filepath.Join(root, scenario)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		csv := "scenario_id,phase,success,consistent,critical_node,combine_us\n" + scenario + ",measured,true,true,true,1000\n"
		if err := os.WriteFile(filepath.Join(dir, "node_measurements.csv"), []byte(csv), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	duplicateDir := filepath.Join(root, "remote-n4-b8-libp2p", "scenarios", "batch-8", "results")
	if err := os.MkdirAll(duplicateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	duplicate := "scenario_id,phase,success,consistent,critical_node,combine_us\nremote-n4-b8-libp2p,measured,true,true,true,1000\n"
	if err := os.WriteFile(filepath.Join(duplicateDir, "node_measurements.csv"), []byte(duplicate), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := readSidecarPath("same-AZ", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d sidecar aggregates, want 2", len(rows))
	}
}

func TestRenderReportIncludesHardwareAndAttribution(t *testing.T) {
	rows := []placementAggregate{
		{Instance: "c7a.large", Variant: "paper-opt2-sequential-t2", Topology: "paper", BatchSize: 8, HostP50: []float64{15000, 17000}},
		{Instance: "c7a.large", Variant: "bloc-core-unique-t2-unverified", Topology: "bloc-config", BatchSize: 8, HostP50: []float64{18000, 20000}},
	}
	report := renderReport(rows, nil)
	for _, want := range []string{"Paper-Equivalent Hardware Comparison", "c7a.large", "configuration", "material"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q", want)
		}
	}
}

func TestReadCampaignSummariesExcludesProfiles(t *testing.T) {
	root := t.TempDir()
	header := "host_label,instance_type,zone,variant,topology,batch_size,threshold,alpha,runs,p50_us\n"
	for _, item := range []struct {
		path, row string
	}{
		{filepath.Join(root, "host-a", "timed", "scenario_summary.csv"), "host-a,c7a.large,a,paper-opt2-sequential-t2,paper,128,2,22,30,500000\n"},
		{filepath.Join(root, "host-a", "profiles", "paper", "scenario_summary.csv"), "host-a-profile,c7a.large,a,paper-opt2-sequential-t2,paper,128,2,22,10,600000\n"},
	} {
		if err := os.MkdirAll(filepath.Dir(item.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(item.path, []byte(header+item.row), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := readCampaignSummaries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Runs != 30 {
		t.Fatalf("got %#v, want only timed summary", rows)
	}
}

func TestPairedRatiosAndDirectionAgreement(t *testing.T) {
	from := placementAggregate{Placements: map[string]float64{"a": 10, "b": 20}}
	to := placementAggregate{Placements: map[string]float64{"a": 12, "b": 22}}
	ratios := pairedRatios(from, to)
	if len(ratios) != 2 || directionAgreement(ratios) != "yes" {
		t.Fatalf("got ratios %v and agreement %q", ratios, directionAgreement(ratios))
	}
	to.Placements["b"] = 18
	if got := directionAgreement(pairedRatios(from, to)); got != "mixed" {
		t.Fatalf("got agreement %q, want mixed", got)
	}
}
