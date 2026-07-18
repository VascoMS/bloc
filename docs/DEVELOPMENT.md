# Development

## Repo Conventions

- Keep cross-cutting durable documentation in root `docs/`.
- Keep module READMEs focused on local usage, commands, and entry points.
- Prefer updating canonical docs over creating new standalone `.md` files.
- Use `docs/archive/` for historical notes that still matter but are no longer canonical.

## Code Layout

- Source code is split into multiple Go modules.
- Most module code lives under `cmd/` and `internal/`.
- Tests are colocated as `*_test.go`.
- Generated or temporary outputs belong in ignored artifact directories such as `results/`.

## Naming and Style

- Use standard Go formatting and short lowercase package names.
- Prefer explicit protocol names such as `InclusionList`, `WireShare`, and `MaterializedTransactionSet`.
- Add doc comments for exported identifiers and protocol-critical internals when the code is not self-evident.
- Keep campaign entrypoints compatible with macOS Bash 3.2 and Linux Bash.
  Use `scripts/lib/campaign-common.sh` for lifecycle primitives and the shared
  Python helper for JSON/CSV processing. Every runner must keep
  `--validate-only` side-effect free.

## Artifact Policy

- Do not introduce generated outputs into source directories unless the file is a committed interface artifact such as generated protobuf bindings already tracked by the repo.
- Keep evaluator output, demo runs, and ad hoc experiment logs under ignored `results/` directories.
- Treat local demo key material and cluster configs as prototype-only artifacts.

## Documentation Ownership

- `README.md`: repo entry point
- `AGENTS.md`: task router and agent operating contract
- `docs/ARCHITECTURE.md`: canonical system design
- `docs/modules/*.md`: canonical protocol implementation details by module
- `docs/WORKFLOWS.md`: implementation and documentation process
- `docs/VALIDATION.md`: what to run and why
- `docs/DECISIONS.md`: major design decisions
- `docs/CHANGELOG.md`: implementation-level history

## When To Create A New Doc

Create a new standalone Markdown file only if it is:

- a canonical root doc under `docs/`,
- a canonical protocol deep dive under `docs/modules/`,
- a module entry-point README,
- a temporary scratchpad for one task,
- or a historical note that will live under `docs/archive/`.

Otherwise, extend an existing canonical document.
