# Issue 8 Distributed-Campaign Preflight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the superseded local p99 campaign with a bounded, validation-only distributed-campaign preflight and make issue #15 the first M5 thesis-performance evidence task.

**Architecture:** Canonical documents and GitHub issues define the evidence boundary before execution. The preflight runs the unchanged release-candidate executable from source `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`, uses short local evaluator matrices only for configuration and artifact-contract coverage, and binds the exact distributed `mock-placeholder` corpus/configuration through provenance plus the accepted release-candidate Compose rehearsal. Ignored artifacts are validated and chart-loaded, but no local timing statistic is promoted as thesis evidence.

**Tech Stack:** Markdown, Git/GitHub Project v2, Go evaluator CLI, Bash validation runners, Python artifact validators and latency-chart tooling.

## Global Constraints

- Executable source is exactly `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`.
- Distributed image identity is exactly `bloc-node@sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`.
- Evaluator schema is exactly `bloc-eval-suite/v3`.
- Seed is exactly `20260621`.
- Completed-within-deadline boundary is exactly 12 seconds.
- Primary cells are `n=4,t=3` and `n=7,t=5` at batches `8/32/128`, with one warmup and one measured observation per cell.
- Extension cells are `n=10,t=7` at batches `8/32/128` plus batch `512` at `n=4/7/10`, with one warmup and three measured observations per unique cell.
- The protocol corpus is `deploy/docker-compose/corpus/mock-targets.jsonl`, SHA-256 `52121653abf114f7230b19794433e2a71da8726df3472acd1ce184f4d4131cc2`, with distribution `28/50/12/8/2`.
- Local outputs are `classification=validation-only` and `performance_claims_allowed=false`.
- Local CPU, memory, and network measurements are out of scope.
- A required code, configuration, corpus, schema, image, or campaign-tooling change invalidates the frozen release candidate and stops issue #8.
- No AWS resource operation, image push, Git push, or pull request is authorized.

---

## File and State Map

- Modify `docs/STATUS.md`: select M5, keep evidence completeness open, and route immediate action through the preflight and then #15.
- Modify `docs/ROADMAP.md`: remove local p99/resource evidence from M5 and distinguish local preflight from VM performance evidence.
- Modify `docs/VALIDATION.md`: define the exact preflight contract, exclude local output from final RQ1/RQ2 performance claims, and correct stale milestone prose.
- Modify `docs/DECISIONS.md`: add Decision 0022 making final sidecar performance evidence VM-per-operator only.
- Modify `docs/CHANGELOG.md`: record the evidence-strategy change and, after execution, the accepted preflight.
- Modify `docs/superpowers/plans/2026-07-23-thesis-evaluation.md`: replace Task 9 with the bounded preflight and make Task 10 the first performance campaign.
- Update GitHub issues #8, #15, and #16 and Project 1 execution fields.
- Produce ignored `results/local/distributed-preflight-2bc8efc/`: exact-RC validation logs, three evaluator sub-campaigns, aggregate artifacts, diagnostic charts, a provenance manifest, and a non-performance report.

### Tracker identities

- Project: `PVT_kwHOBGYBgs4Bd3OA`
- Status field: `PVTSSF_lAHOBGYBgs4Bd3OAzhYVn9Q`
- `In progress`: `47fc9ee4`
- `Ready`: `f5265f77`
- `Done`: `98236657`
- Area field: `PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-U`; `evidence`: `4891ddf0`
- Priority field: `PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-Y`; `High`: `e07e7db7`
- Roadmap target field: `PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-c`; `M5`: `021eaba5`
- Issue #8 item: `PVTI_lAHOBGYBgs4Bd3OAzgzXewo`
- Issue #15 item: `PVTI_lAHOBGYBgs4Bd3OAzgz3_Ig`
- Issue #16 item: `PVTI_lAHOBGYBgs4Bd3OAzgz3_Jw`

---

### Task 1: Re-scope the M5 evidence contract and tracker

**Files:**
- Modify: `docs/STATUS.md`
- Modify: `docs/ROADMAP.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/DECISIONS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/superpowers/plans/2026-07-23-thesis-evaluation.md`

**Interfaces:**
- Consumes: the approved issue #8 design and frozen issue #14 release-candidate identity.
- Produces: one consistent M5 evidence policy and tracker ordering consumed by the preflight and issues #15/#16.

- [ ] **Step 1: Mark issue #8 in progress with exact Project fields**

Use `gh project item-edit` with the identities above:

```bash
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgzXewo --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn9Q --single-select-option-id 47fc9ee4
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgzXewo --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-U --single-select-option-id 4891ddf0
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgzXewo --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-Y --single-select-option-id e07e7db7
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgzXewo --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn-c --single-select-option-id 021eaba5
```

Expected: issue #8 is `In progress`, Area `evidence`, Priority `High`, Roadmap target `M5`.

- [ ] **Step 2: Replace issue #8's superseded local-p99 contract**

Update issue #8 to title `Run the distributed-campaign contract preflight` and body:

```markdown
## Objective
Prove locally that the frozen release candidate can execute and retain every planned distributed-campaign configuration before AWS resources are allocated.

This is validation evidence only. It does not produce thesis-performance measurements or local p99/scaling claims.

## Scope
- Use source `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`, image digest `sha256:ee99ceb095e241fb75af930e5b2c0674ba2fa32f63abba754882aa5611f7b754`, schema `bloc-eval-suite/v3`, seed `20260621`, and the committed weighted protocol corpus.
- Run `n=4/7`, batches `8/32/128`, with 1 warmup and 1 measured observation per cell.
- Run `n=10`, batches `8/32/128`, plus batch `512` at `n=4/7/10`, with 1 warmup and 3 measured observations per unique extension cell.
- Validate startup/teardown, attempt retention, outcome classification, cross-node consistency, 12-second primary completion, timing additivity, provenance, schema completeness, and chart-loader compatibility.
- Retain artifacts under `results/local/distributed-preflight-2bc8efc/` with `classification=validation-only` and `performance_claims_allowed=false`.
- Do not collect local CPU, memory, or network evidence.

## Dependencies
Blocked by #11, #12, #13, and #14. All four dependencies are complete.

## Acceptance criteria
- Every primary measured observation is successful, consistent, within 12 seconds, and artifact-valid.
- Every extension observation terminates consistently with a complete retained outcome; a deadline miss is retained but is not by itself a preflight failure.
- The exact release-candidate, image, corpus, configuration, seed, and schema identities are bound in the preflight manifest.
- The aggregate artifact loads through the chart pipeline without manual repair.
- The issue reports no local performance statistic as thesis evidence.
- Any required frozen-contract or tooling change stops the issue and records an invalidation decision.

## Validation
- Four Go module suites and latency-chart tests.
- Campaign-runner and side-effect-free deployment validation.
- Exact matrix count/outcome/provenance/additivity checks.
- Local Markdown link, ownership, coherence, and `git diff --check` review.

## Status impact
Selects M5 while leaving performance/resource evidence incomplete. On success, issue #15 becomes the immediate next thesis-performance task.
```

- [ ] **Step 3: Make #15 and #16 dependencies explicit**

Update issue #15's dependency section to:

```markdown
## Dependencies
Blocked by the successful distributed-campaign preflight (#8), frozen release candidate (#14), p99 tooling (#12), resource instrumentation (#11), and transaction corpus (#13).

This is the first M5 thesis-performance evidence task. Use the exact source, image, corpus, configuration, seed, and schema accepted by #8.
```

Update issue #16's dependency section to:

```markdown
## Dependencies
Blocked by the accepted matched same-region campaign (#15), successful distributed-campaign preflight (#8), frozen release candidate (#14), resource instrumentation (#11), p99 tooling (#12), and transaction corpus (#13).

Use the accepted M3 run only as a historical baseline, not as a substitute for the final matched campaign. Compare same-region and three-region results only after their manifests and configurations match.
```

Retain all other issue #15/#16 sections verbatim.

- [ ] **Step 4: Rewrite the canonical evidence policy**

Make these exact semantic changes:

- `STATUS.md`: set `Last reviewed` to `2026-07-27`, `Active milestone` to `M5. Performance, Scaling, And Resource Evidence`, keep M4 as latest complete, rewrite evidence completeness to require same-region/three-region performance and resource evidence but not local p99, and make the immediate actions #8 preflight followed by #15 and #16.
- `ROADMAP.md`: set M5 status to active; make the primary p99 matrix same-region and three-region VM only; list the local issue #8 matrix as a validation-only preflight deliverable; remove local resource evidence and the exact local performance bundle from deliverables.
- `VALIDATION.md`: define local `eval-suite` as correctness/configuration/artifact preflight only; add the exact primary and extension matrix and acceptance differences; explicitly prohibit local quantile, throughput, scaling, topology, resource, and local-versus-VM claims; make final RQ1/RQ2 performance evidence VM-only; replace the outdated M1 corrected-baseline paragraph; update the milestone table and state M4 complete/M5 active.
- `DECISIONS.md`: add Decision 0022, `Use local evaluation only as final-campaign preflight`, recording VM-per-operator performance evidence, validation-only local output, no local resource phase, and the issue #8 to #15 ordering.
- `CHANGELOG.md`: add a 2026-07-27 M5 evidence-strategy row naming the approved design, tracker re-scope, affected canonical docs, documentation/tracker validation, and issue #8 as the next action.
- `docs/superpowers/plans/2026-07-23-thesis-evaluation.md`: rename Task 9 to the distributed-campaign preflight with the exact 1/1 and 1/3 matrices; make Task 10 explicitly the first performance campaign; make Task 11 depend on accepted Task 10 manifests.

- [ ] **Step 5: Run the documentation and tracker consistency checks**

```bash
git diff --check
rg -n 'final p99 local|final local p99|complete corrected 315|M3 is the latest|M4 is active|local/VM campaigns' \
  docs/STATUS.md docs/ROADMAP.md docs/VALIDATION.md docs/superpowers/plans/2026-07-23-thesis-evaluation.md
gh issue view 8 --repo VascoMS/bloc --json title,body,state,milestone,projectItems
gh issue view 15 --repo VascoMS/bloc --json title,body,state,milestone,projectItems
gh issue view 16 --repo VascoMS/bloc --json title,body,state,milestone,projectItems
```

Expected: the stale-reference search has no matches; #8 is the preflight; #15 depends on #8 and is the first performance task; #16 follows #15.

Run this local Markdown-link check:

```bash
python3 - <<'PY'
import re
from pathlib import Path

roots = [Path("AGENTS.md"), *Path("docs").rglob("*.md")]
missing = []
for source in roots:
    text = source.read_text(encoding="utf-8")
    for target in re.findall(r"\[[^\]]+\]\(([^)]+)\)", text):
        if "://" in target or target.startswith("#"):
            continue
        path = target.split("#", 1)[0]
        if path and not (source.parent / path).resolve().exists():
            missing.append(f"{source}: {target}")
if missing:
    raise SystemExit("\n".join(missing))
print("local Markdown links resolve")
PY
```

- [ ] **Step 6: Commit the strategy and tracker-owned documentation**

```bash
git add docs/STATUS.md docs/ROADMAP.md docs/VALIDATION.md docs/DECISIONS.md \
  docs/CHANGELOG.md docs/superpowers/plans/2026-07-23-thesis-evaluation.md
git commit -m "docs: make local evaluation a distributed preflight"
```

---

### Task 2: Validate the unchanged release-candidate contract

**Files:**
- Produce ignored: `results/local/distributed-preflight-2bc8efc/logs/`
- Produce temporary: `.worktrees/issue-8-rc/`

**Interfaces:**
- Consumes: frozen commit `2bc8efc9269798a7f7ab58021f8b9bda1012ae5d`.
- Produces: exact-source module, chart, campaign-runner, and deployment-validation logs required before matrix execution.

- [ ] **Step 1: Create an exact-source detached worktree**

```bash
git check-ignore -q .worktrees
git worktree add --detach .worktrees/issue-8-rc 2bc8efc9269798a7f7ab58021f8b9bda1012ae5d
test "$(git -C .worktrees/issue-8-rc rev-parse HEAD)" = \
  "2bc8efc9269798a7f7ab58021f8b9bda1012ae5d"
test -z "$(git -C .worktrees/issue-8-rc status --short)"
```

Expected: the execution worktree is clean and detached at the frozen source.

- [ ] **Step 2: Record source, corpus, image, toolchain, host, and command provenance**

Create `results/local/distributed-preflight-2bc8efc/provenance.json` through `scripts/lib/campaign_artifacts.py write-json`. It must record:

- source SHA and clean detached-worktree status;
- image repository digest and `linux/amd64` contract;
- protocol corpus path, SHA-256, row distribution, and `mock-placeholder` role;
- schema, seed, 12-second deadline, BMax 512, exact matrices;
- Go, Python, Terraform, Docker, OS, architecture, and timestamp values;
- accepted transaction-source reference
  `results/release-candidate/2bc8efc9269798a7f7ab58021f8b9bda1012ae5d/validation/compose-mock-eval/`;
- `classification: validation-only`;
- `performance_claims_allowed: false`;
- `local_matrix_transaction_source: synthetic`;
- an explanation that synthetic direct submissions exercise every evaluator configuration while the separately accepted Compose artifact proves the frozen `mock-placeholder` corpus/materialization boundary.

Verify:

```bash
jq -e '
  .source_sha == "2bc8efc9269798a7f7ab58021f8b9bda1012ae5d" and
  .corpus.sha256 == "52121653abf114f7230b19794433e2a71da8726df3472acd1ce184f4d4131cc2" and
  .classification == "validation-only" and
  .performance_claims_allowed == false
' results/local/distributed-preflight-2bc8efc/provenance.json
```

- [ ] **Step 3: Run the four exact-source Go suites**

From `.worktrees/issue-8-rc`, use task-scoped caches and retain each log:

```bash
env GOCACHE=/tmp/bloc-issue8-go-build GOMODCACHE=/tmp/bloc-issue8-go-mod \
  go test ./... -count=1
```

Run that command separately from `bloc-node`, `mempool-il`,
`bte/btd-impl-main`, and `sbc/hbbft`, saving output as:

- `logs/bloc-node-go-test.log`
- `logs/mempool-il-go-test.log`
- `logs/bte-go-test.log`
- `logs/hbbft-go-test.log`

Expected: all four commands exit 0.

- [ ] **Step 4: Run chart and campaign-runner validation**

```bash
cd .worktrees/issue-8-rc/latency-charts
PYTHONPYCACHEPREFIX=/tmp/bloc-issue8-pycache python3 -m pytest

cd .worktrees/issue-8-rc
bash scripts/test-campaign-runners.sh
```

Save complete output as `logs/latency-charts-pytest.log` and
`logs/campaign-runners.log`.

Expected: chart tests and all validation-only runner checks pass without an external lifecycle operation.

- [ ] **Step 5: Run side-effect-free Terraform validation**

Use the already initialized Terraform directories in the main checkout:

```bash
terraform -chdir=deploy/ec2/terraform fmt -check -diff
terraform -chdir=deploy/ec2/terraform validate
terraform -chdir=deploy/ec2/terraform-three-region fmt -check -diff
terraform -chdir=deploy/ec2/terraform-three-region validate
```

Save output as `logs/terraform-single-fmt.log`,
`logs/terraform-single-validate.log`, `logs/terraform-three-region-fmt.log`,
and `logs/terraform-three-region-validate.log`.

Expected: all four commands exit 0 and no plan/apply is run.

---

### Task 3: Execute the bounded local configuration matrix

**Files:**
- Produce ignored: `results/local/distributed-preflight-2bc8efc/primary/`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/extension-n10/`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/extension-b512/`

**Interfaces:**
- Consumes: the exact-RC binary built by `go run` in the detached worktree and the validated provenance contract.
- Produces: six primary measured observations and eighteen extension measured observations, plus warmups and raw per-node artifacts.

- [ ] **Step 1: Run the six primary cells**

From `.worktrees/issue-8-rc/bloc-node`:

```bash
env GOCACHE=/tmp/bloc-issue8-go-build GOMODCACHE=/tmp/bloc-issue8-go-mod \
  go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent \
  --node-counts 4,7 \
  --batch-sizes 8,32,128 \
  --bmax 512 \
  --warmups 1 \
  --repetitions 1 \
  --repetition-blocks 1 \
  --seed 20260621 \
  --deadline 12s \
  --timeout 30s \
  --base-port 24000 \
  --experiment-id distributed-preflight-primary-2bc8efc \
  --out-dir /Users/vascosilva/Projects/bloc/results/local/distributed-preflight-2bc8efc/primary
```

Expected: 6 warmups and 6 measured rows are retained.

- [ ] **Step 2: Run the four unique n=10 extension cells**

```bash
env GOCACHE=/tmp/bloc-issue8-go-build GOMODCACHE=/tmp/bloc-issue8-go-mod \
  go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent \
  --node-counts 10 \
  --batch-sizes 8,32,128,512 \
  --bmax 512 \
  --warmups 1 \
  --repetitions 3 \
  --repetition-blocks 1 \
  --seed 20260621 \
  --deadline 12s \
  --timeout 30s \
  --base-port 28000 \
  --experiment-id distributed-preflight-extension-n10-2bc8efc \
  --out-dir /Users/vascosilva/Projects/bloc/results/local/distributed-preflight-2bc8efc/extension-n10
```

Expected: 4 warmups and 12 measured rows are retained.

- [ ] **Step 3: Run the remaining two batch-512 extension cells**

```bash
env GOCACHE=/tmp/bloc-issue8-go-build GOMODCACHE=/tmp/bloc-issue8-go-mod \
  go run ./cmd/bloc-node eval-suite \
  --execution-mode persistent \
  --node-counts 4,7 \
  --batch-sizes 512 \
  --bmax 512 \
  --warmups 1 \
  --repetitions 3 \
  --repetition-blocks 1 \
  --seed 20260621 \
  --deadline 12s \
  --timeout 30s \
  --base-port 26000 \
  --experiment-id distributed-preflight-extension-b512-2bc8efc \
  --out-dir /Users/vascosilva/Projects/bloc/results/local/distributed-preflight-2bc8efc/extension-b512
```

Expected: 2 warmups and 6 measured rows are retained.

- [ ] **Step 4: Stop on a frozen-contract failure**

If any command requires changing code, corpus, configuration semantics, schema,
image, or campaign tooling, do not patch it. Retain the failing subdirectory and
log, mark `provenance.json` status `invalid`, comment on issue #8 with the exact
command/configuration/failure class, and leave #8 open and Project status
`Blocked`.

---

### Task 4: Validate, aggregate, and classify the preflight

**Files:**
- Produce ignored: `results/local/distributed-preflight-2bc8efc/run_measurements.csv`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/node_measurements.csv`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/manifest.json`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/PREFLIGHT_REPORT.md`
- Produce ignored: `results/local/distributed-preflight-2bc8efc/charts/`

**Interfaces:**
- Consumes: all three complete evaluator outputs.
- Produces: one aggregate, chart-compatible validation artifact and a binary pass/fail decision.

- [ ] **Step 1: Assert exact measured counts and successful consistency**

```bash
python3 scripts/lib/campaign_artifacts.py assert-evaluator \
  --require-success \
  --csv results/local/distributed-preflight-2bc8efc/primary/run_measurements.csv \
  --expected 4/8=1 --expected 4/32=1 --expected 4/128=1 \
  --expected 7/8=1 --expected 7/32=1 --expected 7/128=1

python3 scripts/lib/campaign_artifacts.py assert-evaluator \
  --require-success \
  --csv results/local/distributed-preflight-2bc8efc/extension-n10/run_measurements.csv \
  --expected 10/8=3 --expected 10/32=3 --expected 10/128=3 --expected 10/512=3

python3 scripts/lib/campaign_artifacts.py assert-evaluator \
  --require-success \
  --csv results/local/distributed-preflight-2bc8efc/extension-b512/run_measurements.csv \
  --expected 4/512=3 --expected 7/512=3
```

Expected: all commands exit 0.

- [ ] **Step 2: Assert schema, deadline, provenance, node coverage, and additivity**

Run this focused Python contract check:

```bash
PYTHONPATH=latency-charts/src python3 - <<'PY'
import csv
import json
from collections import Counter, defaultdict
from pathlib import Path

from bloc_latency_charts.data import (
    load_experiment,
    validate_merge_plan_additivity,
    validate_stage_additivity,
)

root = Path("results/local/distributed-preflight-2bc8efc")
contracts = {
    "primary": {
        "warmup": {(4, 8): 1, (4, 32): 1, (4, 128): 1,
                   (7, 8): 1, (7, 32): 1, (7, 128): 1},
        "measured": {(4, 8): 1, (4, 32): 1, (4, 128): 1,
                     (7, 8): 1, (7, 32): 1, (7, 128): 1},
        "deadline_required": True,
    },
    "extension-n10": {
        "warmup": {(10, 8): 1, (10, 32): 1, (10, 128): 1, (10, 512): 1},
        "measured": {(10, 8): 3, (10, 32): 3, (10, 128): 3, (10, 512): 3},
        "deadline_required": False,
    },
    "extension-b512": {
        "warmup": {(4, 512): 1, (7, 512): 1},
        "measured": {(4, 512): 3, (7, 512): 3},
        "deadline_required": False,
    },
}
required_columns = {
    "run_id", "scenario_id", "phase", "nodes", "threshold", "batch_size",
    "success", "consistent", "outcome", "deadline_met", "timed_out", "error",
    "total_slot_us", "proposal_preparation_us", "acs_us", "merge_plan_us",
    "acs_output_decode_us", "agreed_set_us", "merge_us",
    "ciphertext_decode_us", "batch_plan_us", "share_generation_us",
    "threshold_wait_us", "combine_us", "materialization_us",
    "commit_to_plaintext_us", "measurement_block", "block_iteration",
    "schedule_seed", "planned_scenario_runs",
}

for name, contract in contracts.items():
    directory = root / name
    manifest = json.loads((directory / "manifest.json").read_text(encoding="utf-8"))
    assert manifest["schema_version"] == "bloc-eval-suite/v3", name
    assert manifest["status"] == "complete" and manifest["valid"] is True, name
    assert manifest["seed"] == 20260621, name
    assert manifest["deadline"] == "12s", name
    assert manifest["bmax"] == 512, name
    assert manifest["tx_source"] == "synthetic", name

    with (directory / "run_measurements.csv").open(newline="", encoding="utf-8-sig") as handle:
        reader = csv.DictReader(handle)
        assert not required_columns.difference(reader.fieldnames or []), name
        rows = list(reader)

    for phase in ("warmup", "measured"):
        actual = Counter(
            (int(row["nodes"]), int(row["batch_size"]))
            for row in rows if row["phase"] == phase
        )
        assert actual == Counter(contract[phase]), (name, phase, actual)

    measured = [row for row in rows if row["phase"] == "measured"]
    for row in measured:
        assert row["success"].lower() == "true", (name, row["run_id"])
        assert row["consistent"].lower() == "true", (name, row["run_id"])
        assert row["outcome"] == "completed", (name, row["run_id"])
        assert row["timed_out"].lower() == "false", (name, row["run_id"])
        assert not row["error"], (name, row["run_id"])
        assert int(row["schedule_seed"]) == 20260621, (name, row["run_id"])
        if contract["deadline_required"]:
            assert row["deadline_met"].lower() == "true", (name, row["run_id"])

    with (directory / "node_measurements.csv").open(newline="", encoding="utf-8-sig") as handle:
        node_rows = [row for row in csv.DictReader(handle) if row["phase"] == "measured"]
    by_run = defaultdict(set)
    expected_nodes = {}
    for row in measured:
        expected_nodes[row["run_id"]] = int(row["nodes"])
    for row in node_rows:
        by_run[row["run_id"]].add(int(row["node_id"]))
    assert set(by_run) == set(expected_nodes), name
    for run_id, nodes in expected_nodes.items():
        assert by_run[run_id] == set(range(nodes)), (name, run_id, by_run[run_id])

    experiment = load_experiment(directory)
    validate_stage_additivity(experiment.runs, tolerance_us=20)
    validate_merge_plan_additivity(experiment.runs, tolerance_us=20)

print("distributed preflight artifact contract passed")
PY
```

Expected output: `distributed preflight artifact contract passed`.

- [ ] **Step 3: Merge chart inputs and write the aggregate manifest**

```bash
python3 scripts/lib/campaign_artifacts.py merge-csv \
  --output results/local/distributed-preflight-2bc8efc/run_measurements.csv \
  results/local/distributed-preflight-2bc8efc/primary/run_measurements.csv \
  results/local/distributed-preflight-2bc8efc/extension-n10/run_measurements.csv \
  results/local/distributed-preflight-2bc8efc/extension-b512/run_measurements.csv

python3 scripts/lib/campaign_artifacts.py merge-csv \
  --output results/local/distributed-preflight-2bc8efc/node_measurements.csv \
  results/local/distributed-preflight-2bc8efc/primary/node_measurements.csv \
  results/local/distributed-preflight-2bc8efc/extension-n10/node_measurements.csv \
  results/local/distributed-preflight-2bc8efc/extension-b512/node_measurements.csv
```

Write aggregate `manifest.json` with:

- schema `bloc-distributed-preflight/v1`;
- status `complete`, valid `true`;
- the frozen source/image/corpus/evaluator identities;
- exact primary/extension counts and acceptance rules;
- paths to all component manifests and validation logs;
- `classification: validation-only`;
- `performance_claims_allowed: false`;
- `resource_measurements_collected: false`;
- `local_matrix_transaction_source: synthetic`;
- distributed transaction source `mock-placeholder` and accepted Compose reference;
- no-mixed-candidate rule.

- [ ] **Step 4: Prove chart compatibility**

```bash
cd latency-charts
PYTHONPATH=src PYTHONPYCACHEPREFIX=/tmp/bloc-issue8-pycache python3 -m bloc_latency_charts \
  ../results/local/distributed-preflight-2bc8efc \
  --output-dir ../results/local/distributed-preflight-2bc8efc/charts
```

Expected: the loader and stage-additivity checks pass and diagnostic figures are
created. Every figure remains ignored and validation-only.

- [ ] **Step 5: Write the non-performance preflight report**

`PREFLIGHT_REPORT.md` must state only:

- pass/fail classification;
- exact source, image, corpus, schema, seed, and matrix identities;
- planned and retained attempt counts;
- whether primary deadline coverage passed;
- whether extensions terminated consistently;
- whether schema/provenance/additivity/chart checks passed;
- that no resource phase ran;
- `classification=validation-only`;
- `performance_claims_allowed=false`;
- that no p50/p95/p99/max/throughput/scaling value is eligible for thesis use;
- that #15 is the next evidence task after acceptance.

- [ ] **Step 6: Remove the temporary exact-source worktree**

After every process is stopped and artifacts are retained:

```bash
git worktree remove .worktrees/issue-8-rc
git worktree list
```

Expected: only intended persistent worktrees remain; the frozen commit itself is
still reachable and no branch is deleted.

---

### Task 5: Register the accepted preflight and hand M5 to issue #15

**Files:**
- Modify: `docs/STATUS.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: a passing `bloc-distributed-preflight/v1` manifest and report.
- Produces: accepted validation evidence, a closed #8, and #15 as the immediate ready task.

- [ ] **Step 1: Record the accepted validation outcome**

Update:

- `STATUS.md`: retain M5 active; add the preflight root to the last-known-good validation evidence; keep performance/resource evidence completeness open; make #15 the first immediate action and #16 follow #15.
- `VALIDATION.md`: record the preflight root and pass result without timing values; reiterate that it is not RQ1/RQ2 performance evidence.
- `CHANGELOG.md`: add a 2026-07-27 M5 issue #8 row with the exact matrix counts, artifact root, frozen identities, successful contract/chart validation, no resource collection, and #15 as the next action.

- [ ] **Step 2: Re-run repository consistency validation**

```bash
git diff --check
rg -n 'final p99 local|final local p99|complete corrected 315|M3 is the latest|M4 is active|local/VM campaigns' \
  docs/STATUS.md docs/ROADMAP.md docs/VALIDATION.md docs/superpowers/plans/2026-07-23-thesis-evaluation.md
```

Run the Task 1 Markdown-link checker again.

Verify ignored evidence and tracked scope:

```bash
git check-ignore -v results/local/distributed-preflight-2bc8efc/manifest.json
git status --short
```

Expected: only the three intended tracked documentation files are modified and
the evidence root is ignored.

- [ ] **Step 3: Commit the accepted preflight registration**

```bash
git add docs/STATUS.md docs/VALIDATION.md docs/CHANGELOG.md
git commit -m "docs: register distributed campaign preflight"
```

- [ ] **Step 4: Post the exact issue #8 validation outcome and close it**

Add an issue #8 comment containing:

- branch and both implementation commit SHAs;
- artifact root;
- exact release-candidate/image/corpus/schema identities;
- primary `6/6` and extension `18/18` measured consistency result;
- primary deadline result;
- module/chart/runner/Terraform validation outcomes;
- `classification=validation-only`;
- `performance_claims_allowed=false`;
- no AWS/resource/push activity;
- #15 as the next task.

Close #8 as completed only after the local commit exists and every result above
is true.

- [ ] **Step 5: Move Project execution to issue #15**

```bash
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgzXewo --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn9Q --single-select-option-id 98236657
gh project item-edit --id PVTI_lAHOBGYBgs4Bd3OAzgz3_Ig --project-id PVT_kwHOBGYBgs4Bd3OA \
  --field-id PVTSSF_lAHOBGYBgs4Bd3OAzhYVn9Q --single-select-option-id f5265f77
```

Keep #16 in Backlog.

- [ ] **Step 6: Perform the final branch and publication audit**

```bash
git status --short --branch
git rev-list --left-right --count main...HEAD
git rev-list --left-right --count origin/main...HEAD
git log --oneline --decorate -5
gh issue view 8 --repo VascoMS/bloc --json title,state,closedAt,projectItems
gh issue view 15 --repo VascoMS/bloc --json title,state,projectItems,body
gh issue view 16 --repo VascoMS/bloc --json title,state,projectItems,body
```

Expected: branch `codex/issue-8-distributed-preflight` is clean, local commits
are not pushed, #8 is closed/Done, #15 is open/Ready, #16 is open/Backlog, and
no AWS resource or image publication occurred.
