# Final Campaign Health Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the final campaign wait for actual node readiness and retain usable Compose logs on failure.

**Architecture:** Keep the existing lifecycle and health contract. Poll the complete per-node contract every 10 seconds for at most 60 attempts, and run recovery logging with the same immutable BLOC and mempool image variables used at service startup.

**Tech Stack:** Bash 3.2, SSH, Docker Compose, existing shell regression harness.

## Global Constraints

- Do not change frozen source `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`, image digests, bundle, corpus, protocol behavior, or metric contract.
- Health success still requires node `/healthz`, node `/metrics`, mempool `/healthz`, and an eight-item corpus response.
- Retry only readiness failures; fail after 60 attempts with 10 seconds between attempts.

---

### Task 1: Bounded Health Readiness and Recovery Logs

**Files:**
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: `final_ssh`, `FINAL_BLOC_IMAGE`, `FINAL_MEMPOOL_IMAGE`, and campaign inventory.
- Produces: `final_wait_node_healthy key host`, bounded `final_health_gate`, and interpolatable recovery logs.

- [ ] **Step 1: Write failing shell regressions**

Add controlled `final_ssh` and `sleep` fakes proving that health retries until the third successful attempt, stops immediately after success, and fails after exactly 60 attempts. Capture the recovery command and require both `BLOC_IMAGE` and `MEMPOOL_IMAGE` assignments before `docker compose logs`.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `bash scripts/tests/test-final-campaign-lifecycle.sh`

Expected: failure because health is attempted once and recovery omits image variables.

- [ ] **Step 3: Implement the minimal lifecycle correction**

Add a 60-attempt, 10-second readiness loop around the unchanged health command. Add the two quoted immutable image assignments to the existing recovery log command.

- [ ] **Step 4: Run focused and topology regression tests**

Run:

```sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
```

Expected: all lifecycle and adapter contract tests pass.

- [ ] **Step 5: Validate and commit**

Run `git diff --check`, apply the identical lifecycle/test overlay to the detached frozen execution worktree, run the frozen `--validate-only` path, update canonical status, and commit only task files. Do not push.

### Task 2: Container-Readable Final Campaign Inputs

**Files:**
- Modify: `deploy/ec2/operator-compose.yaml`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: the materialized `cluster.json` value `crs_file: cluster.crs`, staged public files under `/etc/bloc`, and `final_ssh`.
- Produces: readable public bind mounts, a mode-0600 operator secret, and SSH calls with bounded connection establishment.

- [ ] **Step 1: Write failing deployment-boundary regressions**

Resolve the real Compose file and require a read-only `/config/cluster.crs` mount. Exercise staging through the existing SSH fake and require mode `0644` for `cluster.json`, `cluster.crs`, and `encrypted-corpus.json` while retaining mode `0600` for `operator.json`. Exercise `final_ssh` through an `ssh` fake and require `ConnectTimeout=10`, one connection attempt, and bounded server-alive settings.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `bash scripts/tests/test-final-campaign-lifecycle.sh`

Expected: failure because the canonical CRS target, explicit public modes, and SSH bounds are absent.

- [ ] **Step 3: Implement the minimal compatible correction**

Retain the legacy `/config/cluster.ec2.crs` bind target and add `/config/cluster.crs` for final configs. Set the three public inputs to `0644` after checksum-verified staging, retain the operator secret at `0600`, and add `ConnectTimeout=10`, `ConnectionAttempts=1`, `ServerAliveInterval=10`, and `ServerAliveCountMax=2` to `final_ssh`.

- [ ] **Step 4: Run focused and topology regressions**

Run the lifecycle test normally and with `same-az` and `three-region`, then run `docker compose config --format json` with immutable placeholder images and verify both CRS targets are read-only.

- [ ] **Step 5: Commit, overlay, and retry**

Run `git diff --check`, commit only the approved deployment/tooling files, apply their exact overlay to the detached frozen execution worktree, pass `p5 --validate-only`, and run the separately authorized n4 same-AZ readiness pilot without changing frozen protocol identities.

### Task 3: Private Operator Secret Runtime Ownership

**Files:**
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: the staged `/etc/bloc/operator.json` and frozen image runtime identity `10001:10001`.
- Produces: a runtime-readable private operator secret that remains mode `0600`.

- [x] **Step 1: Write a failing staging regression**

Exercise staging through the existing SSH fake and require the private operator secret to be owned by `10001:10001` while remaining mode `0600`. Keep public file ownership and modes unchanged.

- [x] **Step 2: Run the focused test and verify RED**

Run: `bash scripts/tests/test-final-campaign-lifecycle.sh`

Expected: failure because staging sets the private mode but not the frozen runtime ownership.

- [x] **Step 3: Implement the single staging correction**

After checksum-verified staging, assign only `/etc/bloc/operator.json` to UID/GID `10001:10001` and retain mode `0600`. Do not change its contents or any frozen protocol identity.

- [ ] **Step 4: Verify, commit, and overlay**

Run the lifecycle test normally and with `same-az` and `three-region`, run `git diff --check`, commit the task files, and copy the exact lifecycle/test overlay into the detached frozen worktree.

- [ ] **Step 5: Validate and retry**

Pass the p6 `--validate-only` command, then execute the authorized n4 same-AZ readiness pilot. Accept it only if the readiness measurements, artifacts, and authenticated teardown all pass.
