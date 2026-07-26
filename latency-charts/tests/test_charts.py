from __future__ import annotations

import csv
import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.charts import generate_all
from bloc_latency_charts.cli import find_repository_root, output_directory
from bloc_latency_charts.data import (
    evidence_summary,
    load_experiment,
    scaling_summary,
    validate_merge_plan_additivity,
    validate_stage_additivity,
)


HEADER = [
    "run_id", "scenario_id", "phase", "iteration", "order_index", "nodes", "threshold",
    "batch_size", "network", "success", "consistent", "error", "critical_node_id",
    "total_slot_us", "proposal_preparation_us", "acs_us", "merge_plan_us",
    "share_generation_us", "threshold_wait_us", "combine_us", "materialization_us",
    "commit_to_plaintext_us", "harness_wall_us", "start_skew_us",
]


def write_fixture(root: Path, *, bad_total: bool = False) -> None:
    rows = []
    order = 0
    for network in ("tcp", "libp2p"):
        for nodes in (4, 7, 10):
            for batch in (8, 32, 128):
                for iteration in (1, 2, 3):
                    order += 1
                    stages = [500, 10_000 + nodes * 100, 3_000 + batch * 10, 5_000, 2_000 + batch * 5, 1_000]
                    total = sum(stages) + (100 if bad_total and order == 1 else 0)
                    rows.append([
                        f"run-{order}", f"n{nodes}-b{batch}-{network}", "measured", iteration, order,
                        nodes, {4: 3, 7: 5, 10: 7}[nodes], batch, network, "true", "true", "", 0,
                        total, stages[0], stages[1], stages[2], 2_500, stages[3], stages[4], stages[5],
                        total - stages[0] - stages[1], total + 100_000, 100,
                    ])
    rows.append(["warmup", "n4-b8-tcp", "warmup", 1, 99, 4, 3, 8, "tcp", "true", "true", "", 0, 1, 0, 0, 0, 0, 0, 0, 1, 1, 1, 0])
    rows.append(["failed", "n4-b8-tcp", "measured", 4, 100, 4, 3, 8, "tcp", "false", "false", "timeout", 0, 1, 0, 0, 0, 0, 0, 0, 1, 1, 1, 0])

    with (root / "run_measurements.csv").open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerow(HEADER)
        writer.writerows(rows)
    (root / "manifest.json").write_text(json.dumps({"experiment_id": "fixture-experiment"}), encoding="utf-8")


def test_load_filters_warmups_and_failed_runs(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    experiment = load_experiment(tmp_path)
    assert experiment.experiment_id == "fixture-experiment"
    assert len(experiment.runs) == 54
    assert len(experiment.attempts) == 55
    assert experiment.skipped_runs == 1


def test_load_accepts_utf8_bom_manifest(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    (tmp_path / "manifest.json").write_text(json.dumps({"experiment_id": "bom-fixture"}), encoding="utf-8-sig")
    experiment = load_experiment(tmp_path)
    assert experiment.experiment_id == "bom-fixture"


def test_scaling_summary_calculates_percentiles(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    summary = scaling_summary(load_experiment(tmp_path).runs)
    assert len(summary) == 18
    assert set(summary.columns) == {
        "nodes",
        "batch_size",
        "network",
        "p50",
        "p95",
        "p99",
        "p99_eligible",
        "count",
    }
    assert (summary["count"] == 3).all()
    assert not summary["p99_eligible"].any()
    assert summary["p99"].isna().all()


def test_evidence_summary_counts_every_attempt_and_excludes_failures_from_quantiles() -> None:
    attempts = pd.DataFrame(
        [
            {
                "nodes": 4,
                "batch_size": 8,
                "network": "libp2p",
                "success": True,
                "consistent": True,
                "timed_out": False,
                "deadline_met": True,
                "total_slot_us": value,
                "error": "",
            }
            for value in range(1, 1001)
        ]
        + [
            {
                "nodes": 4,
                "batch_size": 8,
                "network": "libp2p",
                "success": False,
                "consistent": False,
                "timed_out": False,
                "deadline_met": False,
                "total_slot_us": 9_000_000,
                "error": "terminal failure",
            },
            {
                "nodes": 4,
                "batch_size": 8,
                "network": "libp2p",
                "success": False,
                "consistent": False,
                "timed_out": True,
                "deadline_met": False,
                "total_slot_us": 10_000_000,
                "error": "timed out",
            },
            {
                "nodes": 4,
                "batch_size": 8,
                "network": "libp2p",
                "success": True,
                "consistent": False,
                "timed_out": False,
                "deadline_met": False,
                "total_slot_us": 11_000_000,
                "error": "",
            },
        ]
    )

    row = evidence_summary(attempts).iloc[0]

    assert (
        row.attempted,
        row.completed,
        row.consistent_within_deadline,
        row.failed,
        row.timed_out,
    ) == (1003, 1000, 1000, 2, 1)
    assert row.excluded_reasons == "failed=1;inconsistent=1;timed_out=1"
    assert row.p50_us == 500.5
    assert row.p95_us == 950.05
    assert row.p99_us == pytest.approx(990.01)
    assert row.p99_ci_lower_us == 984.0
    assert row.p99_ci_upper_us == 997.0
    assert bool(row.p99_eligible)


def test_stage_additivity_rejects_misleading_stack(tmp_path: Path) -> None:
    write_fixture(tmp_path, bad_total=True)
    experiment = load_experiment(tmp_path)
    with pytest.raises(ValueError, match="do not add"):
        validate_stage_additivity(experiment.runs)


def test_generate_all_outputs_svg_and_png(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    output = tmp_path / "charts"
    paths = generate_all(load_experiment(tmp_path), output)
    assert len(paths) == 7
    assert {path.suffix for path in paths} == {".svg", ".png", ".csv"}
    assert all(path.stat().st_size > 100 for path in paths)
    summary = pd.read_csv(output / "latency-evidence-summary.csv")
    assert {
        "attempted",
        "completed",
        "consistent_within_deadline",
        "failed",
        "timed_out",
        "p50_us",
        "p95_us",
        "p99_us",
        "p99_eligible",
    }.issubset(summary.columns)


def test_generate_all_supports_libp2p_only_campaign(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    source = tmp_path / "run_measurements.csv"
    with source.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.reader(handle))
    network_index = rows[0].index("network")
    with source.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerow(rows[0])
        writer.writerows(row for row in rows[1:] if row[network_index] == "libp2p")

    output = tmp_path / "libp2p-charts"
    paths = generate_all(load_experiment(tmp_path), output)
    assert len(paths) == 7
    assert all(path.stat().st_size > 100 for path in paths)


def test_generate_all_adds_merge_plan_attribution_when_available(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    source = tmp_path / "run_measurements.csv"
    with source.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.reader(handle))
    merge_index = rows[0].index("merge_plan_us")
    attribution = [100, 200, 300, 400]
    rows[0][merge_index + 1:merge_index + 1] = [
        "acs_output_decode_us", "agreed_set_us", "merge_us", "ciphertext_decode_us", "batch_plan_us",
    ]
    for row in rows[1:]:
        merge_total = int(row[merge_index])
        values = attribution + [merge_total - sum(attribution)]
        row[merge_index + 1:merge_index + 1] = [str(value) for value in values]
    with source.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerows(rows)

    experiment = load_experiment(tmp_path)
    validate_merge_plan_additivity(experiment.runs)
    paths = generate_all(experiment, tmp_path / "attribution-charts")
    assert len(paths) == 9
    assert any(path.name == "merge-plan-breakdown.png" for path in paths)


def test_missing_columns_are_reported(tmp_path: Path) -> None:
    (tmp_path / "run_measurements.csv").write_text("run_id,phase\nx,measured\n", encoding="utf-8")
    with pytest.raises(ValueError, match="missing columns"):
        load_experiment(tmp_path)


def test_default_output_uses_repository_chart_root(tmp_path: Path) -> None:
    repository = tmp_path / "repo"
    (repository / ".git").mkdir(parents=True)
    result_dir = repository / "bloc-node" / "results" / "baseline"
    result_dir.mkdir(parents=True)
    write_fixture(result_dir)
    experiment = load_experiment(result_dir)
    assert find_repository_root(result_dir) == repository
    assert output_directory(experiment) == repository / "results" / "charts" / "fixture-experiment"


def test_output_falls_back_beside_external_results(tmp_path: Path) -> None:
    result_dir = tmp_path / "external" / "baseline"
    result_dir.mkdir(parents=True)
    write_fixture(result_dir)
    experiment = load_experiment(result_dir)
    assert output_directory(experiment) == result_dir.parent / "charts" / "fixture-experiment"


def test_explicit_output_override_is_preserved(tmp_path: Path) -> None:
    result_dir = tmp_path / "baseline"
    result_dir.mkdir()
    write_fixture(result_dir)
    experiment = load_experiment(result_dir)
    explicit = tmp_path / "custom-figures"
    assert output_directory(experiment, explicit) == explicit.resolve()
