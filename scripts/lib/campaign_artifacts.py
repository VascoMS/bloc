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
FINAL_RESOURCE_BATCHES = (8, 32, 128)
FINAL_RESOURCE_BLOCKS = 10


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


def aggregate_final_resource_summaries(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Aggregate validated per-block node summaries into per-batch evidence."""
    node_groups: dict[tuple[str, str, str, str], list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        if row["scope"] != "node":
            continue
        match = re.fullmatch(r"(n[47]-b(?:8|32|128))-block-(?:[1-9]|10)", str(row["scenario"]))
        if not match:
            raise ValueError(f"invalid final resource scenario {row['scenario']!r}")
        node_groups[(str(row["node"]), str(row["region"]), match.group(1), str(row["phase"]))].append(row)

    node_rows: list[dict[str, Any]] = []
    for (node, region, scenario, phase), values in sorted(node_groups.items()):
        node_rows.append({
            "scope": "node", "node": node, "region": region, "scenario": scenario, "phase": phase,
            "samples": sum(int(row["samples"]) for row in values),
            "first_timestamp": min(str(row["first_timestamp"]) for row in values),
            "last_timestamp": max(str(row["last_timestamp"]) for row in values),
            "cpu_usage_delta_us": sum(int(row["cpu_usage_delta_us"]) for row in values),
            "memory_current_max_bytes": max(int(row["memory_current_max_bytes"]) for row in values),
            "memory_peak_bytes": max(int(row["memory_peak_bytes"]) for row in values),
            "network_receive_delta_bytes": sum(int(row["network_receive_delta_bytes"]) for row in values),
            "network_transmit_delta_bytes": sum(int(row["network_transmit_delta_bytes"]) for row in values),
        })

    cluster_rows: list[dict[str, Any]] = []
    for scenario, phase in sorted({(row["scenario"], row["phase"]) for row in node_rows}):
        values = [row for row in node_rows if row["scenario"] == scenario and row["phase"] == phase]
        cluster_rows.append({
            "scope": "cluster", "node": "all", "region": "cluster", "scenario": scenario, "phase": phase,
            "samples": sum(int(row["samples"]) for row in values),
            "first_timestamp": min(str(row["first_timestamp"]) for row in values),
            "last_timestamp": max(str(row["last_timestamp"]) for row in values),
            "cpu_usage_delta_us": sum(int(row["cpu_usage_delta_us"]) for row in values),
            "memory_current_max_bytes": sum(int(row["memory_current_max_bytes"]) for row in values),
            "memory_peak_bytes": sum(int(row["memory_peak_bytes"]) for row in values),
            "network_receive_delta_bytes": sum(int(row["network_receive_delta_bytes"]) for row in values),
            "network_transmit_delta_bytes": sum(int(row["network_transmit_delta_bytes"]) for row in values),
        })
    return node_rows + cluster_rows


def _final_resource_segments(phase_root: Path) -> tuple[list[dict[str, str]], set[tuple[str, str]]]:
    inventory = json.loads((phase_root / "inventory.json").read_text(encoding="utf-8-sig"))
    placement = {str(node["id"]): str(node["region"]) for node in inventory.get("nodes", [])}
    if set(placement) not in ({str(node) for node in range(4)}, {str(node) for node in range(7)}):
        raise ValueError(f"{phase_root}: final resource placement is invalid")

    expected_paths: dict[Path, tuple[str, str, str]] = {}
    expected_configurations: set[tuple[str, str]] = set()
    for node, region in placement.items():
        for block in range(1, FINAL_RESOURCE_BLOCKS + 1):
            for batch in FINAL_RESOURCE_BATCHES:
                scenario = f"n{len(placement)}-b{batch}-block-{block}"
                path = phase_root / f"logs/node-{node}/resources/node-{node}-block-{block}-batch-{batch}.csv"
                expected_paths[path] = (node, region, scenario)
                expected_configurations.add((scenario, "resource-measured"))
    actual_paths = set(phase_root.glob("logs/node-*/resources/*.csv"))
    if actual_paths != set(expected_paths):
        missing = sorted(str(path.relative_to(phase_root)) for path in set(expected_paths) - actual_paths)
        extra = sorted(str(path.relative_to(phase_root)) for path in actual_paths - set(expected_paths))
        raise ValueError(f"{phase_root}: resource segment set is incomplete or unexpected; missing={missing}, extra={extra}")

    merged: list[dict[str, str]] = []
    for path, (node, region, scenario) in sorted(expected_paths.items(), key=lambda item: str(item[0])):
        log_path = path.with_suffix(".log")
        if not log_path.is_file():
            raise ValueError(f"{phase_root}: resource segment log is missing: {log_path.relative_to(phase_root)}")
        with path.open(encoding="utf-8-sig", newline="") as handle:
            reader = csv.DictReader(handle)
            if (reader.fieldnames or []) != RESOURCE_TIMESERIES_FIELDS:
                raise ValueError(f"{path}: invalid resource_timeseries.csv columns")
            rows = list(reader)
        if any(
            row["node"] != node or row["region"] != region or row["scenario"] != scenario
            or row["phase"] != "resource-measured"
            for row in rows
        ):
            raise ValueError(f"{path}: resource segment metadata mismatch")
        merged.extend(rows)
    return merged, expected_configurations


def finalize_final_resource_artifacts(phase_root: Path) -> None:
    merged, expected_configurations = _final_resource_segments(phase_root)
    expected_nodes = {str(node["id"]) for node in json.loads(
        (phase_root / "inventory.json").read_text(encoding="utf-8-sig")
    )["nodes"]}
    write_csv(phase_root / "resource_timeseries.csv", merged, RESOURCE_TIMESERIES_FIELDS)
    segment_summaries = resource_evidence_summary(
        phase_root / "resource_timeseries.csv", expected_nodes, expected_configurations
    )
    write_csv(phase_root / "resource-segment-summary.csv", segment_summaries, RESOURCE_SUMMARY_FIELDS)
    write_csv(
        phase_root / "resource-summary.csv",
        aggregate_final_resource_summaries(segment_summaries),
        RESOURCE_SUMMARY_FIELDS,
    )


def _summary_as_strings(rows: Iterable[dict[str, Any]]) -> list[dict[str, str]]:
    return [{field: str(row[field]) for field in RESOURCE_SUMMARY_FIELDS} for row in rows]


def assert_final_resource_artifacts(phase_root: Path) -> None:
    merged, expected_configurations = _final_resource_segments(phase_root)
    inventory = json.loads((phase_root / "inventory.json").read_text(encoding="utf-8-sig"))
    expected_nodes = {str(node["id"]) for node in inventory["nodes"]}
    timeseries_path = phase_root / "resource_timeseries.csv"
    if read_csv(timeseries_path) != merged:
        raise ValueError(f"{timeseries_path}: merged resource segments do not match recovered evidence")
    segment_summaries = resource_evidence_summary(timeseries_path, expected_nodes, expected_configurations)
    segment_path = phase_root / "resource-segment-summary.csv"
    if read_csv(segment_path) != _summary_as_strings(segment_summaries):
        raise ValueError(f"{segment_path}: resource segment summary does not match recovered evidence")
    summary_path = phase_root / "resource-summary.csv"
    expected_summary = aggregate_final_resource_summaries(segment_summaries)
    if read_csv(summary_path) != _summary_as_strings(expected_summary):
        raise ValueError(f"{summary_path}: resource summary does not match recovered evidence")


def parse_expected(values: list[str]) -> dict[tuple[str, str], int]:
    result: dict[tuple[str, str], int] = {}
    for value in values:
        match = re.fullmatch(r"([^/]+)/([^=]+)=(\d+)", value)
        if not match:
            raise ValueError(f"invalid expected count {value!r}; use N/BATCH=COUNT")
        result[(match.group(1), match.group(2))] = int(match.group(3))
    return result


def evaluator_assert(path: Path, expected_values: list[str], require_success: bool = False) -> None:
    rows = [row for row in read_csv(path) if row.get("phase") == "measured"]
    bad = [row for row in rows if row.get("success", "").lower() != "true" or row.get("consistent", "").lower() != "true"]
    if require_success and bad:
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
        if not (_true(row.get("success", "")) and _true(row.get("consistent", ""))):
            row["critical_node_region"] = ""
            row["critical_node_availability_zone"] = ""
            continue
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
    outcomes_present = bool(runs) and all("outcome" in row for row in runs)
    timeouts_present = bool(runs) and all("timed_out" in row for row in runs)
    expected_batches = {int(value) for value in manifest["batch_sizes"]}
    for batch in expected_batches:
        batch_runs = [row for row in runs if int(row["batch_size"]) == batch]
        if len(batch_runs) != repetitions or len({row["run_id"] for row in batch_runs}) != repetitions:
            raise ValueError(f"batch {batch}: expected exactly {repetitions} unique measured runs")
        completed_run_ids: set[str] = set()
        for row in batch_runs:
            completed = _true(row["success"]) and _true(row["consistent"])
            if not completed:
                if outcomes_present and row["outcome"] not in {"failed", "timed_out"}:
                    raise ValueError(f"batch {batch}: invalid retained failure outcome")
                if timeouts_present and _true(row["timed_out"]) != (row.get("outcome") == "timed_out"):
                    raise ValueError(f"batch {batch}: invalid retained timeout classification")
                continue
            if outcomes_present and row["outcome"] not in {"", "completed"}:
                raise ValueError(f"batch {batch}: invalid completed outcome")
            completed_run_ids.add(row["run_id"])
            if int(row["selected_ciphertexts"]) != batch:
                raise ValueError(f"batch {batch}: completed run selected the wrong ciphertext count")
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
        by_run: dict[str, list[dict[str, str]]] = defaultdict(list)
        for row in batch_nodes:
            by_run[row["run_id"]].append(row)
            node_id = int(row["node_id"])
            if (
                node_id not in placement
                or row.get("region") != placement[node_id]["region"]
                or row.get("availability_zone") != placement[node_id]["zone"]
                or row.get("instance_type") != "t3.small"
            ):
                raise ValueError(f"batch {batch}: invalid node placement")
            if row["run_id"] not in completed_run_ids:
                continue
            if (
                not _true(row["success"])
                or not _true(row["consistent"])
                or not _true(row["metrics_finalized"])
                or int(row["selected_ciphertexts"]) != batch
            ):
                raise ValueError(f"batch {batch}: invalid or unfinalized completed node measurement")
            substages = sum(int(row[name]) for name in (
                "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us"
            ))
            if abs(substages - int(row["merge_plan_us"])) > tolerance_us:
                raise ValueError(f"{row['run_id']}/node-{node_id}: merge-plan additivity exceeds {tolerance_us} us")
        if any({int(row["node_id"]) for row in by_run[run_id]} != set(range(nodes)) for run_id in completed_run_ids):
            raise ValueError(f"batch {batch}: a run is missing node measurements")
        if any(sum(_true(row["critical_node"]) for row in by_run[run_id]) != 1 for run_id in completed_run_ids):
            raise ValueError(f"batch {batch}: each run must identify exactly one critical node")
        if any(len({int(row["node_id"]) for row in values}) != len(values) for values in by_run.values()):
            raise ValueError(f"batch {batch}: duplicate node measurement")

    if len(runs) != repetitions * len(expected_batches) or len(node_rows) > repetitions * len(expected_batches) * nodes:
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
    campaign_order: dict[tuple[int, str], int] = {}
    next_campaign_order = 0
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
                if name in {"run_measurements.csv", "node_measurements.csv"} and "campaign_order_index" not in fields:
                    fields.append("campaign_order_index")
            for row in rows:
                original_run_id = row.get("run_id", "")
                row["measurement_block"] = str(block)
                if name == "run_measurements.csv":
                    next_campaign_order += 1
                    campaign_order[(block, original_run_id)] = next_campaign_order
                    row["campaign_order_index"] = str(next_campaign_order)
                elif name == "node_measurements.csv":
                    order = campaign_order.get((block, original_run_id))
                    if order is None:
                        raise ValueError(f"{path}: node row references unknown run_id {original_run_id!r}")
                    row["campaign_order_index"] = str(order)
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


FINAL_ECR_IMAGE = re.compile(
    r"^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$"
)
ACS_TRACE_SCHEMAS = {"bloc-acs-trace/v1", "bloc-acs-trace/v2", "bloc-acs-trace/v3"}
ACS_MESSAGE_SUBTYPES = {"proof", "echo", "ready", "bval", "aux"}


def _assert_nonnegative_trace_offsets(value: Any, location: str) -> None:
    if isinstance(value, dict):
        if value.get("recorded") is True and "offset_us" in value and int(value["offset_us"]) < 0:
            raise ValueError(f"{location}: negative ACS trace offset")
        for child in value.values():
            _assert_nonnegative_trace_offsets(child, location)
    elif isinstance(value, list):
        for child in value:
            _assert_nonnegative_trace_offsets(child, location)


def _acs_trace_key(record: dict[str, Any]) -> tuple[int, str, int, int]:
    key = record.get("key", {})
    return (int(key["measurement_block"]), str(key["run_id"]), int(key["node_id"]), int(key["slot"]))


def assert_acs_trace_artifacts(scenario_root: Path, nodes: int, trace_schema: str, stream_mode: str) -> None:
    if trace_schema and trace_schema not in ACS_TRACE_SCHEMAS:
        raise ValueError(f"unsupported ACS trace schema {trace_schema!r}")
    evaluator_manifest_path = scenario_root / "manifest.json"
    evaluator_manifest = json.loads(evaluator_manifest_path.read_text(encoding="utf-8-sig"))
    if evaluator_manifest.get("stream_mode") != stream_mode:
        raise ValueError(f"{evaluator_manifest_path}: stream mode mismatch")
    if str(evaluator_manifest.get("acs_trace_schema", "")) != trace_schema:
        raise ValueError(f"{evaluator_manifest_path}: ACS trace schema mismatch")

    run_path = scenario_root / "run_measurements.csv"
    run_rows = read_csv(run_path)
    if any(row.get("stream_mode") != stream_mode for row in run_rows):
        raise ValueError(f"{run_path}: stream mode mismatch")
    if not trace_schema:
        return
    slots: dict[tuple[int, str], int] = {}
    for row in run_rows:
        run_key = (int(row.get("measurement_block", 0)), row["run_id"])
        if run_key in slots:
            raise ValueError(f"{run_path}: duplicate block-scoped run identity {run_key}")
        slots[run_key] = int(row["slot"])

    node_path = scenario_root / "node_measurements.csv"
    node_rows = read_csv(node_path)
    expected: dict[tuple[int, str, int, int], dict[str, str]] = {}
    for row in node_rows:
        run_key = (int(row.get("measurement_block", 0)), row["run_id"])
        if run_key not in slots:
            raise ValueError(f"{node_path}: node row has no matching run {run_key}")
        key = (*run_key, int(row["node_id"]), slots[run_key])
        if key in expected:
            raise ValueError(f"{node_path}: duplicate expected ACS trace key {key}")
        if row.get("acs_trace_schema") != trace_schema:
            raise ValueError(f"{node_path}: ACS trace schema mismatch")
        if row.get("stream_mode") != stream_mode:
            raise ValueError(f"{node_path}: stream mode mismatch")
        expected[key] = row

    trace_path = scenario_root / "acs_trace.jsonl"
    records: dict[tuple[int, str, int, int], dict[str, Any]] = {}
    with trace_path.open(encoding="utf-8-sig") as handle:
        for line_number, line in enumerate(handle, 1):
            if not line.strip():
                continue
            record = json.loads(line)
            key = _acs_trace_key(record)
            if key in records:
                raise ValueError(f"{trace_path}:{line_number}: duplicate ACS trace key {key}")
            records[key] = record
    missing = set(expected) - set(records)
    unexpected = set(records) - set(expected)
    if missing:
        raise ValueError(f"{trace_path}: missing node trace for {sorted(missing)[0]}")
    if unexpected:
        raise ValueError(f"{trace_path}: unexpected node trace for {sorted(unexpected)[0]}")

    membership = set(range(nodes))
    for key, record in records.items():
        if record.get("schema_version") != trace_schema or record.get("enabled") is not True:
            raise ValueError(f"{trace_path}: disabled or mismatched ACS trace {key}")
        if trace_schema == "bloc-acs-trace/v3":
            transport = record.get("transport", {})
            if transport.get("sealed") is not True or transport.get("finalized") is not True:
                raise ValueError(f"{trace_path}: transport trace is not sealed and finalized for {key}")
        for name in ("rbc", "bba"):
            proposer_ids = [int(item["proposer_id"]) for item in record.get(name, [])]
            if len(proposer_ids) != len(set(proposer_ids)) or set(proposer_ids) != membership:
                raise ValueError(f"{trace_path}: invalid {name.upper()} proposer membership for {key}")
        messages = record.get("messages", [])
        subtypes = [str(item.get("subtype", "")) for item in messages]
        if len(subtypes) != len(set(subtypes)) or set(subtypes) != ACS_MESSAGE_SUBTYPES:
            raise ValueError(f"{trace_path}: invalid fixed ACS message subtypes for {key}")
        _assert_nonnegative_trace_offsets(record, f"{trace_path} {key}")
        core = record.get("aggregate", {}).get("core_decision", {})
        received = record.get("adapter", {}).get("node_output_received", {})
        if core.get("recorded") is not True or received.get("recorded") is not True:
            raise ValueError(f"{trace_path}: missing ACS completion offset for {key}")
        if int(core["offset_us"]) > int(received["offset_us"]):
            raise ValueError(f"{trace_path}: core decision occurs after node output receipt for {key}")

        totals = {
            "acs_inbound_messages": sum(int(item["trace"]["inbound_count"]) for item in messages),
            "acs_inbound_bytes": sum(int(item["trace"]["inbound_bytes"]) for item in messages),
            "acs_outbound_messages": sum(int(item["trace"]["outbound_count"]) for item in messages),
            "acs_outbound_bytes": sum(int(item["trace"]["outbound_bytes"]) for item in messages),
            "acs_send_count": sum(int(item["trace"]["send_count"]) for item in messages),
            "acs_send_total_us": sum(int(item["trace"]["send_total_us"]) for item in messages),
            "acs_send_max_us": max(int(item["trace"]["send_max_us"]) for item in messages),
            "acs_send_failures": sum(int(item["trace"]["send_failure_count"]) for item in messages),
        }
        if any(int(expected[key].get(field, -1)) != value for field, value in totals.items()):
            raise ValueError(f"{trace_path}: aggregate/detail ACS message mismatch for {key}")
        if trace_schema in {"bloc-acs-trace/v2", "bloc-acs-trace/v3"}:
            phase_fields = {
                "encode": ("acs_encode_total_us", "acs_encode_max_us"),
                "queue_wait": ("acs_queue_wait_total_us", "acs_queue_wait_max_us"),
                "stream_open": ("acs_stream_open_total_us", "acs_stream_open_max_us"),
                "write": ("acs_write_total_us", "acs_write_max_us"),
                "finalize": ("acs_finalize_total_us", "acs_finalize_max_us"),
            }
            phase_totals: dict[str, int] = {}
            for phase, (total_field, max_field) in phase_fields.items():
                values = [item["trace"].get(phase) for item in messages]
                if any(not isinstance(value, dict) for value in values):
                    raise ValueError(f"{trace_path}: missing ACS send phase {phase} for {key}")
                for item, value in zip(messages, values):
                    send_count = int(item["trace"]["send_count"])
                    count, total, maximum = int(value["count"]), int(value["total_us"]), int(value["max_us"])
                    if count != send_count or min(total, maximum) < 0 or maximum > total:
                        raise ValueError(f"{trace_path}: invalid ACS send phase {phase} for {key}")
                phase_totals[total_field] = sum(int(value["total_us"]) for value in values)
                phase_totals[max_field] = max(int(value["max_us"]) for value in values)
            open_count = sum(int(item["trace"].get("stream_open_count", -1)) for item in messages)
            reuse_count = sum(int(item["trace"].get("stream_reuse_count", -1)) for item in messages)
            if open_count + reuse_count != totals["acs_send_count"]:
                raise ValueError(f"{trace_path}: ACS stream open/reuse count mismatch for {key}")
            phase_totals["acs_stream_open_count"] = open_count
            phase_totals["acs_stream_reuse_count"] = reuse_count
            if any(int(expected[key].get(field, -1)) != value for field, value in phase_totals.items()):
                raise ValueError(f"{trace_path}: aggregate/detail ACS send phase mismatch for {key}")
        if trace_schema == "bloc-acs-trace/v3":
            for item in messages:
                subtype, trace = item["subtype"], item["trace"]
                scheduled = int(trace.get("scheduled_count", -1))
                terminal = int(trace.get("terminal_count", -1))
                pending = int(trace.get("pending_at_decision", -1))
                outbound = int(trace["outbound_count"])
                sent = int(trace["send_count"])
                failed = int(trace["send_failure_count"])
                if scheduled != terminal:
                    raise ValueError(
                        f"{trace_path}: ACS message subtype {subtype!r} scheduled count {scheduled} "
                        f"does not match terminal count {terminal} for {key}"
                    )
                if outbound > terminal or failed != terminal - outbound:
                    raise ValueError(
                        f"{trace_path}: ACS message subtype {subtype!r} terminal count does not "
                        f"match successful plus failed outcomes for {key}"
                    )
                if sent != outbound or pending < 0 or pending > scheduled:
                    raise ValueError(
                        f"{trace_path}: ACS message subtype {subtype!r} has inconsistent "
                        f"successful or pending counts for {key}"
                    )


def assert_final_phase(phase_root: Path, expected_topology: str, expected_phase: str) -> None:
    manifest_path = phase_root / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    if manifest.get("schema_version") != "bloc-final-campaign-phase-v1" or manifest.get("status") != "complete":
        raise ValueError(f"{manifest_path}: final phase is not complete")
    if manifest.get("topology") != expected_topology or manifest.get("phase") != expected_phase:
        raise ValueError(f"{manifest_path}: topology or phase mismatch")
    if not re.fullmatch(r"[0-9a-f]{40}", str(manifest.get("source_sha", ""))):
        raise ValueError(f"{manifest_path}: source identity is invalid")
    if not FINAL_ECR_IMAGE.fullmatch(str(manifest.get("bloc_image", ""))) or not FINAL_ECR_IMAGE.fullmatch(str(manifest.get("mempool_image", ""))):
        raise ValueError(f"{manifest_path}: image identity is invalid")
    if manifest.get("bundle_version") != "bloc-campaign-bundle-v1" or not manifest.get("public_config_id") or not manifest.get("encrypted_corpus_id"):
        raise ValueError(f"{manifest_path}: bundle identities are incomplete")
    if manifest.get("batches") != [8, 32, 128] or manifest.get("seed") != 20260621 or manifest.get("deadline") != "12s":
        raise ValueError(f"{manifest_path}: fixed schedule identity is invalid")
    trace_schema = str(manifest.get("acs_trace_schema", ""))
    if trace_schema and trace_schema not in ACS_TRACE_SCHEMAS:
        raise ValueError(f"{manifest_path}: unsupported ACS trace schema {trace_schema!r}")
    stream_mode = str(manifest.get("stream_mode", ""))
    if stream_mode not in {"fresh", "persistent", "persistent-lanes"}:
        raise ValueError(f"{manifest_path}: missing or unsupported stream mode")
    if stream_mode == "persistent" and trace_schema not in {"bloc-acs-trace/v2", "bloc-acs-trace/v3"}:
        raise ValueError(
            f"{manifest_path}: persistent stream mode requires ACS trace schema "
            "bloc-acs-trace/v2 or bloc-acs-trace/v3"
        )
    if stream_mode == "persistent-lanes" and trace_schema != "bloc-acs-trace/v3":
        raise ValueError(
            f"{manifest_path}: persistent-lanes stream mode requires ACS trace schema bloc-acs-trace/v3"
        )
    cluster_path = phase_root / "generated-public" / "cluster.json"
    remote_path = phase_root / "generated-public" / "remote-eval.json"
    cluster = json.loads(cluster_path.read_text(encoding="utf-8-sig"))
    remote = json.loads(remote_path.read_text(encoding="utf-8-sig"))
    if cluster.get("network", {}).get("stream_mode") != stream_mode:
        raise ValueError(f"{cluster_path}: stream mode mismatch")
    if remote.get("stream_mode") != stream_mode:
        raise ValueError(f"{remote_path}: stream mode mismatch")
    schedules = {
        "readiness-pilot": (4, 1, 3, 1, "off"),
        "latency": (None, 10, 1000, 10, "off"),
        "resource": (None, 0, 1000, 10, "on"),
    }
    if trace_schema and expected_phase == "latency":
        schedules["latency"] = (None, 5, 30, 3, "off")
    if expected_phase not in schedules:
        raise ValueError(f"unsupported final phase {expected_phase}")
    required_n, warmups, repetitions, blocks, sampler = schedules[expected_phase]
    nodes = int(manifest.get("node_count", 0))
    if nodes not in {4, 7} or (required_n is not None and nodes != required_n):
        raise ValueError(f"{manifest_path}: node count is invalid")
    if int(manifest.get("warmups", -1)) != warmups or int(manifest.get("repetitions", -1)) != repetitions:
        raise ValueError(f"{manifest_path}: repetitions or warmups are invalid")
    if int(manifest.get("blocks", -1)) != blocks or manifest.get("sampler") != sampler:
        raise ValueError(f"{manifest_path}: blocks or sampler phase is invalid")

    inventory = json.loads((phase_root / "inventory.json").read_text(encoding="utf-8-sig"))
    placement = inventory.get("nodes", [])
    if len(placement) != nodes or {int(node["id"]) for node in placement} != set(range(nodes)):
        raise ValueError(f"{phase_root}: placement is incomplete")
    for node in placement:
        if node.get("instance_type") != "t3.small" or not node.get("region") or not node.get("zone"):
            raise ValueError(f"{phase_root}: placement is invalid")
        if expected_topology == "same-az" and (node["region"], node["zone"]) != ("us-east-1", "us-east-1a"):
            raise ValueError(f"{phase_root}: same-AZ placement is invalid")
    if expected_topology == "three-region":
        regions = ["us-east-1", "eu-west-1", "eu-central-1"]
        if any(node["region"] != regions[int(node["id"]) % 3] for node in placement):
            raise ValueError(f"{phase_root}: three-region placement is invalid")

    leaked = [path for path in phase_root.rglob("*") if path.is_file() and (path.name.startswith("operator-") or "secret" in path.name.lower() or "secrets" in path.parts)]
    if leaked:
        raise ValueError(f"{phase_root}: secret material leaked into public artifacts")
    run_paths = list((phase_root / "scenarios").glob("**/run_measurements.csv"))
    if not run_paths:
        raise ValueError(f"{phase_root}: run measurements are missing")
    rows = [row for path in run_paths for row in read_csv(path) if row.get("phase") == "measured"]
    for path in run_paths:
        assert_acs_trace_artifacts(path.parent, nodes, trace_schema, stream_mode)
    for batch in (8, 32, 128):
        selected = [row for row in rows if int(row.get("batch_size", 0)) == batch]
        attempt_ids = {(row.get("measurement_block"), row.get("run_id")) for row in selected}
        if len(selected) != repetitions or len(attempt_ids) != repetitions:
            raise ValueError(f"batch {batch}: expected {repetitions} retained attempts")
        if {int(row.get("measurement_block", 0)) for row in selected} != set(range(1, blocks + 1)):
            raise ValueError(f"batch {batch}: measurement block coverage is incomplete")
        if any(int(row.get("planned_scenario_runs", 0)) != repetitions for row in selected):
            raise ValueError(f"batch {batch}: planned attempt count is invalid")
        for row in selected:
            success = _true(row.get("success", "")) and _true(row.get("consistent", ""))
            if success and int(row.get("selected_ciphertexts", 0)) != batch:
                raise ValueError(f"batch {batch}: successful row selected the wrong transaction count")
            if not success and row.get("outcome") not in {"failed", "timed_out"}:
                raise ValueError(f"batch {batch}: failed row was not retained with a classification")
            if row.get("outcome") == "timed_out" and not _true(row.get("timed_out", "")):
                raise ValueError(f"batch {batch}: timeout classification is inconsistent")
    if expected_phase == "resource":
        assert_final_resource_artifacts(phase_root)


def assert_final_cleanup(path: Path, expected_regions: list[str]) -> None:
    payload = json.loads(path.read_text(encoding="utf-8-sig"))
    regions = payload.get("regions")
    if not isinstance(regions, dict) or set(regions) != set(expected_regions):
        raise ValueError(f"{path}: cleanup regions are incomplete")
    categories = (
        "instances", "volumes", "vpcs", "subnets", "security_groups",
        "route_tables", "key_pairs", "peering_connections",
    )
    for region in expected_regions:
        record = regions[region]
        if record.get("query_succeeded") is not True:
            raise ValueError(f"{path}: cleanup query failed in {region}")
        for category in categories:
            if category not in record or record[category] != []:
                raise ValueError(f"{path}: {region} {category} is missing or non-empty")
    iam = payload.get("iam", {})
    if iam.get("query_succeeded") is not True or iam.get("roles") != [] or iam.get("instance_profiles") != []:
        raise ValueError(f"{path}: IAM cleanup is incomplete")
    if payload.get("terraform_state") != []:
        raise ValueError(f"{path}: Terraform state is not empty")


def command_main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    write = sub.add_parser("write-json")
    write.add_argument("--output", required=True, type=Path)
    write.add_argument("--input", type=Path)
    check = sub.add_parser("assert-evaluator")
    check.add_argument("--csv", required=True, type=Path)
    check.add_argument("--expected", action="append", default=[])
    check.add_argument("--require-success", action="store_true")
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
    final_phase = sub.add_parser("assert-final-phase")
    final_phase.add_argument("--phase-root", required=True, type=Path)
    final_phase.add_argument("--expected-topology", required=True)
    final_phase.add_argument("--expected-phase", required=True)
    final_resources = sub.add_parser("finalize-final-resources")
    final_resources.add_argument("--phase-root", required=True, type=Path)
    final_cleanup = sub.add_parser("assert-final-cleanup")
    final_cleanup.add_argument("--cleanup", required=True, type=Path)
    final_cleanup.add_argument("--region", action="append", required=True)
    args = parser.parse_args()

    if args.command == "write-json":
        value = json.loads(args.input.read_text(encoding="utf-8-sig") if args.input else sys.stdin.read())
        write_json(args.output, value)
    elif args.command == "assert-evaluator":
        evaluator_assert(args.csv, args.expected, args.require_success)
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
    elif args.command == "assert-final-phase":
        assert_final_phase(args.phase_root, args.expected_topology, args.expected_phase)
    elif args.command == "finalize-final-resources":
        finalize_final_resource_artifacts(args.phase_root)
    elif args.command == "assert-final-cleanup":
        assert_final_cleanup(args.cleanup, args.region)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(command_main())
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
