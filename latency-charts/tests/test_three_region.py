from __future__ import annotations

import json
from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.three_region import analyze_three_region, prepare_three_region_runs


def write_fixture(root: Path, *, bad_total: bool = False) -> None:
    phase = root / "n4"
    phase.mkdir()
    regions = ["us-east-1", "eu-west-1", "eu-central-1"]
    placement = [
        {"id": node, "region": regions[node % 3], "zone": f"{regions[node % 3]}a", "instance_type": "t3.small"}
        for node in range(4)
    ]
    digest = "sha256:" + "a" * 64
    campaign = {
        "schema_version": "bloc-ec2-m3-three-region/v1", "status": "complete", "topology": "T2-three-region",
        "experiment_id": "three-region-fixture", "node_counts": [4], "batch_sizes": [8], "repetitions": 1,
        "docker_image_digest": digest,
    }
    (root / "manifest.json").write_text(json.dumps(campaign), encoding="utf-8")
    phase_manifest = {
        "schema_version": "bloc-ec2-three-region-phase/v1", "status": "complete", "topology": "T2-three-region",
        "node_count": 4, "repetitions": 1, "batch_sizes": [8], "primary_region": regions[0],
        "secondary_region": regions[1], "tertiary_region": regions[2], "operator_instance_type": "t3.small",
        "controller_instance_type": "t3.small", "docker_image_digest": digest, "placement": placement,
        "peering_connection_ids": {
            "primary_secondary": "pcx-1", "primary_tertiary": "pcx-2", "secondary_tertiary": "pcx-3",
        },
    }
    (phase / "manifest.json").write_text(json.dumps(phase_manifest), encoding="utf-8")
    total = 2101 if bad_total else 2080
    run = {
        "run_id": "run-1", "phase": "measured", "iteration": 1, "nodes": 4, "threshold": 3,
        "batch_size": 8, "network": "libp2p", "success": True, "consistent": True,
        "critical_node_id": 2, "critical_node_region": regions[2],
        "critical_node_availability_zone": f"{regions[2]}a", "selected_ciphertexts": 8,
        "total_slot_us": total, "proposal_preparation_us": 100, "acs_us": 200, "merge_plan_us": 300,
        "threshold_wait_us": 400, "combine_us": 500, "materialization_us": 580,
    }
    pd.DataFrame([run]).to_csv(root / "run_measurements.csv", index=False)
    pd.DataFrame([run]).to_csv(phase / "run_measurements.csv", index=False)
    node_rows = []
    for node in range(4):
        node_rows.append({
            "run_id": "run-1", "phase": "measured", "success": True, "consistent": True,
            "metrics_finalized": True, "node_id": node, "critical_node": node == 2,
            "selected_ciphertexts": 8, "region": regions[node % 3], "availability_zone": f"{regions[node % 3]}a",
            "instance_type": "t3.small", "merge_plan_us": 50, "acs_output_decode_us": 10,
            "agreed_set_us": 10, "merge_us": 10, "ciphertext_decode_us": 10, "batch_plan_us": 10,
        })
    pd.DataFrame(node_rows).to_csv(phase / "node_measurements.csv", index=False)
    targets = {"data": {"activeTargets": [{"health": "up"} for _ in range(4)]}}
    for name in ("prometheus-targets-before.json", "prometheus-targets.json"):
        (phase / name).write_text(json.dumps(targets), encoding="utf-8")
    network = []
    for source in range(4):
        for target in range(4):
            network.append({
                "phase": "pre", "source_node_id": source, "source_region": regions[source % 3],
                "target_node_id": target, "target_region": regions[target % 3], "attempts": 5, "successes": 5,
                "avg_connect_ms": float(source + target + 1), "avg_total_ms": float(source + target + 2),
            })
    pd.DataFrame(network).to_csv(phase / "network-pre.csv", index=False)
    for row in network:
        row["phase"] = "post"
    pd.DataFrame(network).to_csv(phase / "network-post.csv", index=False)
    resources = []
    for node in range(4):
        for index in range(4):
            resources.append({
                "timestamp": f"2026-07-24T00:00:00.{index * 250:03d}Z", "sample_index": index, "node": node,
                "region": regions[node % 3], "scenario": "n4-b8", "phase": "resource-measured",
                "cpu_usage_us": 100 + index, "memory_current_bytes": 10, "memory_peak_bytes": 12,
                "network_receive_bytes": 1000 + index, "network_transmit_bytes": 2000 + index,
                "restart_count": 0, "oom_killed": False,
            })
    pd.DataFrame(resources).to_csv(phase / "resource_timeseries.csv", index=False)
    (phase / "cleanup-verification.json").write_text("{}", encoding="utf-8")


def test_three_region_analysis_outputs_required_summaries(tmp_path: Path) -> None:
    write_fixture(tmp_path)
    output = analyze_three_region(tmp_path)
    expected = {
        "three-region-latency-summary.csv", "four-stage-summary.csv", "pairwise-network-summary.csv",
        "critical-node-region-summary.csv", "REPORT.md", "three-region-latency.png",
        "three-region-four-stage.png", "three-region-pairwise-network.png", "host-resource-summary.csv",
    }
    assert expected.issubset({path.name for path in output.iterdir()})
    pairs = set(pd.read_csv(output / "pairwise-network-summary.csv")["region_pair"])
    assert pairs == {"intra-region", "US–Ireland", "US–Frankfurt", "Ireland–Frankfurt"}
    assert "Critical-node region attribution" in (output / "REPORT.md").read_text(encoding="utf-8")
    resources = pd.read_csv(output / "host-resource-summary.csv")
    assert set(resources["scope"]) == {"node", "cluster"}


def test_three_region_analysis_rejects_non_additive_stages(tmp_path: Path) -> None:
    write_fixture(tmp_path, bad_total=True)
    with pytest.raises(ValueError, match="four-stage attribution"):
        prepare_three_region_runs(tmp_path)
