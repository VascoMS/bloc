from __future__ import annotations

import os
import tempfile
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", str(Path(tempfile.gettempdir()) / "bloc-latency-charts-matplotlib"))

import matplotlib

matplotlib.use("Agg")

import matplotlib.pyplot as plt
from matplotlib.ticker import ScalarFormatter

from .data import ExperimentData, STAGES, scaling_summary, stage_summary, validate_stage_additivity


TRANSPORT_COLORS = {"tcp": "#0072B2", "libp2p": "#D55E00"}
STAGE_COLORS = ("#56B4E9", "#0072B2", "#009E73", "#F0E442", "#E69F00", "#CC79A7")


def generate_all(experiment: ExperimentData, output_dir: str | Path) -> list[Path]:
    output = Path(output_dir).expanduser().resolve()
    output.mkdir(parents=True, exist_ok=True)
    _set_style()

    generated: list[Path] = []
    generated.extend(_plot_scaling(experiment, output))
    generated.extend(_plot_stage_breakdown(experiment, output))
    generated.extend(_plot_distribution(experiment, output))
    return generated


def _plot_scaling(experiment: ExperimentData, output: Path) -> list[Path]:
    summary = scaling_summary(experiment.runs)
    node_counts = sorted(summary["nodes"].unique())
    fig, axes = plt.subplots(1, len(node_counts), figsize=(6.0 * len(node_counts), 4.4), squeeze=False)

    for axis, nodes in zip(axes[0], node_counts, strict=True):
        panel = summary[summary["nodes"] == nodes]
        batches = sorted(panel["batch_size"].unique())
        for network in sorted(panel["network"].unique()):
            values = panel[panel["network"] == network].sort_values("batch_size")
            color = TRANSPORT_COLORS.get(network, None)
            axis.plot(values["batch_size"], values["p50"], marker="o", color=color, label=f"{network} p50")
            axis.plot(values["batch_size"], values["p95"], marker="s", linestyle="--", color=color, alpha=0.8, label=f"{network} p95")
        if len(batches) > 1:
            axis.set_xscale("log", base=2)
            axis.xaxis.set_major_formatter(ScalarFormatter())
        axis.set_xticks(batches)
        axis.set_title(f"{nodes} operators")
        axis.set_xlabel("Batch size (transactions)")
        axis.set_ylabel("Total latency (ms)")
        axis.grid(axis="y", alpha=0.25)

    handles, labels = axes[0][0].get_legend_handles_labels()
    fig.suptitle(f"End-to-end latency scaling — {experiment.experiment_id}", y=0.99)
    fig.legend(handles, labels, loc="upper center", bbox_to_anchor=(0.5, 0.93), ncol=max(1, len(labels)))
    fig.tight_layout(rect=(0, 0, 1, 0.82))
    return _save(fig, output, "total-latency-scaling")


def _plot_stage_breakdown(experiment: ExperimentData, output: Path) -> list[Path]:
    validate_stage_additivity(experiment.runs)
    summary = stage_summary(experiment.runs)
    networks = sorted(summary["network"].unique())
    fig, axes = plt.subplots(1, len(networks), figsize=(7.0 * len(networks), 4.8), squeeze=False)

    for axis, network in zip(axes[0], networks, strict=True):
        panel = summary[summary["network"] == network].sort_values(["nodes", "batch_size"])
        x = list(range(len(panel)))
        bottoms = [0.0] * len(panel)
        for (source, label), color in zip(STAGES, STAGE_COLORS, strict=True):
            column = source.removesuffix("_us") + "_ms"
            values = panel[column].tolist()
            axis.bar(x, values, bottom=bottoms, color=color, label=label, width=0.72)
            bottoms = [bottom + value for bottom, value in zip(bottoms, values, strict=True)]
        axis.set_xticks(x, [f"n={int(row.nodes)}\nb={int(row.batch_size)}" for row in panel.itertuples()])
        axis.set_title(network)
        axis.set_xlabel("Configuration")
        axis.set_ylabel("Mean critical-path latency (ms)")
        axis.grid(axis="y", alpha=0.25)

    handles, labels = axes[0][0].get_legend_handles_labels()
    fig.suptitle(f"Mean critical-path stage breakdown — {experiment.experiment_id}", y=0.99)
    fig.legend(handles, labels, loc="upper center", bbox_to_anchor=(0.5, 0.93), ncol=3)
    fig.tight_layout(rect=(0, 0, 1, 0.82))
    return _save(fig, output, "critical-path-breakdown")


def _plot_distribution(experiment: ExperimentData, output: Path) -> list[Path]:
    networks = sorted(experiment.runs["network"].unique())
    fig, axes = plt.subplots(1, len(networks), figsize=(7.0 * len(networks), 4.8), squeeze=False)

    for axis, network in zip(axes[0], networks, strict=True):
        panel = experiment.runs[experiment.runs["network"] == network]
        configurations = panel[["nodes", "batch_size"]].drop_duplicates().sort_values(["nodes", "batch_size"])
        samples: list[list[float]] = []
        labels: list[str] = []
        for row in configurations.itertuples(index=False):
            values = panel[(panel["nodes"] == row.nodes) & (panel["batch_size"] == row.batch_size)]["total_slot_ms"].tolist()
            samples.append(values)
            labels.append(f"n={int(row.nodes)}\nb={int(row.batch_size)}")
        boxes = axis.boxplot(samples, tick_labels=labels, patch_artist=True, showfliers=False)
        for box in boxes["boxes"]:
            box.set_facecolor(TRANSPORT_COLORS.get(network, "#999999"))
            box.set_alpha(0.35)
        for position, values in enumerate(samples, start=1):
            count = len(values)
            offsets = [0.0] if count == 1 else [-0.12 + (0.24 * i / (count - 1)) for i in range(count)]
            axis.scatter([position + offset for offset in offsets], values, s=14, alpha=0.55, color=TRANSPORT_COLORS.get(network, "#333333"), zorder=3)
        axis.set_title(network)
        axis.set_xlabel("Configuration")
        axis.set_ylabel("Total latency (ms)")
        axis.grid(axis="y", alpha=0.25)

    fig.suptitle(f"Measured end-to-end latency distribution — {experiment.experiment_id}", y=1.02)
    fig.tight_layout()
    return _save(fig, output, "total-latency-distribution")


def _save(fig: plt.Figure, output: Path, stem: str) -> list[Path]:
    paths = [output / f"{stem}.svg", output / f"{stem}.png"]
    fig.savefig(paths[0], format="svg", bbox_inches="tight")
    fig.savefig(paths[1], format="png", dpi=300, bbox_inches="tight")
    plt.close(fig)
    return paths


def _set_style() -> None:
    plt.rcParams.update(
        {
            "figure.facecolor": "white",
            "axes.facecolor": "white",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "font.size": 10,
            "axes.titleweight": "bold",
            "savefig.facecolor": "white",
        }
    )
