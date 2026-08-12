# Final Campaign Image-Pull Retry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make final-campaign digest-pinned image distribution recover from at
most two transient SSH failures while failing immediately after any host image
exhausts three attempts.

**Architecture:** Keep the existing private-ECR command and bounded SSH
transport unchanged. Add a three-attempt loop around one exact image operation,
then make the operator/controller traversal explicitly propagate every failure.
The task-branch helper is copied byte-for-byte into the detached frozen worktree
only after focused tests pass.

**Tech Stack:** Bash 3.2+, OpenSSH, Docker CLI, AWS CLI/ECR, `jq`, Terraform,
Python `unittest`.

## Global Constraints

- Frozen source remains `cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f`.
- BLOC image remains `632783683536.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:a58d8ef4ef5a674ce89341538798b47a422ffdc66d72637d8b3f4351282a2eec`.
- Mempool image remains `632783683536.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:3c0c147a92d66c89293f9bda89967bded2ae22795bd37de09fa466ca4dbe38aa`.
- Do not change the bundle, corpus, schema, topology, schedule, evaluator
  arguments, configuration, or protocol semantics.
- Every retry repeats the existing full digest, `RepoDigests`, and
  `linux/amd64` checks; no tag fallback, rebuild, or substitution is allowed.
- Do not call AWS APIs, run Terraform plan/apply, write to ECR, create EC2
  resources, push Git commits, or launch p6 in this implementation task.

---

### Task 1: Bound One Exact Image Operation

**Files:**
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh:260-265`

**Interfaces:**
- Consumes: `final_ssh key host command`, which already bounds connection and
  server-alive behavior.
- Produces: `final_pull_one_image key host image`, returning zero on the first
  verified pull and nonzero after exactly three failed attempts.

- [ ] **Step 1: Add the transient-recovery and exhaustion regression**

Add this focused block after the existing SCP retry tests and before the
remote-job tests:

```bash
if task6_selected image-pull-retry; then
  image_pull_log="$fixture/image-pull.log"
  image_pull_attempts=0
  image_pull_fail_until=2
  image_pull_sleeps=0
  final_ssh() {
    image_pull_attempts=$((image_pull_attempts + 1))
    printf '%s\n' "$*" >>"$image_pull_log"
    [[ "$image_pull_attempts" -gt "$image_pull_fail_until" ]]
  }
  sleep() { image_pull_sleeps=$((image_pull_sleeps + 1)); }
  test_image='123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'

  final_pull_one_image test-key.pem 192.0.2.10 "$test_image" || {
    echo "image pull did not recover on its third bounded attempt" >&2
    exit 1
  }
  [[ "$image_pull_attempts" -eq 3 && "$image_pull_sleeps" -eq 2 ]] || {
    echo "image pull used an unexpected recovery retry schedule" >&2
    exit 1
  }
  grep -Fq "docker pull '$test_image'" "$image_pull_log" || {
    echo "image pull no longer uses the exact digest reference" >&2
    exit 1
  }
  grep -Fq "Architecture}}')\" = amd64" "$image_pull_log" || {
    echo "image pull no longer verifies amd64 architecture" >&2
    exit 1
  }
  grep -Fq "grep -F '@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'" "$image_pull_log" || {
    echo "image pull no longer verifies the requested RepoDigest" >&2
    exit 1
  }

  : >"$image_pull_log"
  image_pull_attempts=0
  image_pull_fail_until=99
  image_pull_sleeps=0
  if final_pull_one_image test-key.pem 192.0.2.10 "$test_image"; then
    echo "image pull accepted an operation that exhausted every attempt" >&2
    exit 1
  fi
  [[ "$image_pull_attempts" -eq 3 && "$image_pull_sleeps" -eq 2 ]] || {
    echo "image pull did not stop at the bounded attempt limit" >&2
    exit 1
  }
  unset -f final_ssh sleep
fi
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```sh
TASK6_CASE=image-pull-retry bash scripts/tests/test-final-campaign-lifecycle.sh
```

Expected: FAIL with `image pull did not recover on its third bounded attempt`
because the current helper calls `final_ssh` only once.

- [ ] **Step 3: Add the minimal three-attempt loop**

Replace `final_pull_one_image` with:

```bash
final_pull_one_image() {
  local key="$1" host="$2" image="$3" region registry digest attempt=1
  region="$(sed -E 's#^[0-9]{12}\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com/.*#\1#' <<<"$image")"
  registry="${image%%/*}"; digest="${image##*@}"
  while [[ "$attempt" -le 3 ]]; do
    if final_ssh "$key" "$host" "aws ecr get-login-password --region '$region' | docker login --username AWS --password-stdin '$registry' >/dev/null && docker pull '$image' >/dev/null && test \"\$(docker image inspect '$image' --format '{{.Architecture}}')\" = amd64 && docker image inspect '$image' --format '{{join .RepoDigests \"\\n\"}}' | grep -F '@$digest' >/dev/null"; then
      return 0
    fi
    [[ "$attempt" -eq 3 ]] || sleep 2
    attempt=$((attempt + 1))
  done
  return 1
}
```

- [ ] **Step 4: Run the focused test and verify GREEN**

```sh
TASK6_CASE=image-pull-retry bash scripts/tests/test-final-campaign-lifecycle.sh
```

Expected: PASS with `final campaign lifecycle tests passed`.

- [ ] **Step 5: Commit the bounded operation**

```sh
git add scripts/lib/final-campaign-lifecycle.sh scripts/tests/test-final-campaign-lifecycle.sh
git commit -m "fix(issue-15): retry exact image pulls"
```

---

### Task 2: Stop At The First Exhausted Host Image

**Files:**
- Modify: `scripts/tests/test-final-campaign-lifecycle.sh`
- Modify: `scripts/lib/final-campaign-lifecycle.sh:267-276`

**Interfaces:**
- Consumes: the Task 1 `final_pull_one_image` return status.
- Produces: `final_pull_verify_images artifact_root`, returning immediately on
  the first failed operator BLOC image, operator mempool image, or controller
  BLOC image.

- [ ] **Step 1: Add the masking regression**

Append inside the `image-pull-retry` test block, before unsetting its fakes:

```bash
  image_inventory_root="$fixture/image-inventory"
  mkdir -p "$image_inventory_root"
  printf '%s\n' '{"controller":{"public_ip":"192.0.2.1"},"nodes":[{"id":0,"public_ip":"192.0.2.10"},{"id":1,"public_ip":"192.0.2.11"}]}' >"$image_inventory_root/inventory.json"
  FINAL_BLOC_IMAGE="$test_image"
  FINAL_MEMPOOL_IMAGE='123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
  image_verify_calls=()
  final_topology_key_for_host() { printf 'test-key.pem\n'; }
  final_pull_one_image() {
    image_verify_calls+=("$2|$3")
    [[ "$2" != 192.0.2.10 || "$3" != "$FINAL_BLOC_IMAGE" ]]
  }
  if final_pull_verify_images "$image_inventory_root"; then
    echo "image verification masked the first operator failure" >&2
    exit 1
  fi
  [[ "${#image_verify_calls[@]}" -eq 1 ]] || {
    echo "image verification continued after the first operator failure" >&2
    exit 1
  }
  unset -f final_topology_key_for_host final_pull_one_image
```

- [ ] **Step 2: Run the focused test and verify RED**

```sh
TASK6_CASE=image-pull-retry bash scripts/tests/test-final-campaign-lifecycle.sh
```

Expected: FAIL with `image verification masked the first operator failure`
because the current loop continues to later images and returns the controller's
successful status.

- [ ] **Step 3: Propagate every image failure explicitly**

Change the body of `final_pull_verify_images` to:

```bash
  while IFS= read -r host_json; do
    host="$(jq -r .public_ip <<<"$host_json")"; key="$(final_topology_key_for_host "$host_json")"
    final_pull_one_image "$key" "$host" "$FINAL_BLOC_IMAGE" || return 1
    final_pull_one_image "$key" "$host" "$FINAL_MEMPOOL_IMAGE" || return 1
  done < <(jq -c '.nodes[]' "$artifact_root/inventory.json")
  host_json="$(jq -c .controller "$artifact_root/inventory.json")"; host="$(jq -r .public_ip <<<"$host_json")"
  key="$(final_topology_key_for_host "$host_json")"
  final_pull_one_image "$key" "$host" "$FINAL_BLOC_IMAGE" || return 1
```

- [ ] **Step 4: Run focused and complete lifecycle tests**

```sh
TASK6_CASE=image-pull-retry bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
```

Expected: all four commands pass.

- [ ] **Step 5: Commit fail-fast traversal**

```sh
git add scripts/lib/final-campaign-lifecycle.sh scripts/tests/test-final-campaign-lifecycle.sh
git commit -m "fix(issue-15): fail fast on image verification"
```

---

### Task 3: Freeze, Validate, And Document The Correction

**Files:**
- Modify: `.worktrees/issue-15-frozen-cf36/scripts/lib/final-campaign-lifecycle.sh`
- Modify: `deploy/ec2/README.md`
- Modify: `docs/VALIDATION.md`
- Modify: `docs/STATUS.md`
- Modify: `docs/CHANGELOG.md`

**Interfaces:**
- Consumes: the tested task-branch lifecycle helper and frozen n7 bundle.
- Produces: a byte-identical frozen execution overlay, documented retry
  semantics, and a locally validated p6 command that remains separately gated.

- [ ] **Step 1: Apply the same helper patch to the frozen worktree**

Use `apply_patch` to make the Task 1 and Task 2 lifecycle changes in:

```text
.worktrees/issue-15-frozen-cf36/scripts/lib/final-campaign-lifecycle.sh
```

Then prove exact equality:

```sh
cmp scripts/lib/final-campaign-lifecycle.sh .worktrees/issue-15-frozen-cf36/scripts/lib/final-campaign-lifecycle.sh
shasum -a 256 scripts/lib/final-campaign-lifecycle.sh .worktrees/issue-15-frozen-cf36/scripts/lib/final-campaign-lifecycle.sh
```

Expected: `cmp` exits zero and both SHA-256 values are identical.

- [ ] **Step 2: Update canonical operational documentation**

In `deploy/ec2/README.md` and `docs/VALIDATION.md`, state that each host pulls
only the exact digest-addressed image, each image operation receives at most
three attempts over bounded SSH, every attempt repeats architecture/digest
verification, and exhaustion on any host stops the phase before services.

In `docs/STATUS.md`, replace the p5 decision blocker with the implemented local
correction and make the next action separate authorization for a from-zero p6.
Add a dated `docs/CHANGELOG.md` entry with the red/green and no-AWS evidence.

- [ ] **Step 3: Run the complete no-AWS campaign checks**

```sh
bash -n scripts/lib/final-campaign-lifecycle.sh scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-lifecycle.sh
bash scripts/tests/test-final-campaign-lifecycle.sh same-az
bash scripts/tests/test-final-campaign-lifecycle.sh three-region
bash scripts/tests/test-final-campaign-contract.sh
python3 -m unittest scripts.tests.test_campaign_artifacts
bash scripts/tests/test-final-campaign-race-gate-contract.sh
bash scripts/test-campaign-runners.sh
terraform -chdir=deploy/ec2/terraform fmt -check -diff
terraform -chdir=deploy/ec2/terraform validate
terraform -chdir=deploy/ec2/terraform-three-region fmt -check -diff
terraform -chdir=deploy/ec2/terraform-three-region validate
git diff --check
```

Expected: every command exits zero without contacting AWS.

- [ ] **Step 4: Validate the exact frozen p6 contract**

From `.worktrees/issue-15-frozen-cf36`, run:

```sh
bash deploy/ec2/run-same-az-campaign.sh \
  --phase latency \
  --bundle-root /Users/vascosilva/Projects/bloc/results/local/final-campaign-readiness-8818ff020d2c5c95345df7169e2bccf4bc914512/private-bundles/n7 \
  --node-count 7 \
  --source-sha cf36eb06bea12eb3b0fcfdfaf94a349c2dbe784f \
  --bloc-image 632783683536.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:a58d8ef4ef5a674ce89341538798b47a422ffdc66d72637d8b3f4351282a2eec \
  --mempool-image 632783683536.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:3c0c147a92d66c89293f9bda89967bded2ae22795bd37de09fa466ca4dbe38aa \
  --experiment-id bloc-ec2-i15-sa-n7-latency-p6 \
  --admin-cidr 148.69.201.111/32 \
  --aws-profile default \
  --validate-only
```

Expected: validation succeeds without sourcing a live topology adapter or
calling AWS, Terraform, Docker, SSH, SCP, or rsync.

- [ ] **Step 5: Commit documentation and update issue #15**

```sh
git add deploy/ec2/README.md docs/VALIDATION.md docs/STATUS.md docs/CHANGELOG.md
git commit -m "docs(issue-15): validate bounded image pulls"
```

Post the exact helper hash and validation result to issue #15, return its
Project item from Blocked to In Progress, and state that p6 still requires
separate live authorization. Do not push.
