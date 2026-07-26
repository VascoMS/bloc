from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Iterable

import pandas as pd


P99_CONTRACTED_SAMPLES = 1000


@dataclass(frozen=True)
class QuantileEstimate:
    quantile: float
    confidence: float
    sample_count: int
    eligible: bool
    value: float | None
    lower: float | None
    upper: float | None
    lower_rank: int | None
    upper_rank: int | None


def estimate_quantile(
    values: Iterable[float], quantile: float, confidence: float = 0.95
) -> QuantileEstimate:
    """Return a Type-7 estimate and a distribution-free order-statistic interval.

    Ranks are one-based. A p99 estimate is deliberately withheld until the
    successful sample contains the 1,000 observations required by the final
    evidence contract.
    """

    if not 0.0 < quantile < 1.0:
        raise ValueError("quantile must be between zero and one")
    if not 0.0 < confidence < 1.0:
        raise ValueError("confidence must be between zero and one")

    series = pd.Series(list(values), dtype="float64")
    if not series.map(math.isfinite).all():
        raise ValueError("quantile values must be finite")
    ordered = series.sort_values(ignore_index=True)
    sample_count = len(ordered)
    minimum = P99_CONTRACTED_SAMPLES if quantile >= 0.99 else 1
    if sample_count < minimum:
        return QuantileEstimate(
            quantile=quantile,
            confidence=confidence,
            sample_count=sample_count,
            eligible=False,
            value=None,
            lower=None,
            upper=None,
            lower_rank=None,
            upper_rank=None,
        )

    alpha = 1.0 - confidence
    lower_rank = _binomial_quantile(sample_count, quantile, alpha / 2.0) + 1
    upper_rank = min(
        sample_count,
        _binomial_quantile(sample_count, quantile, 1.0 - alpha / 2.0) + 1,
    )
    return QuantileEstimate(
        quantile=quantile,
        confidence=confidence,
        sample_count=sample_count,
        eligible=True,
        value=float(ordered.quantile(quantile, interpolation="linear")),
        lower=float(ordered.iloc[lower_rank - 1]),
        upper=float(ordered.iloc[upper_rank - 1]),
        lower_rank=lower_rank,
        upper_rank=upper_rank,
    )


def _binomial_quantile(trials: int, probability: float, target_cdf: float) -> int:
    cumulative = 0.0
    for successes in range(trials + 1):
        cumulative += (
            math.comb(trials, successes)
            * probability**successes
            * (1.0 - probability) ** (trials - successes)
        )
        if cumulative >= target_cdf:
            return successes
    return trials
