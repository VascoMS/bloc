"""Compare two matched evaluator campaigns without discarding observations."""

from __future__ import annotations

import argparse
from pathlib import Path

import matplotlib
import pandas as pd

from .data import STAGES, load_experiment

matplotlib.use("Agg")
import matplotlib.pyplot as plt


COMPARISON_STAGES = (("total_slot_us", "Total slot"), *STAGES)


def _change_percent(before: float, after: float) -> float:
    if before == 0:
        return 0.0 if after == 0 else float("nan")
    return 100 * (after / before - 1)


def _summary(label: str, runs: pd.DataFrame) -> pd.DataFrame:
    rows = []
    keys = ["nodes", "batch_size", "network"]
    for config, group in runs.groupby(keys, sort=True):
        for column, stage in COMPARISON_STAGES:
            values = group[column]
            rows.append({
                "campaign": label,
                **dict(zip(keys, config)),
                "stage": stage,
                "count": len(values),
                "p50_us": values.quantile(0.50, interpolation="linear"),
                "p95_us": values.quantile(0.95, interpolation="linear"),
                "mean_us": values.mean(),
                "min_us": values.min(),
                "max_us": values.max(),
            })
    return pd.DataFrame(rows)


def compare_campaigns(baseline_root: Path, candidate_root: Path, output: Path) -> Path:
    baseline = load_experiment(baseline_root)
    candidate = load_experiment(candidate_root)
    output.mkdir(parents=True, exist_ok=True)

    baseline_configs = set(map(tuple, baseline.runs[["nodes", "batch_size", "network"]].drop_duplicates().to_numpy()))
    candidate_configs = set(map(tuple, candidate.runs[["nodes", "batch_size", "network"]].drop_duplicates().to_numpy()))
    if baseline_configs != candidate_configs:
        raise ValueError("baseline and candidate campaign configurations do not match")

    summary = pd.concat([
        _summary("baseline", baseline.runs),
        _summary("candidate", candidate.runs),
    ], ignore_index=True)
    index = ["nodes", "batch_size", "network", "stage"]
    before = summary[summary["campaign"] == "baseline"].set_index(index)
    after = summary[summary["campaign"] == "candidate"].set_index(index)
    rows = []
    for key in before.index:
        left = before.loc[key]
        right = after.loc[key]
        rows.append({
            **dict(zip(index, key)),
            "baseline_count": int(left["count"]),
            "candidate_count": int(right["count"]),
            "baseline_p50_us": left["p50_us"],
            "candidate_p50_us": right["p50_us"],
            "p50_delta_us": right["p50_us"] - left["p50_us"],
            "p50_change_percent": _change_percent(left["p50_us"], right["p50_us"]),
            "baseline_p95_us": left["p95_us"],
            "candidate_p95_us": right["p95_us"],
            "p95_delta_us": right["p95_us"] - left["p95_us"],
            "p95_change_percent": _change_percent(left["p95_us"], right["p95_us"]),
        })
    comparison = pd.DataFrame(rows).sort_values(index, ignore_index=True)
    summary.to_csv(output / "campaign-summary.csv", index=False)
    comparison.to_csv(output / "comparison.csv", index=False)

    total = comparison[comparison["stage"] == "Total slot"]
    fig, ax = plt.subplots(figsize=(10, 5))
    labels = [f"n{row.nodes}/b{row.batch_size}" for row in total.itertuples()]
    positions = range(len(total))
    width = 0.38
    ax.bar([pos - width / 2 for pos in positions], total["baseline_p50_us"] / 1000, width, label="Baseline")
    ax.bar([pos + width / 2 for pos in positions], total["candidate_p50_us"] / 1000, width, label="Optimized")
    ax.set_xticks(list(positions), labels)
    ax.set_ylabel("p50 total-slot latency (ms)")
    ax.set_title("Cross-AZ campaign before and after optimization")
    ax.legend()
    fig.tight_layout()
    fig.savefig(output / "before-after-p50.png", dpi=180)
    fig.savefig(output / "before-after-p50.svg")
    plt.close(fig)

    lines = [
        "# Cross-AZ Optimization Comparison", "",
        f"Baseline: `{baseline.experiment_id}`", "",
        f"Candidate: `{candidate.experiment_id}`", "",
        "All successful, consistent measured observations are retained. Headline values are p50 and p95; no p99 claim is made.", "",
        "## Total-Slot Results", "",
        "| Nodes | Batch | Baseline p50 | Optimized p50 | Change | Baseline p95 | Optimized p95 | Change |",
        "|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for row in total.itertuples():
        lines.append(
            f"| {row.nodes} | {row.batch_size} | {row.baseline_p50_us / 1000:.1f} ms | "
            f"{row.candidate_p50_us / 1000:.1f} ms | {row.p50_change_percent:+.1f}% | "
            f"{row.baseline_p95_us / 1000:.1f} ms | {row.candidate_p95_us / 1000:.1f} ms | "
            f"{row.p95_change_percent:+.1f}% |"
        )
    lines.extend(["", "Negative percentages indicate lower latency."])
    (output / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    return output


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    print(compare_campaigns(args.baseline, args.candidate, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
