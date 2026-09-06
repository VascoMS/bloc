# hbbft

Practical implementation of the Honey Badger Byzantine Fault Tolerance consensus algorithm written in Go.

In this repository, the ACS core is also used by the BLOC prototype through a
separate slot-scoped adapter. For the cross-module BLOC boundary, read
[docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md). For the RBC/BBA/ACS state
machines, slot adapter, paper deviations, and known limitations, read
[docs/modules/hbbft.md](../../docs/modules/hbbft.md). For validation
expectations, read [docs/VALIDATION.md](../../docs/VALIDATION.md).

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

## ACS Diagnostic Traces

`SlotConfig.Trace.Enabled` opts a slot into the bounded diagnostic recorder.
New traces use `bloc-acs-trace/v3`; v1/v2 remain readable historical formats.
The recorder admits outbound ACS sends synchronously when they are emitted,
seals admission at local ACS output, and finalizes after every admitted send
terminates. For each subtype a final trace satisfies
`scheduled_count = terminal_count = send_count + send_failure_count`.
`pending_at_decision` is the immutable number still in flight when the trace
sealed and is not reduced as those sends finish.

Successful encode, queue-wait, stream-open, write, and finalization totals are
sender-side completion observations, not remote receipt. Trace-enabled result
publication in `bloc-node` waits for finalization, but `ACSUS`, protocol
progress, merge/plan, share generation, and materialization do not. RBC entries
also record the first READY trigger (`echo_quorum` or `ready_relay`) and the
matching ECHO/READY counts before local self-admission.

## References

- [simulation/README.md](../../sbc/hbbft/simulation/README.md)
- [bench/README.md](../../sbc/hbbft/bench/README.md)
