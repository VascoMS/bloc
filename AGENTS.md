# Repository Guidelines

## Project Structure & Module Organization

This repository contains several Go modules that work together as the BLOC prototype:

- `bloc-node/`: deployable prototype node, local evaluator, transport code, and result artifacts.
- `mempool-il/`: standalone mempool inclusion-list service.
- `bte/btd-impl-main/`: BEAT-MEV batched threshold encryption library and benchmarks.
- `sbc/hbbft/`: HoneyBadger ACS implementation used by `bloc-node`.
- `papers/`: research PDFs and protocol references.

Most source code lives under `cmd/` and `internal/` inside each module. Tests are colocated as `*_test.go`.

## Build, Test, and Development Commands

Run commands from the relevant module directory:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
```

Useful local runs:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8 --network tcp
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8 --network libp2p
```

BTE benchmarks:

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath'
```

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on edited Go files. Keep package names short and lowercase. Prefer explicit protocol/data names such as `InclusionList`, `WireShare`, and `MaterializedTransactionSet`. Add Go doc comments for exported identifiers and protocol-critical internals.

Do not introduce generated or temporary artifacts into source directories. Keep evaluator outputs under ignored `results/` directories.

## Testing Guidelines

Use Go’s built-in `testing` package. Name tests `TestBehaviorUnderCondition`, and keep deterministic protocol tests focused on hashes, ordering, consistency, and failure modes. For node changes, run `bloc-node` unit tests and at least one `eval-local` smoke test. For BTE or ACS changes, also run the corresponding module tests.

## Commit & Pull Request Guidelines

Recent history uses short conventional prefixes such as `feat:` and `fix:`. Use concise commit subjects, for example:

```text
feat: add signed ethereum tx demo payloads
fix: preserve deterministic inclusion merge order
```

Pull requests should include a short summary, affected modules, commands run, and known limitations. For protocol changes, mention compatibility impacts and update relevant docs.

## Security & Configuration Tips

Current configs may contain prototype trusted-dealer BTE shares and libp2p private keys. Treat them as local demo material only. Do not claim production readiness without DKG, stronger share verification, real protobuf schemas, and hardened P2P identity handling.
