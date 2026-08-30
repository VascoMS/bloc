"""Load matched ACS diagnostics and write lean, overlap-aware summaries."""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

import pandas as pd


TRACE_SCHEMA = "bloc-acs-trace/v1"
MATCH_FIELDS = (
    "phase", "node_count", "source_sha", "bloc_image", "mempool_image",
    "bundle_version", "public_config_id", "encrypted_corpus_id", "batches",
    "seed", "deadline", "warmups", "repetitions", "blocks", "sampler",
    "acs_trace_schema",
)


@dataclass(frozen=True)
class DiagnosticRoot:
    root: Path
    manifest: dict[str, Any]
    runs: pd.DataFrame
    nodes: pd.DataFrame
    traces: list[dict[str, Any]]


@dataclass(frozen=True)
class MatchedDiagnostics:
    same_az: DiagnosticRoot
    three_region: DiagnosticRoot


def load_diagnostic_root(root: Path) -> DiagnosticRoot:
    root = Path(root)
    manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8-sig"))
    if manifest.get("status") != "complete" or manifest.get("phase") != "latency":
        raise ValueError("matched diagnostic contract requires a complete latency phase")
    if manifest.get("acs_trace_schema") != TRACE_SCHEMA:
        raise ValueError("matched diagnostic contract requires bloc-acs-trace/v1")
    run_frames, node_frames, traces = [], [], []
    for run_path in sorted((root / "scenarios").glob("**/run_measurements.csv")):
        scenario = str(run_path.parent.relative_to(root))
        scenario_manifest = json.loads((run_path.parent / "manifest.json").read_text(encoding="utf-8-sig"))
        if scenario_manifest.get("acs_trace_schema") != TRACE_SCHEMA:
            raise ValueError("matched diagnostic contract has an evaluator schema mismatch")
        runs = pd.read_csv(run_path)
        nodes = pd.read_csv(run_path.parent / "node_measurements.csv")
        runs["scenario_root"] = scenario
        nodes["scenario_root"] = scenario
        run_frames.append(runs)
        node_frames.append(nodes)
        with (run_path.parent / "acs_trace.jsonl").open(encoding="utf-8-sig") as handle:
            for line in handle:
                if line.strip():
                    record = json.loads(line)
                    record["scenario_root"] = scenario
                    traces.append(record)
    if not run_frames or not traces:
        raise ValueError("matched diagnostic contract has no trace evidence")
    return DiagnosticRoot(
        root=root,
        manifest=manifest,
        runs=pd.concat(run_frames, ignore_index=True),
        nodes=pd.concat(node_frames, ignore_index=True),
        traces=traces,
    )


def load_matched_diagnostics(same_az: Path, three_region: Path) -> MatchedDiagnostics:
    left = load_diagnostic_root(same_az)
    right = load_diagnostic_root(three_region)
    if left.manifest.get("topology") != "same-az" or right.manifest.get("topology") != "three-region":
        raise ValueError("matched diagnostic contract requires same-AZ and three-region roots")
    if any(left.manifest.get(field) != right.manifest.get(field) for field in MATCH_FIELDS):
        raise ValueError("matched diagnostic contract differs across roots")
    return MatchedDiagnostics(left, right)


def _quantile_rows(
    rows: Iterable[dict[str, Any]],
    keys: list[str],
    *,
    value_column: str = "value_us",
    output_suffix: str = "_us",
) -> pd.DataFrame:
    frame = pd.DataFrame(rows)
    output = []
    for group_key, group in frame.groupby(keys, sort=True):
        values = group[value_column].astype(float)
        if not isinstance(group_key, tuple):
            group_key = (group_key,)
        output.append({
            **dict(zip(keys, group_key, strict=True)), "count": len(values),
            f"p50{output_suffix}": values.quantile(0.50, interpolation="linear"),
            f"p95{output_suffix}": values.quantile(0.95, interpolation="linear"),
            f"max{output_suffix}": values.max(),
        })
    return pd.DataFrame(output)


def _campaign_records(campaign: DiagnosticRoot, topology: str):
    run_lookup = {}
    for row in campaign.runs.itertuples(index=False):
        run_lookup[(row.scenario_root, int(row.measurement_block), str(row.run_id), int(row.slot))] = row
    for trace in campaign.traces:
        key = trace["key"]
        identity = (trace["scenario_root"], int(key["measurement_block"]), str(key["run_id"]), int(key["slot"]))
        run = run_lookup[identity]
        if str(run.phase) == "measured" and _true(run.success) and _true(run.consistent):
            yield topology, run, trace


def _true(value: Any) -> bool:
    return value is True or str(value).strip().lower() == "true"


def milestone_summary(matched: MatchedDiagnostics) -> pd.DataFrame:
    rows = []
    names = (
        "input_started", "first_rbc_output", "rbc_output_quorum", "first_true_bba",
        "true_bba_quorum", "all_bba_decided", "truthy_rbc_ready", "core_decision",
    )
    adapter = ("common_subset_decoded", "block_body_built", "node_output_received")
    for campaign, topology in ((matched.same_az, "same-az"), (matched.three_region, "three-region")):
        for _, run, trace in _campaign_records(campaign, topology):
            for name in names:
                point = trace["aggregate"][name]
                if point["recorded"]:
                    rows.append({"topology": topology, "nodes": int(run.nodes), "batch_size": int(run.batch_size),
                                 "milestone": name, "value_us": int(point["offset_us"])})
            for name in adapter:
                point = trace["adapter"][name]
                rows.append({"topology": topology, "nodes": int(run.nodes), "batch_size": int(run.batch_size),
                             "milestone": name, "value_us": int(point["offset_us"])})
    return _quantile_rows(rows, ["topology", "nodes", "batch_size", "milestone"])


def wait_summary(matched: MatchedDiagnostics) -> pd.DataFrame:
    rows = []
    for campaign, topology in ((matched.same_az, "same-az"), (matched.three_region, "three-region")):
        for _, run, trace in _campaign_records(campaign, topology):
            for wait, value in trace["wait_us"].items():
                rows.append({"topology": topology, "nodes": int(run.nodes), "batch_size": int(run.batch_size),
                             "wait": wait, "value_us": int(value)})
    return _quantile_rows(rows, ["topology", "nodes", "batch_size", "wait"])


def message_summary(matched: MatchedDiagnostics) -> pd.DataFrame:
    rows = []
    units = {
        "inbound_count": "count", "outbound_count": "count", "send_count": "count",
        "send_failure_count": "count", "inbound_bytes": "bytes", "outbound_bytes": "bytes",
        "send_total_us": "microseconds", "send_max_us": "microseconds",
    }
    for campaign, topology in ((matched.same_az, "same-az"), (matched.three_region, "three-region")):
        for _, run, trace in _campaign_records(campaign, topology):
            for message in trace["messages"]:
                for metric, value in message["trace"].items():
                    rows.append({"topology": topology, "nodes": int(run.nodes), "batch_size": int(run.batch_size),
                                 "subtype": message["subtype"], "metric": metric, "unit": units[metric],
                                 "value": int(value)})
    return _quantile_rows(
        rows,
        ["topology", "nodes", "batch_size", "subtype", "metric", "unit"],
        value_column="value",
        output_suffix="",
    )


def critical_node_summary(matched: MatchedDiagnostics) -> pd.DataFrame:
    output = []
    for campaign, topology in ((matched.same_az, "same-az"), (matched.three_region, "three-region")):
        successful = {}
        for run in campaign.runs.itertuples(index=False):
            key = (run.scenario_root, int(run.measurement_block), str(run.run_id))
            if str(run.phase) == "measured" and _true(run.success) and _true(run.consistent):
                successful[key] = run
        measured = campaign.nodes[(campaign.nodes["phase"] == "measured")].copy()
        for keys, group in measured.groupby(["scenario_root", "measurement_block", "run_id"], sort=True):
            if keys not in successful:
                continue
            total = group.sort_values(["total_slot_us", "node_id"], ascending=[False, True]).iloc[0]
            acs = group.sort_values(["acs_us", "node_id"], ascending=[False, True]).iloc[0]
            run = successful[keys]
            output.append({
                "topology": topology, "nodes": int(campaign.manifest["node_count"]),
                "batch_size": int(run.batch_size),
                "measurement_block": int(keys[1]), "run_id": keys[2],
                "slowest_total_node_id": int(total["node_id"]), "slowest_total_us": int(total["total_slot_us"]),
                "slowest_acs_node_id": int(acs["node_id"]), "slowest_acs_us": int(acs["acs_us"]),
            })
    return pd.DataFrame(output)


def write_core_summaries(matched: MatchedDiagnostics, output: Path) -> Path:
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    milestone_summary(matched).to_csv(output / "acs-milestone-summary.csv", index=False)
    wait_summary(matched).to_csv(output / "acs-wait-summary.csv", index=False)
    message_summary(matched).to_csv(output / "acs-message-summary.csv", index=False)
    critical_node_summary(matched).to_csv(output / "acs-critical-node-summary.csv", index=False)
    return output


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("same_az", type=Path)
    parser.add_argument("three_region", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args(argv)
    print(write_core_summaries(load_matched_diagnostics(args.same_az, args.three_region), args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
