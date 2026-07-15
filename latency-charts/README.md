# BLOC Latency Charts

This module turns one `bloc-node eval-suite` result directory into static SVG
and PNG latency figures.

## Setup

```powershell
cd latency-charts
python -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install -e ".[test]"
```

## Generate Charts

```powershell
python -m bloc_latency_charts ..\bloc-node\results\m1-local\baseline
```

Figures are written to `<repository>/results/charts/<experiment-id>` unless
`--output-dir` is provided. For the baseline this is
`results/charts/m1-baseline`. Inputs outside the repository fall back to a
sibling `charts/<experiment-id>` directory. The command reads
`run_measurements.csv`, excludes warmups and failed or inconsistent measured
runs, and prints every generated path without modifying the source dataset.

The initial chart set contains:

- p50/p95 end-to-end latency versus batch size,
- mean sequential critical-path stage breakdown,
- optional merge/plan substage attribution when all five columns are present,
- raw end-to-end latency boxplots with individual observations.

The stage stack excludes `share_generation_us` and
`commit_to_plaintext_us` because those intervals overlap the sequential
critical path.

For an accepted M3 cross-region campaign, generate the thesis-facing
four-stage report with:

```powershell
python -m bloc_latency_charts.cross_region <campaign-directory>
```

This preserves the raw six-stage CSV and groups threshold wait, combine, and
materialization into Decryption + Materialization. It writes p50/p95 summaries,
four-stage attribution, distributions, PNG/SVG figures, and `REPORT.md`; it
also rejects incomplete samples, invalid placement, unhealthy Prometheus
targets, or non-empty cleanup evidence.

## Test

```powershell
python -m pytest
```
