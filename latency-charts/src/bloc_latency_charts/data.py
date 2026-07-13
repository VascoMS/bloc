from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

import pandas as pd


STAGES = (
    ("proposal_preparation_us", "Proposal"),
    ("acs_us", "ACS"),
    ("merge_plan_us", "Merge + plan"),
    ("threshold_wait_us", "Threshold wait"),
    ("combine_us", "Combine"),
    ("materialization_us", "Materialization"),
)

REQUIRED_COLUMNS = {
    "run_id",
    "phase",
    "nodes",
    "threshold",
    "batch_size",
    "network",
    "success",
    "consistent",
    "total_slot_us",
    *(name for name, _ in STAGES),
}


@dataclass(frozen=True)
class ExperimentData:
    result_dir: Path
    experiment_id: str
    runs: pd.DataFrame
    skipped_runs: int


def load_experiment(result_dir: str | Path) -> ExperimentData:
    root = Path(result_dir).expanduser().resolve()
    csv_path = root / "run_measurements.csv"
    if not csv_path.is_file():
        raise ValueError(f"missing result file: {csv_path}")

    runs = pd.read_csv(csv_path)
    missing = sorted(REQUIRED_COLUMNS.difference(runs.columns))
    if missing:
        raise ValueError(f"run_measurements.csv is missing columns: {', '.join(missing)}")

    runs = runs.copy()
    runs["success"] = _boolean_series(runs["success"], "success")
    runs["consistent"] = _boolean_series(runs["consistent"], "consistent")
    numeric = ["nodes", "threshold", "batch_size", "total_slot_us", *(name for name, _ in STAGES)]
    for column in numeric:
        try:
            runs[column] = pd.to_numeric(runs[column], errors="raise")
        except (TypeError, ValueError) as exc:
            raise ValueError(f"column {column!r} contains a non-numeric value") from exc

    measured = runs["phase"].astype(str).str.lower().eq("measured")
    valid = measured & runs["success"] & runs["consistent"]
    skipped = int(measured.sum() - valid.sum())
    runs = runs.loc[valid].copy()
    if runs.empty:
        raise ValueError("no successful, consistent measured runs found")

    runs["total_slot_ms"] = runs["total_slot_us"] / 1000.0
    for name, _ in STAGES:
        runs[name.removesuffix("_us") + "_ms"] = runs[name] / 1000.0
    runs.sort_values(["nodes", "batch_size", "network", "iteration"], inplace=True, ignore_index=True)

    experiment_id = root.name
    manifest_path = root / "manifest.json"
    if manifest_path.is_file():
        try:
            manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
            experiment_id = str(manifest.get("experiment_id") or experiment_id)
        except (OSError, json.JSONDecodeError) as exc:
            raise ValueError(f"invalid manifest.json: {exc}") from exc

    return ExperimentData(root, experiment_id, runs, skipped)


def validate_stage_additivity(runs: pd.DataFrame, tolerance_us: float = 20.0) -> None:
    stage_total = runs[[name for name, _ in STAGES]].sum(axis=1)
    difference = (stage_total - runs["total_slot_us"]).abs()
    bad = difference > tolerance_us
    if bad.any():
        row = runs.loc[bad].iloc[0]
        raise ValueError(
            "critical-path stages do not add to total latency for "
            f"run {row['run_id']!r} (difference {difference.loc[bad].iloc[0]:.0f} us)"
        )


def scaling_summary(runs: pd.DataFrame) -> pd.DataFrame:
    return (
        runs.groupby(["nodes", "batch_size", "network"], as_index=False)["total_slot_ms"]
        .agg(
            p50=lambda values: values.quantile(0.50, interpolation="linear"),
            p95=lambda values: values.quantile(0.95, interpolation="linear"),
            count="count",
        )
        .sort_values(["nodes", "network", "batch_size"], ignore_index=True)
    )


def stage_summary(runs: pd.DataFrame) -> pd.DataFrame:
    columns = [name.removesuffix("_us") + "_ms" for name, _ in STAGES]
    return (
        runs.groupby(["nodes", "batch_size", "network"], as_index=False)[columns]
        .mean()
        .sort_values(["network", "nodes", "batch_size"], ignore_index=True)
    )


def _boolean_series(values: pd.Series, name: str) -> pd.Series:
    if pd.api.types.is_bool_dtype(values):
        return values.astype(bool)
    normalized = values.astype(str).str.strip().str.lower()
    invalid = ~normalized.isin({"true", "false"})
    if invalid.any():
        raise ValueError(f"column {name!r} contains values other than true/false")
    return normalized.eq("true")
