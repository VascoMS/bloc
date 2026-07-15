# hbbft

Practical implementation of the Honey Badger Byzantine Fault Tolerance consensus algorithm written in Go.

In this repository, the ACS core is also used by the BLOC prototype through a
separate slot-scoped adapter. For the cross-module BLOC boundary, read
[docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md). For the RBC/BBA/ACS state
machines, slot adapter, paper deviations, and known limitations, read
[docs/modules/hbbft.md](/bloc/docs/modules/hbbft.md). For validation
expectations, read [docs/VALIDATION.md](/bloc/docs/VALIDATION.md).

## Summary

This package contains the building blocks for:

- Reliable Broadcast (`RBC`)
- Binary Byzantine Agreement (`BBA`)
- Asynchronous Common Subset (`ACS`)
- the original HoneyBadger driver
- the BLOC-specific slot-scoped adapter used by `bloc-node`

The BLOC path keeps the consensus core but bypasses the original recurring HoneyBadger driver as the top-level integration boundary.

## Usage

Run tests:

```sh
go test ./...
```

Run the local simulation:

```sh
go run simulation/main.go
```

Run the benchmark harness:

```sh
go run bench/main.go
```

## References

- [simulation/README.md](/bloc/sbc/hbbft/simulation/README.md)
- [bench/README.md](/bloc/sbc/hbbft/bench/README.md)
