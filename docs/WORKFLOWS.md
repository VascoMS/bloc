# Workflows

## Standard Development Flow

1. Understand the task.
2. Check `docs/STATUS.md` for the active milestone, immediate next actions, and the last known good state.
3. Read the minimum relevant context.
4. Implement the smallest useful change.
5. Run the required validation.
6. Update canonical docs if behavior, workflow, roadmap state, or rationale changed.
7. Update `docs/STATUS.md` if a milestone started, completed, blocked, or gained a stronger validated baseline.
8. Summarize outcome, validation, and remaining gaps.

## Codex Task Flow

For agent-driven work, use this read order:

1. [AGENTS.md](/bloc/AGENTS.md)
2. [docs/CODEX_GUIDE.md](/bloc/docs/CODEX_GUIDE.md)
3. [docs/STATUS.md](/bloc/docs/STATUS.md)
4. The relevant canonical doc such as architecture or validation
5. The relevant module README
6. Only the code files needed for the task

## Task-Specific Context Packets

A good task packet for Codex should include only:

- objective,
- active milestone,
- affected module,
- likely files,
- required validation,
- relevant constraints or invariants.

Do not repeatedly paste long protocol summaries or benchmark outputs when the same information already lives in canonical docs.

## Documentation Update Workflow

Update docs based on the kind of change:

- Milestone state or next-step changes: update `docs/STATUS.md`
- Workflow or developer process changes: update `docs/WORKFLOWS.md` or `docs/DEVELOPMENT.md`
- Architecture or protocol boundary changes: update `docs/ARCHITECTURE.md` and `docs/DECISIONS.md`
- Validation command or acceptance-criteria changes: update `docs/VALIDATION.md`
- Small implementation changes: update `docs/CHANGELOG.md` and link the entry to the milestone it advanced

## Demo and Experiment Flow

Use the `bloc-node` demo flow when you need a fast end-to-end prototype check:

```sh
cd bloc-node
./scripts/demo-local.sh
```

Use `eval-local` for targeted experiments:

```sh
cd bloc-node
go run ./cmd/bloc-node eval-local --nodes 4 --batch-sizes 8
```

Use persistent `eval-suite` runs for repeated steady-state latency samples. The
named M1 profile starts one cluster for each operator count and runs fresh slots
through it; use `--execution-mode isolated` only when per-sample process
lifecycle validation is the intended evidence.

```sh
cd bloc-node
go run ./cmd/bloc-node eval-suite --profile m1-baseline --experiment-id m1-baseline --out-dir results/m1-local/baseline-persistent
```

Use BTE benchmarks for cryptographic full-path measurements:

```sh
cd bte/btd-impl-main
go test ./be -run '^$' -bench '^BenchmarkHybridFullPath'
```

When interpreting M1 results, remember that the integrated BTE path already
uses deterministic BEAT-MEV `Opt-2` sub-batching: `alpha = ceil(2*sqrt(B))`.
M1 is for integrated slot latency, not for comparing BTE optimization variants.
Use a separate M2 benchmark or evaluator sweep when the question is normal vs
`sqrt(B)` vs `2*sqrt(B)` vs parallel combine, or when testing batch sizes beyond
the M1 `BMax=128` profile.

## Distributed Sidecar Workflow

Use Docker Compose as the local bridge before cloud/Kubernetes work:

```sh
cd deploy/docker-compose
docker compose up --build
```

Then run the remote evaluator from `bloc-node`:

```sh
go run ./cmd/bloc-node eval-remote --config ../deploy/docker-compose/remote-eval.compose.json --experiment-id compose-smoke --batch-size 8 --repetitions 1 --out-dir results/distributed/compose-smoke
```

For Kubernetes, generate `cluster.json` with `--address-mode kubernetes`, create
the `bloc-cluster-config` ConfigMap from that generated file, apply the
manifests in `deploy/k8s/`, and use `eval-remote` with endpoint URLs that are
reachable from where the evaluator is running.

## Scratchpads vs Durable Docs

- Scratchpads are temporary and task-scoped.
- Canonical docs are durable and cross-task.
- `docs/STATUS.md` is the live project-state surface and should stay short.
- Historical debugging notes belong in `docs/archive/` after their durable lessons are extracted.
