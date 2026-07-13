# btd-impl-main

Proof-of-concept implementation of the BEAT-MEV batched threshold encryption scheme plus the cluster-facing library used by `bloc-node`.

This module is still prototype-grade and should not be treated as production cryptography.

For the system role of this module, read [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md). For test coverage and benchmarks, read [TESTING.md](/bloc/bte/btd-impl-main/TESTING.md) and [docs/VALIDATION.md](/bloc/docs/VALIDATION.md).

## Notes

- The source groups `G_1` and `G_2` are swapped relative to the paper because `G_1` operations are more efficient in this implementation.
- The cluster-facing `PlanBatch` path used by `bloc-node` enables BEAT-MEV-style `Opt-2` sub-batching by default: `alpha = ceil(2*sqrt(B))`, raised only when repeated indices require more sub-batches.
- The integrated path does not currently expose runtime switches for normal combine, `Opt-1`, or parallel combine; use the inherited benchmark code for those comparisons.
- [CLUSTER_BTE.md](/bloc/bte/btd-impl-main/CLUSTER_BTE.md) remains the module-local deep dive on the cluster-facing library boundary.

## Tests and Benchmarks

Run all tests:

```sh
go test ./...
```

Run the cluster-facing full-path benchmarks:

```sh
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath' -benchtime=1x
```

Run a controlled combine-attribution matrix without network or ACS work:

```sh
go run ./cmd/bte-attribution run \
  --batch-sizes 8,32,128 \
  --warmups 5 \
  --repetitions 30 \
  --out-dir results/bte-attribution/local
```

The matrix starts with the paper-equivalent sequential `Opt-2`, `t=2`,
unverified path, then changes one relevant dimension at a time: BLOC planning,
round-robin repeated indices, production thresholds, proof verification, and
the hybrid wrapper. Use `--variants` and `--cpu-profile` for a separate focused
profile; do not profile the primary timed matrix.

You can also rerun the original benchmark script with:

```sh
./bench.sh
```
