# BLOC Latency Charts

This module turns one `bloc-node eval-suite` result directory into static SVG
and PNG latency figures.

## Setup

```sh
cd latency-charts
python -m venv .venv
. .venv/bin/activate
python -m pip install -e ".[test]"
```

## Generate Charts

```sh
python -m bloc_latency_charts ../bloc-node/results/m1-local/baseline
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

For an accepted M3 three-region campaign, generate the thesis-facing report
with:

```sh
python -m bloc_latency_charts.three_region <campaign-directory>
```

This preserves the raw six-stage CSV and groups threshold wait, combine, and
materialization into Decryption + Materialization. It writes protocol p50/p95,
four-stage attribution, pairwise network summaries for intra-region,
US–Ireland, US–Frankfurt, and Ireland–Frankfurt traffic, critical-node-region
attribution, PNG/SVG figures, and `REPORT.md`. It rejects incomplete samples,
invalid placement, unhealthy Prometheus targets, failed five-attempt pairwise
health checks, resource restarts/OOMs, image-digest drift, or non-empty cleanup
evidence. The older `cross_region` module remains available only for historical
two-region artifacts.

## Test

```sh
python -m pytest
```
