#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
# shellcheck source=../../scripts/lib/campaign-common.sh
source "$repo_root/scripts/lib/campaign-common.sh"

usage() {
  cat <<'EOF'
Usage: bash bloc-node/scripts/run-merge-plan-campaign.sh --phase baseline|optimized --campaign-id ID [options]

Options:
  --resume         Continue a partially completed phase from retained artifacts
  --compare-only   Rebuild summaries and comparison from existing artifacts
  --validate-only  Validate arguments and dependencies without writing or running
EOF
}

phase=""
campaign_id=""
compare_only=0
validate_only=0
resume=0
bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --phase) phase="$2"; shift 2 ;;
    --campaign-id) campaign_id="$2"; shift 2 ;;
    --compare-only) compare_only=1; shift ;;
    --resume) resume=1; shift ;;
    --validate-only) validate_only=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; bloc_usage_error "unknown argument: $1" ;;
  esac
done
case "$phase" in baseline|optimized) ;; *) bloc_usage_error "--phase must be baseline or optimized" ;; esac
[[ -n "$campaign_id" ]] || bloc_usage_error "--campaign-id is required"
bloc_validate_id "$campaign_id" CampaignId
for command in go git python3; do bloc_require_cmd "$command"; done
if [[ "$validate_only" -eq 1 ]]; then bloc_validate_only_message "run-merge-plan-campaign.sh"; exit 0; fi

campaign_root="$repo_root/results/local/merge-plan-optimization/$campaign_id"
phase_root="$campaign_root/$phase"
module_root="$repo_root/bloc-node"
bte_root="$repo_root/bte/btd-impl-main"

summarize_phase() {
  local root="$1"
  bloc_python "$repo_root" benchmark-summary --output "$root/benchmark-summary.csv" "$root/bloc-node-bench.txt" "$root/bte-bench.txt"
  bloc_python "$repo_root" evaluator-summary --csv "$root/eval-suite/run_measurements.csv" --output "$root/evaluator-summary.csv"
}

if [[ "$compare_only" -eq 1 ]]; then
  summarize_phase "$campaign_root/baseline"
  summarize_phase "$campaign_root/optimized"
  bloc_python "$repo_root" compare-merge-plan --campaign "$campaign_root"
  exit 0
fi

if [[ -e "$phase_root" && "$resume" -ne 1 ]]; then bloc_die "campaign phase already exists: $phase_root"; fi
mkdir -p "$phase_root"
export GOMAXPROCS=1 GOCACHE="$repo_root/.gocache"
commands_file="$phase_root/commands.txt"
[[ "$resume" -eq 1 && -f "$commands_file" ]] || : >"$commands_file"

run_logged() {
  local cwd="$1" log="$2"; shift 2
  bloc_run_logged "$commands_file" "$log" "$cwd" "$@" || bloc_die "command failed: $(bloc_shell_quote "$@")"
}

if [[ ! ( "$resume" -eq 1 && -s "$phase_root/bloc-node-bench.txt" && "$(tail -n 2 "$phase_root/bloc-node-bench.txt"|head -n 1)" == PASS ) ]]; then
  run_logged "$module_root" "$phase_root/bloc-node-bench.txt" go test ./internal/app -run '^$' -bench '^BenchmarkMergePlanAttribution$' -count 10 -benchtime 1s -benchmem
fi
for mode in disjoint overlap; do
  if [[ "$resume" -eq 1 && -s "$phase_root/cpu-n7-b128-$mode.pprof" && -s "$phase_root/mem-n7-b128-$mode.pprof" ]]; then continue; fi
  bloc_append_command "$commands_file" go test ./internal/app -run '^$' -bench "^BenchmarkMergePlanAttribution/n7-b128-$mode/pipeline$" -count 1 -benchtime 3s -cpuprofile "$phase_root/cpu-n7-b128-$mode.pprof" -memprofile "$phase_root/mem-n7-b128-$mode.pprof"
  (cd "$module_root" && go test ./internal/app -run '^$' -bench "^BenchmarkMergePlanAttribution/n7-b128-$mode/pipeline$" -count 1 -benchtime 3s -cpuprofile "$phase_root/cpu-n7-b128-$mode.pprof" -memprofile "$phase_root/mem-n7-b128-$mode.pprof") >/dev/null || bloc_die "profile benchmark failed for $mode"
done
eval_complete=0
if [[ "$resume" -eq 1 && -s "$phase_root/eval-suite/run_measurements.csv" ]]; then
  bloc_python "$repo_root" assert-evaluator --require-success --csv "$phase_root/eval-suite/run_measurements.csv" \
    --expected 4/8=10 --expected 4/32=10 --expected 4/128=10 \
    --expected 7/8=10 --expected 7/32=10 --expected 7/128=10 >/dev/null 2>&1 && eval_complete=1
fi
if [[ "$eval_complete" -ne 1 ]]; then
  rm -rf "$phase_root/eval-suite"
  run_logged "$module_root" "$phase_root/eval-suite.log" go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7 --batch-sizes 8,32,128 --warmups 3 --repetitions 10 --experiment-id "merge-plan-$phase" --out-dir "$phase_root/eval-suite"
fi
if [[ ! ( "$resume" -eq 1 && -s "$phase_root/bte-bench.txt" && "$(tail -n 2 "$phase_root/bte-bench.txt"|head -n 1)" == PASS ) ]]; then
  run_logged "$bte_root" "$phase_root/bte-bench.txt" go test ./be -run '^$' -bench '^BenchmarkBatchPlanningAttribution$' -count 10 -benchtime 1s -benchmem
fi

jq_status="$(git -C "$repo_root" status --short)"
python3 - "$phase_root/manifest.json" "$campaign_id" "$phase" "$repo_root" "$BASH_VERSION" "$(bloc_python_version)" "$jq_status" <<'PY'
import json, platform, subprocess, sys
out, campaign, phase, repo, bash_version, python_version, git_status = sys.argv[1:]
value = {"schema_version": 1, "campaign_id": campaign, "phase": phase,
         "git_commit": subprocess.check_output(["git", "-C", repo, "rev-parse", "HEAD"], text=True).strip(),
         "git_status": [line for line in git_status.splitlines() if line],
         "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
         "runner": {"shell": "bash", "bash_version": bash_version, "python_version": python_version},
         "os": platform.platform(), "processor": platform.machine(), "gomaxprocs": 1,
         "created_at": __import__("datetime").datetime.now(__import__("datetime").timezone.utc).isoformat(),
         "commands": open(out.rsplit('/', 1)[0] + "/commands.txt", encoding="utf-8").read().splitlines()}
with open(out, "w", encoding="utf-8", newline="") as handle:
    json.dump(value, handle, indent=2); handle.write("\n")
PY
summarize_phase "$phase_root"
if [[ "$phase" == optimized && -f "$campaign_root/baseline/benchmark-summary.csv" ]]; then bloc_python "$repo_root" compare-merge-plan --campaign "$campaign_root"; fi
