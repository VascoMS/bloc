"""Validate and analyze the BLOC three-region EC2 latency campaign."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

import matplotlib
import pandas as pd

matplotlib.use("Agg")
import matplotlib.pyplot as plt

from .charts import _save, _set_style
from .data import load_experiment


FOUR_STAGES = (
    ("proposal_preparation_us", "Proposal"),
    ("acs_us", "ACS"),
    ("merge_plan_us", "Merge + Plan"),
    ("decryption_materialization_us", "Decryption + Materialization"),
)
REGION_PAIR_LABELS = {
    frozenset(("us-east-1", "eu-west-1")): "US–Ireland",
    frozenset(("us-east-1", "eu-central-1")): "US–Frankfurt",
    frozenset(("eu-west-1", "eu-central-1")): "Ireland–Frankfurt",
}


def _nonempty(value: object) -> list[object]:
    if isinstance(value, dict):
        found: list[object] = []
        for child in value.values():
            found.extend(_nonempty(child))
        return found
    if isinstance(value, list):
        found = []
        for child in value:
            found.extend(_nonempty(child))
        return found
    return [] if value in (None, "", False, "None") else [value]


def _targets_up(path: Path, nodes: int) -> None:
    payload = json.loads(path.read_text(encoding="utf-8-sig"))
    targets = payload.get("data", {}).get("activeTargets", [])
    if len(targets) != nodes or any(target.get("health") != "up" for target in targets):
        raise ValueError(f"Prometheus targets are not {nodes}/{nodes} up in {path}")


def _validate_phase(root: Path, manifest: dict) -> tuple[pd.DataFrame, pd.DataFrame]:
    nodes, repetitions = int(manifest["node_count"]), int(manifest["repetitions"])
    regions = [manifest["primary_region"], manifest["secondary_region"], manifest["tertiary_region"]]
    if manifest.get("schema_version") != "bloc-ec2-three-region-phase/v1" or manifest.get("status") != "complete":
        raise ValueError(f"{root}: phase manifest is not complete")
    if manifest.get("topology") != "T2-three-region" or len(set(regions)) != 3:
        raise ValueError(f"{root}: invalid three-region topology")
    if manifest.get("operator_instance_type") != "t3.small" or manifest.get("controller_instance_type") != "t3.small":
        raise ValueError(f"{root}: accepted evidence requires t3.small throughout")
    placement = {int(row["id"]): row for row in manifest.get("placement", [])}
    if set(placement) != set(range(nodes)):
        raise ValueError(f"{root}: incomplete placement")
    for node_id, node in placement.items():
        if node.get("region") != regions[node_id % 3] or not node.get("zone"):
            raise ValueError(f"{root}: invalid placement for node {node_id}")
    peerings = manifest.get("peering_connection_ids", {})
    if set(peerings) != {"primary_secondary", "primary_tertiary", "secondary_tertiary"} or len(set(peerings.values())) != 3:
        raise ValueError(f"{root}: three unique peering connections are required")

    runs = pd.read_csv(root / "run_measurements.csv")
    runs = runs[runs["phase"].astype(str).str.lower().eq("measured")].copy()
    node_rows = pd.read_csv(root / "node_measurements.csv")
    node_rows = node_rows[node_rows["phase"].astype(str).str.lower().eq("measured")].copy()
    expected_runs = repetitions * len(manifest["batch_sizes"])
    if len(runs) != expected_runs or len(node_rows) != expected_runs * nodes:
        raise ValueError(f"{root}: measured run/node row totals are invalid")
    if runs["run_id"].nunique() != expected_runs:
        raise ValueError(f"{root}: measured run IDs are not unique")
    for batch in manifest["batch_sizes"]:
        if len(runs[pd.to_numeric(runs["batch_size"]).eq(int(batch))]) != repetitions:
            raise ValueError(f"{root}: batch {batch} does not contain exactly {repetitions} measured runs")
    for column in ("success", "consistent"):
        if not runs[column].astype(str).str.lower().eq("true").all():
            raise ValueError(f"{root}: failed or inconsistent measured run")
    for column in ("success", "consistent", "metrics_finalized"):
        if not node_rows[column].astype(str).str.lower().eq("true").all():
            raise ValueError(f"{root}: failed, inconsistent, or unfinalized node row")
    if not pd.to_numeric(runs["selected_ciphertexts"]).eq(pd.to_numeric(runs["batch_size"])).all():
        raise ValueError(f"{root}: run selected-ciphertext count is invalid")
    if not pd.to_numeric(node_rows["selected_ciphertexts"]).eq(
        node_rows["run_id"].map(runs.set_index("run_id")["batch_size"]).astype(int)
    ).all():
        raise ValueError(f"{root}: node selected-ciphertext count is invalid")
    run_node_counts = node_rows.groupby("run_id")["node_id"].nunique()
    if len(run_node_counts) != expected_runs or not run_node_counts.eq(nodes).all():
        raise ValueError(f"{root}: a measured run does not contain all nodes")
    critical_counts = node_rows.assign(
        critical=node_rows["critical_node"].astype(str).str.lower().eq("true")
    ).groupby("run_id")["critical"].sum()
    if not critical_counts.eq(1).all():
        raise ValueError(f"{root}: a measured run does not identify exactly one critical node")
    expected_regions = node_rows["node_id"].astype(int).map({node_id: node["region"] for node_id, node in placement.items()})
    expected_zones = node_rows["node_id"].astype(int).map({node_id: node["zone"] for node_id, node in placement.items()})
    if not node_rows["region"].eq(expected_regions).all() or not node_rows["availability_zone"].eq(expected_zones).all():
        raise ValueError(f"{root}: node measurement placement is invalid")
    substages = node_rows[[
        "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us"
    ]].apply(pd.to_numeric, errors="raise").sum(axis=1)
    if substages.sub(pd.to_numeric(node_rows["merge_plan_us"], errors="raise")).abs().gt(20).any():
        raise ValueError(f"{root}: Merge + Plan substage additivity exceeds 20 us")

    for path in (root / "prometheus-targets-before.json", root / "prometheus-targets.json"):
        _targets_up(path, nodes)
    for name in ("network-pre.csv", "network-post.csv"):
        network = pd.read_csv(root / name)
        pairs = set(zip(network["source_node_id"].astype(int), network["target_node_id"].astype(int)))
        expected_pairs = {(source, target) for source in range(nodes) for target in range(nodes)}
        if len(network) != nodes * nodes or pairs != expected_pairs:
            raise ValueError(f"{root / name}: incomplete pairwise matrix")
        if not network["attempts"].eq(5).all() or not network["successes"].eq(5).all():
            raise ValueError(f"{root / name}: not all five pairwise health attempts succeeded")
    resources = pd.read_csv(root / "resource-samples.csv")
    if (
        not resources["container_status"].eq("running").all()
        or not resources["restart_count"].eq(0).all()
        or resources["oom_killed"].astype(str).str.lower().eq("true").any()
    ):
        raise ValueError(f"{root}: stopped, restarted, or OOM-killed operator detected")
    cleanup = json.loads((root / "cleanup-verification.json").read_text(encoding="utf-8-sig"))
    if _nonempty(cleanup):
        raise ValueError(f"{root}: teardown verification contains residual resources")
    errors = root / "cleanup-verification-errors.log"
    if errors.exists() and errors.stat().st_size:
        raise ValueError(f"{root}: teardown verification contains API errors")
    return runs, node_rows


def validate_campaign_artifacts(root: Path) -> tuple[pd.DataFrame, pd.DataFrame, dict]:
    campaign = json.loads((root / "manifest.json").read_text(encoding="utf-8-sig"))
    if campaign.get("schema_version") != "bloc-ec2-m3-three-region/v1" or campaign.get("status") != "complete":
        raise ValueError("campaign manifest is not complete three-region evidence")
    if campaign.get("topology") != "T2-three-region":
        raise ValueError("campaign topology is invalid")
    runs, nodes, digests = [], [], set()
    for node_count in campaign["node_counts"]:
        phase_root = root / f"n{int(node_count)}"
        phase_manifest = json.loads((phase_root / "manifest.json").read_text(encoding="utf-8-sig"))
        phase_runs, phase_nodes = _validate_phase(phase_root, phase_manifest)
        runs.append(phase_runs)
        nodes.append(phase_nodes)
        digests.add(phase_manifest["docker_image_digest"])
    all_runs, all_nodes = pd.concat(runs, ignore_index=True), pd.concat(nodes, ignore_index=True)
    expected_runs = len(campaign["batch_sizes"]) * int(campaign["repetitions"]) * len(campaign["node_counts"])
    expected_nodes = len(campaign["batch_sizes"]) * int(campaign["repetitions"]) * sum(int(n) for n in campaign["node_counts"])
    if len(all_runs) != expected_runs or len(all_nodes) != expected_nodes:
        raise ValueError(f"campaign totals are {len(all_runs)} runs/{len(all_nodes)} node rows; expected {expected_runs}/{expected_nodes}")
    if len(digests) != 1 or campaign.get("docker_image_digest") not in digests:
        raise ValueError("campaign phases did not use one image digest")
    return all_runs, all_nodes, campaign


def prepare_three_region_runs(root: str | Path, tolerance_us: float = 20.0) -> tuple[str, pd.DataFrame, pd.DataFrame, dict]:
    path = Path(root).expanduser().resolve()
    _, node_rows, campaign = validate_campaign_artifacts(path)
    experiment = load_experiment(path)
    runs = experiment.runs.copy()
    runs["decryption_materialization_us"] = runs["threshold_wait_us"] + runs["combine_us"] + runs["materialization_us"]
    attributed = runs[[column for column, _ in FOUR_STAGES]].sum(axis=1)
    difference = (attributed - runs["total_slot_us"]).abs()
    if difference.gt(tolerance_us).any():
        bad = difference[difference.gt(tolerance_us)].index[0]
        raise ValueError(f"four-stage attribution exceeds {tolerance_us:g} us for {runs.loc[bad, 'run_id']}")
    return experiment.experiment_id, runs, node_rows, campaign


def _protocol_summary(runs: pd.DataFrame) -> pd.DataFrame:
    return (
        runs.groupby(["nodes", "batch_size"], as_index=False)["total_slot_us"]
        .agg(count="count", p50_us=lambda x: x.quantile(.50), p95_us=lambda x: x.quantile(.95), mean_us="mean")
        .sort_values(["nodes", "batch_size"], ignore_index=True)
    )


def _stage_summary(runs: pd.DataFrame) -> pd.DataFrame:
    rows: list[dict[str, object]] = []
    for (nodes, batch), group in runs.groupby(["nodes", "batch_size"], sort=True):
        for column, label in FOUR_STAGES:
            values = group[column]
            rows.append({"nodes": int(nodes), "batch_size": int(batch), "stage": label, "count": len(values),
                         "p50_us": values.quantile(.50), "p95_us": values.quantile(.95), "mean_us": values.mean()})
    return pd.DataFrame(rows)


def _pair_label(source: str, target: str) -> str:
    if source == target:
        return "intra-region"
    key = frozenset((source, target))
    if key not in REGION_PAIR_LABELS:
        raise ValueError(f"unexpected region pair: {source}/{target}")
    return REGION_PAIR_LABELS[key]


def _network_summary(root: Path, node_counts: list[int]) -> pd.DataFrame:
    frames = []
    for nodes in node_counts:
        for name in ("network-pre.csv", "network-post.csv"):
            frame = pd.read_csv(root / f"n{nodes}" / name)
            frame["nodes"] = int(nodes)
            frame["region_pair"] = [_pair_label(source, target) for source, target in zip(frame["source_region"], frame["target_region"])]
            frames.append(frame)
    network = pd.concat(frames, ignore_index=True)
    return (
        network.groupby(["nodes", "phase", "region_pair"], as_index=False)
        .agg(pair_count=("avg_total_ms", "count"), p50_connect_ms=("avg_connect_ms", lambda x: x.quantile(.50)),
             p95_connect_ms=("avg_connect_ms", lambda x: x.quantile(.95)), p50_total_ms=("avg_total_ms", lambda x: x.quantile(.50)),
             p95_total_ms=("avg_total_ms", lambda x: x.quantile(.95)))
        .sort_values(["nodes", "phase", "region_pair"], ignore_index=True)
    )


def _critical_region_summary(runs: pd.DataFrame) -> pd.DataFrame:
    return (
        runs.groupby(["nodes", "batch_size", "critical_node_region"], as_index=False)["total_slot_us"]
        .agg(count="count", p50_us=lambda x: x.quantile(.50), p95_us=lambda x: x.quantile(.95), mean_us="mean")
        .sort_values(["nodes", "batch_size", "critical_node_region"], ignore_index=True)
    )


def analyze_three_region(result_dir: str | Path, output_dir: str | Path | None = None) -> Path:
    root = Path(result_dir).expanduser().resolve()
    output = Path(output_dir).expanduser().resolve() if output_dir else root / "analysis"
    output.mkdir(parents=True, exist_ok=True)
    experiment_id, runs, _, campaign = prepare_three_region_runs(root)
    latency, stages = _protocol_summary(runs), _stage_summary(runs)
    network = _network_summary(root, [int(value) for value in campaign["node_counts"]])
    critical = _critical_region_summary(runs)
    latency.to_csv(output / "three-region-latency-summary.csv", index=False)
    stages.to_csv(output / "four-stage-summary.csv", index=False)
    network.to_csv(output / "pairwise-network-summary.csv", index=False)
    critical.to_csv(output / "critical-node-region-summary.csv", index=False)
    _set_style()
    _plot_latency(latency, experiment_id, output)
    _plot_stages(runs, experiment_id, output)
    _plot_network(network, experiment_id, output)
    _write_report(experiment_id, runs, latency, network, critical, output)
    return output


def _plot_latency(summary: pd.DataFrame, experiment_id: str, output: Path) -> None:
    fig, axis = plt.subplots(figsize=(7.2, 4.8))
    for nodes in sorted(summary["nodes"].unique()):
        panel = summary[summary["nodes"] == nodes]
        axis.plot(panel["batch_size"], panel["p50_us"] / 1000, marker="o", label=f"n={nodes} p50")
        axis.plot(panel["batch_size"], panel["p95_us"] / 1000, marker="s", linestyle="--", label=f"n={nodes} p95")
    axis.set_xscale("log", base=2); axis.set_xticks(sorted(summary["batch_size"].unique()))
    axis.get_xaxis().set_major_formatter(plt.ScalarFormatter())
    axis.set(xlabel="Batch size", ylabel="Total latency (ms)", title=f"Three-region latency — {experiment_id}")
    axis.grid(axis="y", alpha=.25); axis.legend(); fig.tight_layout()
    _save(fig, output, "three-region-latency")


def _plot_stages(runs: pd.DataFrame, experiment_id: str, output: Path) -> None:
    columns = [column for column, _ in FOUR_STAGES]
    grouped = runs.groupby(["nodes", "batch_size"], as_index=False)[columns].mean()
    labels = [f"n={int(row.nodes)}\nb={int(row.batch_size)}" for row in grouped.itertuples()]
    fig, axis = plt.subplots(figsize=(9, 5)); bottom = pd.Series(0.0, index=grouped.index)
    for column, label in FOUR_STAGES:
        values = grouped[column] / 1000
        axis.bar(range(len(grouped)), values, bottom=bottom, label=label); bottom += values
    axis.set_xticks(range(len(grouped)), labels)
    axis.set(ylabel="Mean critical-path latency (ms)", title=f"Four-stage attribution — {experiment_id}")
    axis.grid(axis="y", alpha=.25); axis.legend(ncol=2); fig.tight_layout()
    _save(fig, output, "three-region-four-stage")


def _plot_network(summary: pd.DataFrame, experiment_id: str, output: Path) -> None:
    post = summary[summary["phase"] == "post"]
    pivot = post.pivot(index="region_pair", columns="nodes", values="p50_total_ms")
    axis = pivot.plot(kind="bar", figsize=(8.5, 4.8), rot=20)
    axis.set(xlabel="Region pair", ylabel="Pair-level p50 health latency (ms)", title=f"Pairwise network latency — {experiment_id}")
    axis.grid(axis="y", alpha=.25); axis.figure.tight_layout()
    _save(axis.figure, output, "three-region-pairwise-network")


def _write_report(experiment_id: str, runs: pd.DataFrame, latency: pd.DataFrame, network: pd.DataFrame,
                  critical: pd.DataFrame, output: Path) -> None:
    lines = [f"# Three-Region Latency Report: {experiment_id}", "",
             "Standalone current-build evidence; all observations are retained.", "",
             f"Measured slots: {len(runs)}.", "", "## Protocol latency", "",
             "| Nodes | Batch | Samples | p50 (ms) | p95 (ms) |", "|---:|---:|---:|---:|---:|"]
    for row in latency.itertuples(index=False):
        lines.append(f"| {int(row.nodes)} | {int(row.batch_size)} | {int(row.count)} | {row.p50_us/1000:.3f} | {row.p95_us/1000:.3f} |")
    lines += ["", "## Pairwise network latency", "",
              "Values summarize the per-node-pair average across five successful health attempts.", "",
              "| Nodes | Phase | Pair | Pairs | p50 total (ms) | p95 total (ms) |", "|---:|---|---|---:|---:|---:|"]
    for row in network.itertuples(index=False):
        lines.append(f"| {int(row.nodes)} | {row.phase} | {row.region_pair} | {int(row.pair_count)} | {row.p50_total_ms:.3f} | {row.p95_total_ms:.3f} |")
    lines += ["", "## Critical-node region attribution", "",
              "| Nodes | Batch | Critical region | Samples | p50 (ms) | p95 (ms) |", "|---:|---:|---|---:|---:|---:|"]
    for row in critical.itertuples(index=False):
        lines.append(f"| {int(row.nodes)} | {int(row.batch_size)} | {row.critical_node_region} | {int(row.count)} | {row.p50_us/1000:.3f} | {row.p95_us/1000:.3f} |")
    lines += ["", "Four-stage attribution uses Proposal, ACS, Merge + Plan, and Decryption + Materialization; every retained row passed the 20 µs additivity gate.", ""]
    (output / "REPORT.md").write_text("\n".join(lines), encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("result_dir")
    parser.add_argument("--output")
    args = parser.parse_args()
    analyze_three_region(args.result_dir, args.output)


if __name__ == "__main__":
    main()
