from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

import pandas as pd

from .statistics import estimate_quantile


STAGES = (
    ("proposal_preparation_us", "Proposal"),
    ("acs_us", "ACS"),
    ("merge_plan_us", "Merge + plan"),
    ("threshold_wait_us", "Threshold wait"),
    ("combine_us", "Combine"),
    ("materialization_us", "Materialization"),
)

MERGE_PLAN_STAGES = (
    ("acs_output_decode_us", "ACS output decode"),
    ("agreed_set_us", "Agreed set"),
    ("merge_us", "Merge"),
    ("ciphertext_decode_us", "Ciphertext decode"),
    ("batch_plan_us", "Batch plan"),
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
    attempts: pd.DataFrame
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
    merge_columns = {name for name, _ in MERGE_PLAN_STAGES}
    present_merge_columns = merge_columns.intersection(runs.columns)
    if present_merge_columns and present_merge_columns != merge_columns:
        missing_merge = sorted(merge_columns.difference(runs.columns))
        raise ValueError(f"run_measurements.csv has partial merge-plan attribution; missing: {', '.join(missing_merge)}")

    numeric = ["nodes", "threshold", "batch_size", "total_slot_us", *(name for name, _ in STAGES)]
    if present_merge_columns:
        numeric.extend(name for name, _ in MERGE_PLAN_STAGES)
    for column in numeric:
        try:
            runs[column] = pd.to_numeric(runs[column], errors="raise")
        except (TypeError, ValueError) as exc:
            raise ValueError(f"column {column!r} contains a non-numeric value") from exc
    if "timed_out" in runs:
        runs["timed_out"] = _optional_boolean_series(
            runs["timed_out"], "timed_out", pd.Series(False, index=runs.index)
        )
    if "deadline_met" in runs:
        legacy_deadline = runs["success"] & runs["consistent"] & runs["total_slot_us"].le(12_000_000)
        runs["deadline_met"] = _optional_boolean_series(
            runs["deadline_met"], "deadline_met", legacy_deadline
        )

    measured = runs["phase"].astype(str).str.lower().eq("measured")
    attempts = runs.loc[measured].copy()
    valid = measured & runs["success"] & runs["consistent"]
    skipped = int(measured.sum() - valid.sum())
    runs = runs.loc[valid].copy()
    if runs.empty:
        raise ValueError("no successful, consistent measured runs found")

    runs["total_slot_ms"] = runs["total_slot_us"] / 1000.0
    for name, _ in STAGES:
        runs[name.removesuffix("_us") + "_ms"] = runs[name] / 1000.0
    for name, _ in MERGE_PLAN_STAGES:
        if name in runs:
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

    return ExperimentData(root, experiment_id, attempts, runs, skipped)


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
    rows: list[dict[str, object]] = []
    for (nodes, batch, network), group in runs.groupby(
        ["nodes", "batch_size", "network"], sort=True
    ):
        values = group["total_slot_ms"]
        p99 = estimate_quantile(values, 0.99)
        rows.append(
            {
                "nodes": nodes,
                "batch_size": batch,
                "network": network,
                "p50": values.quantile(0.50, interpolation="linear"),
                "p95": values.quantile(0.95, interpolation="linear"),
                "p99": p99.value,
                "p99_eligible": p99.eligible,
                "count": len(values),
            }
        )
    return pd.DataFrame(rows).sort_values(
        ["nodes", "network", "batch_size"], ignore_index=True
    )


def evidence_summary(attempts: pd.DataFrame) -> pd.DataFrame:
    rows: list[dict[str, object]] = []
    for (nodes, batch, network), group in attempts.groupby(
        ["nodes", "batch_size", "network"], sort=True
    ):
        completed = group["success"].astype(bool) & group["consistent"].astype(bool)
        if "timed_out" in group:
            timed_out = group["timed_out"].astype(bool) & ~completed
        else:
            errors = group.get("error", pd.Series("", index=group.index)).fillna("").astype(str)
            timed_out = errors.str.contains(r"timed[ -]?out|timeout", case=False, regex=True) & ~completed
        inconsistent = group["success"].astype(bool) & ~group["consistent"].astype(bool)
        ordinary_failed = ~completed & ~timed_out & ~inconsistent
        if "deadline_met" in group:
            within_deadline = completed & group["deadline_met"].astype(bool)
        else:
            within_deadline = completed & group["total_slot_us"].le(12_000_000)

        values = group.loc[completed, "total_slot_us"]
        p50 = estimate_quantile(values, 0.50)
        p95 = estimate_quantile(values, 0.95)
        p99 = estimate_quantile(values, 0.99)
        reasons = [
            (label, int(mask.sum()))
            for label, mask in (
                ("failed", ordinary_failed),
                ("inconsistent", inconsistent),
                ("timed_out", timed_out),
            )
            if mask.any()
        ]
        rows.append(
            {
                "nodes": nodes,
                "batch_size": batch,
                "network": network,
                "attempted": len(group),
                "completed": int(completed.sum()),
                "consistent_within_deadline": int(within_deadline.sum()),
                "failed": int((ordinary_failed | inconsistent).sum()),
                "timed_out": int(timed_out.sum()),
                "excluded_reasons": ";".join(f"{label}={count}" for label, count in reasons),
                "p50_us": p50.value,
                "p50_ci_lower_us": p50.lower,
                "p50_ci_upper_us": p50.upper,
                "p95_us": p95.value,
                "p95_ci_lower_us": p95.lower,
                "p95_ci_upper_us": p95.upper,
                "p99_us": p99.value,
                "p99_ci_lower_us": p99.lower,
                "p99_ci_upper_us": p99.upper,
                "p99_eligible": p99.eligible,
                "maximum_us": float(values.max()) if len(values) else None,
            }
        )
    return pd.DataFrame(rows).sort_values(
        ["nodes", "network", "batch_size"], ignore_index=True
    )


def stage_summary(runs: pd.DataFrame) -> pd.DataFrame:
    columns = [name.removesuffix("_us") + "_ms" for name, _ in STAGES]
    return (
        runs.groupby(["nodes", "batch_size", "network"], as_index=False)[columns]
        .mean()
        .sort_values(["network", "nodes", "batch_size"], ignore_index=True)
    )


def has_merge_plan_attribution(runs: pd.DataFrame) -> bool:
    return all(name in runs.columns for name, _ in MERGE_PLAN_STAGES)


def validate_merge_plan_additivity(runs: pd.DataFrame, tolerance_us: float = 20.0) -> None:
    if not has_merge_plan_attribution(runs):
        return
    attributed = runs[[name for name, _ in MERGE_PLAN_STAGES]].sum(axis=1)
    difference = (attributed - runs["merge_plan_us"]).abs()
    bad = difference > tolerance_us
    if bad.any():
        row = runs.loc[bad].iloc[0]
        raise ValueError(
            "merge-plan substages do not add to merge_plan_us for "
            f"run {row['run_id']!r} (difference {difference.loc[bad].iloc[0]:.0f} us)"
        )


def merge_plan_summary(runs: pd.DataFrame) -> pd.DataFrame:
    if not has_merge_plan_attribution(runs):
        raise ValueError("merge-plan attribution columns are not available")
    columns = [name.removesuffix("_us") + "_ms" for name, _ in MERGE_PLAN_STAGES]
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


def _optional_boolean_series(values: pd.Series, name: str, defaults: pd.Series) -> pd.Series:
    normalized = values.astype("string").str.strip().str.lower()
    missing = values.isna() | normalized.eq("")
    invalid = ~missing & ~normalized.isin({"true", "false"})
    if invalid.any():
        raise ValueError(f"column {name!r} contains values other than true/false/blank")
    result = normalized.eq("true").fillna(False)
    result.loc[missing] = defaults.loc[missing].astype(bool)
    return result.astype(bool)
