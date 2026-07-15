from __future__ import annotations

import argparse
import json
from pathlib import Path

import matplotlib.pyplot as plt
import pandas as pd

from .charts import _save, _set_style
from .data import load_experiment


FOUR_STAGES = (
    ("proposal_preparation_us", "Proposal"),
    ("acs_us", "ACS"),
    ("merge_plan_us", "Merge + Plan"),
    ("decryption_materialization_us", "Decryption + Materialization"),
)
STAGE_COLORS = ("#56B4E9", "#0072B2", "#009E73", "#E69F00")


def validate_phase_contract(
    manifest: dict,
    runs: pd.DataFrame,
    node_rows: pd.DataFrame,
    targets: dict,
    cleanup: dict,
) -> None:
    nodes = int(manifest["node_count"])
    repetitions = int(manifest["repetitions"])
    primary = str(manifest["primary_region"])
    secondary = str(manifest["secondary_region"])
    if manifest.get("status") != "complete" or manifest.get("topology") != "T2-cross-region":
        raise ValueError("cross-region phase manifest is not complete")
    if manifest.get("operator_instance_type") != "t3.medium" or manifest.get("controller_instance_type") != "t3.medium":
        raise ValueError("cross-region phase did not use t3.medium throughout")
    placement = manifest.get("placement", [])
    if len(placement) != nodes:
        raise ValueError("cross-region phase placement count is invalid")
    for node in placement:
        expected = primary if int(node["id"]) % 2 == 0 else secondary
        if node.get("region") != expected:
            raise ValueError("cross-region phase placement is invalid")

    measured = runs[runs["phase"].astype(str).str.lower().eq("measured")].copy()
    measured["success"] = measured["success"].astype(str).str.lower()
    measured["consistent"] = measured["consistent"].astype(str).str.lower()
    measured_nodes = node_rows[node_rows["phase"].astype(str).str.lower().eq("measured")].copy()
    for batch in manifest["batch_sizes"]:
        batch_rows = measured[pd.to_numeric(measured["batch_size"]) == int(batch)]
        if len(batch_rows) != repetitions:
            raise ValueError(f"batch {batch} does not contain exactly {repetitions} measured runs")
        if not batch_rows["success"].eq("true").all() or not batch_rows["consistent"].eq("true").all():
            raise ValueError(f"batch {batch} contains a failed or inconsistent measured run")
        if not pd.to_numeric(batch_rows["selected_ciphertexts"]).eq(int(batch)).all():
            raise ValueError(f"batch {batch} selected ciphertext count is invalid")
        batch_nodes = measured_nodes[pd.to_numeric(measured_nodes["batch_size"]) == int(batch)]
        finalized = batch_nodes["metrics_finalized"].astype(str).str.lower().eq("true")
        if len(batch_nodes) != repetitions * nodes or not finalized.all():
            raise ValueError(f"batch {batch} node metrics are incomplete")

    active = targets.get("data", {}).get("activeTargets", [])
    if len(active) != nodes or any(target.get("health") != "up" for target in active):
        raise ValueError("Prometheus target acceptance failed")
    if _nonempty_cleanup_values(cleanup):
        raise ValueError("cross-region cleanup verification is not empty")


def validate_campaign_artifacts(root: Path) -> None:
    campaign_path = root / "manifest.json"
    if not campaign_path.is_file():
        return
    campaign = json.loads(campaign_path.read_text(encoding="utf-8-sig"))
    if campaign.get("schema_version") != "bloc-ec2-m3-cross-region/v1":
        return
    for nodes in campaign["node_counts"]:
        phase_root = root / f"n{int(nodes)}"
        manifest = json.loads((phase_root / "manifest.json").read_text(encoding="utf-8-sig"))
        runs = pd.read_csv(phase_root / "run_measurements.csv")
        node_rows = pd.read_csv(phase_root / "node_measurements.csv")
        targets = json.loads((phase_root / "prometheus-targets.json").read_text(encoding="utf-8-sig"))
        cleanup = json.loads((phase_root / "cleanup-verification.json").read_text(encoding="utf-8-sig"))
        validate_phase_contract(manifest, runs, node_rows, targets, cleanup)


def _nonempty_cleanup_values(value: object) -> list[object]:
    if isinstance(value, dict):
        found: list[object] = []
        for child in value.values():
            found.extend(_nonempty_cleanup_values(child))
        return found
    if isinstance(value, list):
        found = []
        for child in value:
            found.extend(_nonempty_cleanup_values(child))
        return found
    return [] if value in (None, "", False) else [value]


def prepare_cross_region_runs(result_dir: str | Path, tolerance_us: float = 20.0) -> tuple[str, pd.DataFrame]:
    experiment = load_experiment(result_dir)
    runs = experiment.runs.copy()
    runs["decryption_materialization_us"] = (
        runs["threshold_wait_us"] + runs["combine_us"] + runs["materialization_us"]
    )
    attributed = runs[[column for column, _ in FOUR_STAGES]].sum(axis=1)
    difference = (attributed - runs["total_slot_us"]).abs()
    if (difference.gt(tolerance_us).any()):
        row = runs.loc[difference.gt(tolerance_us)].iloc[0]
        raise ValueError(
            f"four-stage attribution does not add to total_slot_us for {row['run_id']!r} "
            f"(difference {difference.loc[difference.gt(tolerance_us)].iloc[0]:.0f} us)"
        )
    return experiment.experiment_id, runs


def analyze_cross_region(result_dir: str | Path, output_dir: str | Path | None = None) -> Path:
    root = Path(result_dir).expanduser().resolve()
    validate_campaign_artifacts(root)
    output = Path(output_dir).expanduser().resolve() if output_dir else root / "analysis"
    output.mkdir(parents=True, exist_ok=True)
    experiment_id, runs = prepare_cross_region_runs(root)
    _set_style()

    latency = (
        runs.groupby(["nodes", "batch_size"], as_index=False)["total_slot_us"]
        .agg(
            count="count",
            p50_us=lambda values: values.quantile(0.50, interpolation="linear"),
            p95_us=lambda values: values.quantile(0.95, interpolation="linear"),
            mean_us="mean",
        )
        .sort_values(["nodes", "batch_size"], ignore_index=True)
    )
    latency.to_csv(output / "cross-region-latency-summary.csv", index=False)

    stage_rows: list[dict[str, float | int | str]] = []
    for (nodes, batch), group in runs.groupby(["nodes", "batch_size"], sort=True):
        for column, label in FOUR_STAGES:
            values = group[column]
            stage_rows.append(
                {
                    "nodes": int(nodes),
                    "batch_size": int(batch),
                    "stage": label,
                    "count": int(len(values)),
                    "mean_us": float(values.mean()),
                    "p50_us": float(values.quantile(0.50, interpolation="linear")),
                    "p95_us": float(values.quantile(0.95, interpolation="linear")),
                }
            )
    stages = pd.DataFrame(stage_rows)
    stages.to_csv(output / "four-stage-summary.csv", index=False)

    _plot_latency_scaling(latency, experiment_id, output)
    _plot_four_stage_breakdown(runs, experiment_id, output)
    _plot_distributions(runs, experiment_id, output)
    _write_report(experiment_id, runs, latency, output)
    return output


def _plot_latency_scaling(summary: pd.DataFrame, experiment_id: str, output: Path) -> None:
    fig, axis = plt.subplots(figsize=(7.2, 4.8))
    for nodes in sorted(summary["nodes"].unique()):
        panel = summary[summary["nodes"] == nodes].sort_values("batch_size")
        axis.plot(panel["batch_size"], panel["p50_us"] / 1000, marker="o", label=f"n={nodes} p50")
        axis.plot(panel["batch_size"], panel["p95_us"] / 1000, marker="s", linestyle="--", label=f"n={nodes} p95")
    axis.set_xscale("log", base=2)
    axis.set_xticks(sorted(summary["batch_size"].unique()))
    axis.get_xaxis().set_major_formatter(plt.ScalarFormatter())
    axis.set_xlabel("Batch size (transactions)")
    axis.set_ylabel("Total latency (ms)")
    axis.set_title(f"Cross-region latency scaling — {experiment_id}")
    axis.grid(axis="y", alpha=0.25)
    axis.legend()
    fig.tight_layout()
    _save(fig, output, "cross-region-latency-scaling")


def _plot_four_stage_breakdown(runs: pd.DataFrame, experiment_id: str, output: Path) -> None:
    grouped = runs.groupby(["nodes", "batch_size"], as_index=False)[[column for column, _ in FOUR_STAGES]].mean()
    grouped.sort_values(["nodes", "batch_size"], inplace=True)
    labels = [f"n={int(row.nodes)}\nb={int(row.batch_size)}" for row in grouped.itertuples()]
    x = list(range(len(grouped)))
    bottoms = [0.0] * len(grouped)
    fig, axis = plt.subplots(figsize=(9.0, 5.0))
    for (column, label), color in zip(FOUR_STAGES, STAGE_COLORS, strict=True):
        values = (grouped[column] / 1000).tolist()
        axis.bar(x, values, bottom=bottoms, color=color, width=0.72, label=label)
        bottoms = [bottom + value for bottom, value in zip(bottoms, values, strict=True)]
    axis.set_xticks(x, labels)
    axis.set_xlabel("Configuration")
    axis.set_ylabel("Mean critical-path latency (ms)")
    axis.set_title(f"Four-stage cross-region critical path — {experiment_id}")
    axis.grid(axis="y", alpha=0.25)
    axis.legend(ncol=2)
    fig.tight_layout()
    _save(fig, output, "cross-region-four-stage-breakdown")


def _plot_distributions(runs: pd.DataFrame, experiment_id: str, output: Path) -> None:
    configurations = runs[["nodes", "batch_size"]].drop_duplicates().sort_values(["nodes", "batch_size"])
    samples, labels = [], []
    for row in configurations.itertuples(index=False):
        samples.append((runs[(runs["nodes"] == row.nodes) & (runs["batch_size"] == row.batch_size)]["total_slot_us"] / 1000).tolist())
        labels.append(f"n={int(row.nodes)}\nb={int(row.batch_size)}")
    fig, axis = plt.subplots(figsize=(9.0, 5.0))
    axis.boxplot(samples, tick_labels=labels, showfliers=False)
    for position, values in enumerate(samples, start=1):
        offsets = [0.0] if len(values) == 1 else [-0.12 + 0.24 * index / (len(values) - 1) for index in range(len(values))]
        axis.scatter([position + offset for offset in offsets], values, s=13, alpha=0.5)
    axis.set_xlabel("Configuration")
    axis.set_ylabel("Total latency (ms)")
    axis.set_title(f"Cross-region latency distributions — {experiment_id}")
    axis.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    _save(fig, output, "cross-region-latency-distributions")


def _write_report(experiment_id: str, runs: pd.DataFrame, summary: pd.DataFrame, output: Path) -> None:
    lines = [
        f"# Cross-Region Latency Report: {experiment_id}",
        "",
        "This is standalone current-build evidence. Older same-AZ and cross-AZ campaigns used different commits and are historical context, not controlled topology baselines.",
        "",
        "The report uses Type-7 p50/p95 and makes no p99 claim. All included rows are measured, successful, and cross-node consistent.",
        "",
        "## Scenario Summary",
        "",
        "| Nodes | Batch | Samples | p50 (ms) | p95 (ms) |",
        "|---:|---:|---:|---:|---:|",
    ]
    for row in summary.itertuples(index=False):
        lines.append(f"| {int(row.nodes)} | {int(row.batch_size)} | {int(row.count)} | {row.p50_us / 1000:.3f} | {row.p95_us / 1000:.3f} |")
    lines += [
        "",
        "## Four-Stage Definition",
        "",
        "1. Proposal",
        "2. ACS",
        "3. Merge + Plan",
        "4. Decryption + Materialization (threshold wait + combine + materialization)",
        "",
        f"Measured rows: {len(runs)}.",
    ]
    (output / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Analyze a BLOC cross-region latency campaign")
    parser.add_argument("result_dir")
    parser.add_argument("--output")
    args = parser.parse_args()
    analyze_cross_region(args.result_dir, args.output)


if __name__ == "__main__":
    main()
