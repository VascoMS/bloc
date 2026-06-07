package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type demoReportRow struct {
	Scenario    string
	ArtifactDir string
	Run         EvalRun
}

func reportDemo(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	root := fs.String("dir", "results/mvp-demo", "demo artifact directory or one scenario directory")
	out := fs.String("out", "", "optional markdown output path; stdout when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rows, err := collectDemoReportRows(*root)
	if err != nil {
		return err
	}
	report := renderDemoReport(*root, rows)
	if *out == "" {
		fmt.Print(report)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		return err
	}
	return os.WriteFile(*out, []byte(report), 0644)
}

func collectDemoReportRows(root string) ([]demoReportRow, error) {
	if rows, err := readScenarioSummary(root, filepath.Base(root)); err == nil {
		return rows, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var rows []demoReportRow
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scenarioDir := filepath.Join(root, entry.Name())
		scenarioRows, err := readScenarioSummary(scenarioDir, entry.Name())
		if err != nil {
			continue
		}
		rows = append(rows, scenarioRows...)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no summary.json files found under %s", root)
	}
	return rows, nil
}

func readScenarioSummary(dir, scenario string) ([]demoReportRow, error) {
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		return nil, err
	}
	var runs []EvalRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, err
	}
	rows := make([]demoReportRow, 0, len(runs))
	for _, run := range runs {
		rows = append(rows, demoReportRow{
			Scenario:    scenario,
			ArtifactDir: dir,
			Run:         run,
		})
	}
	return rows, nil
}

func renderDemoReport(root string, rows []demoReportRow) string {
	var b strings.Builder
	allPassed := true
	for _, row := range rows {
		allPassed = allPassed && row.Run.Success && row.Run.Consistent
	}
	status := "PASS"
	if !allPassed {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "# BLOC MVP Demo Report\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Artifacts: `%s`\n", root)
	fmt.Fprintf(&b, "- Overall: `%s`\n\n", status)
	fmt.Fprintf(&b, "## Scenario Summary\n\n")
	fmt.Fprintf(&b, "| Scenario | Network | Status | Agreed Lists | Selected Txs | Skipped | Selected Gas | Slot ms | ACS ms | Decrypt ms |\n")
	fmt.Fprintf(&b, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range rows {
		result := firstResult(row.Run)
		rowStatus := "FAIL"
		if row.Run.Success && row.Run.Consistent {
			rowStatus = "PASS"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %d | %d | %d | %d |\n",
			escapeMarkdown(row.Scenario),
			escapeMarkdown(row.Run.Network),
			rowStatus,
			result.Metrics.AgreedLists,
			result.Metrics.SelectedCiphertexts,
			result.Metrics.SkippedCiphertexts,
			result.Metrics.SelectedGas,
			result.Metrics.TotalSlotMS,
			result.Metrics.ACSMS,
			result.Metrics.CommitToPlaintextMS,
		)
	}
	fmt.Fprintf(&b, "\n## Scenario Details\n\n")
	for _, row := range rows {
		result := firstResult(row.Run)
		fmt.Fprintf(&b, "### %s\n\n", row.Scenario)
		fmt.Fprintf(&b, "- Run ID: `%s`\n", row.Run.RunID)
		fmt.Fprintf(&b, "- Success: `%t`, consistent: `%t`\n", row.Run.Success, row.Run.Consistent)
		if row.Run.Error != "" {
			fmt.Fprintf(&b, "- Error: `%s`\n", escapeBackticks(row.Run.Error))
		}
		fmt.Fprintf(&b, "- Batch ID: `%s`\n", result.BatchID)
		fmt.Fprintf(&b, "- Merged set hash: `%s`\n", result.Materialized.MergedSetHash)
		fmt.Fprintf(&b, "- Ethereum tx hashes: `%d`\n", len(result.Materialized.EthereumTxHashes))
		fmt.Fprintf(&b, "- Output directory: `%s`\n\n", row.ArtifactDir)
	}
	fmt.Fprintf(&b, "## Current Scope\n\n")
	fmt.Fprintf(&b, "- Demonstrates local ACS agreement, deterministic inclusion-list merge, BTE share exchange, and syntactically valid signed Ethereum transaction recovery.\n")
	fmt.Fprintf(&b, "- Uses trusted-dealer key material and static local networking; PBS, DVT signing, DKG, and Ethereum execution remain outside this MVP demo.\n")
	return b.String()
}

func firstResult(run EvalRun) Result {
	if len(run.Results) == 0 {
		return Result{}
	}
	return run.Results[0]
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func escapeBackticks(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}
