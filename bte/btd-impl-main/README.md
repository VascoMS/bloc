# btd-impl-main

Proof-of-concept implementation of the BEAT-MEV batched threshold encryption scheme plus the cluster-facing library used by `bloc-node`.

This module is still prototype-grade and should not be treated as production cryptography.

For the system role of this module, read [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).
For the cryptographic construction, hybrid envelope, wire format, planner, and
known security limitations, read [docs/modules/bte.md](../../docs/modules/bte.md).
For test coverage and benchmarks, read [TESTING.md](../../bte/btd-impl-main/TESTING.md)
and [docs/VALIDATION.md](../../docs/VALIDATION.md).

## Notes

- The source groups `G_1` and `G_2` are swapped relative to the paper because `G_1` operations are more efficient in this implementation.
- The cluster-facing `PlanBatch` path used by `bloc-node` enables BEAT-MEV-style `Opt-2` sub-batching by default: `alpha = ceil(2*sqrt(B))`, raised only when repeated indices require more sub-batches.
- The integrated path does not currently expose runtime switches for normal combine, `Opt-1`, or parallel combine; use the inherited benchmark code for those comparisons.
- Cluster combination validates operator/share indices and uses deterministic
  bounded subset recovery. `bloc-node` supplies the shared per-sub-batch budget
  and records every cryptographic attempt.
- Active cluster callers load a versioned public CRS artifact rather than a
  shared setup seed. The artifact still contains inherited diagonal elements
  marked insecure for a real setup, so this is only partial hardening and not a
  production CRS.
- [CLUSTER_BTE.md](../../bte/btd-impl-main/CLUSTER_BTE.md) is retained only as a compatibility pointer to the canonical deep dive.

## Tests and Benchmarks

Run all tests:

```sh
go test ./...
```

Run the cluster-facing full-path benchmarks:

```sh
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath' -benchtime=1x
```

You can also rerun the original benchmark script with:

```sh
./bench.sh
```
