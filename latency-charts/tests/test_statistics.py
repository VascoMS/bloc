from __future__ import annotations

import pytest

from bloc_latency_charts.statistics import estimate_quantile


def test_type7_quantiles_and_exact_order_statistic_intervals_for_1000_values() -> None:
    values = list(range(1, 1001))

    p50 = estimate_quantile(values, 0.50)
    p95 = estimate_quantile(values, 0.95)
    p99 = estimate_quantile(values, 0.99)

    assert (p50.value, p50.lower, p50.upper, p50.lower_rank, p50.upper_rank) == (
        500.5,
        470.0,
        532.0,
        470,
        532,
    )
    assert (p95.value, p95.lower, p95.upper, p95.lower_rank, p95.upper_rank) == (
        950.05,
        937.0,
        964.0,
        937,
        964,
    )
    assert p99.value == pytest.approx(990.01)
    assert (p99.lower, p99.upper, p99.lower_rank, p99.upper_rank) == (
        984.0,
        997.0,
        984,
        997,
    )
    assert p99.eligible


def test_order_statistic_interval_handles_duplicate_values() -> None:
    estimate = estimate_quantile([7.0] * 1000, 0.99)

    assert estimate.value == 7.0
    assert estimate.lower == 7.0
    assert estimate.upper == 7.0
    assert (estimate.lower_rank, estimate.upper_rank) == (984, 997)


def test_p99_is_ineligible_below_the_contracted_sample_count() -> None:
    estimate = estimate_quantile(range(1, 1000), 0.99)

    assert estimate.sample_count == 999
    assert not estimate.eligible
    assert estimate.value is None
    assert estimate.lower is None
    assert estimate.upper is None
    assert estimate.lower_rank is None
    assert estimate.upper_rank is None


@pytest.mark.parametrize(
    ("quantile", "confidence"),
    ((0.0, 0.95), (1.0, 0.95), (0.50, 0.0), (0.50, 1.0)),
)
def test_quantile_estimate_rejects_invalid_probability_bounds(
    quantile: float, confidence: float
) -> None:
    with pytest.raises(ValueError):
        estimate_quantile([1.0], quantile, confidence)
