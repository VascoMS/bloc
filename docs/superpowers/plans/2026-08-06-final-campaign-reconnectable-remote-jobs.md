# Reconnectable Final-Campaign Remote Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run every final-campaign evaluator invocation exactly once across transient SSH disconnects.

**Architecture:** Add one controller-local job helper with an atomic directory claim and durable status files. The shared lifecycle sends the unchanged evaluator argv through an idempotent start request and uses bounded, reconnectable status polls instead of a foreground SSH process.

**Tech Stack:** Bash 3.2-compatible lifecycle code, Bash on Ubuntu controllers, SSH/SCP/rsync, existing shell regression suite.

## Global Constraints

- Do not change frozen source, image digests, corpus, evaluator argv semantics, schedule, schema, protocol, topology, or acceptance thresholds.
- Never re-execute an existing, ambiguous, or lost job identity.
- Use three idempotent start attempts, ten-second polls, and at most 180 polls per invocation.
- Do not run AWS or p4 until local validation is complete and live execution is explicitly authorized.

---

### Task 1: Atomic Controller Job Helper

**Files:**
- Create: `scripts/lib/final-remote-job.sh`
- Create: `scripts/tests/test-final-remote-job.sh`
- Modify: `scripts/test-campaign-runners.sh`

**Interfaces:**
- Consumes: `start <job-id> <command> [args...]` and `status <job-id>`.
- Produces: exactly-once directory claim plus `RUNNING`, `EXIT:<code>`, `MISSING`, `AMBIGUOUS`, or `LOST`.

- [x] Write a shell regression that starts one controlled command twice and requires one execution, atomic `EXIT:0`, and retained stdout/stderr.
- [x] Add nonzero-exit and preclaimed-directory cases requiring `EXIT:23` and `AMBIGUOUS` without execution.
- [x] Run `bash scripts/tests/test-final-remote-job.sh`; require failure because the helper does not exist.
- [x] Implement the minimal validated-ID, atomic-claim, detached-command, PID, log, and atomic-exit helper.
- [x] Run the focused test and require success; add it to runner portability and Bash syntax checks.

### Task 2: Reconnectable Lifecycle Integration

**Files:**
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`

**Interfaces:**
- Consumes: `final_run_remote_job <key> <host> <job-id> <command> [args...]`.
- Produces: bounded idempotent start/poll behavior and unchanged monotonic evaluator argv.

- [x] Add a regression whose first start response is lost after local launch, whose first two status connections fail, and which requires success with one command execution.
- [x] Add exhaustion and nonzero-status regressions requiring failure without a second execution.
- [x] Update the existing measurement failure and slot-allocation regressions to exercise `final_run_remote_job`, retaining exact starts `1/5/9` and the 30 primary starts through `2931`.
- [x] Run the lifecycle test and require the new boundary regressions to fail against foreground `final_ssh` execution.
- [x] Implement argument quoting, three idempotent start requests, 180 bounded status polls, terminal-state handling, controller staging, and job artifact recovery.
- [x] Run focused lifecycle, helper, final contract, 29 artifact, Bash syntax, diff, and complete runner portability checks.

### Task 3: Frozen Overlay And Readiness Handoff

**Files:**
- Modify: `deploy/ec2/README.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`
- Modify: issue #15 tracker state/comment

**Interfaces:**
- Consumes: tested task-branch helper and lifecycle files.
- Produces: byte-identical frozen tooling overlay and a validated p4 command awaiting live authorization.

- [x] Document reconnectable execution and the no-reexecution failure rule in the canonical runbook and validation contract.
- [x] Overlay the helper/lifecycle byte-for-byte onto detached frozen source and prove matching SHA-256 values.
- [x] Run exact n7 latency p4 `--validate-only` with the frozen bundle and digests.
- [x] Commit focused implementation/documentation changes, move issue #15 to In Progress, and post validation evidence.
- [x] Stop before AWS and report the exact live p4 command for separate authorization.
