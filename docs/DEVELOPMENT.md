# Development

## Repo Conventions

- Keep cross-cutting durable documentation in root `docs/`.
- Keep module READMEs focused on local usage, commands, and entry points.
- Keep environment-specific operational procedures in the matching
  `deploy/*/README.md`.
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

## Git Workflow

- Keep `main` in a validated, releasable state and use one short-lived
  `codex/<task>` branch per non-trivial objective.
- Do not accumulate unrelated work on a branch whose name describes an older
  task. Integrate or close the branch before starting the next objective.
- Commit coherent changes with their tests and canonical documentation. Do not
  mix generated `results/` artifacts into source commits.
- Prefer `git merge --ff-only` when the validated task history descends directly
  from `main`. Review true divergence rather than creating an automatic merge
  commit.
- Delete only branches whose tips are reachable from the published `main`.
  Unique or obsolete work must be reviewed and documented before it is ported
  or removed.
- Do not force-push shared branches. Agents are responsible for checking status,
  divergence, validation, commits, and branch cleanup as part of the task that
  created the changes.

## Documentation Ownership

- `README.md`: repo entry point
- `AGENTS.md`: task router and agent operating contract
- `docs/STATUS.md`: live milestone, blocker, evidence, and next-action state
- `docs/ARCHITECTURE.md`: canonical system design
- `docs/modules/*.md`: canonical protocol implementation details by module
- `docs/WORKFLOWS.md`: generic implementation, documentation, and artifact process
- `docs/VALIDATION.md`: what to run and why
- `deploy/*/README.md`: environment-specific operational runbooks
- `docs/DECISIONS.md`: major design decisions
- `docs/CHANGELOG.md`: implementation-level history

## Status Maintenance

`docs/STATUS.md` is concise live state, not an experiment diary. Review it when
a task changes milestone state, blockers, accepted or rejected evidence, the
last known good baseline, or immediate next actions. Update it in the same task
when any of those facts changes, remove resolved blockers, and move historical
detail to evidence reports or the changelog.

No agent may select a new active milestone unless that decision is explicitly in
scope. Every task handoff states whether status was reviewed and whether it
required an update.

## When To Create A New Doc

Create a new standalone Markdown file only if it is:

- a canonical root doc under `docs/`,
- a canonical protocol deep dive under `docs/modules/`,
- a module entry-point README,
- an environment runbook under `deploy/`,
- a temporary scratchpad for one task,
- or a historical note that will live under `docs/archive/`.

Otherwise, extend an existing canonical document.
