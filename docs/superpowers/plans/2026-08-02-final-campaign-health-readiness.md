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

- [x] **Step 4: Verify, commit, and overlay**

Run the lifecycle test normally and with `same-az` and `three-region`, run `git diff --check`, commit the task files, and copy the exact lifecycle/test overlay into the detached frozen worktree.

- [ ] **Step 5: Validate and retry**

Pass the p6 `--validate-only` command, then execute the authorized n4 same-AZ readiness pilot. Accept it only if the readiness measurements, artifacts, and authenticated teardown all pass.

P6 was interrupted by the local execution channel before reaching the changed
boundary. Persistent-session p7 proved the ownership correction but exposed a
separate mismatch: the health gate probes host loopback port 8080 while Compose
keeps that port internal. Step 5 remains incomplete pending an explicit frozen
deployment invalidation decision.

### Task 4: Loopback-Only Mempool Health Binding

**Files:**
- Modify: `deploy/ec2/operator-compose.yaml`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: the existing host-loopback mempool health probe.
- Produces: host-local access to mempool port 8080 without VPC exposure or a change to the internal Compose protocol path.

- [x] **Step 1: Write and verify a failing resolved-Compose regression**

Require mempool target port 8080 to publish only on host IP `127.0.0.1` and host
port 8080. The current Compose model must fail because it publishes no port.

- [x] **Step 2: Add the single loopback-only binding**

Add `127.0.0.1:8080:8080` to the mempool service. Do not change its command,
image, volume, internal service name, or the BLOC provider URL.

- [x] **Step 3: Run focused and topology regressions**

Run the lifecycle test normally and with `same-az` and `three-region`; require
the resolved Compose port to use host IP `127.0.0.1` and reject wildcard
publication.

- [x] **Step 4: Commit, overlay, and validate**

Commit the approved Compose/test/plan change, apply the exact Compose overlay to
the detached frozen worktree, and pass p8 `--validate-only`.

- [ ] **Step 5: Run and validate p8**

Execute p8 in a persistent terminal. Accept it only if all readiness
observations, artifacts, and authenticated cleanup pass.

P8 proved the loopback binding: the host reached the mempool endpoint. It then
failed before measurement because the operator AMI does not install `jq`, which
the unchanged readiness command uses to verify the eight-item response. All 15
resources and both temporary keys were removed, and authenticated cleanup is
empty. Step 5 remains incomplete pending an explicit frozen deployment
invalidation decision for the missing host package.

### Task 5: Operator-Host JSON Parser Dependency

**Files:**
- Modify: `deploy/ec2/terraform/user-data.sh`
- Modify: `deploy/ec2/terraform-three-region/user-data.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: the existing host-side readiness command and Ubuntu package manager.
- Produces: explicit `jq` availability on every same-AZ and three-region EC2 host.

- [x] **Step 1: Write and verify a failing user-data regression**

Execute both real user-data scripts against a controlled `apt-get` boundary and
require their initial host-package installation to request `jq`. The current
scripts must fail because they install `awscli`, certificates, `curl`, and
`gnupg`, but not `jq`.

- [x] **Step 2: Add the single package dependency**

Add `jq` to the existing initial package list in both user-data variants. Do not
change the health command, AMI selection, images, protocol configuration,
corpus, source, or metric path.

- [x] **Step 3: Run focused and topology regressions**

Run the lifecycle test normally and with `same-az` and `three-region`, then run
Terraform validation for both topology modules.

- [x] **Step 4: Commit, overlay, and validate**

Commit the approved user-data/test/plan change, apply the exact user-data
overlay to the detached frozen worktree, and pass p9 `--validate-only`.

- [ ] **Step 5: Run and validate p9**

Execute the authorized p9 n4 same-AZ readiness pilot in a persistent terminal.
Accept it only if readiness, pilot measurements, artifact validation, and
authenticated cleanup all pass.

P9 passed every readiness gate, proving the explicit `jq` installation. It then
failed all three controller evaluator invocations because runtime UID `10001`
could not create output below the host-owned results bind mount. The loop did not
propagate those failures, live artifact validation was disabled, the empty phase
was incorrectly marked complete, recovery omitted Compose `NODE_ID`, and local
key cleanup was incomplete. The exact local key was removed manually; all cloud
resources, cloud keys, and Terraform state are empty. Step 5 remains incomplete.

### Task 6: Controller Measurement And Acceptance Integrity

**Files:**
- Modify: `scripts/lib/final-campaign-lifecycle.sh`
- Modify: `deploy/ec2/run-final-campaign.sh`
- Modify: `deploy/ec2/final-topology-same-az.sh`
- Modify: `deploy/ec2/final-topology-three-region.sh`
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`

**Interfaces:**
- Consumes: controller bind-mounted output, evaluator SSH status, recovered
  Compose logs, final artifact validator, and topology-local temporary keys.
- Produces: writable evaluator output, fail-closed measurement and acceptance,
  usable recovery logs, and complete local/cloud key cleanup.

- [ ] **Step 1: Write failing boundary regressions**

Require the controller results directory to be owned by `10001:10001`, any
evaluator SSH failure to fail measurement immediately, live execution to enable
artifact validation, recovery to supply each operator's `NODE_ID`, and both
topology destroy paths to remove their exact local temporary keys.

- [ ] **Step 2: Implement only the five proven corrections**

Change only the controller output-directory ownership, explicit evaluator error
propagation, live validator switch, recovery environment, and local-key removal.
Do not change source, images, corpus, schema, protocol configuration, schedule,
or metric semantics.

- [ ] **Step 3: Verify, commit, overlay, and validate**

Run focused and topology regressions plus Terraform validation, commit the
approved tooling change, apply the exact overlay to frozen source `cf36eb`, and
pass p10 `--validate-only`.

- [ ] **Step 4: Run and validate p10**

Execute p10 only after explicit approval. Require all nine retained readiness
pilot measurements, valid artifacts, usable logs, and authenticated local/cloud
cleanup before proceeding to primary collection.
