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

## Test

```sh
python -m pytest
```
