#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
helper="$repo_root/scripts/lib/final-remote-job.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-remote-job.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT
job_root="$fixture/jobs"

wait_for_status() {
  local job_id="$1" expected="$2" attempt=1 status
  while [[ "$attempt" -le 100 ]]; do
    status="$(BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" status "$job_id")"
    [[ "$status" == "$expected" ]] && return 0
    [[ "$status" == RUNNING ]] || {
      echo "job $job_id reached unexpected status $status" >&2
      return 1
    }
    sleep 0.05
    attempt=$((attempt + 1))
  done
  echo "job $job_id did not reach $expected" >&2
  return 1
}

count_file="$fixture/executions"
BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" start job-once \
  bash -c 'printf "run\n" >>"$1"; printf "job stdout\n"; printf "job stderr\n" >&2' _ "$count_file"
BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" start job-once \
  bash -c 'printf "duplicate\n" >>"$1"' _ "$count_file"
wait_for_status job-once EXIT:0
[[ "$(wc -l <"$count_file" | tr -d ' ')" -eq 1 ]] || {
  echo "duplicate start re-executed the remote job" >&2
  exit 1
}
grep -Fqx 'run' "$count_file" || { echo "remote job ran the wrong command" >&2; exit 1; }
grep -Fqx 'job stdout' "$job_root/job-once/stdout.log" || { echo "remote stdout was not retained" >&2; exit 1; }
grep -Fqx 'job stderr' "$job_root/job-once/stderr.log" || { echo "remote stderr was not retained" >&2; exit 1; }
[[ "$(cat "$job_root/job-once/exit.status")" == 0 ]] || { echo "remote exit status was not published" >&2; exit 1; }

BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" start job-fails bash -c 'exit 23'
wait_for_status job-fails EXIT:23

mkdir -p "$job_root/job-ambiguous"
BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" start job-ambiguous \
  bash -c 'printf "unsafe\n" >"$1"' _ "$fixture/unsafe"
[[ "$(BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" status job-ambiguous)" == AMBIGUOUS ]] || {
  echo "a preclaimed job without a PID was not ambiguous" >&2
  exit 1
}
[[ ! -e "$fixture/unsafe" ]] || { echo "an ambiguous job was re-executed" >&2; exit 1; }

mkdir -p "$job_root/job-lost"
printf '%s\n' 99999999 >"$job_root/job-lost/pid"
[[ "$(BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" status job-lost)" == LOST ]] || {
  echo "a dead job without an exit status was not reported lost" >&2
  exit 1
}

mkdir -p "$job_root/job-exit-publication-race"
printf '%s\n' 99999998 >"$job_root/job-exit-publication-race/pid"
race_exit_status="$job_root/job-exit-publication-race/exit.status"
race_env="$fixture/race-env.sh"
cat >"$race_env" <<'EOF'
kill() {
  printf '0\n' >"$BLOC_REMOTE_JOB_RACE_EXIT_STATUS"
  return 1
}
EOF
race_status="$(
  BASH_ENV="$race_env" \
    BLOC_REMOTE_JOB_RACE_EXIT_STATUS="$race_exit_status" \
    BLOC_REMOTE_JOB_ROOT="$job_root" \
    bash "$helper" status job-exit-publication-race
)"
[[ "$race_status" == EXIT:0 ]] || {
  echo "a status published during the liveness check was reported $race_status, want EXIT:0" >&2
  exit 1
}

if BLOC_REMOTE_JOB_ROOT="$job_root" bash "$helper" start '../escape' true 2>/dev/null; then
  echo "remote job helper accepted an unsafe identity" >&2
  exit 1
fi

echo "final remote job tests passed"
