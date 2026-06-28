from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .charts import generate_all
from .data import ExperimentData, load_experiment


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Generate BLOC latency charts from an eval-suite result directory.")
    parser.add_argument("result_dir", type=Path, help="directory containing run_measurements.csv")
    parser.add_argument(
        "--output-dir",
        type=Path,
        help="figure directory; defaults to <repository>/results/charts/<experiment-id>",
    )
    return parser


def find_repository_root(start: Path) -> Path | None:
    current = start.expanduser().resolve()
    if current.is_file():
        current = current.parent
    for candidate in (current, *current.parents):
        if (candidate / ".git").exists() or (candidate / "AGENTS.md").is_file():
            return candidate
    return None


def output_directory(experiment: ExperimentData, explicit: Path | None = None) -> Path:
    if explicit is not None:
        return explicit.expanduser().resolve()
    experiment_name = experiment.experiment_id
    if not experiment_name or Path(experiment_name).name != experiment_name or experiment_name in {".", ".."}:
        raise ValueError(f"invalid experiment_id for chart directory: {experiment_name!r}")
    repository = find_repository_root(experiment.result_dir)
    if repository is not None:
        return repository / "results" / "charts" / experiment_name
    return experiment.result_dir.parent / "charts" / experiment_name


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        experiment = load_experiment(args.result_dir)
        output = output_directory(experiment, args.output_dir)
        generated = generate_all(experiment, output)
    except (OSError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    if experiment.skipped_runs:
        print(f"excluded {experiment.skipped_runs} failed or inconsistent measured run(s)")
    for path in generated:
        print(path)
    return 0
