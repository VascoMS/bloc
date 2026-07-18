#!/usr/bin/env bash

# Generic same-region M3 matrix. The sourcing entrypoint sets M3_* defaults.
bloc_m3_matrix_main() {
  local admin_cidrs=() aws_profile=bloc node_counts_csv="$M3_NODE_COUNTS" batch_sizes_csv=8,32,128
  local warmups=5 repetitions=30 campaign_id="" baseline_root="" auto_plan=0 auto_phases=0 unattended=0 keep_failure=0 skip_charts=0 validate_only=0
  bloc_validate_flag_values "$@"
  while [[ $# -gt 0 ]]; do case "$1" in
    --admin-cidr) admin_cidrs+=("$2"); shift 2;; --aws-profile) aws_profile="$2"; shift 2;;
    --aws-region) M3_AWS_REGION="$2"; shift 2;; --availability-zone) M3_PRIMARY_AZ="$2"; shift 2;;
    --availability-zones) M3_AVAILABILITY_ZONES="$2"; shift 2;; --subnet-cidrs) M3_SUBNET_CIDRS="$2"; shift 2;;
    --operator-instance-type) M3_OPERATOR_TYPE="$2"; shift 2;; --controller-instance-type) M3_CONTROLLER_TYPE="$2"; shift 2;;
    --node-counts) node_counts_csv="$2"; shift 2;; --batch-sizes) batch_sizes_csv="$2"; shift 2;;
    --warmups) warmups="$2"; shift 2;; --repetitions) repetitions="$2"; shift 2;;
    --campaign-id) campaign_id="$2"; shift 2;; --baseline-campaign-root) baseline_root="$2"; shift 2;;
    --auto-approve-plan) auto_plan=1; shift;; --auto-approve-phases) auto_phases=1; shift;;
    --unattended) unattended=1; auto_plan=1; auto_phases=1; shift;; --keep-resources-on-failure) keep_failure=1; shift;;
    --skip-chart-generation) skip_charts=1; shift;; --validate-only) validate_only=1; shift;;
    -h|--help) bloc_m3_matrix_usage; return 0;; *) bloc_m3_matrix_usage; bloc_usage_error "unknown argument: $1";; esac; done
  [[ "${#admin_cidrs[@]}" -gt 0 ]] || bloc_usage_error "at least one --admin-cidr is required"
  bloc_csv_contains_only "$node_counts_csv" 4,7,10 NodeCounts; bloc_csv_contains_only "$batch_sizes_csv" 8,32,128 BatchSizes
  bloc_is_uint "$warmups" || bloc_usage_error "--warmups must be non-negative"; bloc_is_positive_int "$repetitions" || bloc_usage_error "--repetitions must be positive"
  [[ -n "$campaign_id" ]] || campaign_id="$M3_CAMPAIGN_PREFIX-$(bloc_utc_stamp)"
  [[ "$campaign_id" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || bloc_usage_error "invalid campaign id: $campaign_id"
  if [[ "$validate_only" -eq 1 ]]; then
    local validate_args=(--admin-cidr "${admin_cidrs[0]}" --validate-only)
    bash "$M3_REPO_ROOT/deploy/ec2/run-a1-pilot.sh" "${validate_args[@]}"
    bloc_validate_only_message "$M3_ENTRY_NAME"; return 0
  fi
  local campaign_root="$M3_REPO_ROOT/results/ec2/$campaign_id" phase_runner="$M3_REPO_ROOT/deploy/ec2/run-a1-pilot.sh"
  [[ ! -e "$campaign_root" ]] || bloc_die "campaign already exists: $campaign_root"; mkdir -p "$campaign_root"
  local commands="$campaign_root/commands.txt" phases="$campaign_root/phases.jsonl" started_at="$(bloc_utc_iso)"; : >"$commands"; : >"$phases"
  local old_ifs="$IFS" node_count phase_id source_root dest_root exit_code node_args batch_args expected_args n b
  IFS=','; set -- $node_counts_csv; IFS="$old_ifs"; local nodes=("$@")
  IFS=','; set -- $batch_sizes_csv; IFS="$old_ifs"; local batches=("$@")
  local phase_roots=()
  for node_count in "${nodes[@]}"; do
    phase_id="bloc-ec2-$M3_PHASE_PREFIX-n$node_count-${campaign_id#${M3_CAMPAIGN_PREFIX}-}"
    source_root="$M3_REPO_ROOT/results/ec2/$phase_id"; dest_root="$campaign_root/n$node_count"
    node_args=(--aws-profile "$aws_profile" --aws-region "$M3_AWS_REGION" --availability-zone "$M3_PRIMARY_AZ" --node-count "$node_count" --operator-instance-type "$M3_OPERATOR_TYPE" --controller-instance-type "$M3_CONTROLLER_TYPE" --batch-sizes "$batch_sizes_csv" --warmups "$warmups" --repetitions "$repetitions" --campaign-label "$M3_CAMPAIGN_LABEL" --topology "$M3_TOPOLOGY" --experiment-id "$phase_id")
    [[ -n "$M3_AVAILABILITY_ZONES" ]] && node_args+=(--availability-zones "$M3_AVAILABILITY_ZONES")
    [[ -n "$M3_SUBNET_CIDRS" ]] && node_args+=(--subnet-cidrs "$M3_SUBNET_CIDRS")
    for b in "${admin_cidrs[@]}"; do node_args+=(--admin-cidr "$b"); done
    [[ "$auto_plan" -eq 1 ]] && node_args+=(--auto-approve-plan); [[ "$keep_failure" -eq 1 ]] && node_args+=(--keep-resources-on-failure); [[ "$skip_charts" -eq 1 ]] && node_args+=(--skip-chart-generation)
    bloc_append_command "$commands" bash "$phase_runner" "${node_args[@]}"
    set +e; bash "$phase_runner" "${node_args[@]}"; exit_code=$?; set -e
    [[ ! -d "$source_root" ]] || mv "$source_root" "$dest_root"
    jq -n --argjson node_count "$node_count" --arg experiment_id "$phase_id" --arg artifact_root "$dest_root" --argjson exit_code "$exit_code" '{node_count:$node_count,experiment_id:$experiment_id,artifact_root:$artifact_root,exit_code:$exit_code,status:(if $exit_code==0 then "complete" else "invalid" end)}' >>"$phases"
    [[ "$exit_code" -eq 0 ]] || { bloc_m3_write_manifest invalid "phase n=$node_count failed"; return 1; }
    expected_args=(assert-evaluator --csv "$dest_root/run_measurements.csv"); for b in "${batches[@]}"; do expected_args+=(--expected "$node_count/$b=$repetitions"); done
    bloc_python "$M3_REPO_ROOT" "${expected_args[@]}"
    phase_roots+=("$dest_root")
    if [[ "$auto_phases" -ne 1 && "$node_count" != "${nodes[${#nodes[@]}-1]}" ]]; then read -r -p "Phase n=$node_count passed. Type NEXT to continue: " answer; [[ "$answer" == NEXT ]] || bloc_die "operator stopped campaign"; fi
  done
  local name inputs
  for name in run_measurements.csv node_measurements.csv scenario_summary.csv resource-samples.csv; do inputs=(); for dest_root in "${phase_roots[@]}"; do inputs+=("$dest_root/$name"); done; bloc_python "$M3_REPO_ROOT" merge-csv --output "$campaign_root/$name" "${inputs[@]}"; done
  inputs=(); for dest_root in "${phase_roots[@]}"; do inputs+=("$dest_root/scenario_summary.json"); done; bloc_python "$M3_REPO_ROOT" merge-json --output "$campaign_root/scenario_summary.json" "${inputs[@]}"
  M3_CAMPAIGN_ROOT="$campaign_root" M3_PHASES="$phases" M3_STARTED_AT="$started_at" M3_CAMPAIGN_ID="$campaign_id" M3_NODE_COUNTS="$node_counts_csv" M3_BATCH_SIZES="$batch_sizes_csv" M3_WARMUPS="$warmups" M3_REPETITIONS="$repetitions" M3_UNATTENDED="$unattended" M3_BASELINE="$baseline_root" bloc_m3_write_manifest complete ""
  if [[ "$skip_charts" -ne 1 && -x "$M3_REPO_ROOT/latency-charts/.venv/bin/python" ]]; then (cd "$M3_REPO_ROOT/latency-charts" && .venv/bin/python -m bloc_latency_charts "$campaign_root"); fi
  if [[ -n "$baseline_root" ]]; then (cd "$M3_REPO_ROOT/latency-charts" && "${M3_REPO_ROOT}/latency-charts/.venv/bin/python" -m bloc_latency_charts.campaign_comparison "$baseline_root" "$campaign_root" --output "$campaign_root/comparison"); fi
}

bloc_m3_write_manifest() {
  local status="$1" reason="$2" root="${M3_CAMPAIGN_ROOT:-$campaign_root}" phase_file="${M3_PHASES:-$phases}"
  jq -s '.' "$phase_file" >"$root/phases.json"
  jq -n --arg schema_version 'bloc-ec2-m3-campaign/v1' --arg experiment_id "${M3_CAMPAIGN_ID:-$campaign_id}" --arg campaign "$M3_CAMPAIGN_LABEL" --arg status "$status" --arg reason "$reason" --arg started_at "${M3_STARTED_AT:-$started_at}" --arg finished_at "$(bloc_utc_iso)" --arg aws_region "$M3_AWS_REGION" --arg availability_zone "$M3_PRIMARY_AZ" --arg topology "$M3_TOPOLOGY" --arg node_counts "${M3_NODE_COUNTS:-$node_counts_csv}" --arg batch_sizes "${M3_BATCH_SIZES:-$batch_sizes_csv}" --argjson warmups "${M3_WARMUPS:-$warmups}" --argjson repetitions "${M3_REPETITIONS:-$repetitions}" --arg baseline "${M3_BASELINE:-$baseline_root}" --argjson unattended "${M3_UNATTENDED:-$unattended}" --slurpfile phases "$root/phases.json" '{schema_version:$schema_version,experiment_id:$experiment_id,campaign:$campaign,status:$status,invalid_reason:(if $reason=="" then null else $reason end),started_at:$started_at,finished_at:$finished_at,aws_region:$aws_region,availability_zone:$availability_zone,topology:$topology,tx_source:"synthetic",node_counts:($node_counts|split(",")|map(tonumber)),batch_sizes:($batch_sizes|split(",")|map(tonumber)),warmups:$warmups,repetitions:$repetitions,baseline_campaign_root:(if $baseline=="" then null else $baseline end),unattended:($unattended==1),resource_policy:"destroy-after-success; keep only on failure when requested",failure_rule:"any failed or inconsistent measured run invalidates the phase",phases:$phases[0]}' >"$root/manifest.json"
}
