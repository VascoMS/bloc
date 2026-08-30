from __future__ import annotations

import csv
import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.acs_attribution import load_matched_diagnostics, write_core_summaries


TRACE_SCHEMA = "bloc-acs-trace/v1"


def _write_csv(path: Path, rows: list[dict]) -> None:
    with path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def _write_diagnostic(root: Path, topology: str, scale: int) -> None:
    root.mkdir()
    manifest = {
        "schema_version": "bloc-final-campaign-phase-v1", "status": "complete",
        "topology": topology, "phase": "latency", "node_count": 4,
        "source_sha": "c" * 40, "bloc_image": "bloc@sha256:" + "a" * 64,
        "mempool_image": "mempool@sha256:" + "b" * 64,
        "bundle_version": "bloc-campaign-bundle-v1", "public_config_id": "public",
        "encrypted_corpus_id": "corpus", "batches": [8], "seed": 20260621,
        "deadline": "12s", "warmups": 5, "repetitions": 30, "blocks": 3,
        "sampler": "off", "acs_trace_schema": TRACE_SCHEMA,
    }
    (root / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    scenario = root / "scenarios" / "controller" / "results" / "cell"
    scenario.mkdir(parents=True)
    (scenario / "manifest.json").write_text(json.dumps({"acs_trace_schema": TRACE_SCHEMA}), encoding="utf-8")
    runs, nodes, traces = [], [], []
    for run_index in range(4):
        run_id, slot = f"run-{run_index}", run_index + 1
        runs.append({
            "run_id": run_id, "phase": "measured", "measurement_block": 1,
            "slot": slot, "nodes": 4, "batch_size": 8, "success": "true",
            "consistent": "true", "outcome": "completed",
        })
        for node_id in range(4):
            core = scale * (10 + run_index * 10 + node_id)
            node = {
                "run_id": run_id, "phase": "measured", "measurement_block": 1,
                "node_id": node_id, "total_slot_us": 100 + node_id * 10,
                "acs_us": 50 + (20 if node_id == 2 else node_id),
                "acs_trace_schema": TRACE_SCHEMA,
            }
            nodes.append(node)
            point = lambda value: {"recorded": True, "offset_us": value}
            traces.append({
                "key": {"measurement_block": 1, "run_id": run_id, "node_id": node_id, "slot": slot},
                "schema_version": TRACE_SCHEMA, "enabled": True,
                "aggregate": {
                    "input_started": point(0), "first_rbc_output": point(core - 6),
                    "rbc_output_quorum": point(core - 5), "first_true_bba": point(core - 4),
                    "true_bba_quorum": point(core - 3), "false_input_injected": {"recorded": False, "offset_us": 0},
                    "all_bba_decided": point(core - 2), "truthy_rbc_ready": point(core - 1),
                    "core_decision": point(core),
                },
                "wait_us": {"true_bba_quorum_us": core - 3, "all_bba_us": 1, "truthy_rbc_us": 2},
                "adapter": {"common_subset_decoded": point(core + 1), "block_body_built": point(core + 2),
                            "node_output_received": point(core + 3)},
                "rbc": [{"proposer_id": proposer, "trace": {}} for proposer in range(4)],
                "bba": [{"proposer_id": proposer, "trace": {"max_epoch": proposer}} for proposer in range(4)],
                "messages": [{"subtype": subtype, "trace": {
                    "inbound_count": 1, "inbound_bytes": 10, "outbound_count": 2,
                    "outbound_bytes": 20, "send_count": 2, "send_total_us": 4,
                    "send_max_us": 2, "send_failure_count": 0,
                }} for subtype in ("proof", "echo", "ready", "bval", "aux")],
            })
    _write_csv(scenario / "run_measurements.csv", runs)
    _write_csv(scenario / "node_measurements.csv", nodes)
    with (scenario / "acs_trace.jsonl").open("w", encoding="utf-8") as handle:
        for trace in traces:
            handle.write(json.dumps(trace) + "\n")


def test_load_matched_diagnostics_accepts_matching_roots_and_rejects_contract_drift(tmp_path: Path) -> None:
    same, remote = tmp_path / "same", tmp_path / "remote"
    _write_diagnostic(same, "same-az", 1)
    _write_diagnostic(remote, "three-region", 2)
    matched = load_matched_diagnostics(same, remote)
    assert len(matched.same_az.traces) == len(matched.three_region.traces) == 16

    path = remote / "manifest.json"
    original = json.loads(path.read_text())
    for field, value in (
        ("source_sha", "d" * 40), ("bloc_image", "different"),
        ("encrypted_corpus_id", "different"), ("public_config_id", "different"),
        ("acs_trace_schema", "bloc-acs-trace/v999"), ("seed", 7),
    ):
        changed = dict(original); changed[field] = value
        path.write_text(json.dumps(changed), encoding="utf-8")
        with pytest.raises(ValueError, match="matched diagnostic contract"):
            load_matched_diagnostics(same, remote)
    path.write_text(json.dumps(original), encoding="utf-8")


def test_write_core_summaries_uses_type7_and_separate_critical_nodes(tmp_path: Path) -> None:
    same, remote = tmp_path / "same", tmp_path / "remote"
    _write_diagnostic(same, "same-az", 1)
    _write_diagnostic(remote, "three-region", 2)
    output = write_core_summaries(load_matched_diagnostics(same, remote), tmp_path / "out")

    milestones = pd.read_csv(output / "acs-milestone-summary.csv")
    core = milestones[(milestones.topology == "same-az") & (milestones.milestone == "core_decision")].iloc[0]
    assert (core["count"], core["p50_us"], core["p95_us"], core["max_us"]) == (16, 26.5, 42.25, 43)
    assert "p99_us" not in milestones.columns
    critical = pd.read_csv(output / "acs-critical-node-summary.csv")
    assert set(critical["slowest_total_node_id"]) == {3}
    assert set(critical["slowest_acs_node_id"]) == {2}
    assert (output / "acs-wait-summary.csv").stat().st_size > 0
    messages = pd.read_csv(output / "acs-message-summary.csv")
    assert {"metric", "unit", "p50", "p95", "max"} <= set(messages.columns)
    assert "p50_us" not in messages.columns
    assert set(messages["unit"]) == {"count", "bytes", "microseconds"}


def test_core_summaries_exclude_failed_attempts_without_dropping_trace_records(tmp_path: Path) -> None:
    same, remote = tmp_path / "same", tmp_path / "remote"
    _write_diagnostic(same, "same-az", 1)
    _write_diagnostic(remote, "three-region", 2)
    run_path = next(same.glob("scenarios/**/run_measurements.csv"))
    runs = pd.read_csv(run_path)
    runs.loc[runs.run_id == "run-0", ["success", "consistent", "outcome"]] = [False, False, "failed"]
    runs.to_csv(run_path, index=False)

    matched = load_matched_diagnostics(same, remote)
    assert len(matched.same_az.traces) == 16
    milestones = write_core_summaries(matched, tmp_path / "out") / "acs-milestone-summary.csv"
    rows = pd.read_csv(milestones)
    core = rows[(rows.topology == "same-az") & (rows.milestone == "core_decision")].iloc[0]
    assert core["count"] == 12
    critical = pd.read_csv(tmp_path / "out" / "acs-critical-node-summary.csv")
    assert len(critical[critical.topology == "same-az"]) == 3
