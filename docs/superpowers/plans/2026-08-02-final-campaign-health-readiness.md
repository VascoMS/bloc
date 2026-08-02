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
