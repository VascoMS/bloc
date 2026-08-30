"""Compare matched fresh and persistent libp2p stream campaigns."""

from __future__ import annotations

import argparse
import json
from collections import Counter
from pathlib import Path
from typing import Any, Iterable

import pandas as pd

from .statistics import QuantileEstimate, estimate_quantile


TRACE_SCHEMA = "bloc-acs-trace/v2"
MODES = ("fresh", "persistent")
EXPECTED_SUCCESSFUL_MEASUREMENTS = 30
SUBTYPES = ("proof", "echo", "ready", "bval", "aux")
PHASES = ("encode", "queue_wait", "stream_open", "write", "finalize")
MILESTONE_COLUMNS = (
    "acs_input_started_us",
    "acs_first_rbc_output_us",
    "acs_rbc_output_quorum_us",
    "acs_first_true_bba_us",
    "acs_true_bba_quorum_us",
    "acs_all_bba_decided_us",
    "acs_truthy_rbc_ready_us",
    "acs_core_decision_us",
    "acs_common_subset_decoded_us",
    "acs_block_body_built_us",
    "acs_node_output_received_us",
)
ALLOWED_MANIFEST_DIFFERENCES = {
    "stream_mode",
    "command",
    "started_at",
    "finished_at",
    "experiment_id",
    "config_sha256",
    "config_file_sha256",
    "cluster_config_sha256",
}


def _true(value: Any) -> bool:
    return value is True or str(value).strip().lower() == "true"


def _read_manifest(root: Path, expected_mode: str) -> dict[str, Any]:
    manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8-sig"))
    if manifest.get("status") != "complete" or not _true(manifest.get("valid")):
        raise ValueError("matched transport campaigns require complete, valid manifests")
    if manifest.get("acs_trace_schema") != TRACE_SCHEMA:
        raise ValueError(f"matched transport campaigns require {TRACE_SCHEMA}")
    if manifest.get("stream_mode") != expected_mode:
        raise ValueError(
            f"matched transport campaigns require mode {expected_mode!r}, got {manifest.get('stream_mode')!r}"
        )
    return manifest


def _comparable_manifest(manifest: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in manifest.items() if key not in ALLOWED_MANIFEST_DIFFERENCES}


def _require_columns(frame: pd.DataFrame, columns: Iterable[str], artifact: str) -> None:
    missing = sorted(set(columns) - set(frame.columns))
    if missing:
        raise ValueError(f"matched transport campaigns {artifact} is missing columns: {', '.join(missing)}")


def _successful_measured(runs: pd.DataFrame) -> pd.Series:
    return (
        runs["phase"].astype(str).eq("measured")
        & runs["success"].map(_true)
        & runs["consistent"].map(_true)
        & ~runs["timed_out"].map(_true)
    )


def _load_campaign(root: Path, mode: str) -> tuple[dict[str, Any], pd.DataFrame, pd.DataFrame]:
    root = Path(root)
    manifest = _read_manifest(root, mode)
    runs = pd.read_csv(root / "run_measurements.csv")
    nodes = pd.read_csv(root / "node_measurements.csv")
    required_run_columns = (
        "run_id", "scenario_id", "phase", "iteration", "order_index",
        "measurement_block", "block_iteration", "schedule_seed",
        "planned_scenario_runs", "slot", "nodes", "threshold", "batch_size",
        "network", "stream_mode", "success", "consistent", "outcome",
        "deadline_met", "timed_out", "acs_us",
    )
    _require_columns(runs, required_run_columns, "run measurements")
    _require_columns(
        nodes,
        ("run_id", "measurement_block", "stream_mode", "critical_node", "acs_trace_schema", *MILESTONE_COLUMNS),
        "node measurements",
    )
    if set(runs["stream_mode"].dropna().astype(str)) != {mode}:
        raise ValueError("matched transport campaigns have run stream-mode drift")
    if set(nodes["stream_mode"].dropna().astype(str)) != {mode}:
        raise ValueError("matched transport campaigns have node stream-mode drift")
    if set(nodes["acs_trace_schema"].dropna().astype(str)) != {TRACE_SCHEMA}:
        raise ValueError("matched transport campaigns have node trace-schema drift")

    topology = str(manifest.get("topology", "local"))
    runs = runs.copy()
    runs["topology"] = topology
    runs["campaign_root"] = str(root.resolve())
    measured = runs[runs["phase"].astype(str).eq("measured")]
    for (batch_size,), cell in measured.groupby(["batch_size"], sort=True):
        successful = cell[_successful_measured(cell)]
        if len(successful) != EXPECTED_SUCCESSFUL_MEASUREMENTS:
            raise ValueError(
                f"matched transport campaigns require 30 successful measurements per cell; "
                f"mode={mode} batch={batch_size} has {len(successful)}"
            )
        if len(cell) != EXPECTED_SUCCESSFUL_MEASUREMENTS:
            raise ValueError(
                f"matched transport campaigns require an exact 30-attempt measured schedule; "
                f"mode={mode} batch={batch_size} has {len(cell)}"
            )

    critical = nodes[nodes["critical_node"].map(_true)].copy()
    critical_key = ["measurement_block", "run_id"]
    if critical.duplicated(critical_key).any():
        raise ValueError("matched transport campaigns have duplicate critical-node rows")
    runs = runs.merge(
        critical[[*critical_key, *MILESTONE_COLUMNS]],
        on=critical_key,
        how="left",
        validate="one_to_one",
    )
    successful_runs = runs[_successful_measured(runs)]
    if successful_runs[list(MILESTONE_COLUMNS)].isna().any().any():
        raise ValueError("matched transport campaigns have missing critical-node ACS milestones")

    run_lookup = {
        (int(row.measurement_block), str(row.run_id), int(row.slot)): row
        for row in runs.itertuples(index=False)
    }
    message_rows: list[dict[str, Any]] = []
    with (root / "acs_trace.jsonl").open(encoding="utf-8-sig") as handle:
        for line in handle:
            if not line.strip():
                continue
            record = json.loads(line)
            if record.get("schema_version") != TRACE_SCHEMA or not _true(record.get("enabled")):
                raise ValueError("matched transport campaigns require enabled v2 trace records")
            key = record["key"]
            identity = (int(key["measurement_block"]), str(key["run_id"]), int(key["slot"]))
            run = run_lookup.get(identity)
            if run is None:
                raise ValueError("matched transport campaigns contain an unmatched trace record")
            messages = record.get("messages", [])
            if {message.get("subtype") for message in messages} != set(SUBTYPES):
                raise ValueError("matched transport campaigns require all five fixed ACS message subtypes")
            if not (
                str(run.phase) == "measured"
                and _true(run.success)
                and _true(run.consistent)
                and not _true(run.timed_out)
            ):
                continue
            for message in messages:
                trace = message["trace"]
                if int(trace.get("send_failure_count", 0)) != 0:
                    raise ValueError("matched transport campaigns contain an unexpected send failure")
                send_count = int(trace["send_count"])
                row: dict[str, Any] = {
                    "topology": topology,
                    "batch_size": int(run.batch_size),
                    "nodes": int(run.nodes),
                    "threshold": int(run.threshold),
                    "stream_mode": mode,
                    "scenario_id": str(run.scenario_id),
                    "measurement_block": int(run.measurement_block),
                    "run_id": str(run.run_id),
                    "slot": int(run.slot),
                    "node_id": int(key["node_id"]),
                    "subtype": str(message["subtype"]),
                    "send_count": send_count,
                    "send_total_us": int(trace["send_total_us"]),
                    "send_max_us": int(trace["send_max_us"]),
                    "stream_open_count": int(trace["stream_open_count"]),
                    "stream_reuse_count": int(trace["stream_reuse_count"]),
                }
                if row["stream_open_count"] + row["stream_reuse_count"] != send_count:
                    raise ValueError("matched transport campaigns contain inconsistent open/reuse counts")
                for phase in PHASES:
                    phase_trace = trace[phase]
                    if int(phase_trace["count"]) != send_count:
                        raise ValueError("matched transport campaigns contain inconsistent phase counts")
                    row[f"{phase}_total_us"] = int(phase_trace["total_us"])
                    row[f"{phase}_max_us"] = int(phase_trace["max_us"])
                message_rows.append(row)
    if not message_rows:
        raise ValueError("matched transport campaigns contain no successful v2 message traces")
    return manifest, runs, pd.DataFrame(message_rows)


def _schedule_identity(runs: pd.DataFrame) -> list[tuple[Any, ...]]:
    columns = [
        "scenario_id", "phase", "iteration", "order_index", "measurement_block",
        "block_iteration", "schedule_seed", "planned_scenario_runs", "slot",
        "nodes", "threshold", "batch_size", "network",
    ]
    return sorted(map(tuple, runs[columns].to_numpy()))


def load_matched_transport_campaigns(
    fresh_root: Path, persistent_root: Path
) -> tuple[pd.DataFrame, pd.DataFrame]:
    fresh_manifest, fresh_runs, fresh_messages = _load_campaign(Path(fresh_root), "fresh")
    persistent_manifest, persistent_runs, persistent_messages = _load_campaign(Path(persistent_root), "persistent")
    if _comparable_manifest(fresh_manifest) != _comparable_manifest(persistent_manifest):
        raise ValueError("matched transport campaigns differ in retained provenance")
    if _schedule_identity(fresh_runs) != _schedule_identity(persistent_runs):
        raise ValueError("matched transport campaigns differ in retained run schedule")
    runs = pd.concat([fresh_runs, persistent_runs], ignore_index=True)
    messages = pd.concat([fresh_messages, persistent_messages], ignore_index=True)
    return runs, messages


def _estimate_columns(values: Iterable[float]) -> dict[str, float | int]:
    series = pd.Series(list(values), dtype="float64")
    p50 = estimate_quantile(series, 0.50)
    p95 = estimate_quantile(series, 0.95)
    return {
        "count": len(series),
        "p50_us": _required_estimate(p50.value),
        "p50_lower_us": _required_estimate(p50.lower),
        "p50_upper_us": _required_estimate(p50.upper),
        "p95_us": _required_estimate(p95.value),
        "p95_lower_us": _required_estimate(p95.lower),
        "p95_upper_us": _required_estimate(p95.upper),
        "max_us": float(series.max()),
    }


def _required_estimate(value: float | None) -> float:
    if value is None:
        raise ValueError("matched transport campaigns lack an eligible p50/p95 estimate")
    return float(value)


def summarize_transport_attribution(
    runs: pd.DataFrame, messages: pd.DataFrame
) -> tuple[pd.DataFrame, pd.DataFrame]:
    successful = runs[_successful_measured(runs)].copy()
    acs_rows: list[dict[str, Any]] = []
    metrics = ("acs_us", *MILESTONE_COLUMNS)
    for keys, group in successful.groupby(["topology", "batch_size", "stream_mode"], sort=True):
        for metric in metrics:
            acs_rows.append({
                "topology": keys[0],
                "batch_size": int(keys[1]),
                "stream_mode": keys[2],
                "metric": metric.removeprefix("acs_").removesuffix("_us"),
                **_estimate_columns(group[metric]),
            })
    phase_rows: list[dict[str, Any]] = []
    for keys, group in messages.groupby(["topology", "batch_size", "stream_mode", "subtype"], sort=True):
        row: dict[str, Any] = {
            "topology": keys[0],
            "batch_size": int(keys[1]),
            "stream_mode": keys[2],
            "subtype": keys[3],
            "count": len(group),
            "stream_open_count": int(group["stream_open_count"].sum()),
            "stream_reuse_count": int(group["stream_reuse_count"].sum()),
        }
        for output_name, column in (("send", "send_total_us"), *( (phase, f"{phase}_total_us") for phase in PHASES )):
            estimates = _estimate_columns(group[column])
            for name, value in estimates.items():
                if name == "count":
                    continue
                row[f"{output_name}_{name}"] = value
        phase_rows.append(row)
    return pd.DataFrame(acs_rows), pd.DataFrame(phase_rows)


def _direction(fresh: QuantileEstimate, persistent: QuantileEstimate) -> str:
    if fresh.lower is None or fresh.upper is None or persistent.lower is None or persistent.upper is None:
        raise ValueError("matched transport campaigns lack confidence intervals")
    if persistent.upper < fresh.lower:
        return "persistent-better"
    if persistent.lower > fresh.upper:
        return "persistent-worse"
    return "overlap"


def classify_transport_cell(values: dict[str, Any]) -> str:
    if (
        values["finalize_direction"] == "persistent-better"
        and values["acs_p50_direction"] == "overlap"
        and values["acs_p95_direction"] != "persistent-better"
    ):
        return "sender-finalization-only"
    if values["persistent_queue_wait_p50_us"] > 0 and values["acs_p50_direction"] == "persistent-worse":
        return "queue-regression"
    if (
        values["acs_p50_direction"] == "persistent-better"
        and values["persistent_acs_p95"] < values["fresh_acs_p95"]
        and values.get("new_failures", 0) == 0
        and values.get("consistency_errors", 0) == 0
    ):
        return "acs-signal"
    return "null-or-mixed"


def _cell_report(runs: pd.DataFrame, messages: pd.DataFrame) -> list[dict[str, Any]]:
    successful = runs[_successful_measured(runs)]
    cells: list[dict[str, Any]] = []
    for (topology, batch_size), cell_runs in successful.groupby(["topology", "batch_size"], sort=True):
        by_mode = {mode: cell_runs[cell_runs["stream_mode"] == mode] for mode in MODES}
        acs = {
            mode: {
                "p50": estimate_quantile(by_mode[mode]["acs_us"], 0.50),
                "p95": estimate_quantile(by_mode[mode]["acs_us"], 0.95),
            }
            for mode in MODES
        }
        cell_messages = messages[
            (messages["topology"] == topology) & (messages["batch_size"] == batch_size)
        ]
        identity = ["stream_mode", "measurement_block", "run_id", "slot", "node_id"]
        per_trace = cell_messages.groupby(identity, sort=True)[
            ["queue_wait_total_us", "finalize_total_us"]
        ].sum().reset_index()
        phase_values = {
            mode: per_trace[per_trace["stream_mode"] == mode]
            for mode in MODES
        }
        finalize = {
            mode: estimate_quantile(phase_values[mode]["finalize_total_us"], 0.50)
            for mode in MODES
        }
        queue_p50 = estimate_quantile(phase_values["persistent"]["queue_wait_total_us"], 0.50)
        measured_cell = runs[(runs["topology"] == topology) & (runs["batch_size"] == batch_size) & (runs["phase"] == "measured")]
        failures = {
            mode: int((~measured_cell[measured_cell["stream_mode"] == mode]["success"].map(_true)).sum())
            for mode in MODES
        }
        consistency = {
            mode: int((~measured_cell[measured_cell["stream_mode"] == mode]["consistent"].map(_true)).sum())
            for mode in MODES
        }
        values = {
            "acs_p50_direction": _direction(acs["fresh"]["p50"], acs["persistent"]["p50"]),
            "acs_p95_direction": _direction(acs["fresh"]["p95"], acs["persistent"]["p95"]),
            "finalize_direction": _direction(finalize["fresh"], finalize["persistent"]),
            "fresh_acs_p50": acs["fresh"]["p50"].value,
            "persistent_acs_p50": acs["persistent"]["p50"].value,
            "fresh_acs_p95": acs["fresh"]["p95"].value,
            "persistent_acs_p95": acs["persistent"]["p95"].value,
            "persistent_queue_wait_p50_us": _required_estimate(queue_p50.value),
            "new_failures": max(0, failures["persistent"] - failures["fresh"]),
            "consistency_errors": consistency["persistent"],
        }
        cells.append({
            "topology": topology,
            "batch_size": int(batch_size),
            "classification": classify_transport_cell(values),
            "values": values,
            "intervals": {
                f"{mode}_{quantile}": {
                    "lower_us": estimate.lower,
                    "upper_us": estimate.upper,
                }
                for mode in MODES
                for quantile, estimate in acs[mode].items()
            },
        })
    return cells


def _cross_batch_summary(cells: list[dict[str, Any]]) -> list[dict[str, Any]]:
    output: list[dict[str, Any]] = []
    opposites = {"acs-signal": "queue-regression", "queue-regression": "acs-signal"}
    for topology in sorted({cell["topology"] for cell in cells}):
        topology_cells = [cell for cell in cells if cell["topology"] == topology]
        if len(topology_cells) != 3:
            output.append({"topology": topology, "stable": False, "reason": "requires exactly three batches"})
            continue
        counts = Counter(cell["classification"] for cell in topology_cells)
        candidates = [name for name, count in counts.items() if count >= 2]
        if not candidates:
            output.append({"topology": topology, "stable": False, "reason": "fewer than two batches agree"})
            continue
        classification = sorted(candidates)[0]
        opposite = opposites.get(classification)
        stable = opposite is None or counts.get(opposite, 0) == 0
        output.append({
            "topology": topology,
            "stable": stable,
            "classification": classification if stable else "null-or-mixed",
            "batch_classifications": {str(cell["batch_size"]): cell["classification"] for cell in topology_cells},
        })
    return output


def write_transport_attribution(fresh_root: Path, persistent_root: Path, output: Path) -> Path:
    runs, messages = load_matched_transport_campaigns(fresh_root, persistent_root)
    acs, phases = summarize_transport_attribution(runs, messages)
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    acs.to_csv(output / "transport-acs-summary.csv", index=False)
    phases.to_csv(output / "transport-phase-summary.csv", index=False)
    cells = _cell_report(runs, messages)
    report = {
        "trace_schema": TRACE_SCHEMA,
        "modes": list(MODES),
        "provenance_checks": {"matched": True, "successful_measurements_per_cell": EXPECTED_SUCCESSFUL_MEASUREMENTS},
        "causality_note": (
            "These classifications are experiment outcomes, not proof of a transport root cause; "
            "sender completion is not receiver acknowledgement or ACS progress."
        ),
        "cells": cells,
        "cross_batch": _cross_batch_summary(cells),
    }
    (output / "transport-attribution.json").write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    return output


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("fresh", type=Path)
    parser.add_argument("persistent", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args(argv)
    print(write_transport_attribution(args.fresh, args.persistent, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
