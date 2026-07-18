#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
# shellcheck source=../../scripts/lib/campaign-common.sh
source "$repo_root/scripts/lib/campaign-common.sh"

usage() {
  cat <<'EOF'
Usage: bash bloc-node/scripts/run-acs-safety-campaign.sh [options]

Options:
  --campaign-id ID       Default: acs-safety-<UTC stamp>
  --schedule-seeds N     Must be 1000
  --gate-repetitions N   Must be 100
  --matrix-repetitions N Must be 30
  --start-at STAGE       safety, race, gate, matrix, or identity
  --resume               Resume an existing campaign
  --report-only          Refresh artifacts for an existing campaign
  --skip-race            Skip the race stage
  --validate-only        Validate without writing artifacts or running commands
EOF
}

campaign_id="acs-safety-$(bloc_utc_stamp)"
schedule_seeds=1000
gate_repetitions=100
matrix_repetitions=30
start_at=safety
resume=0
report_only=0
skip_race=0
validate_only=0

bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --campaign-id) campaign_id="$2"; shift 2 ;;
    --schedule-seeds) schedule_seeds="$2"; shift 2 ;;
    --gate-repetitions) gate_repetitions="$2"; shift 2 ;;
    --matrix-repetitions) matrix_repetitions="$2"; shift 2 ;;
    --start-at) start_at="$2"; shift 2 ;;
    --resume) resume=1; shift ;;
    --report-only) report_only=1; shift ;;
    --skip-race) skip_race=1; shift ;;
    --validate-only) validate_only=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; bloc_usage_error "unknown argument: $1" ;;
  esac
done

bloc_validate_id "$campaign_id" CampaignId
[[ "$schedule_seeds" == 1000 ]] || bloc_usage_error "the thesis safety gate requires exactly 1000 scheduler seeds"
[[ "$gate_repetitions" == 100 ]] || bloc_usage_error "the sustained gate requires exactly 100 measured repetitions"
[[ "$matrix_repetitions" == 30 ]] || bloc_usage_error "the compatibility matrix requires exactly 30 measured repetitions per scenario"
case "$start_at" in safety|race|gate|matrix|identity) ;; *) bloc_usage_error "invalid --start-at stage: $start_at" ;; esac
[[ "$report_only" -eq 0 || "$resume" -eq 1 ]] || bloc_usage_error "--report-only requires --resume"
for command in go git python3 jq; do bloc_require_cmd "$command"; done
bloc_require_dir "$repo_root/sbc/hbbft"
bloc_require_dir "$repo_root/bte/btd-impl-main"

if [[ "$validate_only" -eq 1 ]]; then
  bloc_validate_only_message "run-acs-safety-campaign.sh"
  exit 0
fi

module_root="$repo_root/bloc-node"
hbbft_root="$repo_root/sbc/hbbft"
bte_root="$repo_root/bte/btd-impl-main"
campaign_root="$repo_root/results/local/acs-common-subset-safety/$campaign_id"
logs_root="$campaign_root/logs"
gate_root="$campaign_root/gate-n4-b128"
matrix_root="$campaign_root/matrix-n4-n7"
records_file="$campaign_root/command-records.tsv"
previous_commands="$campaign_root/previous-commands.json"

if [[ -d "$campaign_root" && "$resume" -ne 1 ]]; then bloc_die "campaign already exists; use --resume and --start-at: $campaign_root"; fi
if [[ ! -d "$campaign_root" && "$resume" -eq 1 ]]; then bloc_die "cannot resume missing campaign: $campaign_root"; fi

started_at="$(bloc_utc_iso)"
resumed_status=""
if [[ "$resume" -eq 1 ]]; then
  bloc_require_file "$campaign_root/manifest.json"
  started_at="$(jq -r .started_at "$campaign_root/manifest.json")"
  resumed_status="$(jq -r .status "$campaign_root/manifest.json")"
  jq '.commands // []' "$campaign_root/manifest.json" >"$previous_commands"
fi
mkdir -p "$logs_root"
: >"$records_file"
export GOCACHE="$repo_root/.gocache"

stage_number() { case "$1" in safety) echo 0;; race) echo 1;; gate) echo 2;; matrix) echo 3;; identity) echo 4;; esac; }
run_stage() { [[ "$(stage_number "$1")" -ge "$(stage_number "$start_at")" ]]; }

campaign_status=running
failure_stage=""
failure_message=""

write_artifacts() {
  local finished_at commands_new commands_final scheduler rows gate_csv matrix_csv failed_count source_hashes
  finished_at="$(bloc_utc_iso)"
  commands_new="$campaign_root/commands-new.json"
  commands_final="$campaign_root/commands.json"
  bloc_python "$repo_root" commands-json --records "$records_file" --output "$commands_new"
  if [[ -f "$previous_commands" ]]; then bloc_python "$repo_root" merge-json --output "$commands_final" "$previous_commands" "$commands_new"; else cp "$commands_new" "$commands_final"; fi
  scheduler="$campaign_root/scheduler.json"
  jq -n --argjson schedules "$schedule_seeds" '{test:"TestACSCommonSubsetAcrossReorderedDeliverySchedules",seed_start:0,seed_end_inclusive:($schedules-1),schedules:$schedules,nodes:4,delivery:"select one pending RBC/BBA message using math/rand seeded by the schedule id",assertions:["identical proposer set","identical payload bytes","subset size >= N-F"],ec2_regression:"all four RBC outputs with only three completed true BBA results must remain incomplete"}' >"$scheduler"
  source_hashes="$(jq -n \
    --arg a "$(bloc_sha256 "$repo_root/sbc/hbbft/acs.go")" \
    --arg at "$(bloc_sha256 "$repo_root/sbc/hbbft/acs_test.go")" \
    --arg b "$(bloc_sha256 "$repo_root/sbc/hbbft/bba.go")" \
    --arg bt "$(bloc_sha256 "$repo_root/sbc/hbbft/bba_test.go")" \
    '{"sbc/hbbft/acs.go":$a,"sbc/hbbft/acs_test.go":$at,"sbc/hbbft/bba.go":$b,"sbc/hbbft/bba_test.go":$bt}')"
  jq -n \
    --argjson schema_version 1 --arg campaign_id "$campaign_id" --arg status "$campaign_status" \
    --arg failure_stage "$failure_stage" --arg failure_message "$failure_message" \
    --arg started_at "$started_at" --arg finished_at "$finished_at" \
    --arg git_commit "$(git -C "$repo_root" rev-parse HEAD)" --arg go_version "$(go version)" \
    --arg goos "$(go env GOOS)" --arg goarch "$(go env GOARCH)" --arg bash_version "$BASH_VERSION" \
    --arg python_version "$(bloc_python_version)" --arg os "$(bloc_platform_os)" --arg processor "$(uname -m)" \
    --argjson source_hashes "$source_hashes" --slurpfile scheduler "$scheduler" --slurpfile commands "$commands_final" \
    --arg git_status "$(git -C "$repo_root" status --short)" \
    '{schema_version:$schema_version,campaign_id:$campaign_id,status:$status,failure_stage:$failure_stage,failure_message:$failure_message,started_at:$started_at,finished_at:$finished_at,git_commit:$git_commit,git_status:($git_status|split("\n")|map(select(length>0))),protocol_source_sha256:$source_hashes,go_version:$go_version,go_env:{goos:$goos,goarch:$goarch},runner:{shell:"bash",bash_version:$bash_version,python_version:$python_version},os:$os,processor:$processor,aws_allocated:false,scheduler:$scheduler[0],commands:$commands[0]}' >"$campaign_root/manifest.json"

  rows="$campaign_root/evaluator-rows.json"
  printf '[]\n' >"$rows"
  gate_csv="$gate_root/run_measurements.csv"; matrix_csv="$matrix_root/run_measurements.csv"
  if [[ -f "$gate_csv" ]]; then bloc_python "$repo_root" evaluator-rows --csv "$gate_csv" --output "$campaign_root/gate-rows.json"; fi
  if [[ -f "$matrix_csv" ]]; then bloc_python "$repo_root" evaluator-rows --csv "$matrix_csv" --output "$campaign_root/matrix-rows.json"; fi
  local row_inputs=()
  [[ -f "$campaign_root/gate-rows.json" ]] && row_inputs+=("$campaign_root/gate-rows.json")
  [[ -f "$campaign_root/matrix-rows.json" ]] && row_inputs+=("$campaign_root/matrix-rows.json")
  [[ "${#row_inputs[@]}" -eq 0 ]] || bloc_python "$repo_root" merge-json --output "$rows" "${row_inputs[@]}"
  failed_count="$(jq '[.[]|select(.exit_code != 0)]|length' "$commands_final")"
  {
    printf '# Local ACS Common-Subset Safety Report\n\n- Campaign: `%s`\n- Status: **%s**\n- Base commit: `%s`\n- Exact protocol source hashes: `manifest.json`\n- AWS resources allocated: no\n- Raw observations were retained; no outliers were removed.\n\n' "$campaign_id" "$campaign_status" "$(git -C "$repo_root" rev-parse HEAD)"
    printf '## ACS Safety\n\nThe campaign tests 1,000 fixed reordered RBC/BBA delivery schedules and the EC2 3-versus-4-list regression.\n\n## ACS Liveness\n\n| Nodes | Batch | Measured | Successful and consistent | Failed |\n|---:|---:|---:|---:|---:|\n'
    jq -r '.[]|"| \(.nodes) | \(.batch) | \(.measured) | \(.successful) | \(.failed) |"' "$rows"
    printf '\nThe n4/batch-128 campaign is the sustained 100-slot gate. The n4/n7 matrix covers batches 8, 32, and 128 with 30 measured slots per scenario.\n\n## Failure\n\n'
    if [[ "$campaign_status" == passed ]]; then printf 'No campaign acceptance stage failed.\n'; else printf 'Stage `%s` failed: %s\n' "$failure_stage" "$failure_message"; fi
    printf 'The command history preserves %s failed tooling attempt(s).\n\n## Artifacts\n\n- `manifest.json` records commands and environment metadata.\n- `scheduler.json` records deterministic delivery coverage.\n- `logs/` contains unit, race, compatibility, and benchmark output.\n' "$failed_count"
  } >"$campaign_root/REPORT.md"
  rm -f "$commands_new" "$commands_final" "$rows" "$campaign_root/gate-rows.json" "$campaign_root/matrix-rows.json" "$previous_commands"
}

if [[ "$report_only" -eq 1 ]]; then campaign_status="$resumed_status"; write_artifacts; printf 'ACS safety campaign report refreshed: %s\n' "$campaign_root"; exit 0; fi

set +e
if run_stage safety; then bloc_run_recorded "$records_file" "hbbft repeated safety tests" "$logs_root/hbbft-count20.log" "$hbbft_root" go test ./... -count=20 -timeout=10m || failure_stage="hbbft repeated safety tests"; fi
if [[ -z "$failure_stage" ]] && run_stage race && [[ "$skip_race" -ne 1 ]]; then bloc_run_recorded "$records_file" "hbbft race tests" "$logs_root/hbbft-race.log" "$hbbft_root" go test -race ./... -run 'Test(ACS|BBA|SlotACS)' -count=1 -timeout=10m || failure_stage="hbbft race tests"; fi
if [[ -z "$failure_stage" ]] && run_stage gate; then
  bloc_run_recorded "$records_file" "n4 batch-128 sustained gate" "$logs_root/gate-n4-b128.log" "$module_root" go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4 --batch-sizes 128 --warmups 5 --repetitions "$gate_repetitions" --max-restarts 1 --timeout 30s --seed 640 --experiment-id "$campaign_id-gate" --out-dir "$gate_root" || failure_stage="n4 batch-128 sustained gate"
  [[ -n "$failure_stage" ]] || bloc_python "$repo_root" assert-evaluator --csv "$gate_root/run_measurements.csv" --expected "4/128=$gate_repetitions" || failure_stage="n4 batch-128 sustained gate"
fi
if [[ -z "$failure_stage" ]] && run_stage matrix; then
  bloc_run_recorded "$records_file" "n4 n7 compatibility matrix" "$logs_root/matrix-n4-n7.log" "$module_root" go run ./cmd/bloc-node eval-suite --execution-mode persistent --node-counts 4,7 --batch-sizes 8,32,128 --warmups 3 --repetitions "$matrix_repetitions" --max-restarts 1 --timeout 30s --seed 640 --experiment-id "$campaign_id-matrix" --out-dir "$matrix_root" || failure_stage="n4 n7 compatibility matrix"
  if [[ -z "$failure_stage" ]]; then
    args=(assert-evaluator --csv "$matrix_root/run_measurements.csv")
    for n in 4 7; do for b in 8 32 128; do args+=(--expected "$n/$b=$matrix_repetitions"); done; done
    bloc_python "$repo_root" "${args[@]}" || failure_stage="n4 n7 compatibility matrix"
  fi
fi
if [[ -z "$failure_stage" ]] && run_stage identity; then
  bloc_run_recorded "$records_file" "bloc-node protocol identity tests" "$logs_root/bloc-node-tests.log" "$module_root" go test ./... -count=1 -timeout=10m || failure_stage="bloc-node protocol identity tests"
  [[ -n "$failure_stage" ]] || bloc_run_recorded "$records_file" "BTE protocol identity tests" "$logs_root/bte-tests.log" "$bte_root" go test ./... -count=1 -timeout=10m || failure_stage="BTE protocol identity tests"
  [[ -n "$failure_stage" ]] || bloc_run_recorded "$records_file" "Merge Plan focused benchmark" "$logs_root/merge-plan-benchmark.log" "$module_root" go test ./internal/app -run '^$' -bench '^BenchmarkMergePlanAttribution$' -count 3 -benchtime 1s -benchmem || failure_stage="Merge Plan focused benchmark"
fi
set -e

if [[ -z "$failure_stage" ]]; then campaign_status=passed; else campaign_status=failed; failure_message="$failure_stage failed"; fi
write_artifacts
printf 'ACS safety campaign %s: %s\n' "$campaign_status" "$campaign_root"
[[ "$campaign_status" == passed ]] || exit 1
