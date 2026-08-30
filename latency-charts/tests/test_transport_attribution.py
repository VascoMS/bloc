from __future__ import annotations

import csv
import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.transport_attribution import (
    TRACE_SCHEMA,
    classify_transport_cell,
    load_matched_transport_campaigns,
    summarize_transport_attribution,
    write_transport_attribution,
)


def _write_csv(path: Path, rows: list[dict]) -> None:
    with path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def _write_campaign(
    root: Path,
    mode: str,
    *,
    repetitions: int = 30,
    batches: tuple[int, ...] = (8,),
    acs_base: int = 100,
    queue_wait: int = 0,
) -> None:
    root.mkdir()
    scenarios = [
        {"id": f"n4-b{batch}-libp2p", "nodes": 4, "threshold": 3, "batch_size": batch, "network": "libp2p"}
        for batch in batches
    ]
    manifest = {
        "schema_version": "bloc-eval-suite/v3",
        "acs_trace_schema": TRACE_SCHEMA,
        "stream_mode": mode,
        "experiment_id": f"phase1-{mode}",
        "status": "complete",
        "valid": True,
        "started_at": "2026-08-30T10:00:00Z",
        "finished_at": "2026-08-30T11:00:00Z",
        "command": ["eval-suite", "--stream-mode", mode, "--out-dir", str(root)],
        "source_sha": "c" * 40,
        "image_digest": "sha256:" + "a" * 64,
        "nodes": 4,
        "threshold": 3,
        "batch_sizes": list(batches),
        "corpus_identity": "corpus-1",
        "seed": 640,
        "warmups": 5,
        "repetitions": repetitions,
        "repetition_blocks": 3,
        "schedule": "sequential-by-node-count-seeded-batch-interleave",
        "topology": "local",
        "bmax": 128,
        "tx_size": 256,
        "tx_gas": 21000,
        "scenarios": scenarios,
        "config_sha256": ("f" if mode == "fresh" else "e") * 64,
    }
    (root / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    runs: list[dict] = []
    nodes: list[dict] = []
    traces: list[dict] = []
    order_index = 0
    for batch in batches:
        scenario_id = f"n4-b{batch}-libp2p"
        for iteration in range(1, repetitions + 1):
            order_index += 1
            run_id = f"measured-{batch}-{iteration:03d}"
            slot = order_index
            acs_us = acs_base + batch + iteration
            common = {
                "run_id": run_id,
                "scenario_id": scenario_id,
                "phase": "measured",
                "iteration": iteration,
                "order_index": order_index,
                "measurement_block": ((iteration - 1) // 10) + 1,
                "block_iteration": ((iteration - 1) % 10) + 1,
                "schedule_seed": 640,
                "planned_scenario_runs": repetitions,
                "slot": slot,
                "nodes": 4,
                "threshold": 3,
                "batch_size": batch,
                "network": "libp2p",
                "stream_mode": mode,
                "success": True,
                "consistent": True,
                "outcome": "completed",
                "deadline_met": True,
                "timed_out": False,
                "acs_us": acs_us,
            }
            runs.append(common)
            nodes.append({
                **common,
                "node_id": 0,
                "critical_node": True,
                "acs_trace_schema": TRACE_SCHEMA,
                "acs_input_started_us": 0,
                "acs_first_rbc_output_us": acs_us - 50,
                "acs_rbc_output_quorum_us": acs_us - 40,
                "acs_first_true_bba_us": acs_us - 30,
                "acs_true_bba_quorum_us": acs_us - 20,
                "acs_all_bba_decided_us": acs_us - 10,
                "acs_truthy_rbc_ready_us": acs_us - 5,
                "acs_core_decision_us": acs_us - 3,
                "acs_common_subset_decoded_us": acs_us - 2,
                "acs_block_body_built_us": acs_us - 1,
                "acs_node_output_received_us": acs_us,
            })
            messages = []
            for subtype in ("proof", "echo", "ready", "bval", "aux"):
                messages.append({
                    "subtype": subtype,
                    "trace": {
                        "inbound_count": 1,
                        "inbound_bytes": 10,
                        "outbound_count": 2,
                        "outbound_bytes": 20,
                        "send_count": 2,
                        "send_total_us": 20 + iteration,
                        "send_max_us": 12 + iteration,
                        "send_failure_count": 0,
                        "encode": {"count": 2, "total_us": 2, "max_us": 1},
                        "queue_wait": {"count": 2, "total_us": queue_wait, "max_us": queue_wait},
                        "stream_open": {"count": 2, "total_us": 10 if mode == "fresh" else 0, "max_us": 5 if mode == "fresh" else 0},
                        "write": {"count": 2, "total_us": 8 + iteration, "max_us": 5 + iteration},
                        "finalize": {"count": 2, "total_us": 8 if mode == "fresh" else 0, "max_us": 4 if mode == "fresh" else 0},
                        "stream_open_count": 2 if mode == "fresh" else 0,
                        "stream_reuse_count": 0 if mode == "fresh" else 2,
                    },
                })
            traces.append({
                "key": {
                    "measurement_block": common["measurement_block"],
                    "run_id": run_id,
                    "node_id": 0,
                    "slot": slot,
                },
                "schema_version": TRACE_SCHEMA,
                "enabled": True,
                "messages": messages,
            })
    _write_csv(root / "run_measurements.csv", runs)
    _write_csv(root / "node_measurements.csv", nodes)
    with (root / "acs_trace.jsonl").open("w", encoding="utf-8") as handle:
        for trace in traces:
            handle.write(json.dumps(trace) + "\n")


def test_loader_accepts_only_matched_v2_provenance(tmp_path: Path) -> None:
    fresh, persistent = tmp_path / "fresh", tmp_path / "persistent"
    _write_campaign(fresh, "fresh")
    _write_campaign(persistent, "persistent")
    runs, messages = load_matched_transport_campaigns(fresh, persistent)
    assert set(runs.stream_mode) == {"fresh", "persistent"}
    assert len(runs) == 60
    assert set(messages.subtype) == {"proof", "echo", "ready", "bval", "aux"}

    path = persistent / "manifest.json"
    original = json.loads(path.read_text())
    for field, changed in (
        ("source_sha", "d" * 40),
        ("image_digest", "sha256:" + "b" * 64),
        ("nodes", 7),
        ("threshold", 5),
        ("batch_sizes", [8, 32]),
        ("corpus_identity", "different"),
        ("seed", 641),
        ("schedule", "different"),
        ("warmups", 4),
        ("repetitions", 31),
        ("topology", "same-az"),
        ("acs_trace_schema", "bloc-acs-trace/v1"),
    ):
        mutated = dict(original)
        mutated[field] = changed
        path.write_text(json.dumps(mutated), encoding="utf-8")
        with pytest.raises(ValueError, match="matched transport campaigns"):
            load_matched_transport_campaigns(fresh, persistent)
    path.write_text(json.dumps(original), encoding="utf-8")


def test_loader_rejects_short_cells_and_send_failures(tmp_path: Path) -> None:
    short_fresh, short_persistent = tmp_path / "short-fresh", tmp_path / "short-persistent"
    _write_campaign(short_fresh, "fresh", repetitions=29)
    _write_campaign(short_persistent, "persistent", repetitions=29)
    with pytest.raises(ValueError, match="30 successful"):
        load_matched_transport_campaigns(short_fresh, short_persistent)

    fresh, persistent = tmp_path / "fresh", tmp_path / "persistent"
    _write_campaign(fresh, "fresh")
    _write_campaign(persistent, "persistent")
    trace_path = persistent / "acs_trace.jsonl"
    lines = trace_path.read_text().splitlines()
    first = json.loads(lines[0])
    first["messages"][0]["trace"]["send_failure_count"] = 1
    lines[0] = json.dumps(first)
    trace_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    with pytest.raises(ValueError, match="send failure"):
        load_matched_transport_campaigns(fresh, persistent)


def test_summaries_and_report_emit_p50_p95_max_without_p99(tmp_path: Path) -> None:
    fresh, persistent = tmp_path / "fresh", tmp_path / "persistent"
    batches = (8, 32, 128)
    _write_campaign(fresh, "fresh", acs_base=500, batches=batches)
    _write_campaign(persistent, "persistent", acs_base=100, batches=batches)
    runs, messages = load_matched_transport_campaigns(fresh, persistent)
    acs, phases = summarize_transport_attribution(runs, messages)
    assert {"p50_us", "p95_us", "max_us", "p50_lower_us", "p50_upper_us"} <= set(acs.columns)
    assert {"send_p50_us", "encode_p95_us", "finalize_max_us", "stream_open_count", "stream_reuse_count"} <= set(phases.columns)
    assert not any("p99" in column for column in (*acs.columns, *phases.columns))

    output = write_transport_attribution(fresh, persistent, tmp_path / "out")
    assert (output / "transport-acs-summary.csv").is_file()
    assert (output / "transport-phase-summary.csv").is_file()
    report = json.loads((output / "transport-attribution.json").read_text())
    assert report["causality_note"].startswith("These classifications are experiment outcomes")
    assert report["cells"][0]["classification"] == "acs-signal"
    assert report["cross_batch"] == [{
        "batch_classifications": {"8": "acs-signal", "32": "acs-signal", "128": "acs-signal"},
        "classification": "acs-signal",
        "stable": True,
        "topology": "local",
    }]
    assert "p99" not in json.dumps(report)


@pytest.mark.parametrize(
    ("inputs", "expected"),
    (
        ({"acs_p50_direction": "overlap", "acs_p95_direction": "overlap", "fresh_acs_p95": 100, "persistent_acs_p95": 95, "finalize_direction": "persistent-better", "persistent_queue_wait_p50_us": 0}, "sender-finalization-only"),
        ({"acs_p50_direction": "persistent-worse", "acs_p95_direction": "persistent-worse", "fresh_acs_p95": 100, "persistent_acs_p95": 150, "finalize_direction": "overlap", "persistent_queue_wait_p50_us": 10}, "queue-regression"),
        ({"acs_p50_direction": "persistent-better", "acs_p95_direction": "overlap", "fresh_acs_p95": 100, "persistent_acs_p95": 80, "finalize_direction": "persistent-better", "persistent_queue_wait_p50_us": 0}, "acs-signal"),
        ({"acs_p50_direction": "overlap", "acs_p95_direction": "overlap", "fresh_acs_p95": 100, "persistent_acs_p95": 101, "finalize_direction": "overlap", "persistent_queue_wait_p50_us": 0}, "null-or-mixed"),
    ),
)
def test_classification_rules(inputs: dict, expected: str) -> None:
    assert classify_transport_cell({**inputs, "new_failures": 0, "consistency_errors": 0}) == expected
