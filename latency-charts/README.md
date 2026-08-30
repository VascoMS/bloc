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

The chart output also includes `latency-evidence-summary.csv`. For every
scenario it reports attempted, completed, completed consistently within the
12-second deadline, failed, timed-out, and excluded-reason counts. Latency
statistics use successful, consistent rows only. p50/p95 remain Type-7
compatible with historical artifacts; p99 and its 95% non-parametric
order-statistic interval are withheld until 1,000 eligible observations exist.

The chart set contains:

- p50/p95 end-to-end latency versus batch size and eligible p99 evidence,
- mean sequential critical-path stage breakdown,
- optional merge/plan substage attribution when all five columns are present,
- raw end-to-end latency boxplots with individual observations.

The stage stack excludes `share_generation_us` and
`commit_to_plaintext_us` because those intervals overlap the sequential
critical path.

For matched Issue #23 ACS diagnostics, validate and summarize one same-AZ root
against its three-region counterpart with:

```sh
python -m bloc_latency_charts.acs_attribution \
  <same-az-phase-root> <three-region-phase-root> \
  --output <acs-summary-directory>
```

The loader fails if source, image, corpus, public configuration, trace schema,
node count, or schedule identities differ. It retains failed and timed-out
attempt records but excludes them from latency distributions. The lean
pre-campaign output is `acs-milestone-summary.csv`, `acs-wait-summary.csv`,
`acs-message-summary.csv`, and `acs-critical-node-summary.csv`. Milestones are
monotonic offsets and are not added across concurrent RBC/BBA instances; p99 is
not published for the 30-observation diagnostic contract. Thesis report prose
and PNG/SVG rendering are intentionally deferred until matched evidence exists.

For an accepted M3 three-region campaign, generate the thesis-facing report
with:

```sh
python -m bloc_latency_charts.three_region <campaign-directory>
```

This preserves the raw six-stage CSV and groups threshold wait, combine, and
materialization into Decryption + Materialization. It writes protocol
p50/p95/p99 eligibility and confidence intervals,
four-stage attribution, pairwise network summaries for intra-region,
US–Ireland, US–Frankfurt, and Ireland–Frankfurt traffic, critical-node-region
attribution, PNG/SVG figures, and `REPORT.md`. It rejects incomplete samples,
invalid placement for completed rows, unhealthy Prometheus targets, failed
five-attempt pairwise health checks, resource restarts/OOMs, image-digest drift,
or non-empty cleanup evidence. Failed and timed-out attempts remain visible but
do not contaminate completed-run latency or stage distributions. The older
`cross_region` module remains available only for historical two-region
artifacts.

## Test

```sh
python -m pytest
```
