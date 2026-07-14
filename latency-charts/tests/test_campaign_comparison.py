from pathlib import Path

import pandas as pd
import pytest

from bloc_latency_charts.campaign_comparison import compare_campaigns


def _write_campaign(root: Path, scale: float, batches: tuple[int, ...] = (8, 32, 128)) -> None:
    root.mkdir()
    rows = []
    for nodes in (4, 7):
        for batch in batches:
            for run in range(30):
                base = scale * (nodes * 1000 + batch * 100 + run)
                rows.append({
                    "run_id": f"n{nodes}-b{batch}-r{run}", "phase": "measured",
                    "nodes": nodes, "threshold": 3 if nodes == 4 else 5,
                    "batch_size": batch, "network": "libp2p",
                    "success": True, "consistent": True,
                    "total_slot_us": base * 6,
                    "proposal_preparation_us": base,
                    "acs_us": base, "merge_plan_us": base,
                    "share_generation_us": base, "threshold_wait_us": base,
                    "combine_us": base, "materialization_us": 0,
                    "iteration": run,
                })
    pd.DataFrame(rows).to_csv(root / "run_measurements.csv", index=False)


def test_compare_campaigns_writes_report_table_and_chart(tmp_path: Path) -> None:
    baseline = tmp_path / "baseline"
    candidate = tmp_path / "candidate"
    _write_campaign(baseline, 1.0)
    _write_campaign(candidate, 0.8)

    output = compare_campaigns(baseline, candidate, tmp_path / "comparison")

    comparison = pd.read_csv(output / "comparison.csv")
    total = comparison[comparison["stage"] == "Total slot"]
    assert len(total) == 6
    assert total["p50_change_percent"].round(6).eq(-20).all()
    assert (output / "REPORT.md").exists()
    assert (output / "before-after-p50.svg").exists()


def test_compare_campaigns_rejects_different_matrices(tmp_path: Path) -> None:
    baseline = tmp_path / "baseline"
    candidate = tmp_path / "candidate"
    _write_campaign(baseline, 1.0)
    _write_campaign(candidate, 0.8, batches=(8, 32))

    with pytest.raises(ValueError, match="configurations do not match"):
        compare_campaigns(baseline, candidate, tmp_path / "comparison")
