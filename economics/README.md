# BLOC Economics

This module imports saved historical MEV labels and finalized-block transaction
ordering, then reports detected-value concentration. It is independent of the
latency-chart package.

## Setup

```sh
cd economics
python -m venv .venv
. .venv/bin/activate
python -m pip install -e ".[test]"
```

## Historical MEV Concentration

Import a saved Flashbots MEV-inspect sample and canonical block RPC responses,
then generate descriptive concentration tables and a figure:

```sh
python -m bloc_economics.mev_inspect <raw-directory> --output <new-normalized.json>
python -m bloc_economics.concentration <new-normalized.json> --output <new-report-directory>
```

Both commands are offline and preserve their inputs. The adapter defaults to
WETH; `--token` selects another profit asset without currency conversion. The
report includes exact amounts, first/last strategy positions, exclusion and
coverage details, a PNG, and input/output checksums. Missing complete receipts
withhold gas concentration. These are gross detected strategy values, not
proposer payments or measured BLOC revenue loss.

Generated inputs and reports belong in ignored `results/` directories. See
[WORKFLOWS.md](../docs/WORKFLOWS.md#historical-mev-pilot) for the acquisition
manifest contract and [VALIDATION.md](../docs/VALIDATION.md#historical-detected-mev-concentration-pilot)
for sampling and interpretation limits.

## Complete-Month Dune Labels

The next cohort uses all rows returned for Ethereum during one half-open UTC
calendar month from `dex.sandwiches` plus `dex.sandwiched`,
`dex.atomic_arbitrages`, and the liquidation rows in `lending.borrow` plus
`lending.supply`. July 2026 is the fixed first collection month:

```sh
export DUNE_API_KEY=<read-scope-key>
python -m bloc_economics.dune_collect results/dune-mev-2026-07 --month 2026-07
```

The Dune raw-SQL endpoint consumes account credits. The collector never writes
the key to disk. It saves the exact rendered SQL, redacted request record,
execution and append-only status responses, raw CSV, response headers, row counts,
time bounds, and SHA-256 hashes. Existing evidence files are never overwritten.

Every export includes the canonical transaction's block hash, block position,
success, gas used, priority fee per gas, and top-level ETH value. Sandwich rows
remain labelled as outer or victim legs. Atomic-arbitrage rows remain individual
trade legs linked by transaction hash. Liquidation debt and collateral events
remain separate sides of the same transaction. Dune `amount_usd` fields are
volumes, not searcher profit or builder revenue. The CSV validator rejects
missing identity columns, non-Ethereum records, malformed hashes, empty results,
and rows outside the requested month.

Raw Dune exports remain local under ignored `results/` until redistribution
terms are established. See the monthly acquisition contract in
[WORKFLOWS.md](../docs/WORKFLOWS.md#complete-month-dune-mev-labels).

## Test

```sh
python -m pytest
```
