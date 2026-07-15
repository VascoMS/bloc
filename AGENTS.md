# AGENTS.md

## Purpose

This is the primary entry point for Codex and other LLM agents working in the BLOC thesis prototype. Start here, then read only the smallest set of canonical docs needed for the task.

## Read Order

1. This file
2. [docs/STATUS.md](/bloc/docs/STATUS.md)
3. [docs/CODEX_GUIDE.md](/bloc/docs/CODEX_GUIDE.md)
4. The task-specific canonical docs listed below

## Repository Map

- `bloc-node/`: integrated prototype node, local evaluator, transport layers, and reports
- `mempool-il/`: deterministic mempool inclusion-list service
- `bte/btd-impl-main/`: BEAT-MEV batched threshold encryption library and benchmarks
- `sbc/hbbft/`: HoneyBadger ACS implementation plus the BLOC slot adapter
- `latency-charts/`: Python chart generation for `eval-suite` latency outputs
- `papers/`: research PDFs and reference material

Most source code lives under `cmd/` and `internal/` inside each module. Tests are colocated as `*_test.go`.

## Canonical Docs

- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [docs/DEVELOPMENT.md](/bloc/docs/DEVELOPMENT.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)
- [docs/DECISIONS.md](/bloc/docs/DECISIONS.md)
- [docs/CHANGELOG.md](/bloc/docs/CHANGELOG.md)
- [docs/ROADMAP.md](/bloc/docs/ROADMAP.md)
- [docs/GLOSSARY.md](/bloc/docs/GLOSSARY.md)
- [docs/CODEX_GUIDE.md](/bloc/docs/CODEX_GUIDE.md)
- [docs/modules/bloc-node.md](/bloc/docs/modules/bloc-node.md)
- [docs/modules/mempool-il.md](/bloc/docs/modules/mempool-il.md)
- [docs/modules/hbbft.md](/bloc/docs/modules/hbbft.md)
- [docs/modules/bte.md](/bloc/docs/modules/bte.md)

## Task Routing

### Architecture or protocol changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- the affected canonical document under [docs/modules/](/bloc/docs/modules/)
- [docs/DECISIONS.md](/bloc/docs/DECISIONS.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)

### `bloc-node` changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [bloc-node/README.md](/bloc/bloc-node/README.md)
- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- [docs/modules/bloc-node.md](/bloc/docs/modules/bloc-node.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)

### `mempool-il` changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [mempool-il/README.md](/bloc/mempool-il/README.md)
- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- [docs/modules/mempool-il.md](/bloc/docs/modules/mempool-il.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)

### BTE changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [bte/btd-impl-main/README.md](/bloc/bte/btd-impl-main/README.md)
- [bte/btd-impl-main/TESTING.md](/bloc/bte/btd-impl-main/TESTING.md)
- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- [docs/modules/bte.md](/bloc/docs/modules/bte.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)

### ACS or `hbbft` changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [sbc/hbbft/README.md](/bloc/sbc/hbbft/README.md)
- [docs/ARCHITECTURE.md](/bloc/docs/ARCHITECTURE.md)
- [docs/modules/hbbft.md](/bloc/docs/modules/hbbft.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)

### Documentation-only changes

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [docs/DEVELOPMENT.md](/bloc/docs/DEVELOPMENT.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- this file

### Validation or debugging work

Read:

- [docs/STATUS.md](/bloc/docs/STATUS.md)
- [docs/WORKFLOWS.md](/bloc/docs/WORKFLOWS.md)
- [docs/VALIDATION.md](/bloc/docs/VALIDATION.md)
- the relevant module README
- any matching archived historical note only if the failure mode is the same

## Repo Rules

- Keep durable cross-cutting documentation in root `docs/`.
- Treat [docs/STATUS.md](/bloc/docs/STATUS.md) as the live source of truth for current milestone state and next actions.
- Keep module READMEs focused on module-local usage and entry points.
- Do not create duplicate design or workflow docs when a canonical file already exists.
- No new standalone `.md` notes unless they are:
  - a temporary scratchpad,
  - a task artifact,
  - or a historical note intended for `docs/archive/`.
- Record major design choices in [docs/DECISIONS.md](/bloc/docs/DECISIONS.md).
- Record implementation-level changes in [docs/CHANGELOG.md](/bloc/docs/CHANGELOG.md).

## Development and Validation Shortcuts

Run module tests from the relevant module directory:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

Useful local prototype runs:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8
./scripts/demo-local.sh
```

BTE benchmark entry point:

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath'
```

## Security Reminder

Current configs may contain prototype trusted-dealer BTE shares and libp2p private keys. Treat them as local demo material only. Do not claim production readiness without DKG, stronger share verification, hardened identity handling, and a real execution or signing boundary.
