import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.merge_plan_campaign import analyze_campaign


PHASES = [
    {"id": "fixed-n4", "path": "fixed-n4", "nodes": 4, "operator_instance_type": "c7i.large"},
    {"id": "fixed-n7", "path": "fixed-n7", "nodes": 7, "operator_instance_type": "c7i.large"},
    {"id": "burstable-n7", "path": "burstable-n7", "nodes": 7, "operator_instance_type": "t3.small"},
]


def write_campaign(root: Path) -> None:
    manifest_phases = []
    for phase in PHASES:
        phase_root = root / phase["path"]
        phase_root.mkdir(parents=True)
        rows = []
        for batch in [8, 32, 128]:
            for run in range(30):
                for node in range(phase["nodes"]):
                    decode = batch * 100 + run + node
                    rows.append({
                        "run_id": f"{phase['id']}-b{batch}-r{run}",
                        "success": True,
                        "consistent": True,
                        "node_id": node,
                        "critical_node": node == phase["nodes"] - 1,
                        "metrics_finalized": True,
                        "selected_ciphertexts": batch,
                        "measurement_block": run // 10 + 1,
                        "acs_output_decode_us": 10,
                        "agreed_set_us": 20,
                        "merge_us": 30,
                        "ciphertext_decode_us": decode,
                        "batch_plan_us": 40,
                        "merge_plan_us": decode + 100,
                    })
        pd.DataFrame(rows).to_csv(phase_root / "node_measurements.csv", index=False)
        targets = {
            "data": {
                "activeTargets": [
                    {"labels": {"job": "bloc-sidecars"}, "health": "up"}
                    for _ in range(phase["nodes"])
                ]
            }
        }
        (phase_root / "prometheus-targets.json").write_text(json.dumps(targets), encoding="utf-8")
        manifest_phases.append({**phase, "image_digest": "sha256:test"})
    (root / "manifest.json").write_text(
        json.dumps({"phases": manifest_phases}), encoding="utf-8"
    )


def test_analyze_campaign_writes_tables_report_and_charts(tmp_path: Path) -> None:
    write_campaign(tmp_path)
    output = analyze_campaign(tmp_path)
    expected = [
        "merge-plan-measurements.csv",
        "merge-plan-summary.csv",
        "comparison.csv",
        "REPORT.md",
        "merge-plan-substages.png",
        "ciphertext-decode-scaling.svg",
        "node-skew.png",
        "per-node-distributions.png",
        "instance-class-comparison.svg",
    ]
    assert all((output / name).exists() for name in expected)
    measurements = pd.read_csv(output / "merge-plan-measurements.csv")
    assert set(measurements["phase"]) == {"fixed-n4", "fixed-n7", "burstable-n7"}
    assert (measurements["decode_us_per_ciphertext"] > 0).all()
    report = (output / "REPORT.md").read_text(encoding="utf-8")
    assert "no p99 claim" in report
    assert "457 ms" in report


def test_analyze_campaign_rejects_substage_mismatch(tmp_path: Path) -> None:
    write_campaign(tmp_path)
    path = tmp_path / "fixed-n4" / "node_measurements.csv"
    frame = pd.read_csv(path)
    frame.loc[0, "merge_plan_us"] += 21
    frame.to_csv(path, index=False)
    with pytest.raises(ValueError, match="additivity"):
        analyze_campaign(tmp_path)
