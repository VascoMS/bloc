# Workflows

## Standard Development Flow

1. Check the active branch, worktree state, and upstream divergence.
2. Read [STATUS.md](STATUS.md) and the minimum task-specific context routed by
   [AGENTS.md](../AGENTS.md).
3. Inspect the relevant source and tests before changing behavior.
4. Implement the smallest coherent change and preserve unrelated user work.
5. Run validation proportional to the affected behavior.
6. Update the owning canonical documentation when behavior, rationale,
   validation, or workflow changed.
7. Review `STATUS.md` against the maintenance contract below.
8. Commit only the task files after validation passes, then report branch,
   commit, validation, publication state, and status-review outcome.

## Task Context Packets

A useful task packet contains only:

- objective and explicit scope;
- affected module and likely files;
- protocol or artifact invariants;
- required validation; and
- relevant blockers or evidence constraints from `STATUS.md`.

Do not repeatedly load every canonical document, long benchmark output, or
unrelated historical notes. Read archived evidence only for the exact failure or
decision being investigated.

## Documentation Update Workflow

Update the document that owns the changed fact:

| Change | Canonical owner |
|---|---|
| Milestone, blocker, accepted/rejected evidence, baseline, or next action | `docs/STATUS.md` |
| System boundary or cross-module invariant | `docs/ARCHITECTURE.md` |
| Module algorithm, state, identity, concurrency, or limitation | affected `docs/modules/*.md` |
| Major design or workflow rationale | `docs/DECISIONS.md` |
| Generic development or documentation process | `docs/WORKFLOWS.md` or `docs/DEVELOPMENT.md` |
| Validation command, evidence meaning, or acceptance rule | `docs/VALIDATION.md` |
| Environment-specific operation | matching `deploy/*/README.md` |
| Implementation-level history | `docs/CHANGELOG.md` |

Do not create a new standalone note when an owner already exists. Module READMEs
remain the local entry points for commands and usage.

## Status Maintenance Workflow

Review [STATUS.md](STATUS.md) whenever a task changes milestone state, an open
blocker, accepted or rejected evidence, the last known good baseline, or
immediate next actions.

- Update changed facts in the same task.
- Remove or rewrite resolved blockers; do not retain them as current risks.
- Keep historical campaign narratives in reports or the changelog.
- Keep one explicitly selected active milestone. `not selected` is valid when no
  selection has been authorized.
- Never infer or activate the next roadmap milestone from sequence alone.
- At handoff, state whether status was reviewed and whether it changed.

## Validation Selection

Use the matrix and evidence definitions in [VALIDATION.md](VALIDATION.md). Module
tests are the default minimum for code changes. Local evaluator or demo runs are
appropriate for end-to-end protocol behavior. Cloud campaigns require explicit
authorization and are never implied by an ordinary implementation task.

Useful local checks:

```sh
cd bloc-node && go test ./...
cd mempool-il && go test ./...
cd bte/btd-impl-main && go test ./...
cd sbc/hbbft && go test ./...
cd latency-charts && python -m pytest
```

For a fast integrated check:

```sh
cd bloc-node
./scripts/demo-local.sh
```

## Experiment And Result Naming

Choose one campaign ID before a recorded run and use it for the result root,
manifest `experiment_id`, chart root, and comparison metadata.

Canonical campaign IDs use:

```text
<milestone>-<topology>-<workload>[-<variant>]-<utc-timestamp>
```

- Use lowercase ASCII, digits, and hyphens only.
- Format timestamps as `yyyyMMdd't'HHmmss'z'`.
- Use milestone terms `m1` through `m5` where applicable.
- Use topology terms such as `local`, `compose`, `same-az`, `cross-az`, or
  `three-region`.
- Use workload terms such as `synthetic`, `replay`, `libp2p`, `bte`, or a short
  fault name.
- Use variants such as `baseline`, `opt`, or `probe` only when they distinguish a
  meaningful analysis dimension.
- Do not use `v2`, `fixed`, `final`, `free`, or `step1`. A rerun gets a new
  timestamp and records its relationship to the earlier run in metadata.

Examples:

```text
m3-three-region-synthetic-20260718t120000z
m3-cross-az-synthetic-probe-20260715t093849z
m1-local-libp2p-baseline-20260715t081500z
m5-local-omit-proposal-20260715t140000z
```

Low-level EC2 phase IDs may require the runner's `bloc-ec2-` IAM prefix. Those
phase IDs are implementation identifiers beneath the canonical campaign and do
not define a second human-facing campaign name.

Store artifacts as:

```text
results/<environment>/<campaign-id>/
  manifest.json
  run_measurements.csv
  n4/
  n7/
  comparison/
results/charts/<campaign-id>/
```

`<environment>` is `local`, `distributed`, or `ec2`. Node-count and scenario
directories are children of one campaign. Link the canonical campaign root in
reports rather than a temporary phase-staging directory.

## Operational Runbooks

- Local Compose rehearsal and mock-placeholder smoke:
  [deploy/docker-compose/README.md](../deploy/docker-compose/README.md)
- VM/EC2 pilots, same/cross-AZ campaigns, three-region evidence, attribution,
  IAM, and teardown: [deploy/ec2/README.md](../deploy/ec2/README.md)
- Module-local evaluator, demo, and campaign entry points:
  [bloc-node/README.md](../bloc-node/README.md)
- BTE benchmarks: [bte/btd-impl-main/TESTING.md](../bte/btd-impl-main/TESTING.md)

## Artifacts And Scratchpads

- Generated evaluator output, charts, logs, configs, and credentials belong in
  ignored artifact directories.
- Public manifests must identify source SHA, image/binary identity, environment,
  workload, and acceptance state without including operator secrets.
- Scratchpads are temporary and task-scoped.
- Durable conclusions must be merged into the owning canonical document.
- `docs/archive/` is reserved for intentional historical evidence records whose
  detailed source or failure analysis remains useful.
