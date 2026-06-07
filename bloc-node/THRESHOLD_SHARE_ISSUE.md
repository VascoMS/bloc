# Threshold Share Combination Issue

This note documents the threshold-decryption issue observed while integrating
HoneyBadger ACS with the BEAT-MEV batched threshold encryption implementation.
It is intended as future debugging context for `bloc-node` and
`bte/btd-impl-main`.

## Symptom

Separate-process evaluator runs sometimes produced the same ACS/materialized
batch identity on all nodes, but one node failed to decrypt part of the batch:

```text
ERROR:cipher: message authentication failed
```

In those failing runs:

- nodes agreed on the same `batch_id`;
- nodes agreed on the same `merged_set_hash`;
- most nodes decrypted all selected Ethereum transactions correctly;
- one node combined a threshold set of shares and produced invalid AEAD keys for
  some sub-batches.

This made the final `ethereum_tx_hashes` differ across nodes, so the evaluator
reported `success=false` and `consistent=false`.

## Initial Hypothesis

The first suspicion was that network arrival order caused nodes to combine
different threshold subsets. `ClusterBTE.CombineShares` previously grouped
shares by sub-batch and passed the first `T` shares to `BatchCombineMessages`.

Sorting shares in `bloc-node` made the local demo deterministic, but that was
only a temporary stabilization. BEAT-MEV expects any valid threshold subset to
decrypt, so correctness must not depend on always selecting a fixed subset such
as `{0,1,2}`.

## Tests Performed

The following checks were run to distinguish a core BTE bug from a node/wire
integration bug:

- Single-process BTE subset check:
  - created one BTE cluster;
  - encrypted a batch;
  - tested all valid `n=4,t=3` subsets;
  - result: all subsets decrypted correctly.

- Process-style BTE check:
  - recreated separate node-local BTE instances from trusted-dealer material;
  - reloaded scalar shares independently;
  - marshaled/unmarshaled share points through the same hex representation used
    by `bloc-node`;
  - varied receiver and share order;
  - result: all tested valid subsets decrypted correctly.

- Mixed sub-batch subset check:
  - used different valid threshold subsets for different sub-batches;
  - result: decrypted correctly.

- Full separate-process demo:
  - after removing the node-side sorting dependency, blockspace-cap runs
    reproduced AEAD failures;
  - this showed that the controlled BTE tests were not covering the practical
    case where extra or incompatible shares are present.

## Conclusions

The BEAT-MEV primitive should allow any valid `t` shares for a batch. The core
threshold reconstruction worked in controlled tests, so the issue was not that
only one specific subset is mathematically valid.

The practical integration problem was that `bloc-node` can collect more than
`t` shares and some candidate threshold subsets may be unusable in the current
prototype because share validity is not publicly verified before reconstruction.
Without public share verification, choosing the first `T` shares can select a
bad candidate subset even when another valid subset is available.

There was also a related integration issue: nodes can receive shares for a
different BTE `BatchID`. Those must not be passed into reconstruction for the
local agreed batch. They are now filtered before threshold checks and combine.

Important distinction:

- fixed requirement: all combined shares must target the same local agreed
  `BatchID` and sub-batch;
- flexible requirement: any valid `t` shares for that batch/sub-batch should be
  usable;
- invalid requirement: force a particular operator subset as the target.

## Implemented Fix

The current fix has two parts.

In `bloc-node`:

- `matchingSharesForPlan` filters shares before threshold checks;
- shares with the wrong `BatchID` or invalid sub-batch are ignored/logged;
- `tryCombine` only passes matching shares into BTE.

In `bte/btd-impl-main`:

- `ClusterBTE.CombineShares` no longer blindly uses the first `T` shares;
- for each sub-batch, it tries threshold combinations from available shares;
- it accepts the first combination that decrypts all items and passes plaintext
  hash checks;
- this avoids targeting a fixed subset while tolerating an extra bad share when
  enough valid shares are available.

## Regression Coverage

Added focused tests:

- `TestCombineSharesAcceptsAnyValidThresholdSubset`
  - verifies every valid `n=4,t=3` subset;
  - includes a shuffled subset order.

- `TestCombineSharesSkipsInvalidExtraShareSubset`
  - corrupts one extra share;
  - verifies combine still succeeds when another valid threshold subset exists.

- `TestMatchingSharesForPlanIgnoresOtherBatchShares`
  - verifies wrong-batch shares do not count toward threshold;
  - verifies valid matching shares still satisfy the threshold.

End-to-end verification after the fix:

```sh
GOCACHE=/private/tmp/bloc-node-go-cache go test ./...
GOCACHE=/private/tmp/bloc-node-go-cache go vet ./...
GOCACHE=/private/tmp/bloc-node-go-cache go test ./...
./scripts/demo-local.sh
```

The final demo passed:

- normal TCP;
- blockspace cap;
- withhold-share;
- libp2p smoke.

## Remaining Limitations

The current fix is robust enough for the MVP, but it is not a replacement for
public decryption-share verification.

Remaining hardening work:

- attach public verification material for each operator share;
- verify partial decryption shares before accepting them;
- reject invalid shares deterministically instead of discovering bad subsets via
  failed plaintext/hash checks;
- preserve the cryptographic share index explicitly in the wire format rather
  than reconstructing it from `OperatorID`;
- eventually replace trusted-dealer setup with DKG or a verifiable setup path.

Until share verification exists, `CombineShares` may need to try multiple
candidate subsets when more than `t` shares are available.
