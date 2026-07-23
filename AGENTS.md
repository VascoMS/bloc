# AGENTS.md

## Purpose

This is the sole mandatory entry point for Codex and other coding agents working
in the BLOC thesis prototype. Read this file first, then load only the smallest
task-specific context needed to act safely.

## Required Read Order

1. This file.
2. [docs/STATUS.md](docs/STATUS.md) for current state, blockers, evidence, and
   immediate actions.
3. The README for the affected module.
4. Only the canonical document and source files required by the task.

Do not load every canonical document for a narrow change. Source code and tests
are authoritative for implemented behavior; when they conflict with canonical
documentation, correct the documentation in the same task.

## Repository Map

- `bloc-node/`: integrated node, evaluators, transport, metrics, and reports
- `mempool-il/`: deterministic mempool inclusion-list service
- `bte/btd-impl-main/`: BEAT-MEV-derived BTE library and benchmarks
- `sbc/hbbft/`: HoneyBadger ACS implementation and BLOC slot adapter
- `latency-charts/`: chart generation and campaign analysis
- `deploy/`: local Compose and VM/EC2 deployment runbooks and artifacts
- `papers/`: research PDFs and reference material

Most Go source lives under module-local `cmd/` and `internal/` directories.
Tests are colocated as `*_test.go`.

## Task Routing

| Task | Minimum context after `STATUS.md` |
|---|---|
| Architecture or cross-module protocol change | [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), affected [module deep dive](docs/modules/), relevant decisions, then validation |
| `bloc-node` change | [bloc-node/README.md](bloc-node/README.md), [node deep dive](docs/modules/bloc-node.md), then validation sections matching the behavior |
| `mempool-il` change | [mempool-il/README.md](mempool-il/README.md), [mempool deep dive](docs/modules/mempool-il.md), then matching validation |
| BTE change | [BTE README](bte/btd-impl-main/README.md), [BTE deep dive](docs/modules/bte.md), and [TESTING.md](bte/btd-impl-main/TESTING.md) when test or benchmark behavior matters |
| ACS or `hbbft` change | [hbbft README](sbc/hbbft/README.md), [hbbft deep dive](docs/modules/hbbft.md), and the ACS safety validation section |
| Deployment or campaign operation | The relevant README under `deploy/`, plus acceptance criteria in [docs/VALIDATION.md](docs/VALIDATION.md) |
| Documentation-only change | [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md), the owning canonical document, and link/ownership validation |
| Debugging | Matching workflow/validation section and a historical note only when it documents the same failure mode |

## Context-Minimization Rules

- Build a small task packet containing the objective, affected module, likely
  files, invariants, and required validation.
- Do not paste long benchmark output or historical debugging narratives when a
  durable conclusion already exists in canonical documentation.
- Read archived material only for the exact subsystem or failure being studied.
- Keep stable context in canonical docs and temporary context in task-scoped
  scratchpads or ignored result artifacts.

## Documentation Ownership

- `README.md`: repository entry point
- `AGENTS.md`: agent operating contract and task router
- `docs/STATUS.md`: live milestone, blocker, evidence, and next-action state
- `docs/ARCHITECTURE.md`: system boundaries and cross-module invariants
- `docs/modules/*.md`: module algorithms, state, identities, and limitations
- `docs/DEVELOPMENT.md`: repository and documentation conventions
- `docs/WORKFLOWS.md`: generic development, documentation, and artifact workflows
- `docs/VALIDATION.md`: evidence semantics, commands, and acceptance criteria
- `deploy/*/README.md`: environment-specific operational runbooks
- `docs/DECISIONS.md`: major design and workflow decisions
- `docs/CHANGELOG.md`: implementation-level history
- `docs/ROADMAP.md`: milestone objectives and done criteria
- `docs/archive/`: intentional historical evidence records that remain useful

Do not create a parallel explanation when one of these owners already exists.
Module READMEs should stay focused on usage, commands, and entry points.

## `STATUS.md` Maintenance Contract

Review `docs/STATUS.md` whenever work changes any of the following:

- milestone selection, start, completion, or blockage;
- an open blocker or risk;
- accepted or rejected evidence;
- the last known good baseline; or
- immediate next actions.

When one of those facts changes, update `STATUS.md` in the same task. Remove or
rewrite resolved blockers instead of preserving them as current risks. Do not
select a new active milestone unless the user or task explicitly authorizes that
decision. Historical detail belongs in evidence reports or the changelog, not in
the live status surface.

Every handoff must state whether `STATUS.md` was reviewed and whether it required
an update.

## GitHub Task Tracking Contract

The [BLOC Thesis Prototype project](https://github.com/users/VascoMS/projects/1)
is the operational tracker for roadmap execution. `docs/ROADMAP.md` owns the
top-down milestone objectives, `docs/STATUS.md` owns current milestone and
blocker state, and GitHub issues own task-level scope, progress, acceptance
criteria, and validation evidence.

- Every non-trivial roadmap task must have one repository issue and project
  item before implementation begins.
- Set the issue's GitHub milestone and Project `Roadmap target` to the matching
  roadmap milestone. Keep `Status`, `Priority`, and `Area` current.
- Post material progress, blockers, evidence decisions, and validation outcomes
  to the issue. Do not reproduce that task diary in `STATUS.md`.
- Each issue must name the canonical local documents it can change. Update
  those documents in the same pull request as the behavior or evidence change.
- When work changes milestone state, major blockers, accepted evidence, the
  baseline, or immediate actions, update `STATUS.md` as well as the issue.
- Close an issue only after its acceptance and validation sections are
  satisfied or after recording why it was deliberately superseded.

## Repository Rules

- Keep durable cross-cutting documentation in root `docs/`.
- Keep operational deployment detail in the matching `deploy/*/README.md`.
- Prefer updating canonical docs over creating standalone Markdown notes.
- Temporary scratchpads must remain task-scoped; preserved historical evidence
  belongs under `docs/archive/`.
- Record major choices in `docs/DECISIONS.md` and implementation changes in
  `docs/CHANGELOG.md`.
- Keep generated evaluator output and local experiment logs in ignored
  `results/` directories.

## Git Workflow Ownership

- Begin and end every task by checking the branch, worktree status, and upstream
  divergence.
- Use one short-lived `codex/<task>` branch for non-trivial changes and do not
  reuse an old task branch for a new objective.
- Preserve user changes and stage only files belonging to the active task.
- Make focused commits after relevant validation passes.
- Before integration, fetch current remote refs and prove whether the task branch
  descends from `main`. Prefer fast-forward integration when it preserves the
  validated history.
- Never force-push shared history or delete an unmerged branch.
- Report the final branch, commit, validation, publication state, retained
  divergent work, and `STATUS.md` review outcome.

## Validation Shortcuts

Run tests from the affected module root:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

Useful local prototype checks:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8
./scripts/demo-local.sh
```

Use [docs/VALIDATION.md](docs/VALIDATION.md) to select stronger checks. Cloud
operations are never implied by an ordinary code or documentation task.

## Security Reminder

Prototype configs can contain trusted-dealer BTE shares and libp2p private keys.
Treat them as local demo material only. Do not claim production readiness without
DKG, public share verification, hardened custody, a cryptographic common coin,
and a real execution or signing boundary.
