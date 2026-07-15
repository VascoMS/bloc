from __future__ import annotations

import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.cross_region import analyze_cross_region, prepare_cross_region_runs, validate_phase_contract


def write_campaign(root: Path, *, bad_total: bool = False) -> None:
    rows = []
    for nodes in (4, 7):
        for batch in (8, 32, 128):
            for iteration in range(1, 4):
                proposal, acs, merge, wait, combine, materialize = 100, 200, 300, 400, 500, 600
                total = proposal + acs + merge + wait + combine + materialize
                if bad_total and nodes == 4 and batch == 8 and iteration == 1:
                    total += 21
                rows.append(
                    {
                        "run_id": f"n{nodes}-b{batch}-{iteration}", "phase": "measured", "iteration": iteration,
                        "nodes": nodes, "threshold": 3 if nodes == 4 else 5, "batch_size": batch, "network": "libp2p",
                        "success": True, "consistent": True, "total_slot_us": total,
                        "proposal_preparation_us": proposal, "acs_us": acs, "merge_plan_us": merge,
                        "threshold_wait_us": wait, "combine_us": combine, "materialization_us": materialize,
                    }
                )
    pd.DataFrame(rows).to_csv(root / "run_measurements.csv", index=False)
    (root / "manifest.json").write_text(json.dumps({"experiment_id": "cross-region-fixture"}), encoding="utf-8")


def test_cross_region_analysis_writes_four_stage_artifacts(tmp_path: Path) -> None:
    write_campaign(tmp_path)
    output = analyze_cross_region(tmp_path)
    expected = {
        "cross-region-latency-summary.csv", "four-stage-summary.csv", "REPORT.md",
        "cross-region-latency-scaling.png", "cross-region-latency-scaling.svg",
        "cross-region-four-stage-breakdown.png", "cross-region-four-stage-breakdown.svg",
        "cross-region-latency-distributions.png", "cross-region-latency-distributions.svg",
    }
    assert expected.issubset({path.name for path in output.iterdir()})
    stages = pd.read_csv(output / "four-stage-summary.csv")
    assert set(stages["stage"]) == {"Proposal", "ACS", "Merge + Plan", "Decryption + Materialization"}
    assert set(stages["count"]) == {3}
    assert "no p99 claim" in (output / "REPORT.md").read_text(encoding="utf-8")


def test_cross_region_analysis_rejects_non_additive_stage_mapping(tmp_path: Path) -> None:
    write_campaign(tmp_path, bad_total=True)
    with pytest.raises(ValueError, match="four-stage attribution"):
        prepare_cross_region_runs(tmp_path)


def valid_phase_contract() -> tuple[dict, pd.DataFrame, pd.DataFrame, dict, dict]:
    manifest = {
        "status": "complete", "topology": "T2-cross-region", "node_count": 4, "repetitions": 1,
        "primary_region": "us-east-1", "secondary_region": "eu-west-1",
        "operator_instance_type": "t3.medium", "controller_instance_type": "t3.medium",
        "batch_sizes": [8],
        "placement": [
            {"id": 0, "region": "us-east-1"}, {"id": 1, "region": "eu-west-1"},
            {"id": 2, "region": "us-east-1"}, {"id": 3, "region": "eu-west-1"},
        ],
    }
    runs = pd.DataFrame([{"phase": "measured", "batch_size": 8, "success": True, "consistent": True, "selected_ciphertexts": 8}])
    nodes = pd.DataFrame([{"phase": "measured", "batch_size": 8, "metrics_finalized": True} for _ in range(4)])
    targets = {"data": {"activeTargets": [{"health": "up"} for _ in range(4)]}}
    cleanup = {"regions": {"us-east-1": {"instances": ""}, "eu-west-1": {"instances": ""}}, "iam_role": ""}
    return manifest, runs, nodes, targets, cleanup


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (lambda values: values[0]["placement"].__setitem__(1, {"id": 1, "region": "us-east-1"}), "placement"),
        (lambda values: values[1].drop(values[1].index, inplace=True), "exactly 1 measured"),
        (lambda values: values[1].loc.__setitem__((0, "consistent"), False), "failed or inconsistent"),
        (lambda values: values[3]["data"].__setitem__("activeTargets", [{"health": "down"}] * 4), "Prometheus"),
        (lambda values: values[4].__setitem__("iam_role", "leftover-role"), "cleanup"),
    ],
)
def test_phase_contract_rejects_invalid_evidence(mutation, message: str) -> None:
    values = list(valid_phase_contract())
    mutation(values)
    with pytest.raises(ValueError, match=message):
        validate_phase_contract(*values)
