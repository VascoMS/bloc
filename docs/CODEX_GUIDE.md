# Codex Guide

## Purpose

This file explains how Codex should read this repository efficiently and how durable context is organized.

## Read Order

1. [AGENTS.md](/bloc/AGENTS.md)
2. [docs/STATUS.md](/bloc/docs/STATUS.md)
3. [README.md](/bloc/README.md)
4. The one canonical doc most relevant to the task
5. The module README for the affected module
6. Only the code files needed to act on the task

## Which Docs To Read

- Architecture or protocol changes:
  - `docs/STATUS.md`
  - `docs/ARCHITECTURE.md`
  - the affected canonical document under `docs/modules/`
  - `docs/DECISIONS.md`
  - `docs/WORKFLOWS.md`
  - `docs/VALIDATION.md`
- Workflow or process changes:
  - `docs/STATUS.md`
  - `docs/WORKFLOWS.md`
  - `docs/DEVELOPMENT.md`
- Validation or experiment work:
  - `docs/STATUS.md`
  - `docs/WORKFLOWS.md`
  - `docs/VALIDATION.md`
  - the relevant module README
- Small implementation changes:
  - `docs/STATUS.md`
  - the module README
  - `docs/WORKFLOWS.md`
  - `docs/VALIDATION.md`
  - `docs/CHANGELOG.md` only when recording the result

## Stable Context vs Temporary Context

Stable context belongs in:

- `AGENTS.md`
- `docs/STATUS.md`
- `README.md`
- root `docs/`
- canonical protocol deep dives under `docs/modules/`
- short module READMEs

Temporary or task-scoped context belongs in:

- task notes,
- temporary scratchpads,
- archived historical notes if the content should be preserved but is no longer canonical.

## Token-Minimization Rules

- Do not re-read every Markdown file in the repo for a narrow task.
- Do not paste long benchmark outputs or old debugging notes into prompts when the durable takeaway already exists in canonical docs.
- Build small task packets with objective, affected module, likely files, and required validation.
- Check `docs/STATUS.md` first so work follows the active milestone and immediate next actions.
- Re-read historical notes only when working on the exact subsystem or failure mode they describe.

## Documentation Rules For Codex

- No new standalone `.md` notes unless they are a temporary scratchpad, a task artifact, or a historical note intended for archive.
- Durable content must be merged into canonical docs.
- Update one clear source of truth instead of adding parallel explanations.
- Keep cross-module handoffs and invariants in `docs/ARCHITECTURE.md`; keep
  stage algorithms, state, wire details, and module limitations in the matching
  `docs/modules/` deep dive.
