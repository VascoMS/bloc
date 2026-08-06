#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "usage: final-remote-job.sh start <job-id> <command> [args...] | status <job-id>" >&2
  exit 2
}

[[ "$#" -ge 2 ]] || usage
action="$1"
job_id="$2"
[[ "$job_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
  echo "invalid remote job identity: $job_id" >&2
  exit 2
}

job_root="${BLOC_REMOTE_JOB_ROOT:-/opt/bloc/ec2/jobs}"
job_dir="$job_root/$job_id"

case "$action" in
  start)
    shift 2
    [[ "$#" -gt 0 ]] || usage
    mkdir -p "$job_root"
    if mkdir "$job_dir" 2>/dev/null; then
      command_file="$job_dir/command.sh"
      {
        printf '#!/usr/bin/env bash\nexec'
        for argument in "$@"; do
          printf ' %q' "$argument"
        done
        printf '\n'
      } >"$command_file"
      chmod 700 "$command_file"
      nohup bash -c '
        set +e
        "$1" >"$2" 2>"$3"
        status=$?
        printf "%s\n" "$status" >"$4.tmp"
        mv "$4.tmp" "$4"
      ' _ "$command_file" "$job_dir/stdout.log" "$job_dir/stderr.log" "$job_dir/exit.status" \
        </dev/null >/dev/null 2>&1 &
      printf '%s\n' "$!" >"$job_dir/pid.tmp"
      mv "$job_dir/pid.tmp" "$job_dir/pid"
      echo CREATED
    else
      echo EXISTS
    fi
    ;;
  status)
    [[ "$#" -eq 2 ]] || usage
    if [[ ! -d "$job_dir" ]]; then
      echo MISSING
    elif [[ -f "$job_dir/exit.status" ]]; then
      status="$(cat "$job_dir/exit.status")"
      [[ "$status" =~ ^[0-9]+$ ]] || { echo AMBIGUOUS; exit 0; }
      echo "EXIT:$status"
    elif [[ ! -f "$job_dir/pid" ]]; then
      echo AMBIGUOUS
    else
      pid="$(cat "$job_dir/pid")"
      if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
        echo RUNNING
      else
        echo LOST
      fi
    fi
    ;;
  *)
    usage
    ;;
esac
