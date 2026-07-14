"""Validate and analyze the EC2 Merge + Plan attribution campaign."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

import matplotlib
import pandas as pd

matplotlib.use("Agg")
import matplotlib.pyplot as plt


SUBSTAGES = [
    "acs_output_decode_us",
    "agreed_set_us",
    "merge_us",
    "ciphertext_decode_us",
    "batch_plan_us",
]
STAGES = ["merge_plan_us", *SUBSTAGES]
REQUIRED_COLUMNS = {
    "run_id", "success", "consistent", "node_id", "critical_node",
    "metrics_finalized", "selected_ciphertexts", "measurement_block", *STAGES,
}


def _as_bool(series: pd.Series) -> pd.Series:
    return series.astype(str).str.lower().map({"true": True, "false": False})


def _load_targets(path: Path, expected: int) -> None:
    if not path.exists():
        raise ValueError(f"missing Prometheus target snapshot: {path}")
    payload = json.loads(path.read_text(encoding="utf-8-sig"))
    targets = payload.get("data", {}).get("activeTargets", [])
    sidecars = [target for target in targets if target.get("labels", {}).get("job") == "bloc-sidecars"]
    if len(sidecars) != expected or any(target.get("health") != "up" for target in sidecars):
        raise ValueError(f"Prometheus targets are not {expected}/{expected} up in {path}")


def _load_phase(root: Path, phase: dict) -> pd.DataFrame:
    phase_root = root / phase["path"]
    path = phase_root / "node_measurements.csv"
    if not path.exists():
        raise ValueError(f"missing node measurements: {path}")
    frame = pd.read_csv(path)
    missing = REQUIRED_COLUMNS - set(frame.columns)
    if missing:
        raise ValueError(f"{phase['id']} is missing columns: {sorted(missing)}")
    for column in ["success", "consistent", "critical_node", "metrics_finalized"]:
        frame[column] = _as_bool(frame[column])
    numeric = ["node_id", "selected_ciphertexts", "measurement_block", *STAGES]
    frame[numeric] = frame[numeric].apply(pd.to_numeric, errors="raise")
    frame["batch"] = frame["selected_ciphertexts"].astype(int)
    frame["phase"] = phase["id"]
    frame["instance_type"] = phase["operator_instance_type"]
    frame["nodes"] = int(phase["nodes"])
    if frame[["success", "consistent", "metrics_finalized"]].isna().any().any():
        raise ValueError(f"{phase['id']} contains invalid boolean values")
    if not frame[["success", "consistent", "metrics_finalized"]].all().all():
        raise ValueError(f"{phase['id']} contains failed, inconsistent, or unfinalized measurements")
    if not frame["batch"].isin([8, 32, 128]).all():
        raise ValueError(f"{phase['id']} contains an unexpected selected ciphertext count")
    if (frame[SUBSTAGES].sum(axis=1).sub(frame["merge_plan_us"]).abs() > 20).any():
        raise ValueError(f"{phase['id']} violates the 20 us Merge + Plan additivity tolerance")
    run_counts = frame.groupby("batch")["run_id"].nunique()
    if set(run_counts.index) != {8, 32, 128} or (run_counts != 30).any():
        raise ValueError(f"{phase['id']} must contain exactly 30 runs per batch: {run_counts.to_dict()}")
    node_counts = frame.groupby(["batch", "run_id"])["node_id"].nunique()
    if (node_counts != int(phase["nodes"])).any():
        raise ValueError(f"{phase['id']} has runs without all node measurements")
    _load_targets(phase_root / "prometheus-targets.json", int(phase["nodes"]))
    return frame


def _summaries(measurements: pd.DataFrame) -> pd.DataFrame:
    rows = []
    scopes = [
        ("critical-node", measurements[measurements["critical_node"]]),
        ("all-nodes", measurements),
    ]
    for scope, scoped in scopes:
        keys = ["phase", "instance_type", "nodes", "batch"]
        for config, group in scoped.groupby(keys, sort=True):
            for stage in STAGES:
                values = group[stage]
                rows.append({
                    **dict(zip(keys, config)),
                    "scope": scope,
                    "stage": stage,
                    "count": len(values),
                    "mean_us": values.mean(),
                    "stddev_us": values.std(ddof=1),
                    "p50_us": values.quantile(0.50, interpolation="linear"),
                    "p95_us": values.quantile(0.95, interpolation="linear"),
                    "min_us": values.min(),
                    "max_us": values.max(),
                })
    return pd.DataFrame(rows)


def _comparisons(summary: pd.DataFrame) -> pd.DataFrame:
    critical = summary[summary["scope"] == "critical-node"].set_index(["phase", "batch", "stage"])
    rows = []
    pairs = [
        ("fixed-n7_vs_fixed-n4", "fixed-n4", "fixed-n7"),
        ("burstable-n7_vs_fixed-n7", "fixed-n7", "burstable-n7"),
    ]
    for label, baseline, candidate in pairs:
        for batch in [8, 32, 128]:
            for stage in STAGES:
                before = critical.loc[(baseline, batch, stage)]
                after = critical.loc[(candidate, batch, stage)]
                rows.append({
                    "comparison": label, "baseline": baseline, "candidate": candidate,
                    "batch": batch, "stage": stage,
                    "baseline_p50_us": before["p50_us"],
                    "candidate_p50_us": after["p50_us"],
                    "p50_delta_us": after["p50_us"] - before["p50_us"],
                    "p50_ratio": after["p50_us"] / before["p50_us"] if before["p50_us"] else float("nan"),
                    "baseline_p95_us": before["p95_us"],
                    "candidate_p95_us": after["p95_us"],
                    "p95_delta_us": after["p95_us"] - before["p95_us"],
                    "p95_ratio": after["p95_us"] / before["p95_us"] if before["p95_us"] else float("nan"),
                })
    return pd.DataFrame(rows)


def _save_figure(fig: plt.Figure, output: Path, name: str) -> None:
    fig.tight_layout()
    fig.savefig(output / f"{name}.png", dpi=180)
    fig.savefig(output / f"{name}.svg")
    plt.close(fig)


def _charts(measurements: pd.DataFrame, summary: pd.DataFrame, output: Path) -> None:
    critical = measurements[measurements["critical_node"]]
    attribution = critical[critical["phase"] == "fixed-n7"].groupby("batch")[SUBSTAGES].median()
    fig, ax = plt.subplots(figsize=(9, 5))
    attribution.plot(kind="bar", stacked=True, ax=ax)
    ax.set_ylabel("Median latency (us)")
    ax.set_title("Fixed n=7 Merge + Plan substage attribution")
    ax.legend(title="Substage", fontsize=8)
    _save_figure(fig, output, "merge-plan-substages")

    fig, ax = plt.subplots(figsize=(8, 5))
    for phase, group in critical.groupby("phase"):
        medians = group.groupby("batch")["ciphertext_decode_us"].median()
        ax.plot(medians.index, medians.values, marker="o", label=phase)
    ax.set_xlabel("Selected ciphertexts")
    ax.set_ylabel("Median decode latency (us)")
    ax.set_title("Ciphertext decoding scaling")
    ax.legend()
    _save_figure(fig, output, "ciphertext-decode-scaling")

    fig, ax = plt.subplots(figsize=(8, 5))
    per_ciphertext = critical.assign(
        decode_us_per_ciphertext=critical["ciphertext_decode_us"] / critical["batch"]
    )
    for phase, group in per_ciphertext.groupby("phase"):
        medians = group.groupby("batch")["decode_us_per_ciphertext"].median()
        ax.plot(medians.index, medians.values, marker="o", label=phase)
    ax.set_xlabel("Selected ciphertexts")
    ax.set_ylabel("Median decode time per ciphertext (us)")
    ax.set_title("Decode cost per ciphertext")
    ax.legend()
    _save_figure(fig, output, "decode-per-ciphertext")

    skew = measurements.groupby(["phase", "batch", "run_id"])["merge_plan_us"].agg(
        lambda values: values.max() - values.min()
    )
    skew_frame = skew.rename("skew_us").reset_index()
    labels = []
    values = []
    for config, group in skew_frame.groupby(["phase", "batch"]):
        labels.append(f"{config[0]}\nb{config[1]}")
        values.append(group["skew_us"].to_numpy())
    fig, ax = plt.subplots(figsize=(10, 5))
    ax.boxplot(values, tick_labels=labels, showfliers=True)
    ax.set_ylabel("Within-run max minus min (us)")
    ax.set_title("Node-to-node Merge + Plan skew")
    ax.tick_params(axis="x", labelrotation=20)
    _save_figure(fig, output, "node-skew")

    per_node = measurements[
        (measurements["phase"] == "fixed-n7") & (measurements["batch"] == 128)
    ]
    node_ids = sorted(per_node["node_id"].unique())
    fig, ax = plt.subplots(figsize=(9, 5))
    ax.boxplot(
        [per_node.loc[per_node["node_id"] == node_id, "ciphertext_decode_us"] for node_id in node_ids],
        tick_labels=[str(node_id) for node_id in node_ids],
        showfliers=True,
    )
    ax.set_xlabel("Operator node")
    ax.set_ylabel("Ciphertext decode latency (us)")
    ax.set_title("Fixed n=7 batch-128 per-node distributions")
    _save_figure(fig, output, "per-node-distributions")

    instance = summary[
        (summary["scope"] == "critical-node")
        & (summary["stage"] == "merge_plan_us")
        & summary["phase"].isin(["fixed-n7", "burstable-n7"])
    ].pivot(index="batch", columns="phase", values="p50_us")
    fig, ax = plt.subplots(figsize=(8, 5))
    instance.plot(kind="bar", ax=ax)
    ax.set_ylabel("p50 Merge + Plan latency (us)")
    ax.set_title("n=7 instance-class comparison")
    _save_figure(fig, output, "instance-class-comparison")


def _report(measurements: pd.DataFrame, summary: pd.DataFrame, output: Path) -> None:
    critical = summary[summary["scope"] == "critical-node"].set_index(["phase", "batch", "stage"])
    fixed_decode = critical.loc[("fixed-n7", 128, "ciphertext_decode_us"), "p50_us"]
    fixed_total = critical.loc[("fixed-n7", 128, "merge_plan_us"), "p50_us"]
    fixed_n4 = critical.loc[("fixed-n4", 128, "merge_plan_us"), "p50_us"]
    burstable = critical.loc[("burstable-n7", 128, "merge_plan_us"), "p50_us"]
    decode_medians = [
        critical.loc[("fixed-n7", batch, "ciphertext_decode_us"), "p50_us"]
        for batch in [8, 32, 128]
    ]
    per_ciphertext = [value / batch for value, batch in zip(decode_medians, [8, 32, 128])]
    per_ciphertext_spread = max(per_ciphertext) / min(per_ciphertext)
    scaling_assessment = "approximately linear" if per_ciphertext_spread <= 1.25 else "not linear within a 25% per-ciphertext band"
    node_ratio = fixed_total / fixed_n4
    node_assessment = "material" if abs(node_ratio - 1) >= 0.10 else "not material under a 10% threshold"
    fixed_p95 = critical.loc[("fixed-n7", 128, "merge_plan_us"), "p95_us"]
    burstable_p95 = critical.loc[("burstable-n7", 128, "merge_plan_us"), "p95_us"]
    local_delta_percent = 100 * (fixed_decode - 457000) / 457000
    local_assessment = "reproduced within 10%" if abs(local_delta_percent) <= 10 else "not reproduced within 10%"
    skew = measurements.groupby(["phase", "batch", "run_id"])["merge_plan_us"].agg(
        lambda values: values.max() - values.min()
    )
    lines = [
        "# EC2 Merge/Plan Attribution Campaign", "",
        "## Scope", "",
        "Same-AZ, synthetic 256-byte transactions, five warmups, and 30 measured runs per batch. Headline statistics are p50 and p95; no p99 claim is made.", "",
        "## Answers", "",
        f"- **Ciphertext decoding share:** at fixed n=7 and batch 128, decoding is {fixed_decode / 1000:.1f} ms p50, or {100 * fixed_decode / fixed_total:.1f}% of Merge + Plan.",
        f"- **Decode scaling:** fixed n=7 p50 values for batches 8/32/128 are {decode_medians[0] / 1000:.1f}, {decode_medians[1] / 1000:.1f}, and {decode_medians[2] / 1000:.1f} ms. This is {scaling_assessment}; the max/min per-ciphertext cost ratio is {per_ciphertext_spread:.2f}x.",
        f"- **Fixed n=7 versus n=4:** batch-128 Merge + Plan p50 changes from {fixed_n4 / 1000:.1f} ms to {fixed_total / 1000:.1f} ms ({node_ratio:.2f}x), which is {node_assessment}.",
        f"- **T3 versus fixed at n=7:** batch-128 Merge + Plan p50 changes from {fixed_total / 1000:.1f} ms to {burstable / 1000:.1f} ms ({burstable / fixed_total:.2f}x). The p95/p50 ratio changes from {fixed_p95 / fixed_total:.2f}x to {burstable_p95 / burstable:.2f}x.",
        f"- **Local reference:** EC2 fixed n=7 batch-128 decode differs from the contextual 457 ms local median by {local_delta_percent:+.1f}% and is {local_assessment}. The machines are not treated as directly comparable hardware baselines.",
        f"- **Node skew:** the median within-run Merge + Plan range is {skew.median() / 1000:.1f} ms; the maximum retained range is {skew.max() / 1000:.1f} ms.", "",
        "## Validity", "",
        "All retained observations passed success, consistency, finalized-metric, selected-count, complete-node, Prometheus-target, and 20 us substage-additivity checks. Outliers were retained.", "",
        "## Artifacts", "",
        "- merge-plan-measurements.csv: node-level observations and decode time per ciphertext",
        "- merge-plan-summary.csv: p50, p95, dispersion, and bounds",
        "- comparison.csv: fixed-size and instance-class comparisons",
        "- PNG and SVG charts: attribution, scaling, per-node distributions, skew, and instance-class comparison",
    ]
    (output / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def analyze_campaign(campaign_root: Path, output: Path | None = None) -> Path:
    campaign_root = campaign_root.resolve()
    output = (output or campaign_root / "analysis").resolve()
    output.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((campaign_root / "manifest.json").read_text(encoding="utf-8-sig"))
    phases = manifest.get("phases", [])
    expected = {"fixed-n4", "fixed-n7", "burstable-n7"}
    if {phase.get("id") for phase in phases} != expected:
        raise ValueError("campaign manifest must contain fixed-n4, fixed-n7, and burstable-n7")
    digests = {phase.get("image_digest") for phase in phases}
    if len(digests) != 1 or None in digests or "" in digests:
        raise ValueError("all phases must record one identical non-empty image digest")
    measurements = pd.concat([_load_phase(campaign_root, phase) for phase in phases], ignore_index=True)
    measurements["decode_us_per_ciphertext"] = measurements["ciphertext_decode_us"] / measurements["batch"]
    columns = [
        "phase", "instance_type", "nodes", "batch", "measurement_block", "run_id",
        "node_id", "critical_node", *SUBSTAGES, "merge_plan_us", "decode_us_per_ciphertext",
    ]
    measurements[columns].to_csv(output / "merge-plan-measurements.csv", index=False)
    summary = _summaries(measurements)
    summary.to_csv(output / "merge-plan-summary.csv", index=False)
    _comparisons(summary).to_csv(output / "comparison.csv", index=False)
    _charts(measurements, summary, output)
    _report(measurements, summary, output)
    return output


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("campaign_root", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args(argv)
    print(analyze_campaign(args.campaign_root, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
