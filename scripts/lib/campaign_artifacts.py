#!/usr/bin/env python3
"""Portable structured-artifact operations used by Bash campaign runners."""

from __future__ import annotations

import argparse
import csv
import json
import os
import re
import statistics
import sys
import tempfile
from datetime import datetime
from collections import defaultdict
from pathlib import Path
from typing import Any, Iterable


def atomic_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            handle.write(text)
        os.replace(temporary, path)
    except BaseException:
        os.unlink(temporary)
        raise


def write_json(path: Path, value: Any) -> None:
    atomic_text(path, json.dumps(value, indent=2, ensure_ascii=False) + "\n")


def read_csv(path: Path) -> list[dict[str, str]]:
    with path.open(encoding="utf-8-sig", newline="") as handle:
        return list(csv.DictReader(handle))


RESOURCE_TIMESERIES_FIELDS = [
    "timestamp", "sample_index", "node", "region", "scenario", "phase", "cpu_usage_us",
    "memory_current_bytes", "memory_peak_bytes", "network_receive_bytes", "network_transmit_bytes",
    "restart_count", "oom_killed",
]
RESOURCE_SUMMARY_FIELDS = [
    "scope", "node", "region", "scenario", "phase", "samples", "first_timestamp", "last_timestamp",
    "cpu_usage_delta_us", "memory_current_max_bytes", "memory_peak_bytes",
    "network_receive_delta_bytes", "network_transmit_delta_bytes",
]
RESOURCE_SAMPLE_INTERVAL_SECONDS = 0.25
RESOURCE_SAMPLE_INTERVAL_TOLERANCE_SECONDS = 0.10
RESOURCE_MINIMUM_SAMPLES = 4


def write_csv(path: Path, rows: Iterable[dict[str, Any]], fields: list[str] | None = None) -> None:
    rows = list(rows)
    if fields is None:
        fields = list(rows[0]) if rows else []
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            writer = csv.DictWriter(handle, fieldnames=fields, lineterminator="\n")
            writer.writeheader()
            writer.writerows(rows)
        os.replace(temporary, path)
    except BaseException:
        os.unlink(temporary)
        raise


def resource_evidence_summary(
    path: Path,
    expected_nodes: set[str] | None = None,
    expected_configurations: set[tuple[str, str]] | None = None,
) -> list[dict[str, Any]]:
    """Validate dedicated resource samples and summarize host counters.

    Host receive/transmit counters deliberately remain separate from the BLOC
    protocol-message metrics, which are neither read nor written here.
    """
    with path.open(encoding="utf-8-sig", newline="") as handle:
        reader = csv.DictReader(handle)
        fields = reader.fieldnames or []
        rows = list(reader)
    if fields != RESOURCE_TIMESERIES_FIELDS:
        raise ValueError(f"{path}: invalid resource_timeseries.csv columns")
    if not rows:
        raise ValueError(f"{path}: resource timeseries is empty")

    expected_nodes = expected_nodes or set()
    expected_configurations = expected_configurations or set()
    groups: dict[tuple[str, str, str], list[dict[str, str]]] = defaultdict(list)
    for row in rows:
        scenario, phase, node = row["scenario"], row["phase"], row["node"]
        if not scenario or phase != "resource-measured":
            raise ValueError(f"{path}: invalid resource phase {phase!r}")
        try:
            for field in (
                "sample_index", "cpu_usage_us", "memory_current_bytes", "memory_peak_bytes",
                "network_receive_bytes", "network_transmit_bytes", "restart_count",
            ):
                if int(row[field]) < 0:
                    raise ValueError(field)
        except ValueError as exc:
            raise ValueError(f"{path}: invalid numeric resource value") from exc
        if int(row["restart_count"]) != 0 or _true(row["oom_killed"]):
            raise ValueError(f"{path}: restart or OOM detected")
        groups[(scenario, phase, node)].append(row)

    configurations = {(scenario, phase) for scenario, phase, _ in groups}
    if expected_configurations and configurations != expected_configurations:
        raise ValueError(f"{path}: incomplete node/configuration coverage")
    for configuration in configurations:
        actual_nodes = {node for scenario, phase, node in groups if (scenario, phase) == configuration}
        if expected_nodes and actual_nodes != expected_nodes:
            raise ValueError(f"{path}: incomplete node/configuration coverage")

    node_summaries: list[dict[str, Any]] = []
    for (scenario, phase, node), samples in sorted(groups.items()):
        samples.sort(key=lambda row: int(row["sample_index"]))
        indexes = [int(row["sample_index"]) for row in samples]
        if len(indexes) < RESOURCE_MINIMUM_SAMPLES:
            raise ValueError(f"{path}: insufficient resource samples for {scenario}/{phase}/node-{node}")
        if indexes != list(range(len(indexes))):
            raise ValueError(f"{path}: missing samples for {scenario}/{phase}/node-{node}")
        try:
            timestamps = [datetime.fromisoformat(row["timestamp"].replace("Z", "+00:00")) for row in samples]
        except ValueError as exc:
            raise ValueError(f"{path}: invalid resource timestamp") from exc
        for previous, current in zip(timestamps, timestamps[1:]):
            spacing = (current - previous).total_seconds()
            if abs(spacing - RESOURCE_SAMPLE_INTERVAL_SECONDS) > RESOURCE_SAMPLE_INTERVAL_TOLERANCE_SECONDS:
                raise ValueError(f"{path}: off-cadence resource samples for {scenario}/{phase}/node-{node}")
        currents = [int(row["memory_current_bytes"]) for row in samples]
        peaks = [int(row["memory_peak_bytes"]) for row in samples]
        if any(peak < current for current, peak in zip(currents, peaks)) or any(current < previous for previous, current in zip(peaks, peaks[1:])):
            raise ValueError(f"{path}: memory peak is below current usage or decreases on {scenario}/{phase}/node-{node}")
        for counter in ("cpu_usage_us", "network_receive_bytes", "network_transmit_bytes"):
            values = [int(row[counter]) for row in samples]
            if any(current < previous for previous, current in zip(values, values[1:])):
                raise ValueError(f"{path}: counter reset for {counter} on {scenario}/{phase}/node-{node}")
        first, last = samples[0], samples[-1]
        node_summaries.append({
            "scope": "node", "node": node, "region": first["region"], "scenario": scenario, "phase": phase,
            "samples": len(samples), "first_timestamp": first["timestamp"], "last_timestamp": last["timestamp"],
            "cpu_usage_delta_us": int(last["cpu_usage_us"]) - int(first["cpu_usage_us"]),
            "memory_current_max_bytes": max(int(row["memory_current_bytes"]) for row in samples),
            "memory_peak_bytes": max(int(row["memory_peak_bytes"]) for row in samples),
            "network_receive_delta_bytes": int(last["network_receive_bytes"]) - int(first["network_receive_bytes"]),
            "network_transmit_delta_bytes": int(last["network_transmit_bytes"]) - int(first["network_transmit_bytes"]),
        })

    cluster_summaries: list[dict[str, Any]] = []
    for scenario, phase in sorted(configurations):
        values = [row for row in node_summaries if row["scenario"] == scenario and row["phase"] == phase]
        cluster_summaries.append({
            "scope": "cluster", "node": "all", "region": "cluster", "scenario": scenario, "phase": phase,
            "samples": sum(int(row["samples"]) for row in values),
            "first_timestamp": min(str(row["first_timestamp"]) for row in values),
            "last_timestamp": max(str(row["last_timestamp"]) for row in values),
            "cpu_usage_delta_us": sum(int(row["cpu_usage_delta_us"]) for row in values),
            # This is the sum of independently observed per-node maxima, not a
            # synchronized cluster-memory reading.
            "memory_current_max_bytes": sum(int(row["memory_current_max_bytes"]) for row in values),
            "memory_peak_bytes": sum(int(row["memory_peak_bytes"]) for row in values),
            "network_receive_delta_bytes": sum(int(row["network_receive_delta_bytes"]) for row in values),
            "network_transmit_delta_bytes": sum(int(row["network_transmit_delta_bytes"]) for row in values),
        })
    return node_summaries + cluster_summaries


def parse_expected(values: list[str]) -> dict[tuple[str, str], int]:
    result: dict[tuple[str, str], int] = {}
    for value in values:
        match = re.fullmatch(r"([^/]+)/([^=]+)=(\d+)", value)
        if not match:
            raise ValueError(f"invalid expected count {value!r}; use N/BATCH=COUNT")
        result[(match.group(1), match.group(2))] = int(match.group(3))
    return result


def evaluator_assert(path: Path, expected_values: list[str]) -> None:
    rows = [row for row in read_csv(path) if row.get("phase") == "measured"]
    bad = [row for row in rows if row.get("success", "").lower() != "true" or row.get("consistent", "").lower() != "true"]
    if bad:
        raise ValueError(f"{path} contains {len(bad)} failed or inconsistent measured runs")
    expected = parse_expected(expected_values)
    for key, count in expected.items():
        actual = sum(row.get("nodes") == key[0] and row.get("batch_size") == key[1] for row in rows)
        if actual != count:
            raise ValueError(f"{path}: {key[0]}/{key[1]} has {actual} measured runs, expected {count}")


def _true(value: str) -> bool:
    return value.strip().lower() == "true"


def annotate_placement(phase_root: Path, inventory_path: Path) -> None:
    inventory = json.loads(inventory_path.read_text(encoding="utf-8-sig"))
    placement = {int(node["id"]): node for node in inventory["nodes"]}

    node_path = phase_root / "node_measurements.csv"
    node_rows = read_csv(node_path)
    for row in node_rows:
        node = placement.get(int(row["node_id"]))
        if node is None:
            raise ValueError(f"{node_path}: unknown node_id {row['node_id']}")
        row["region"] = node["region"]
        row["availability_zone"] = node["zone"]
        row["instance_type"] = node["instance_type"]
    node_fields = list(node_rows[0]) if node_rows else []
    write_csv(node_path, node_rows, node_fields)

    run_path = phase_root / "run_measurements.csv"
    run_rows = read_csv(run_path)
    for row in run_rows:
        node = placement.get(int(row["critical_node_id"]))
        if node is None:
            raise ValueError(f"{run_path}: unknown critical_node_id {row['critical_node_id']}")
        row["critical_node_region"] = node["region"]
        row["critical_node_availability_zone"] = node["zone"]
    run_fields = list(run_rows[0]) if run_rows else []
    write_csv(run_path, run_rows, run_fields)


def _assert_targets(path: Path, nodes: int) -> None:
    payload = json.loads(path.read_text(encoding="utf-8-sig"))
    targets = payload.get("data", {}).get("activeTargets", [])
    if len(targets) != nodes or any(target.get("health") != "up" for target in targets):
        raise ValueError(f"{path}: Prometheus targets are not {nodes}/{nodes} up")


def _assert_network(path: Path, nodes: int, placement: dict[int, dict[str, Any]]) -> None:
    rows = read_csv(path)
    expected_pairs = {(source, target) for source in range(nodes) for target in range(nodes)}
    actual_pairs: set[tuple[int, int]] = set()
    for row in rows:
        source, target = int(row["source_node_id"]), int(row["target_node_id"])
        actual_pairs.add((source, target))
        if int(row["attempts"]) != 5 or int(row["successes"]) != 5:
            raise ValueError(f"{path}: {source}->{target} did not pass all five health attempts")
        if row["source_region"] != placement[source]["region"] or row["target_region"] != placement[target]["region"]:
            raise ValueError(f"{path}: {source}->{target} has incorrect region attribution")
    if actual_pairs != expected_pairs or len(rows) != nodes * nodes:
        raise ValueError(f"{path}: pairwise matrix is incomplete or contains duplicates")


def assert_three_region_phase(phase_root: Path, tolerance_us: int = 20) -> None:
    manifest_path = phase_root / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    if manifest.get("schema_version") != "bloc-ec2-three-region-phase/v1":
        raise ValueError(f"{manifest_path}: unsupported schema")
    if manifest.get("status") != "complete" or manifest.get("topology") != "T2-three-region":
        raise ValueError(f"{manifest_path}: phase is not complete three-region evidence")
    if manifest.get("operator_instance_type") != "t3.small" or manifest.get("controller_instance_type") != "t3.small":
        raise ValueError(f"{manifest_path}: accepted campaign requires t3.small throughout")
    digest = str(manifest.get("docker_image_digest", ""))
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError(f"{manifest_path}: image digest is missing or invalid")

    nodes, repetitions = int(manifest["node_count"]), int(manifest["repetitions"])
    regions = [manifest["primary_region"], manifest["secondary_region"], manifest["tertiary_region"]]
    if len(set(regions)) != 3:
        raise ValueError(f"{manifest_path}: regions are not distinct")
    placement_rows = manifest.get("placement", [])
    if len(placement_rows) != nodes:
        raise ValueError(f"{manifest_path}: placement count is invalid")
    placement = {int(node["id"]): node for node in placement_rows}
    if set(placement) != set(range(nodes)):
        raise ValueError(f"{manifest_path}: placement node IDs are invalid")
    for node_id, node in placement.items():
        if node.get("region") != regions[node_id % 3] or node.get("instance_type") != "t3.small" or not node.get("zone"):
            raise ValueError(f"{manifest_path}: node {node_id} placement is invalid")
    peerings = manifest.get("peering_connection_ids", {})
    if set(peerings) != {"primary_secondary", "primary_tertiary", "secondary_tertiary"} or len(set(peerings.values())) != 3:
        raise ValueError(f"{manifest_path}: three unique peering connections are required")

    runs = [row for row in read_csv(phase_root / "run_measurements.csv") if row.get("phase", "").lower() == "measured"]
    node_rows = [row for row in read_csv(phase_root / "node_measurements.csv") if row.get("phase", "").lower() == "measured"]
    expected_batches = {int(value) for value in manifest["batch_sizes"]}
    for batch in expected_batches:
        batch_runs = [row for row in runs if int(row["batch_size"]) == batch]
        if len(batch_runs) != repetitions or len({row["run_id"] for row in batch_runs}) != repetitions:
            raise ValueError(f"batch {batch}: expected exactly {repetitions} unique measured runs")
        for row in batch_runs:
            if not _true(row["success"]) or not _true(row["consistent"]) or int(row["selected_ciphertexts"]) != batch:
                raise ValueError(f"batch {batch}: failed, inconsistent, or incorrectly selected run")
            attributed = (
                int(row["proposal_preparation_us"])
                + int(row["acs_us"])
                + int(row["merge_plan_us"])
                + int(row["threshold_wait_us"])
                + int(row["combine_us"])
                + int(row["materialization_us"])
            )
            if abs(attributed - int(row["total_slot_us"])) > tolerance_us:
                raise ValueError(f"{row['run_id']}: four-stage attribution exceeds {tolerance_us} us tolerance")
            critical = placement[int(row["critical_node_id"])]
            if row.get("critical_node_region") != critical["region"] or row.get("critical_node_availability_zone") != critical["zone"]:
                raise ValueError(f"{row['run_id']}: critical-node region attribution is invalid")

        run_ids = {row["run_id"] for row in batch_runs}
        batch_nodes = [row for row in node_rows if row["run_id"] in run_ids]
        if len(batch_nodes) != repetitions * nodes:
            raise ValueError(f"batch {batch}: expected {repetitions * nodes} measured node rows")
        by_run: dict[str, list[dict[str, str]]] = defaultdict(list)
        for row in batch_nodes:
            by_run[row["run_id"]].append(row)
            node_id = int(row["node_id"])
            if (
                not _true(row["success"])
                or not _true(row["consistent"])
                or not _true(row["metrics_finalized"])
                or int(row["selected_ciphertexts"]) != batch
                or row.get("region") != placement[node_id]["region"]
                or row.get("availability_zone") != placement[node_id]["zone"]
                or row.get("instance_type") != "t3.small"
            ):
                raise ValueError(f"batch {batch}: invalid or unfinalized node measurement")
            substages = sum(int(row[name]) for name in (
                "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us"
            ))
            if abs(substages - int(row["merge_plan_us"])) > tolerance_us:
                raise ValueError(f"{row['run_id']}/node-{node_id}: merge-plan additivity exceeds {tolerance_us} us")
        if any({int(row["node_id"]) for row in values} != set(range(nodes)) for values in by_run.values()):
            raise ValueError(f"batch {batch}: a run is missing node measurements")
        if any(sum(_true(row["critical_node"]) for row in values) != 1 for values in by_run.values()):
            raise ValueError(f"batch {batch}: each run must identify exactly one critical node")

    if len(runs) != repetitions * len(expected_batches) or len(node_rows) != repetitions * len(expected_batches) * nodes:
        raise ValueError("phase contains unexpected measured rows or batch sizes")
    _assert_targets(phase_root / "prometheus-targets-before.json", nodes)
    _assert_targets(phase_root / "prometheus-targets.json", nodes)
    _assert_network(phase_root / "network-pre.csv", nodes, placement)
    _assert_network(phase_root / "network-post.csv", nodes, placement)
    resource_evidence_summary(
        phase_root / "resource_timeseries.csv",
        expected_nodes={str(node) for node in range(nodes)},
        expected_configurations={(f"n{nodes}-b{batch}", "resource-measured") for batch in expected_batches},
    )


def evaluator_rows(path: Path) -> list[dict[str, Any]]:
    groups: dict[tuple[int, int], list[dict[str, str]]] = defaultdict(list)
    if path.exists():
        for row in read_csv(path):
            if row.get("phase") == "measured":
                groups[(int(row["nodes"]), int(row["batch_size"]))].append(row)
    out = []
    for (nodes, batch), rows in sorted(groups.items()):
        successful = sum(row.get("success", "").lower() == "true" and row.get("consistent", "").lower() == "true" for row in rows)
        out.append({"nodes": nodes, "batch": batch, "measured": len(rows), "successful": successful, "failed": len(rows) - successful})
    return out


BENCHMARK = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([0-9.]+)\s+ns/op\s+([0-9]+)\s+B/op\s+([0-9]+)\s+allocs/op")


def benchmark_summary(paths: list[Path]) -> list[dict[str, Any]]:
    groups: dict[str, list[tuple[float, float, float]]] = defaultdict(list)
    for path in paths:
        for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
            match = BENCHMARK.match(line)
            if match:
                groups[match.group(1)].append(tuple(float(value) for value in match.groups()[1:]))
    rows = []
    for name, samples in sorted(groups.items()):
        rows.append({
            "benchmark": name,
            "samples": len(samples),
            "median_ns_per_op": round(statistics.median(x[0] for x in samples), 2),
            "median_bytes_per_op": round(statistics.median(x[1] for x in samples), 2),
            "median_allocs_per_op": round(statistics.median(x[2] for x in samples), 2),
        })
    return rows


EVAL_FIELDS = ["merge_plan_us", "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us"]


def evaluator_summary(path: Path) -> list[dict[str, Any]]:
    groups: dict[tuple[int, int], list[dict[str, str]]] = defaultdict(list)
    for row in read_csv(path):
        if row.get("phase") == "measured" and row.get("success", "").lower() == "true" and row.get("consistent", "").lower() == "true":
            groups[(int(row["nodes"]), int(row["batch_size"]))].append(row)
    out = []
    for (nodes, batch), rows in sorted(groups.items()):
        item: dict[str, Any] = {"nodes": nodes, "batch_size": batch, "samples": len(rows)}
        for field in EVAL_FIELDS:
            item[f"median_{field}"] = round(statistics.median(float(row[field]) for row in rows), 2)
        out.append(item)
    return out


def compare_merge_plan(campaign: Path) -> None:
    before_bench = {row["benchmark"]: row for row in read_csv(campaign / "baseline/benchmark-summary.csv")}
    after_bench = {row["benchmark"]: row for row in read_csv(campaign / "optimized/benchmark-summary.csv")}
    benchmark_rows = []
    for name in sorted(before_bench.keys() & after_bench.keys()):
        before, after = before_bench[name], after_bench[name]
        before_ns, after_ns = float(before["median_ns_per_op"]), float(after["median_ns_per_op"])
        benchmark_rows.append({
            "benchmark": name, "baseline_median_ns": before_ns, "optimized_median_ns": after_ns,
            "latency_delta_percent": round(((after_ns - before_ns) / before_ns) * 100, 2) if before_ns else 0,
            "baseline_bytes_per_op": float(before["median_bytes_per_op"]),
            "optimized_bytes_per_op": float(after["median_bytes_per_op"]),
            "baseline_allocs_per_op": float(before["median_allocs_per_op"]),
            "optimized_allocs_per_op": float(after["median_allocs_per_op"]),
        })
    write_csv(campaign / "comparison.csv", benchmark_rows)

    before_eval = {(row["nodes"], row["batch_size"]): row for row in read_csv(campaign / "baseline/evaluator-summary.csv")}
    after_eval = {(row["nodes"], row["batch_size"]): row for row in read_csv(campaign / "optimized/evaluator-summary.csv")}
    eval_rows = []
    for key in sorted(before_eval.keys() & after_eval.keys(), key=lambda item: (int(item[0]), int(item[1]))):
        before, after = before_eval[key], after_eval[key]
        before_us, after_us = float(before["median_merge_plan_us"]), float(after["median_merge_plan_us"])
        eval_rows.append({"nodes": key[0], "batch_size": key[1], "baseline_median_merge_plan_us": before_us,
                          "optimized_median_merge_plan_us": after_us,
                          "delta_percent": round(((after_us - before_us) / before_us) * 100, 2) if before_us else 0})
    write_csv(campaign / "evaluator-comparison.csv", eval_rows)
    lines = ["# Merge/Plan Optimization Report", "", f"- Campaign: `{campaign.name}`",
             "- Scope: local deterministic attribution and optimization", "- Raw observations were retained; no outliers were removed.", "",
             "## End-to-End Merge/Plan Medians", "", "| Nodes | Batch | Baseline us | Optimized us | Delta |", "|---:|---:|---:|---:|---:|"]
    lines.extend(f"| {r['nodes']} | {r['batch_size']} | {r['baseline_median_merge_plan_us']} | {r['optimized_median_merge_plan_us']} | {r['delta_percent']}% |" for r in eval_rows)
    lines.extend(["", "## Retention Gate", "", "Batch-32/128 pipeline benchmark medians should remain at or below a 5% regression.", "",
                  "| Benchmark | Time delta | Baseline B/op | Optimized B/op |", "|---|---:|---:|---:|"])
    for row in benchmark_rows:
        if row["benchmark"].endswith("/pipeline") and re.search(r"b(?:32|128)", row["benchmark"]):
            lines.append(f"| `{row['benchmark']}` | {row['latency_delta_percent']}% | {row['baseline_bytes_per_op']} | {row['optimized_bytes_per_op']} |")
    lines.extend(["", "## Artifacts", "", "- `comparison.csv`: benchmark medians and allocation changes.",
                  "- `evaluator-comparison.csv`: end-to-end local evaluator comparison.",
                  "- `baseline/` and `optimized/`: raw benchmarks, profiles, manifests, and evaluator outputs.", ""])
    atomic_text(campaign / "REPORT.md", "\n".join(lines))


def merge_csv(output: Path, inputs: list[Path]) -> None:
    fields: list[str] | None = None
    rows: list[dict[str, str]] = []
    for path in inputs:
        current = read_csv(path)
        with path.open(encoding="utf-8-sig", newline="") as handle:
            current_fields = csv.DictReader(handle).fieldnames or []
        if fields is None:
            fields = current_fields
        elif current_fields != fields:
            raise ValueError(f"CSV columns differ in {path}")
        rows.extend(current)
    write_csv(output, rows, fields or [])


def merge_json(output: Path, inputs: list[Path]) -> None:
    values: list[Any] = []
    for path in inputs:
        value = json.loads(path.read_text(encoding="utf-8-sig"))
        values.extend(value if isinstance(value, list) else [value])
    write_json(output, values)


def merge_scenarios(root: Path, specs: list[str], multiple_blocks: bool) -> None:
    parsed: list[tuple[int, Path]] = []
    for spec in specs:
        block, separator, path = spec.partition(":")
        if not separator:
            raise ValueError(f"invalid scenario spec {spec!r}")
        parsed.append((int(block), Path(path)))
    for name in ("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv"):
        merged: list[dict[str, Any]] = []
        fields: list[str] | None = None
        for block, path in parsed:
            rows = read_csv(path / name)
            if fields is None:
                with (path / name).open(encoding="utf-8-sig", newline="") as handle:
                    fields = list(csv.DictReader(handle).fieldnames or [])
                if "measurement_block" not in fields:
                    fields.append("measurement_block")
            for row in rows:
                row["measurement_block"] = str(block)
                if multiple_blocks and "run_id" in row:
                    row["run_id"] = f"block-{block}-{row['run_id']}"
                merged.append(row)
        write_csv(root / name, merged, fields or [])
    summaries = []
    for block, path in parsed:
        value = json.loads((path / "scenario_summary.json").read_text(encoding="utf-8-sig"))
        values = value if isinstance(value, list) else [value]
        for item in values:
            item["measurement_block"] = block
            summaries.append(item)
    write_json(root / "scenario_summary.json", summaries)


def commands_json(records: Path) -> list[dict[str, Any]]:
    output = []
    if not records.exists():
        return output
    for number, line in enumerate(records.read_text(encoding="utf-8").splitlines(), 1):
        parts = line.split("\t")
        if len(parts) != 7:
            raise ValueError(f"invalid command record at {records}:{number}")
        stage, working_directory, command, started_at, finished_at, exit_code, log = parts
        output.append({"stage": stage, "working_directory": working_directory, "command": command,
                       "started_at": started_at, "finished_at": finished_at,
                       "exit_code": int(exit_code), "log": log})
    return output


def command_main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    write = sub.add_parser("write-json")
    write.add_argument("--output", required=True, type=Path)
    write.add_argument("--input", type=Path)
    check = sub.add_parser("assert-evaluator")
    check.add_argument("--csv", required=True, type=Path)
    check.add_argument("--expected", action="append", default=[])
    rows = sub.add_parser("evaluator-rows")
    rows.add_argument("--csv", required=True, type=Path)
    rows.add_argument("--output", required=True, type=Path)
    merge = sub.add_parser("merge-csv")
    merge.add_argument("--output", required=True, type=Path)
    merge.add_argument("inputs", nargs="+", type=Path)
    mergej = sub.add_parser("merge-json")
    mergej.add_argument("--output", required=True, type=Path)
    mergej.add_argument("inputs", nargs="+", type=Path)
    bench = sub.add_parser("benchmark-summary")
    bench.add_argument("--output", required=True, type=Path)
    bench.add_argument("inputs", nargs="+", type=Path)
    evaluation = sub.add_parser("evaluator-summary")
    evaluation.add_argument("--output", required=True, type=Path)
    evaluation.add_argument("--csv", required=True, type=Path)
    commands = sub.add_parser("commands-json")
    commands.add_argument("--records", required=True, type=Path)
    commands.add_argument("--output", required=True, type=Path)
    comparison = sub.add_parser("compare-merge-plan")
    comparison.add_argument("--campaign", required=True, type=Path)
    scenarios = sub.add_parser("merge-scenarios")
    scenarios.add_argument("--root", required=True, type=Path)
    scenarios.add_argument("--multiple-blocks", action="store_true")
    scenarios.add_argument("specs", nargs="+")
    placement = sub.add_parser("annotate-placement")
    placement.add_argument("--phase-root", required=True, type=Path)
    placement.add_argument("--inventory", required=True, type=Path)
    three_region = sub.add_parser("assert-three-region-phase")
    three_region.add_argument("--phase-root", required=True, type=Path)
    resources = sub.add_parser("resource-summary")
    resources.add_argument("--input", required=True, type=Path)
    resources.add_argument("--output", required=True, type=Path)
    resources.add_argument("--expected-node", action="append", default=[])
    resources.add_argument("--expected-configuration", action="append", default=[])
    args = parser.parse_args()

    if args.command == "write-json":
        value = json.loads(args.input.read_text(encoding="utf-8-sig") if args.input else sys.stdin.read())
        write_json(args.output, value)
    elif args.command == "assert-evaluator":
        evaluator_assert(args.csv, args.expected)
    elif args.command == "evaluator-rows":
        write_json(args.output, evaluator_rows(args.csv))
    elif args.command == "merge-csv":
        merge_csv(args.output, args.inputs)
    elif args.command == "merge-json":
        merge_json(args.output, args.inputs)
    elif args.command == "benchmark-summary":
        write_csv(args.output, benchmark_summary(args.inputs))
    elif args.command == "evaluator-summary":
        write_csv(args.output, evaluator_summary(args.csv))
    elif args.command == "commands-json":
        write_json(args.output, commands_json(args.records))
    elif args.command == "compare-merge-plan":
        compare_merge_plan(args.campaign)
    elif args.command == "merge-scenarios":
        merge_scenarios(args.root, args.specs, args.multiple_blocks)
    elif args.command == "annotate-placement":
        annotate_placement(args.phase_root, args.inventory)
    elif args.command == "assert-three-region-phase":
        assert_three_region_phase(args.phase_root)
    elif args.command == "resource-summary":
        configurations = set()
        for value in args.expected_configuration:
            scenario, separator, phase = value.partition(":")
            if not separator or not scenario or not phase:
                raise ValueError("expected resource configuration must be SCENARIO:PHASE")
            configurations.add((scenario, phase))
        write_csv(
            args.output,
            resource_evidence_summary(args.input, set(args.expected_node), configurations),
            RESOURCE_SUMMARY_FIELDS,
        )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(command_main())
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
