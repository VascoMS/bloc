# Final Campaign Race Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one practical automated local race gate that preserves complete deterministic campaign-bundle coverage and runs concurrency-relevant code under the race detector.

**Architecture:** A Bash 3.2-compatible entrypoint runs the complete bloc-node application tests normally, focused BTE and mempool race suites, and the remaining bloc-node application tests under `-race`. An exact-name skip expression excludes only the eleven CPU-heavy deterministic campaign identity, bundle, secret, and materialization tests from the bloc-node race invocation; those tests remain mandatory in the preceding normal invocation.

**Tech Stack:** Bash 3.2, Go test tooling, shell contract fixtures, Markdown canonical documentation.

## Global Constraints

- Do not change production protocol, BTE, bundle, corpus, schema, or campaign semantics.
- Do not weaken identity, CRS, secret, manifest, digest, or materialization validation.
- Keep the 12-second campaign completion boundary unchanged.
- Do not call AWS APIs, run Terraform plan/apply, write to ECR, create resources, or push Git history.
- The gate must be side-effect free apart from local Go build/test caches.
- Preserve user work and stage only files named by this plan.

---

### Task 1: Automated Split Race Gate

**Files:**
- Create: `scripts/tests/run-final-campaign-race-gate.sh`
- Create: `scripts/tests/test-final-campaign-race-gate-contract.sh`

**Interfaces:**
- Consumes: the repository module roots and Go test packages already used by `docs/VALIDATION.md`.
- Produces: executable `scripts/tests/run-final-campaign-race-gate.sh` with no arguments and exit status zero only when all four validation commands pass.

- [ ] **Step 1: Write the failing shell contract test**

Create `scripts/tests/test-final-campaign-race-gate-contract.sh`. It must create a temporary `go` executable that appends `PWD|arguments` to a call log, put that directory first on `PATH`, execute the missing gate, and compare the log with these four literal invocations in this order:

```text
$repo_root/bloc-node|test ./internal/app
$repo_root/bte/btd-impl-main|test -race ./be
$repo_root/mempool-il|test -race ./internal/mempool ./internal/api
$repo_root/bloc-node|test -race ./internal/app -skip ^(TestBuildAndLoadCampaignBundle|TestVerifyCampaignBundleWritesOnceAndChecksExpectedIdentities|TestBuildCampaignBundleRejectsInvalidFrozenInputs|TestLoadCampaignBundleRejectsMutations|TestBuildCampaignBundleRejectsBadSecretAndEscapingSymlink|TestBuildCampaignIdentityContainsNoDeploymentAddresses|TestGenCampaignIdentityWritesPrivateSecretsAndRefusesOverwrite|TestVerifyCampaignSecretsRejectsWrongP2PIdentity|TestVerifyCampaignSecretsRejectsWrongShareSet|TestMaterializeCampaignConfigPreservesFrozenInputsAcrossTopologies|TestMaterializeCampaignConfigRejectsInvalidPlacement)$
```

The test must also assert that the skip expression does not match
`TestFinalCampaignRejectsSyntheticBeforeExecution`.

- [ ] **Step 2: Run the contract test and verify RED**

Run:

```sh
bash scripts/tests/test-final-campaign-race-gate-contract.sh
```

Expected: non-zero exit because
`scripts/tests/run-final-campaign-race-gate.sh` does not exist.

- [ ] **Step 3: Implement the minimal gate**

Create `scripts/tests/run-final-campaign-race-gate.sh` with
`set -Eeuo pipefail`, derive `repo_root` from `BASH_SOURCE`, and execute exactly:

```sh
(cd "$repo_root/bloc-node" && env GOCACHE="${BLOC_NODE_GOCACHE:-/tmp/bloc-node-final-race}" go test ./internal/app)
(cd "$repo_root/bte/btd-impl-main" && env GOCACHE="${BTE_GOCACHE:-/tmp/bloc-bte-final-race}" go test -race ./be)
(cd "$repo_root/mempool-il" && env GOCACHE="${MEMPOOL_GOCACHE:-/tmp/bloc-mempool-final-race}" go test -race ./internal/mempool ./internal/api)
(cd "$repo_root/bloc-node" && env GOCACHE="${BLOC_NODE_GOCACHE:-/tmp/bloc-node-final-race}" go test -race ./internal/app -skip "$skip_tests")
```

Set `skip_tests` to the exact anchored expression from Step 1. Print one final
`final campaign race gate passed` line only after all commands succeed.

- [ ] **Step 4: Run the contract test and verify GREEN**

Run:

```sh
bash scripts/tests/test-final-campaign-race-gate-contract.sh
bash -n scripts/tests/run-final-campaign-race-gate.sh
```

Expected: the contract prints `final campaign race gate contract passed`; Bash
syntax exits zero.

- [ ] **Step 5: Run the real automated gate**

Run:

```sh
bash scripts/tests/run-final-campaign-race-gate.sh
```

Expected: complete normal bloc-node tests, focused BTE/mempool races, and the
remaining bloc-node race tests pass; the final success line is printed.

- [ ] **Step 6: Run existing regression checks**

Run:

```sh
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
bash scripts/test-campaign-runners.sh
cd latency-charts && .venv/bin/python -m pytest
```

Expected: 28 artifact tests, all final campaign shell contracts, runner
portability, and 37 chart tests pass.

- [ ] **Step 7: Commit the automated gate**

```sh
git add scripts/tests/run-final-campaign-race-gate.sh scripts/tests/test-final-campaign-race-gate-contract.sh
git commit -m "test: add practical final campaign race gate"
```

### Task 2: Canonical Evidence And Handoff

**Files:**
- Modify: `docs/VALIDATION.md`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/superpowers/plans/2026-08-02-final-campaign-readiness.md`
- Generated ignored output: `results/local/final-campaign-readiness-$(git rev-parse HEAD)/validation/preflight-summary.json`

**Interfaces:**
- Consumes: successful Task 1 output and the final committed source SHA.
- Produces: canonical race-gate command, resolved local blocker state, and an updated ignored preflight summary without changing accepted evidence or the last known good baseline.

- [ ] **Step 1: Update canonical validation**

In `docs/VALIDATION.md`, replace the impractical full-package bloc-node race
command for final campaign readiness with:

```sh
bash scripts/tests/run-final-campaign-race-gate.sh
```

Explain that the gate runs all deterministic bundle/materialization tests
normally, races the BTE implementation directly, races mempool API/state, and
races every other bloc-node application test. State explicitly that this is a
test-process split and does not weaken live bundle verification.

- [ ] **Step 2: Resolve the local validation blockers accurately**

Update `docs/STATUS.md` to record that:

- the isolated latency-charts environment runs 37/37 tests;
- the automated split race gate passes;
- the prior 10- and 30-minute full-package timeouts were CPU-bound BMax-128
  parsing with no race report; and
- remaining blockers are the final clean source/image freeze, two published and
  inspected linux/amd64 ECR digests, final n4/n7 manifests, account capacity,
  and separate live authorization.

Do not change the latest accepted milestone, accepted evidence, or last known
good baseline.

- [ ] **Step 3: Record implementation history and plan outcome**

Add one `2026-08-02` entry to `docs/CHANGELOG.md` for the practical race gate.
In `docs/superpowers/plans/2026-08-02-final-campaign-readiness.md`, replace the
old focused-race commands with the new entrypoint and record the observed reason
for the split.

- [ ] **Step 4: Validate and commit documentation**

Run:

```sh
git diff --check
rg -n 'run-final-campaign-race-gate|37/37|30-minute' docs/VALIDATION.md docs/STATUS.md docs/CHANGELOG.md docs/superpowers/plans/2026-08-02-final-campaign-readiness.md
```

Then commit:

```sh
git add docs/VALIDATION.md docs/STATUS.md docs/CHANGELOG.md docs/superpowers/plans/2026-08-02-final-campaign-readiness.md
git commit -m "docs: close final campaign local preflight blockers"
```

- [ ] **Step 5: Run final verification at committed HEAD**

Run:

```sh
bash scripts/tests/run-final-campaign-race-gate.sh
cd latency-charts && .venv/bin/python -m pytest
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-contract.sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
bash scripts/test-campaign-runners.sh
terraform -chdir=deploy/ec2/terraform validate
terraform -chdir=deploy/ec2/terraform-three-region validate
```

Expected: every local readiness check passes. No Terraform plan/apply or AWS
operation runs.

- [ ] **Step 6: Update ignored evidence and GitHub issue #15**

Write `preflight-summary.json` below the final-SHA ignored results root with
`registry_images_verified=false`, `aws_calls_performed=false`, the n4/n7 corpus
identities, 37 chart tests, and the successful split race gate. Post a concise
issue comment with the final branch/SHA, validation outcome, remaining ECR/AWS
gates, and confirmation that nothing was pushed and no AWS resources were
created.

- [ ] **Step 7: Report repository state**

Run:

```sh
git fetch origin --prune
git merge-base --is-ancestor origin/main HEAD
git rev-list --left-right --count origin/main...HEAD
git status --short --branch
```

Report the branch, commits, current divergence, validation, `STATUS.md` update,
remaining external blockers, and the no-push/no-AWS result.
