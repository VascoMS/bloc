#!/usr/bin/env bash

# Shared campaign-runner primitives. Keep this file compatible with the Bash
# 3.2 shipped by macOS: no associative arrays, mapfile, or GNU-only utilities.

bloc_die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

bloc_usage_error() {
  printf 'error: %s\n' "$*" >&2
  exit 2
}

bloc_utc_stamp() { date -u '+%Y%m%dt%H%M%sz'; }
bloc_utc_iso() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

bloc_repo_root() {
  local source_dir
  source_dir="$(cd "$(dirname "${BASH_SOURCE[1]}")" && pwd)"
  while [[ "$source_dir" != "/" ]]; do
    if [[ -f "$source_dir/AGENTS.md" && -d "$source_dir/.git" ]]; then
      printf '%s\n' "$source_dir"
      return 0
    fi
    source_dir="$(dirname "$source_dir")"
  done
  return 1
}

bloc_require_cmd() {
  command -v "$1" >/dev/null 2>&1 || bloc_usage_error "required command not found: $1"
}

bloc_require_file() { [[ -f "$1" ]] || bloc_usage_error "required file not found: $1"; }
bloc_require_dir() { [[ -d "$1" ]] || bloc_usage_error "required directory not found: $1"; }

bloc_is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }
bloc_is_positive_int() { bloc_is_uint "$1" && [[ "$1" -gt 0 ]]; }

bloc_validate_id() {
  [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]] || bloc_usage_error "$2 contains unsupported characters: $1"
}

bloc_validate_go_duration() {
  [[ "$1" =~ ^[0-9]+(ms|s|m)$ ]] || bloc_usage_error "$2 must be a Go duration such as 30s or 1m"
}

bloc_validate_flag_values() {
  # Reject missing option values as usage errors before set -u can turn them
  # into an indistinguishable runtime failure.
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      -h|--help|--validate-only|--report-only|--resume|--resume-completed-phases|--skip-image-build|--regenerate-config|--auto-approve-plan|--auto-approve-phases|--unattended|--keep-resources-on-failure|--keep-resources-after-run|--skip-chart-generation|--plan-only|--compare-only|--multiple-blocks|--short|--skip-race)
        shift ;;
      --*)
        [[ "$#" -ge 2 && "$2" != --* ]] || bloc_usage_error "$1 requires a value"
        shift 2 ;;
      *) shift ;;
    esac
  done
}

bloc_csv_each() {
  local value="$1" callback="$2" name="$3" old_ifs part
  old_ifs="$IFS"; IFS=','
  set -- $value
  IFS="$old_ifs"
  [[ "$#" -gt 0 ]] || bloc_usage_error "$name must not be empty"
  for part in "$@"; do
    part="${part#${part%%[![:space:]]*}}"
    part="${part%${part##*[![:space:]]}}"
    [[ -n "$part" ]] || bloc_usage_error "$name contains an empty item"
    "$callback" "$part" "$name"
  done
}

bloc_validate_positive_item() {
  bloc_is_positive_int "$1" || bloc_usage_error "$2 contains an invalid positive integer: $1"
}

bloc_validate_csv_positive() { bloc_csv_each "$1" bloc_validate_positive_item "$2"; }

bloc_csv_contains_only() {
  local value="$1" allowed="$2" name="$3" old_ifs part
  bloc_validate_csv_positive "$value" "$name"
  old_ifs="$IFS"; IFS=','
  set -- $value
  IFS="$old_ifs"
  for part in "$@"; do
    case ",$allowed," in
      *",$part,"*) ;;
      *) bloc_usage_error "$name item $part is not one of: $allowed" ;;
    esac
  done
}

bloc_shell_quote() {
  local out='' arg quoted
  for arg in "$@"; do
    printf -v quoted '%q' "$arg"
    out="${out}${out:+ }${quoted}"
  done
  printf '%s\n' "$out"
}

bloc_sha256() {
  python3 - "$1" <<'PY'
import hashlib, sys
with open(sys.argv[1], "rb") as handle:
    print(hashlib.sha256(handle.read()).hexdigest())
PY
}

bloc_append_command() {
  local command_file="$1"; shift
  mkdir -p "$(dirname "$command_file")"
  bloc_shell_quote "$@" >>"$command_file"
}

bloc_run_logged() {
  local command_file="$1" log_file="$2" working_dir="$3"; shift 3
  bloc_append_command "$command_file" "$@"
  mkdir -p "$(dirname "$log_file")"
  (cd "$working_dir" && "$@") 2>&1 | tee "$log_file"
  return "${PIPESTATUS[0]}"
}

bloc_run_recorded() {
  local records_file="$1" stage="$2" log_file="$3" working_dir="$4"; shift 4
  local started finished command exit_code
  started="$(bloc_utc_iso)"
  command="$(bloc_shell_quote "$@")"
  mkdir -p "$(dirname "$log_file")" "$(dirname "$records_file")"
  set +e
  (cd "$working_dir" && "$@") 2>&1 | tee "$log_file"
  exit_code="${PIPESTATUS[0]}"
  set -e
  finished="$(bloc_utc_iso)"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$stage" "$working_dir" "$command" "$started" "$finished" "$exit_code" "$log_file" >>"$records_file"
  [[ "$exit_code" -eq 0 ]] || return "$exit_code"
}

bloc_python() {
  local repo_root="$1"; shift
  python3 "$repo_root/scripts/lib/campaign_artifacts.py" "$@"
}

bloc_write_json() {
  local repo_root="$1" output="$2"; shift 2
  bloc_python "$repo_root" write-json --output "$output" "$@"
}

bloc_python_version() { python3 -c 'import platform; print(platform.python_version())'; }

bloc_platform_os() {
  python3 -c 'import platform; print(platform.platform())'
}

bloc_validate_only_message() {
  printf 'validation passed: %s\n' "$1"
}

bloc_install_cleanup_trap() {
  # The caller provides bloc_cleanup. Its implementation must be idempotent.
  trap 'status=$?; trap - EXIT INT TERM HUP; bloc_cleanup "$status"; exit "$status"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
}
