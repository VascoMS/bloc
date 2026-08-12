#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$repo_root/scripts/lib/final-campaign-lifecycle.sh"

task6_case="${TASK6_CASE:-all}"
task6_selected() { [[ "$task6_case" == all || "$task6_case" == "$1" ]]; }

fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-lifecycle.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

ssh_log="$fixture/ssh.log"
ssh() { printf '%s\n' "$*" >"$ssh_log"; }
final_ssh test-key.pem 192.0.2.10 true
grep -Fq -- '-o ConnectTimeout=10' "$ssh_log" || { echo "SSH lacks a connection timeout" >&2; exit 1; }
grep -Fq -- '-o ConnectionAttempts=1' "$ssh_log" || { echo "SSH permits unbounded connection retries" >&2; exit 1; }
grep -Fq -- '-o ServerAliveInterval=10' "$ssh_log" || { echo "SSH lacks a server-alive interval" >&2; exit 1; }
grep -Fq -- '-o ServerAliveCountMax=2' "$ssh_log" || { echo "SSH lacks a bounded server-alive count" >&2; exit 1; }
unset -f ssh

scp_log="$fixture/scp.log"
scp_attempts=0
scp_fail_until=2
scp_sleeps=0
scp() {
  scp_attempts=$((scp_attempts + 1))
  printf '%s\n' "$*" >>"$scp_log"
  [[ "$scp_attempts" -gt "$scp_fail_until" ]]
}
sleep() { scp_sleeps=$((scp_sleeps + 1)); }

if ! final_scp test-key.pem source.tar 192.0.2.10 /tmp/source.tar; then
  echo "SCP did not recover on its third bounded attempt" >&2
  exit 1
fi
[[ "$scp_attempts" -eq 3 && "$scp_sleeps" -eq 2 ]] || {
  echo "SCP used an unexpected recovery retry schedule" >&2
  exit 1
}
grep -Fq -- '-o ConnectTimeout=10' "$scp_log" || { echo "SCP lacks a connection timeout" >&2; exit 1; }
grep -Fq -- '-o ConnectionAttempts=1' "$scp_log" || { echo "SCP permits unbounded connection retries" >&2; exit 1; }
grep -Fq -- '-o ServerAliveInterval=10' "$scp_log" || { echo "SCP lacks a server-alive interval" >&2; exit 1; }
grep -Fq -- '-o ServerAliveCountMax=2' "$scp_log" || { echo "SCP lacks a bounded server-alive count" >&2; exit 1; }

: >"$scp_log"
scp_attempts=0
scp_fail_until=99
scp_sleeps=0
if final_scp test-key.pem source.tar 192.0.2.10 /tmp/source.tar; then
  echo "SCP accepted a transfer that exhausted every attempt" >&2
  exit 1
fi
[[ "$scp_attempts" -eq 3 && "$scp_sleeps" -eq 2 ]] || {
  echo "SCP did not stop at the bounded attempt limit" >&2
  exit 1
}
unset -f scp sleep

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

remote_job_root="$fixture/remote-jobs"
remote_job_count="$fixture/remote-job-count"
remote_status_counter="$fixture/remote-status-counter"
remote_start_calls=0
remote_status_failures=2
printf '%s\n' 0 >"$remote_status_counter"
final_ssh() {
  local key="$1" host="$2" command status_calls
  shift 2
  command="$*"
  if [[ "$command" == *' start '* ]]; then
    remote_start_calls=$((remote_start_calls + 1))
    BLOC_REMOTE_JOB_ROOT="$remote_job_root" bash -c "$command" >/dev/null
    [[ "$remote_start_calls" -ne 1 ]]
    return
  fi
  status_calls=$(($(cat "$remote_status_counter") + 1))
  printf '%s\n' "$status_calls" >"$remote_status_counter"
  if [[ "$status_calls" -le "$remote_status_failures" ]]; then
    return 255
  fi
  BLOC_REMOTE_JOB_ROOT="$remote_job_root" bash -c "$command"
}
sleep() { /bin/sleep 0.01; }
FINAL_REMOTE_JOB_COMMAND="$repo_root/scripts/lib/final-remote-job.sh"
FINAL_REMOTE_JOB_MAX_POLLS=100
FINAL_REMOTE_JOB_POLL_INTERVAL=0.01

final_run_remote_job test-key.pem 192.0.2.10 reconnect-job \
  bash -c 'printf "run\n" >>"$1"' _ "$remote_job_count" || {
    echo "remote evaluator job did not reconnect after lost SSH responses" >&2
    exit 1
  }
[[ "$remote_start_calls" -eq 2 && "$(cat "$remote_status_counter")" -ge 3 ]] || {
  echo "remote evaluator job used an unexpected reconnect schedule" >&2
  exit 1
}
[[ "$(wc -l <"$remote_job_count" | tr -d ' ')" -eq 1 ]] || {
  echo "remote evaluator job was re-executed after a lost start response" >&2
  exit 1
}

remote_status_failures=999
printf '%s\n' 0 >"$remote_status_counter"
remote_start_calls=0
FINAL_REMOTE_JOB_MAX_POLLS=3
if final_run_remote_job test-key.pem 192.0.2.10 reconnect-job \
  bash -c 'printf "duplicate\n" >>"$1"' _ "$remote_job_count"; then
  echo "remote evaluator polling accepted status exhaustion" >&2
  exit 1
fi
remote_status_failures=0
printf '%s\n' 0 >"$remote_status_counter"
FINAL_REMOTE_JOB_MAX_POLLS=100
final_run_remote_job test-key.pem 192.0.2.10 reconnect-job \
  bash -c 'printf "duplicate\n" >>"$1"' _ "$remote_job_count" || {
    echo "completed remote evaluator job could not be reattached" >&2
    exit 1
  }
[[ "$(wc -l <"$remote_job_count" | tr -d ' ')" -eq 1 ]] || {
  echo "reattaching a completed remote evaluator job re-executed it" >&2
  exit 1
}

remote_status_failures=0
printf '%s\n' 0 >"$remote_status_counter"
remote_start_calls=0
if final_run_remote_job test-key.pem 192.0.2.10 failed-job bash -c 'exit 23' 2>/dev/null; then
  echo "remote evaluator accepted a nonzero job exit status" >&2
  exit 1
fi
[[ "$(BLOC_REMOTE_JOB_ROOT="$remote_job_root" bash "$repo_root/scripts/lib/final-remote-job.sh" status failed-job)" == EXIT:23 ]] || {
  echo "remote evaluator failure status was not durable" >&2
  exit 1
}
unset -f final_ssh sleep
unset FINAL_REMOTE_JOB_COMMAND FINAL_REMOTE_JOB_MAX_POLLS FINAL_REMOTE_JOB_POLL_INTERVAL

empty_cleanup="$fixture/empty-cleanup.json"
nonempty_cleanup="$fixture/nonempty-cleanup.json"
printf '%s\n' '{"regions":{"us-east-1":{"query_succeeded":true,"instances":[],"volumes":[],"vpcs":[],"subnets":[],"security_groups":[],"route_tables":[],"key_pairs":[],"peering_connections":[]}},"iam":{"query_succeeded":true,"roles":[],"instance_profiles":[]},"terraform_state":[]}' >"$empty_cleanup"
printf '%s\n' '{"regions":{"us-east-1":{"query_succeeded":true,"instances":["i-leftover"],"volumes":[],"vpcs":[],"subnets":[],"security_groups":[],"route_tables":[],"key_pairs":[],"peering_connections":[]}},"iam":{"query_succeeded":true,"roles":[],"instance_profiles":[]},"terraform_state":[]}' >"$nonempty_cleanup"
final_assert_cleanup_empty "$empty_cleanup"
if final_assert_cleanup_empty "$nonempty_cleanup"; then
  echo "cleanup assertion accepted a retained instance" >&2
  exit 1
fi

stage_root="$fixture/stage"
mkdir -p "$stage_root/bundle/secrets" "$stage_root/generated-public"
printf '%s\n' '{"file_sha256":{"encrypted-corpus.json":"corpus-hash"}}' >"$stage_root/bundle/bundle-manifest.json"
printf '%s\n' '{"controller":{"public_ip":"192.0.2.1"},"nodes":[{"id":0,"public_ip":"192.0.2.10"}]}' >"$stage_root/inventory.json"
stage_log="$stage_root/stage.log"
FINAL_BUNDLE_ROOT="$stage_root/bundle"
FINAL_REPO_ROOT="$repo_root"
final_topology_key_for_host() { printf 'test-key.pem\n'; }
final_ssh() { printf 'ssh|%s|%s\n' "$2" "$3" >>"$stage_log"; }
final_scp() {
  printf 'scp|%s|%s\n' "$3" "$4" >>"$stage_log"
  [[ "${FINAL_STAGE_FAIL_COPY:-0}" -eq 0 ]]
}
sleep() { :; }

FINAL_STAGE_FAIL_COPY=0
final_stage_hosts "$stage_root"
[[ "$(grep -c 'cloud-init status --wait' "$stage_log")" -eq 2 ]] || { echo "staging did not wait for every host" >&2; exit 1; }
grep -Fq 'sudo mkdir -p /opt/bloc/ec2/results' "$stage_log" || { echo "controller staging did not create its directory with sudo" >&2; exit 1; }
grep -Fq 'chmod 644 /etc/bloc/cluster.json /etc/bloc/cluster.crs /etc/bloc/encrypted-corpus.json' "$stage_log" || {
  echo "staging did not make public container inputs readable" >&2
  exit 1
}
grep -Fq 'chmod 600 /etc/bloc/operator.json' "$stage_log" || { echo "staging did not keep the operator secret private" >&2; exit 1; }
grep -Fq 'sudo chown 10001:10001 /etc/bloc/operator.json' "$stage_log" || {
  echo "staging did not assign the operator secret to the frozen runtime identity" >&2
  exit 1
}
grep -Fq 'run-final-remote-job.sh' "$stage_log" || {
  echo "controller staging omitted the reconnectable remote-job helper" >&2
  exit 1
}
if task6_selected controller-output; then
  grep -Fq 'sudo chown 10001:10001 /opt/bloc/ec2/results' "$stage_log" || {
    echo "controller results are not writable by the frozen runtime identity" >&2
    exit 1
  }
fi

: >"$stage_log"
FINAL_STAGE_FAIL_COPY=1
if final_stage_hosts "$stage_root"; then
  echo "staging ignored a failed copy" >&2
  exit 1
fi
[[ "$(grep -c '^scp|' "$stage_log")" -eq 1 ]] || { echo "staging continued after a failed copy" >&2; exit 1; }
unset -f final_topology_key_for_host final_ssh final_scp sleep

health_root="$fixture/health"
mkdir -p "$health_root"
printf '%s\n' '{"nodes":[{"id":0,"public_ip":"192.0.2.10"}]}' >"$health_root/inventory.json"
health_attempts=0
health_sleeps=0
health_always_fail=0
final_topology_key_for_host() { printf 'test-key.pem\n'; }
final_ssh() {
  health_attempts=$((health_attempts + 1))
  if [[ "$health_always_fail" -eq 1 ]]; then
    return 1
  fi
  [[ "$health_attempts" -ge 3 ]]
}
sleep() { health_sleeps=$((health_sleeps + 1)); }

final_health_gate "$health_root" || { echo "health gate did not retry until readiness" >&2; exit 1; }
[[ "$health_attempts" -eq 3 && "$health_sleeps" -eq 2 ]] || {
  echo "health gate used an unexpected retry schedule" >&2
  exit 1
}

health_attempts=0
health_sleeps=0
health_always_fail=1
if final_health_gate "$health_root" 2>/dev/null; then
  echo "health gate accepted a node that never became ready" >&2
  exit 1
fi
[[ "$health_attempts" -eq 60 && "$health_sleeps" -eq 59 ]] || {
  echo "health gate did not stop at the bounded attempt limit" >&2
  exit 1
}
unset -f final_topology_key_for_host final_ssh sleep

if task6_selected measurement-failure; then
  measurement_root="$fixture/measurement-failure"
  mkdir -p "$measurement_root"
  printf '%s\n' '{"controller":{"public_ip":"192.0.2.1"}}' >"$measurement_root/inventory.json"
  FINAL_EXPERIMENT_ID=measurement-failure FINAL_BLOCKS=1 FINAL_REPETITIONS=3
  FINAL_WARMUPS=1 FINAL_SEED=20260621 FINAL_DEADLINE=12s
  FINAL_BLOC_IMAGE='bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  FINAL_SOURCE_SHA=cccccccccccccccccccccccccccccccccccccccc
  measurement_attempts=0
  final_topology_key_for_host() { printf 'test-key.pem\n'; }
  final_ssh() { return 1; }
  final_run_remote_job() { measurement_attempts=$((measurement_attempts + 1)); return 1; }
  if final_execute_measurement "$measurement_root"; then
    echo "measurement accepted failed evaluator SSH commands" >&2
    exit 1
  fi
  [[ "$measurement_attempts" -eq 1 ]] || {
    echo "measurement continued after the first evaluator SSH failure" >&2
    exit 1
  }
  unset -f final_topology_key_for_host final_ssh final_run_remote_job
fi

measurement_slots_root="$fixture/measurement-slots"
mkdir -p "$measurement_slots_root"
printf '%s\n' '{"controller":{"public_ip":"192.0.2.1"}}' >"$measurement_slots_root/inventory.json"
measurement_commands="$measurement_slots_root/commands.log"
FINAL_EXPERIMENT_ID=measurement-slots FINAL_SEED=20260621 FINAL_DEADLINE=12s
FINAL_BLOC_IMAGE='bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
FINAL_SOURCE_SHA=cccccccccccccccccccccccccccccccccccccccc
final_topology_key_for_host() { printf 'test-key.pem\n'; }
final_ssh() { echo "measurement used foreground SSH" >&2; return 1; }
final_run_remote_job() { printf '%s\n' "$*" >>"$measurement_commands"; }

FINAL_BLOCKS=1 FINAL_REPETITIONS=3 FINAL_WARMUPS=1
: >"$measurement_commands"
final_execute_measurement "$measurement_slots_root"
readiness_slots="$(sed -n "s/.*--first-slot \([0-9][0-9]*\).*/\1/p" "$measurement_commands")"
[[ "$readiness_slots" == $'1\n5\n9' ]] || {
  echo "readiness measurement reused or skipped slots: ${readiness_slots:-missing --first-slot}" >&2
  exit 1
}

FINAL_BLOCKS=10 FINAL_REPETITIONS=1000 FINAL_WARMUPS=10
: >"$measurement_commands"
final_execute_measurement "$measurement_slots_root"
primary_slots="$(sed -n "s/.*--first-slot \([0-9][0-9]*\).*/\1/p" "$measurement_commands")"
expected_primary_slots=$'1\n111\n221\n331\n431\n531\n631\n731\n831\n931\n1031\n1131\n1231\n1331\n1431\n1531\n1631\n1731\n1831\n1931\n2031\n2131\n2231\n2331\n2431\n2531\n2631\n2731\n2831\n2931'
[[ "$primary_slots" == "$expected_primary_slots" ]] || {
  echo "primary measurement slot ranges overlap or contain gaps: ${primary_slots:-missing --first-slot}" >&2
  exit 1
}
unset -f final_topology_key_for_host final_ssh final_run_remote_job

recovery_root="$fixture/recovery"
mkdir -p "$recovery_root"
printf '%s\n' '{"controller":{"public_ip":"192.0.2.1"},"nodes":[{"id":0,"public_ip":"192.0.2.10"}]}' >"$recovery_root/inventory.json"
recovery_log="$recovery_root/recovery.log"
recovery_rsync_log="$recovery_root/rsync.log"
FINAL_BLOC_IMAGE='bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
FINAL_MEMPOOL_IMAGE='mempool@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
FINAL_SAMPLER=off
final_topology_key_for_host() { printf 'test-key.pem\n'; }
final_ssh() { printf '%s\n' "$3" >>"$recovery_log"; }
rsync() { printf '%s\n' "$*" >>"$recovery_rsync_log"; return 0; }

run_recovery() {
  local artifact_root="$1"
  final_recover_artifacts "$artifact_root"
}
run_recovery "$recovery_root"
grep -Fq "BLOC_IMAGE='$FINAL_BLOC_IMAGE'" "$recovery_log" || { echo "recovery omitted BLOC_IMAGE" >&2; exit 1; }
grep -Fq "MEMPOOL_IMAGE='$FINAL_MEMPOOL_IMAGE'" "$recovery_log" || { echo "recovery omitted MEMPOOL_IMAGE" >&2; exit 1; }
grep -Fq 'docker compose -f operator-compose.yaml logs --no-color' "$recovery_log" || { echo "recovery omitted Compose logs" >&2; exit 1; }
grep -Fq '/opt/bloc/ec2/jobs/' "$recovery_rsync_log" || { echo "recovery omitted controller job state" >&2; exit 1; }
grep -Fq -- '--timeout=60' "$recovery_rsync_log" || { echo "recovery rsync lacks a bounded I/O timeout" >&2; exit 1; }
grep -Fq -- '-o ConnectTimeout=10' "$recovery_rsync_log" || { echo "recovery rsync SSH lacks a connection timeout" >&2; exit 1; }
if grep -Fq '/opt/bloc/ec2/resources/' "$recovery_rsync_log"; then
  echo "latency recovery requested resource-sampler output while the sampler was off" >&2
  exit 1
fi
if task6_selected recovery-node-id; then
  grep -Fq "NODE_ID='0'" "$recovery_log" || { echo "recovery omitted operator NODE_ID" >&2; exit 1; }
fi

: >"$recovery_log"
: >"$recovery_rsync_log"
FINAL_SAMPLER=on
run_recovery "$recovery_root"
grep -Fq '/opt/bloc/ec2/resources/' "$recovery_rsync_log" || {
  echo "resource recovery omitted sampler output while the sampler was on" >&2
  exit 1
}
grep -Fq -- '--timeout=60' "$recovery_rsync_log" || { echo "resource recovery rsync lacks a bounded I/O timeout" >&2; exit 1; }
unset -f final_topology_key_for_host final_ssh rsync run_recovery

apt_log="$fixture/apt-get.log"
fake_bin="$fixture/fake-bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/apt-get" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$APT_GET_LOG"
[[ "${1:-}" != install ]] || exit 97
EOF
chmod +x "$fake_bin/apt-get"

for user_data in \
  "$repo_root/deploy/ec2/terraform/user-data.sh" \
  "$repo_root/deploy/ec2/terraform-three-region/user-data.sh"; do
  : >"$apt_log"
  status=0
  PATH="$fake_bin:/usr/bin:/bin" APT_GET_LOG="$apt_log" bash "$user_data" >/dev/null 2>&1 || status=$?
  [[ "$status" -eq 97 ]] || { echo "user data did not reach the controlled package boundary: $user_data" >&2; exit 1; }
  awk '
    $1 == "install" {
      for (i = 1; i <= NF; i++) if ($i == "jq") found = 1
    }
    END { exit found ? 0 : 1 }
  ' "$apt_log" || { echo "user data does not install jq: $user_data" >&2; exit 1; }
done

compose_json="$(
  NODE_ID=0 \
  BLOC_IMAGE='123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  MEMPOOL_IMAGE='123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
  docker compose -f "$repo_root/deploy/ec2/operator-compose.yaml" config --format json
)"
jq -e '.services["bloc-node"].volumes | any(.target == "/config/cluster.crs" and .read_only == true)' <<<"$compose_json" >/dev/null || {
  echo "Compose does not expose the canonical CRS path read-only" >&2
  exit 1
}
jq -e '.services["bloc-node"].volumes | any(.target == "/config/cluster.ec2.crs" and .read_only == true)' <<<"$compose_json" >/dev/null || {
  echo "Compose no longer exposes the legacy EC2 CRS path" >&2
  exit 1
}
jq -e '(.services["mempool-il"].ports // []) | any(.target == 8080 and .published == "8080" and .host_ip == "127.0.0.1" and .protocol == "tcp")' <<<"$compose_json" >/dev/null || {
  echo "Compose does not expose mempool health on loopback-only port 8080" >&2
  exit 1
}
jq -e 'all((.services["mempool-il"].ports // [])[]; .target != 8080 or .host_ip == "127.0.0.1")' <<<"$compose_json" >/dev/null || {
  echo "Compose exposes mempool port 8080 beyond host loopback" >&2
  exit 1
}

make_fixture() {
  local root="$1"
  mkdir -p "$root/bundle/secrets"
  printf 'identity\n' >"$root/bundle/cluster-identity.json"
  printf 'crs\n' >"$root/bundle/cluster.crs"
  printf 'corpus\n' >"$root/bundle/encrypted-corpus.json"
  printf 'secret\n' >"$root/bundle/secrets/operator-0.json"
  chmod 600 "$root/bundle/secrets/operator-0.json"
  printf '{}\n' >"$root/bundle/bundle-manifest.json"
}

install_fakes() {
  FINAL_TEST_ROOT="$1"
  FINAL_EVENT_LOG="$FINAL_TEST_ROOT/events"
  : >"$FINAL_EVENT_LOG"
  FINAL_BUNDLE_ROOT="$FINAL_TEST_ROOT/bundle"
  FINAL_NODE_COUNT=4
  FINAL_TOPOLOGY=same-az
  FINAL_EXPERIMENT_ID=test-campaign
  FINAL_SOURCE_SHA=cccccccccccccccccccccccccccccccccccccccc
  FINAL_BLOC_IMAGE=bloc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  FINAL_MEMPOOL_IMAGE=mempool@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  FINAL_PHASE=latency
  FINAL_SAMPLER=off FINAL_WARMUPS=10 FINAL_REPETITIONS=1000 FINAL_BLOCKS=10
  FINAL_SEED=20260621 FINAL_DEADLINE=12s
  FINAL_FAIL_STAGE=""

  final_topology_prepare() { printf 'prepare\n' >>"$FINAL_EVENT_LOG"; }
  final_topology_apply() {
    printf 'apply\n' >>"$FINAL_EVENT_LOG"
    printf '{"controller":{"public_ip":"192.0.2.1"},"nodes":[{"id":0,"public_ip":"192.0.2.10"}]}\n' >"$1/inventory.json"
  }
  final_topology_key_for_host() { printf '%s/key.pem\n' "$FINAL_TEST_ROOT"; }
  final_topology_destroy() { printf 'destroy\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != cleanup ]]; }
  final_topology_verify_absent() {
    printf 'verify-absent\n' >>"$FINAL_EVENT_LOG"
    printf '{}\n' >"$1/cleanup-topology.json"
    FINAL_CLEANUP_REGIONS=us-east-1
    export FINAL_CLEANUP_REGIONS
  }
  final_materialize_public() {
    printf 'materialize\n' >>"$FINAL_EVENT_LOG"
    mkdir -p "$1/generated-public"
    printf 'cluster\n' >"$1/generated-public/cluster.json"
    printf 'crs\n' >"$1/generated-public/cluster.crs"
    printf 'remote\n' >"$1/generated-public/remote-eval.json"
  }
  final_stage_hosts() { printf 'stage\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != checksum ]]; }
  final_pull_verify_images() { printf 'images\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != image ]]; }
  final_start_services() { printf 'start\n' >>"$FINAL_EVENT_LOG"; }
  final_health_gate() { printf 'health\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != health ]]; }
  final_sampler_start() { printf 'sampler-start\n' >>"$FINAL_EVENT_LOG"; }
  final_sampler_stop() { printf 'sampler-stop\n' >>"$FINAL_EVENT_LOG"; }
  final_execute_measurement() { printf 'measure\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != evaluator ]]; }
  final_recover_artifacts() { printf 'recover\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != recovery ]]; }
  python3() {
    case "$*" in
      *assert-final-phase*) printf 'validate-phase\n' >>"$FINAL_EVENT_LOG"; [[ "$FINAL_FAIL_STAGE" != validation ]] ;;
      *assert-final-cleanup*) printf 'validate-cleanup\n' >>"$FINAL_EVENT_LOG" ;;
      *) command python3 "$@" ;;
    esac
  }
}

run_case() {
  local name="$1" phase="$2" sampler="$3" fail_stage="$4" expected="$5"
  local root="$fixture/$name"
  make_fixture "$root"
  install_fakes "$root"
  FINAL_PHASE="$phase" FINAL_SAMPLER="$sampler" FINAL_FAIL_STAGE="$fail_stage"
  status=0
  final_run_campaign_lifecycle "$root/artifacts" || status=$?
  [[ "$status" -eq "$expected" ]] || { echo "$name status=$status, want $expected" >&2; exit 1; }
  printf '%s\n' "$root"
}

if task6_selected mandatory-validation; then
  success_root="$(run_case success latency off '' 0)"
  [[ "$(tr '\n' ' ' <"$success_root/events")" == 'prepare apply materialize stage images start health measure recover destroy verify-absent validate-phase validate-cleanup ' ]] || {
    echo "successful lifecycle did not run mandatory artifact validation" >&2
    exit 1
  }
  ! grep -q sampler "$success_root/events"

  resource_root="$(run_case resource resource on '' 0)"
  grep -Fq sampler-start "$resource_root/events"
  grep -Fq sampler-stop "$resource_root/events"

  for stage in checksum image health evaluator recovery cleanup validation; do
    failed_root="$(run_case "failure-$stage" latency off "$stage" 1)"
    grep -Fq recover "$failed_root/events"
    grep -Fq destroy "$failed_root/events"
    grep -Fq verify-absent "$failed_root/events"
  done

  evaluator_lifecycle="$fixture/failure-evaluator/artifacts/lifecycle.jsonl"
  jq -e 'select(.event == "measurement") | .status == "failed"' "$evaluator_lifecycle" >/dev/null
  jq -e 'select(.event == "recovery") | .status == "ok"' "$evaluator_lifecycle" >/dev/null
  jq -e 'select(.event == "destroy") | .status == "ok"' "$evaluator_lifecycle" >/dev/null
  jq -e 'select(.event == "cleanup-verification") | .status == "ok"' "$evaluator_lifecycle" >/dev/null
  recovery_lifecycle="$fixture/failure-recovery/artifacts/lifecycle.jsonl"
  jq -e 'select(.event == "recovery") | .status == "failed"' "$recovery_lifecycle" >/dev/null
  jq -e 'select(.event == "destroy") | .status == "ok"' "$recovery_lifecycle" >/dev/null

  checksum_root="$fixture/failure-checksum"
  ! grep -Fq start "$checksum_root/events"
  image_root="$fixture/failure-image"
  ! grep -Fq start "$image_root/events"
  jq -e '.status == "invalid"' "$fixture/failure-validation/artifacts/manifest.json" >/dev/null || {
    echo "artifact validation failure did not invalidate the manifest" >&2
    exit 1
  }
fi

if find "$fixture" -path '*/artifacts/*' -type f \( -name '*secret*' -o -name 'operator-*.json' \) | grep -q .; then
  echo "private secret leaked into a public artifact root" >&2
  exit 1
fi

echo "final campaign lifecycle tests passed"

if [[ "${1:-}" == same-az ]]; then
  [[ -f "$repo_root/deploy/ec2/final-topology-same-az.sh" ]] || { echo "same-AZ adapter is missing" >&2; exit 1; }
  source "$repo_root/deploy/ec2/final-topology-same-az.sh"
  adapter_root="$fixture/same-az-adapter"
  mkdir -p "$adapter_root/bundle"
  FINAL_REPO_ROOT="$repo_root" FINAL_NODE_COUNT=4 FINAL_EXPERIMENT_ID=adapter-test
  FINAL_BUNDLE_ROOT="$adapter_root/bundle"
  FINAL_ADMIN_CIDR=127.0.0.1/32 FINAL_AWS_PROFILE=default
  FINAL_BLOC_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:$(printf 'a%.0s' {1..64})"
  FINAL_MEMPOOL_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:$(printf 'b%.0s' {1..64})"
  final_same_az_prepare_files "$adapter_root" 4 || exit 1
  tfvars="$adapter_root/generated-public/terraform/campaign.auto.tfvars"
  grep -Fq 'availability_zone = "us-east-1a"' "$tfvars"
  grep -Fq 'operator_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_root_volume_size = 16' "$tfvars"
  grep -Fq 'volume_size           = var.controller_root_volume_size' "$adapter_root/generated-public/terraform/main.tf"
  grep -Fq 'cpu_credits = "unlimited"' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/bloc-node' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/mempool-il' "$tfvars"
  if task6_selected local-key-cleanup; then
    mkdir -p "$(dirname "$FINAL_SAME_AZ_KEY_PATH")"
    printf 'temporary-key\n' >"$FINAL_SAME_AZ_KEY_PATH"
    terraform() { return 0; }
    aws() { return 0; }
    final_topology_destroy "$adapter_root" || { echo "same-AZ destroy failed under controlled boundaries" >&2; exit 1; }
    [[ ! -e "$FINAL_SAME_AZ_KEY_PATH" ]] || { echo "same-AZ destroy retained its local private key" >&2; exit 1; }
    unset -f terraform aws
  fi
  echo "same-AZ adapter contract tests passed"
fi

if [[ "${1:-}" == three-region ]]; then
  adapter="$repo_root/deploy/ec2/final-topology-three-region.sh"
  [[ -f "$adapter" ]] || { echo "three-region adapter is missing" >&2; exit 1; }
  source "$adapter"
  adapter_root="$fixture/three-region-adapter"
  mkdir -p "$adapter_root/bundle"
  FINAL_REPO_ROOT="$repo_root" FINAL_NODE_COUNT=7 FINAL_EXPERIMENT_ID=adapter-test
  FINAL_BUNDLE_ROOT="$adapter_root/bundle"
  FINAL_ADMIN_CIDR=127.0.0.1/32 FINAL_AWS_PROFILE=default
  FINAL_BLOC_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:$(printf 'a%.0s' {1..64})"
  FINAL_MEMPOOL_IMAGE="123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:$(printf 'b%.0s' {1..64})"
  final_three_region_prepare_files "$adapter_root" 7 || exit 1
  tfvars="$adapter_root/generated-public/terraform/campaign.auto.tfvars"
  grep -Fq 'primary_region = "us-east-1"' "$tfvars"
  grep -Fq 'secondary_region = "eu-west-1"' "$tfvars"
  grep -Fq 'tertiary_region = "eu-central-1"' "$tfvars"
  grep -Fq 'primary_availability_zone = "us-east-1a"' "$tfvars"
  grep -Fq 'secondary_availability_zone = "eu-west-1a"' "$tfvars"
  grep -Fq 'tertiary_availability_zone = "eu-central-1a"' "$tfvars"
  grep -Fq 'operator_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_instance_type = "t3.small"' "$tfvars"
  grep -Fq 'controller_root_volume_size = 16' "$tfvars"
  grep -Fq 'volume_size           = var.controller_root_volume_size' "$adapter_root/generated-public/terraform/main.tf"
  grep -Fq 'cpu_credits = "unlimited"' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/bloc-node' "$tfvars"
  grep -Fq 'arn:aws:ecr:us-east-1:123456789012:repository/mempool-il' "$tfvars"
  [[ "$(grep -c '^resource "aws_vpc_peering_connection"' "$repo_root/deploy/ec2/terraform-three-region/main.tf")" -eq 3 ]]
  [[ "$(grep -c '^resource "aws_route"' "$repo_root/deploy/ec2/terraform-three-region/main.tf")" -eq 6 ]]

  inventory="$adapter_root/inventory.json"
  jq -n '{controller:{instance_type:"t3.small",region:"us-east-1",zone:"us-east-1a"},nodes:[range(0;7)|{id:.,instance_type:"t3.small",region:(["us-east-1","eu-west-1","eu-central-1"][.%3]),zone:"test-zone"}]}' >"$inventory"
  final_three_region_validate_inventory "$inventory" 7
  jq '.nodes[1].region="us-east-1"' "$inventory" >"$inventory.invalid"
  if final_three_region_validate_inventory "$inventory.invalid" 7; then
    echo "three-region inventory validator accepted invalid id-to-region placement" >&2
    exit 1
  fi
  [[ "$(final_topology_key_for_host '{"region":"us-east-1"}')" == "$FINAL_THREE_REGION_PRIMARY_KEY_PATH" ]]
  [[ "$(final_topology_key_for_host '{"region":"eu-west-1"}')" == "$FINAL_THREE_REGION_SECONDARY_KEY_PATH" ]]
  [[ "$(final_topology_key_for_host '{"region":"eu-central-1"}')" == "$FINAL_THREE_REGION_TERTIARY_KEY_PATH" ]]

  final_three_region_query_array() {
    local query="$1"
    shift
    if [[ "$query" == 'Reservations[].Instances[].InstanceId' && " $* " == *' --region eu-west-1 '* ]]; then
      printf '["i-eu-west-leftover"]\n'
    else
      printf '[]\n'
    fi
  }
  cleanup_record="$(final_three_region_region_cleanup eu-west-1 "$FINAL_THREE_REGION_SECONDARY_KEY_NAME")"
  jq -e '.query_succeeded == true and .instances == ["i-eu-west-leftover"] and .vpcs == [] and .peering_connections == []' <<<"$cleanup_record" >/dev/null
  if task6_selected local-key-cleanup; then
    mkdir -p "$(dirname "$FINAL_THREE_REGION_PRIMARY_KEY_PATH")"
    printf 'temporary-us-key\n' >"$FINAL_THREE_REGION_PRIMARY_KEY_PATH"
    printf 'temporary-eu-west-key\n' >"$FINAL_THREE_REGION_SECONDARY_KEY_PATH"
    printf 'temporary-eu-central-key\n' >"$FINAL_THREE_REGION_TERTIARY_KEY_PATH"
    terraform() { return 0; }
    aws() { return 0; }
    final_topology_destroy "$adapter_root" || { echo "three-region destroy failed under controlled boundaries" >&2; exit 1; }
    for key_path in "$FINAL_THREE_REGION_PRIMARY_KEY_PATH" "$FINAL_THREE_REGION_SECONDARY_KEY_PATH" "$FINAL_THREE_REGION_TERTIARY_KEY_PATH"; do
      [[ ! -e "$key_path" ]] || { echo "three-region destroy retained local private key: $key_path" >&2; exit 1; }
    done
    unset -f terraform aws
  fi
  echo "three-region adapter contract tests passed"
fi
