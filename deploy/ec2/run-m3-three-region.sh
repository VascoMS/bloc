#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
source "$repo_root/scripts/lib/campaign-common.sh"

usage() { cat <<'EOF'
Usage: bash deploy/ec2/run-m3-three-region.sh --admin-cidr CIDR [options]
  --aws-profile NAME
  --primary-region REGION --secondary-region REGION --tertiary-region REGION
  --primary-availability-zone AZ --secondary-availability-zone AZ --tertiary-availability-zone AZ
  --operator-instance-type TYPE --controller-instance-type TYPE --cpu-credits standard|unlimited
  --node-counts LIST --batch-sizes LIST --warmups N --repetitions N
  --eval-timeout DURATION --campaign-id ID
  --auto-approve-plan --auto-approve-phases --unattended
  --skip-chart-generation --plan-only --validate-only
EOF
}

original_args=("$@")
admin_cidrs=()
aws_profile=default
primary_region=us-east-1; secondary_region=eu-west-1; tertiary_region=eu-central-1
primary_az=us-east-1a; secondary_az=eu-west-1a; tertiary_az=eu-central-1a
operator_type=t3.small; controller_type=t3.small; cpu_credits=unlimited
node_counts_csv=4,7; batch_sizes_csv=8,32,128; warmups=5; repetitions=30; eval_timeout=60s
campaign_id=""; auto_plan=0; auto_phases=0; unattended=0
skip_charts=0; plan_only=0; validate_only=0

bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --admin-cidr) admin_cidrs+=("$2"); shift 2 ;;
    --aws-profile) aws_profile="$2"; shift 2 ;;
    --primary-region) primary_region="$2"; shift 2 ;;
    --secondary-region) secondary_region="$2"; shift 2 ;;
    --tertiary-region) tertiary_region="$2"; shift 2 ;;
    --primary-availability-zone) primary_az="$2"; shift 2 ;;
    --secondary-availability-zone) secondary_az="$2"; shift 2 ;;
    --tertiary-availability-zone) tertiary_az="$2"; shift 2 ;;
    --operator-instance-type) operator_type="$2"; shift 2 ;;
    --controller-instance-type) controller_type="$2"; shift 2 ;;
    --cpu-credits) cpu_credits="$2"; shift 2 ;;
    --node-counts) node_counts_csv="$2"; shift 2 ;;
    --batch-sizes) batch_sizes_csv="$2"; shift 2 ;;
    --warmups) warmups="$2"; shift 2 ;;
    --repetitions) repetitions="$2"; shift 2 ;;
    --eval-timeout) eval_timeout="$2"; shift 2 ;;
    --campaign-id) campaign_id="$2"; shift 2 ;;
    --auto-approve-plan) auto_plan=1; shift ;;
    --auto-approve-phases) auto_phases=1; shift ;;
    --unattended) unattended=1; auto_plan=1; auto_phases=1; shift ;;
    --skip-chart-generation) skip_charts=1; shift ;;
    --plan-only) plan_only=1; shift ;;
    --validate-only) validate_only=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; bloc_usage_error "unknown argument: $1" ;;
  esac
done

[[ "${#admin_cidrs[@]}" -gt 0 ]] || bloc_usage_error "at least one --admin-cidr is required"
[[ "$primary_region" != "$secondary_region" && "$primary_region" != "$tertiary_region" && "$secondary_region" != "$tertiary_region" ]] || bloc_usage_error "all three regions must differ"
bloc_csv_contains_only "$node_counts_csv" 4,7 NodeCounts
bloc_csv_contains_only "$batch_sizes_csv" 8,32,128 BatchSizes
bloc_is_uint "$warmups" || bloc_usage_error "warmups must be non-negative"
bloc_is_positive_int "$repetitions" || bloc_usage_error "repetitions must be positive"
bloc_validate_go_duration "$eval_timeout" EvalTimeout
[[ "$operator_type" == t3.small ]] || bloc_usage_error "the accepted three-region campaign requires t3.small operators"
[[ "$controller_type" == t3.small ]] || bloc_usage_error "the accepted three-region campaign requires a t3.small controller"
[[ "$cpu_credits" == standard || "$cpu_credits" == unlimited ]] || bloc_usage_error "cpu credits must be standard or unlimited"
[[ -n "$campaign_id" ]] || campaign_id="m3-three-region-synthetic-$(bloc_utc_stamp)"
[[ "$campaign_id" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || bloc_usage_error "invalid campaign id"

for command in aws terraform docker git jq ssh scp go python3; do bloc_require_cmd "$command"; done
bloc_require_dir "$script_dir/terraform-three-region"
if [[ "$validate_only" -eq 1 ]]; then bloc_validate_only_message "run-m3-three-region.sh"; exit 0; fi
if [[ "$skip_charts" -ne 1 && "$plan_only" -ne 1 && ! -x "$repo_root/latency-charts/.venv/bin/python" ]]; then bloc_die "chart generation requires latency-charts/.venv/bin/python or --skip-chart-generation"; fi
if [[ "$plan_only" -ne 1 ]]; then
  dirty="$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all -- bloc-node bte sbc deploy/ec2 latency-charts scripts | sed -n '1p')"
  [[ -z "$dirty" ]] || bloc_die "three-region campaign sources must be committed: $dirty"
fi

old_ifs="$IFS"; IFS=','; set -- $node_counts_csv; IFS="$old_ifs"; nodes=("$@")
IFS=','; set -- $batch_sizes_csv; IFS="$old_ifs"; batches=("$@")
maximum_nodes="$(python3 - "$node_counts_csv" <<'PY'
import sys
print(max(map(int, sys.argv[1].split(','))))
PY
)"

campaign_root="$repo_root/results/ec2/$campaign_id"
[[ ! -e "$campaign_root" ]] || bloc_die "campaign exists: $campaign_root"
mkdir -p "$campaign_root"
source_sha="$(git -C "$repo_root" rev-parse HEAD)"
git_commit="${source_sha:0:12}"
started_at="$(bloc_utc_iso)"; phases_file="$campaign_root/phases.jsonl"; : >"$phases_file"
printf '%q ' "$script_dir/run-m3-three-region.sh" "${original_args[@]}" >"$campaign_root/commands.txt"; printf '\n' >>"$campaign_root/commands.txt"
campaign_complete=0

write_campaign_manifest() {
  local status="$1" reason="$2"
  local phases_json="$campaign_root/phases.json"
  [[ -f "$phases_json" ]] || jq -s . "$phases_file" >"$phases_json"
  jq -n --arg schema_version bloc-ec2-m3-three-region/v1 --arg experiment_id "$campaign_id" --arg status "$status" \
    --arg invalid_reason "$reason" --arg started_at "$started_at" --arg finished_at "$(bloc_utc_iso)" \
    --arg primary_region "$primary_region" --arg secondary_region "$secondary_region" --arg tertiary_region "$tertiary_region" \
    --arg node_counts "$node_counts_csv" --arg batch_sizes "$batch_sizes_csv" --argjson warmups "$warmups" --argjson repetitions "$repetitions" \
    --arg eval_timeout "$eval_timeout" --arg operator_instance_type "$operator_type" --arg controller_instance_type "$controller_type" \
    --arg cpu_credits "$cpu_credits" --arg source_sha "$source_sha" --arg docker_image_digest "$(sed -n '1p' "$campaign_root/image-digest.txt" 2>/dev/null || true)" \
    --slurpfile phases "$phases_json" \
    '{schema_version:$schema_version,experiment_id:$experiment_id,campaign:"M3-three-region-synthetic",status:$status,invalid_reason:(if $invalid_reason=="" then null else $invalid_reason end),started_at:$started_at,finished_at:$finished_at,source_sha:$source_sha,topology:"T2-three-region",primary_region:$primary_region,secondary_region:$secondary_region,tertiary_region:$tertiary_region,node_counts:($node_counts|split(",")|map(tonumber)),batch_sizes:($batch_sizes|split(",")|map(tonumber)),warmups:$warmups,repetitions:$repetitions,eval_timeout:$eval_timeout,operator_instance_type:$operator_instance_type,controller_instance_type:$controller_instance_type,cpu_credits:$cpu_credits,docker_image_digest:$docker_image_digest,reporting_stages:["proposal","acs","merge_plan","decryption_materialization"],comparison_policy:"standalone-current-build; prior topology data is historical context only",phases:$phases[0]}' >"$campaign_root/manifest.json"
}

finalize_campaign() {
  local status=$?
  trap - EXIT
  if [[ "$campaign_complete" -ne 1 ]]; then write_campaign_manifest invalid "campaign exited with status $status" || true; fi
  exit "$status"
}
trap finalize_campaign EXIT

write_prometheus_config() {
  local inventory="$1" output="$2"
  {
    printf '%s\n' 'global:' '  scrape_interval: 2s' 'scrape_configs:' "  - job_name: 'bloc-sidecars'" '    metrics_path: /metrics' '    static_configs:' '      - targets:'
    jq -r '.nodes|sort_by(.id)[]|"          - " + .private_ip + ":8000"' "$inventory"
  } >"$output"
}

assert_prometheus_targets() {
  jq -e --argjson expected "$2" '(.data.activeTargets|length)==$expected and all(.data.activeTargets[];.health=="up")' "$1" >/dev/null || bloc_die "Prometheus target acceptance failed"
}

assert_network_matrix() {
  local path="$1" expected_rows="$2"
  awk -F, -v expected="$expected_rows" 'NR>1 {rows++; if ($7 != 5 || $8 != 5) bad=1} END {exit(rows==expected && !bad ? 0 : 1)}' "$path" || bloc_die "pairwise network acceptance failed for $path"
}

collect_pairwise_network() {
  local inventory="$1" phase="$2" output="$3" source target source_key samples summary
  printf 'phase,source_node_id,source_region,target_node_id,target_region,target_private_ip,attempts,successes,avg_connect_ms,avg_total_ms\n' >"$output"
  while IFS= read -r source; do
    source_key="$(key_for "$source")"
    while IFS= read -r target; do
      samples="$(ssh -n -i "$source_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$(jq -r .public_ip <<<"$source")" "for i in 1 2 3 4 5; do curl --max-time 5 -sS -o /dev/null -w '%{http_code},%{time_connect},%{time_total}\\n' http://$(jq -r .private_ip <<<"$target"):8000/healthz || echo '000,0,0'; done")"
      summary="$(awk -F, '{a++; if ($1==200) {s++; c+=$2; t+=$3}} END {if(s) printf "%d,%d,%.3f,%.3f",a,s,1000*c/s,1000*t/s; else printf "%d,0,,",a}' <<<"$samples")"
      printf '%s,%s,%s,%s,%s,%s,%s\n' "$phase" "$(jq -r .id <<<"$source")" "$(jq -r .region <<<"$source")" "$(jq -r .id <<<"$target")" "$(jq -r .region <<<"$target")" "$(jq -r .private_ip <<<"$target")" "$summary" >>"$output"
    done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
  done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
}

collect_resource_sample() {
  local inventory="$1" phase="$2" batch="$3" output="$4" timestamp node key public inspect stats
  timestamp="$(bloc_utc_iso)"
  [[ -f "$output" ]] || printf 'timestamp,phase,batch_size,node_id,region,container_status,restart_count,oom_killed,cpu_percent,mem_usage,mem_percent,net_io,block_io,pids\n' >"$output"
  while IFS= read -r node; do
    key="$(key_for "$node")"; public="$(jq -r .public_ip <<<"$node")"
    inspect="$(ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" "docker inspect --format '{{.State.Status}},{{.RestartCount}},{{.State.OOMKilled}}' ec2-bloc-node-1" 2>/dev/null || true)"
    stats="$(ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" "docker stats --no-stream --format '{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}},{{.PIDs}}' ec2-bloc-node-1" 2>/dev/null || true)"
    [[ -n "$inspect" ]] || inspect='error,error,error'; [[ -n "$stats" ]] || stats='error,error,error,error,error,error'
    printf '%s,%s,%s,%s,%s,%s,%s\n' "$timestamp" "$phase" "$batch" "$(jq -r .id <<<"$node")" "$(jq -r .region <<<"$node")" "$inspect" "$stats" >>"$output"
  done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
}

aws sts get-caller-identity --profile "$aws_profile" --output json >"$campaign_root/aws-caller-identity.json"
operator_vcpus="$(aws ec2 describe-instance-types --profile "$aws_profile" --region "$primary_region" --instance-types "$operator_type" --query 'InstanceTypes[0].VCpuInfo.DefaultVCpus' --output text)"
controller_vcpus="$(aws ec2 describe-instance-types --profile "$aws_profile" --region "$primary_region" --instance-types "$controller_type" --query 'InstanceTypes[0].VCpuInfo.DefaultVCpus' --output text)"
requirements="$(python3 - "$maximum_nodes" "$operator_vcpus" "$controller_vcpus" <<'PY'
import json, sys
n, operator, controller = map(int, sys.argv[1:])
counts = [sum(node % 3 == remainder for node in range(n)) for remainder in range(3)]
print(json.dumps([counts[0] * operator + controller, counts[1] * operator, counts[2] * operator]))
PY
)"
regions=("$primary_region" "$secondary_region" "$tertiary_region"); zones=("$primary_az" "$secondary_az" "$tertiary_az")
: >"$campaign_root/preflight.jsonl"
for index in 0 1 2; do
  region="${regions[$index]}"; zone="${zones[$index]}"; required="$(jq -r ".[$index]" <<<"$requirements")"
  for instance_type in "$operator_type" "$controller_type"; do
    offered="$(aws ec2 describe-instance-type-offerings --profile "$aws_profile" --region "$region" --location-type availability-zone --filters "Name=location,Values=$zone" "Name=instance-type,Values=$instance_type" --query 'length(InstanceTypeOfferings)' --output text)"
    [[ "$offered" -gt 0 ]] || bloc_die "$instance_type is not offered in $zone"
  done
  free_tier="$(aws ec2 describe-instance-types --profile "$aws_profile" --region "$region" --instance-types "$operator_type" --query 'InstanceTypes[0].FreeTierEligible' --output text)"
  set +e; quota="$(aws service-quotas get-service-quota --profile "$aws_profile" --region "$region" --service-code ec2 --quota-code L-1216C47A --query Quota.Value --output text 2>/dev/null)"; quota_status=$?; set -e
  status=verified
  if [[ "$quota_status" -ne 0 ]]; then status=unavailable; [[ "$plan_only" -eq 1 ]] || bloc_die "unable to verify Standard On-Demand vCPU quota in $region"
  elif ! python3 -c 'import sys; raise SystemExit(0 if float(sys.argv[1]) >= int(sys.argv[2]) else 1)' "$quota" "$required"; then status=insufficient; [[ "$plan_only" -eq 1 ]] || bloc_die "Standard On-Demand vCPU quota is below required $required in $region"
  fi
  jq -n --arg region "$region" --arg zone "$zone" --arg instance_type "$operator_type" --arg free_tier_eligible "$free_tier" --arg quota "${quota:-}" --argjson required_vcpus "$required" --arg quota_status "$status" '{region:$region,availability_zone:$zone,instance_type:$instance_type,free_tier_eligible:($free_tier_eligible|ascii_downcase=="true"),standard_vcpu_quota:$quota,required_vcpus:$required_vcpus,quota_status:$quota_status}' >>"$campaign_root/preflight.jsonl"
done
jq -s . "$campaign_root/preflight.jsonl" >"$campaign_root/preflight.json"

image_tag="bloc-node:three-region-$git_commit"
if [[ "$plan_only" -ne 1 ]]; then
  docker build --platform linux/amd64 -f "$repo_root/bloc-node/Dockerfile" -t "$image_tag" "$repo_root"
  [[ "$(docker image inspect "$image_tag" --format '{{.Architecture}}')" == amd64 ]] || bloc_die "three-region image is not amd64"
fi

run_phase() (
  set -Eeuo pipefail
  node_count="$1"; suffix="${campaign_id#m3-three-region-synthetic-}"; phase_id="bloc-ec2-3r-n$node_count-$suffix"
  [[ "${#phase_id}" -le 44 ]] || bloc_die "campaign id is too long for IAM names"
  phase_root="$campaign_root/n$node_count"; work="$phase_root/generated/terraform-work"
  mkdir -p "$work" "$phase_root/logs" "$phase_root/scenarios"
  cp "$script_dir"/terraform-three-region/*.tf "$work/"; cp "$script_dir/terraform-three-region/user-data.sh" "$work/"; cp "$script_dir/terraform-three-region/.terraform.lock.hcl" "$work/"
  key_dir="$(mktemp -d "${TMPDIR:-/tmp}/bloc-three-region-keys.XXXXXX")"
  primary_key_name="$phase_id-primary-key"; secondary_key_name="$phase_id-secondary-key"; tertiary_key_name="$phase_id-tertiary-key"
  primary_key="$key_dir/$primary_key_name.pem"; secondary_key="$key_dir/$secondary_key_name.pem"; tertiary_key="$key_dir/$tertiary_key_name.pem"
  apply_attempted=0; phase_ok=0; phase_record_ready=0

  key_for() {
    case "$(jq -r .region <<<"$1")" in
      "$primary_region") printf '%s' "$primary_key" ;;
      "$secondary_region") printf '%s' "$secondary_key" ;;
      "$tertiary_region") printf '%s' "$tertiary_key" ;;
      *) bloc_die "unknown host region" ;;
    esac
  }

  run_resource_phase() {
    # Keep sampler overhead out of the primary latency/p99 phase.
    local batch node id public region scenario remote_csv stop_file resource_dir next_resource_slot=100000
    local parts=() summary_args=(resource-summary --input "$phase_root/resource_timeseries.csv" --output "$phase_root/resource-summary.csv")
    mkdir -p "$phase_root/resource-parts"
    for id in $(jq -r '.nodes|sort_by(.id)[]|.id' "$inventory"); do summary_args+=(--expected-node "$id"); done
    for batch in "${batches[@]}"; do
      scenario="n${node_count}-b${batch}"; resource_dir="$phase_root/resource-parts/$scenario"; mkdir -p "$resource_dir"
      summary_args+=(--expected-configuration "$scenario:resource-measured")
      while IFS= read -r node; do
        id="$(jq -r .id <<<"$node")"; public="$(jq -r .public_ip <<<"$node")"; region="$(jq -r .region <<<"$node")"; remote_csv="/opt/bloc/ec2/resources/$scenario.csv"; stop_file="/opt/bloc/ec2/resources/$scenario.stop"
        ssh -n -i "$(key_for "$node")" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" "mkdir -p /opt/bloc/ec2/resources; rm -f '$stop_file'; nohup /opt/bloc/ec2/sample-container-resources.sh run --container ec2-bloc-node-1 --output '$remote_csv' --stop-file '$stop_file' --node '$id' --region '$region' --scenario '$scenario' --phase resource-measured >/opt/bloc/ec2/resources/$scenario.log 2>&1 &"
      done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
      remote_dir="/opt/bloc/ec2/results/$phase_id/resource-$scenario"
      ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" "sudo mkdir -p '$remote_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$runtime_image' eval-remote --config remote-eval.ec2.json --experiment-id '$phase_id-resource-$scenario' --first-slot '$next_resource_slot' --batch-size '$batch' --warmups 0 --repetitions '$repetitions' --out-dir 'results/$phase_id/resource-$scenario' --image-tag '$runtime_image' --git-commit '$git_commit' --timeout '$eval_timeout'"
      while IFS= read -r node; do
        id="$(jq -r .id <<<"$node")"; public="$(jq -r .public_ip <<<"$node")"; remote_csv="/opt/bloc/ec2/resources/$scenario.csv"; stop_file="/opt/bloc/ec2/resources/$scenario.stop"
        ssh -n -i "$(key_for "$node")" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" "touch '$stop_file'; sleep 2; test -s '$remote_csv'"
        scp -i "$(key_for "$node")" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public:$remote_csv" "$resource_dir/node-$id.csv"; parts+=("$resource_dir/node-$id.csv")
      done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
      next_resource_slot=$((next_resource_slot + repetitions))
    done
    bloc_python "$repo_root" merge-csv --output "$phase_root/resource_timeseries.csv" "${parts[@]}"
    bloc_python "$repo_root" "${summary_args[@]}"
  }

  collect_failure_logs() {
    [[ -f "$phase_root/inventory.json" ]] || return 0
    while IFS= read -r node; do
      ssh -n -i "$(key_for "$node")" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$(jq -r .public_ip <<<"$node")" 'docker logs --timestamps ec2-bloc-node-1 2>&1; sudo tail -n 200 /var/log/cloud-init-output.log' >"$phase_root/logs/operator-$(jq -r .id <<<"$node")-failure.log" 2>&1 || true
    done < <(jq -c '.nodes[]' "$phase_root/inventory.json")
  }

  cleanup_phase() {
    original_status=$?; final_status=$original_status; cleanup_status=0; destroy_status=0; verification_errors="$phase_root/cleanup-verification-errors.log"; : >"$verification_errors"; set +e
    trap - EXIT
    [[ "$phase_ok" -eq 1 ]] || collect_failure_logs
    if [[ -d "$phase_root/generated/secrets.ec2" ]]; then rm -rf "$phase_root/generated/secrets.ec2"; fi
    if [[ "$apply_attempted" -eq 1 ]]; then
      : >"$phase_root/terraform-destroy.log"
      destroy_status=1
      for destroy_attempt in 1 2 3; do
        printf 'destroy attempt %s/3\n' "$destroy_attempt" >>"$phase_root/terraform-destroy.log"
        (cd "$work" && AWS_PROFILE="$aws_profile" terraform destroy -auto-approve -var-file=campaign.tfvars >>"$phase_root/terraform-destroy.log" 2>&1)
        destroy_status=$?
        [[ "$destroy_status" -ne 0 ]] || break
        sleep 15
      done
      if [[ "$destroy_status" -ne 0 ]]; then cleanup_status=1; final_status=1; fi
    fi
    if [[ "$plan_only" -eq 1 ]]; then
      rm -f "$primary_key" "$secondary_key" "$tertiary_key"; rmdir "$key_dir" 2>/dev/null || true
      jq -n '{status:"not-applied",resources:[]}' >"$phase_root/cleanup-verification.json"
      exit "$final_status"
    fi
    delete_campaign_key() {
      local region="$1" key_name="$2" present="" attempt
      for attempt in 1 2 3; do
        present="$(aws ec2 describe-key-pairs --profile "$aws_profile" --region "$region" --filters "Name=key-name,Values=$key_name" --query 'length(KeyPairs)' --output text 2>>"$verification_errors")" || present="query-failed"
        [[ "$present" == 0 ]] && return 0
        if [[ "$present" != query-failed ]] && aws ec2 delete-key-pair --profile "$aws_profile" --region "$region" --key-name "$key_name" >/dev/null 2>>"$verification_errors"; then continue; fi
        sleep 5
      done
      present="$(aws ec2 describe-key-pairs --profile "$aws_profile" --region "$region" --filters "Name=key-name,Values=$key_name" --query 'length(KeyPairs)' --output text 2>>"$verification_errors")" || return 1
      [[ "$present" == 0 ]]
    }
    delete_campaign_key "$primary_region" "$primary_key_name" || { cleanup_status=1; final_status=1; }
    delete_campaign_key "$secondary_region" "$secondary_key_name" || { cleanup_status=1; final_status=1; }
    delete_campaign_key "$tertiary_region" "$tertiary_key_name" || { cleanup_status=1; final_status=1; }
    rm -f "$primary_key" "$secondary_key" "$tertiary_key"; rmdir "$key_dir" 2>/dev/null || true
    cleanup_query() {
      local value query_status
      value="$("$@" 2>>"$verification_errors")"; query_status=$?
      if [[ "$query_status" -ne 0 ]]; then printf 'cleanup query failed (%s): %s\n' "$query_status" "$*" >>"$verification_errors"; fi
      printf '%s' "$value"
    }
    exact_iam_name() {
      local resource_type="$1" resource_name="$2" error_file="$phase_root/iam-$resource_type-check.err" query_status
      : >"$error_file"
      if [[ "$resource_type" == role ]]; then
        aws iam get-role --profile "$aws_profile" --role-name "$resource_name" --output json >/dev/null 2>"$error_file"
      else
        aws iam get-instance-profile --profile "$aws_profile" --instance-profile-name "$resource_name" --output json >/dev/null 2>"$error_file"
      fi
      query_status=$?
      if [[ "$query_status" -eq 0 ]]; then
        printf '%s' "$resource_name"
      elif grep -q 'NoSuchEntity' "$error_file"; then
        :
      else
        cat "$error_file" >>"$verification_errors"
        printf 'cleanup query failed (%s): exact IAM %s %s\n' "$query_status" "$resource_type" "$resource_name" >>"$verification_errors"
      fi
      rm -f "$error_file"
    }
    jq -n \
      --arg primary_instances "$(cleanup_query aws ec2 describe-instances --profile "$aws_profile" --region "$primary_region" --filters "Name=tag:Name,Values=$phase_id-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" --query 'Reservations[].Instances[].InstanceId' --output text)" \
      --arg secondary_instances "$(cleanup_query aws ec2 describe-instances --profile "$aws_profile" --region "$secondary_region" --filters "Name=tag:Name,Values=$phase_id-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" --query 'Reservations[].Instances[].InstanceId' --output text)" \
      --arg tertiary_instances "$(cleanup_query aws ec2 describe-instances --profile "$aws_profile" --region "$tertiary_region" --filters "Name=tag:Name,Values=$phase_id-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" --query 'Reservations[].Instances[].InstanceId' --output text)" \
      --arg primary_volumes "$(cleanup_query aws ec2 describe-volumes --profile "$aws_profile" --region "$primary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Volumes[].VolumeId' --output text)" \
      --arg secondary_volumes "$(cleanup_query aws ec2 describe-volumes --profile "$aws_profile" --region "$secondary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Volumes[].VolumeId' --output text)" \
      --arg tertiary_volumes "$(cleanup_query aws ec2 describe-volumes --profile "$aws_profile" --region "$tertiary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Volumes[].VolumeId' --output text)" \
      --arg primary_vpcs "$(cleanup_query aws ec2 describe-vpcs --profile "$aws_profile" --region "$primary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Vpcs[].VpcId' --output text)" \
      --arg secondary_vpcs "$(cleanup_query aws ec2 describe-vpcs --profile "$aws_profile" --region "$secondary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Vpcs[].VpcId' --output text)" \
      --arg tertiary_vpcs "$(cleanup_query aws ec2 describe-vpcs --profile "$aws_profile" --region "$tertiary_region" --filters "Name=tag:Name,Values=$phase_id-*" --query 'Vpcs[].VpcId' --output text)" \
      --arg primary_peerings "$(cleanup_query aws ec2 describe-vpc-peering-connections --profile "$aws_profile" --region "$primary_region" --filters "Name=tag:Name,Values=$phase_id-*-peering" --query "VpcPeeringConnections[?Status.Code!='deleted'].VpcPeeringConnectionId" --output text)" \
      --arg secondary_peerings "$(cleanup_query aws ec2 describe-vpc-peering-connections --profile "$aws_profile" --region "$secondary_region" --filters "Name=tag:Name,Values=$phase_id-*-peering" --query "VpcPeeringConnections[?Status.Code!='deleted'].VpcPeeringConnectionId" --output text)" \
      --arg tertiary_peerings "$(cleanup_query aws ec2 describe-vpc-peering-connections --profile "$aws_profile" --region "$tertiary_region" --filters "Name=tag:Name,Values=$phase_id-*-peering" --query "VpcPeeringConnections[?Status.Code!='deleted'].VpcPeeringConnectionId" --output text)" \
      --arg primary_keys "$(cleanup_query aws ec2 describe-key-pairs --profile "$aws_profile" --region "$primary_region" --filters "Name=key-name,Values=$phase_id-*" --query 'KeyPairs[].KeyName' --output text)" \
      --arg secondary_keys "$(cleanup_query aws ec2 describe-key-pairs --profile "$aws_profile" --region "$secondary_region" --filters "Name=key-name,Values=$phase_id-*" --query 'KeyPairs[].KeyName' --output text)" \
      --arg tertiary_keys "$(cleanup_query aws ec2 describe-key-pairs --profile "$aws_profile" --region "$tertiary_region" --filters "Name=key-name,Values=$phase_id-*" --query 'KeyPairs[].KeyName' --output text)" \
      --arg ecr_repository "$(cleanup_query aws ecr describe-repositories --profile "$aws_profile" --region "$primary_region" --query "repositories[?repositoryName=='bloc-node-$phase_id'].repositoryName" --output text)" \
      --arg iam_role "$(exact_iam_name role "$phase_id-ec2-ecr-readonly")" \
      --arg instance_profile "$(exact_iam_name instance-profile "$phase_id-ec2-ecr-readonly")" \
      '{regions:{primary:{instances:$primary_instances,volumes:$primary_volumes,vpcs:$primary_vpcs,peering_connections:$primary_peerings,key_pairs:$primary_keys},secondary:{instances:$secondary_instances,volumes:$secondary_volumes,vpcs:$secondary_vpcs,peering_connections:$secondary_peerings,key_pairs:$secondary_keys},tertiary:{instances:$tertiary_instances,volumes:$tertiary_volumes,vpcs:$tertiary_vpcs,peering_connections:$tertiary_peerings,key_pairs:$tertiary_keys}},ecr_repository:$ecr_repository,iam_role:$iam_role,instance_profile:$instance_profile}' >"$phase_root/cleanup-verification.json"
    if ! jq -e '[..|strings|select(length>0 and .!="None")]|length==0' "$phase_root/cleanup-verification.json" >/dev/null; then cleanup_status=1; final_status=1; fi
    if [[ -s "$verification_errors" ]]; then cleanup_status=1; final_status=1; fi
    if [[ "$final_status" -ne 0 ]]; then
      if [[ -f "$phase_root/manifest.json" ]]; then
        jq --arg reason "phase exit $original_status; cleanup exit $cleanup_status" '.status="invalid" | .invalid_reason=$reason' "$phase_root/manifest.json" >"$phase_root/manifest.json.tmp" && mv "$phase_root/manifest.json.tmp" "$phase_root/manifest.json"
      else
        placement='[]'; [[ ! -f "$phase_root/inventory.json" ]] || placement="$(jq '.nodes|sort_by(.id)|map({id,region,zone,instance_type})' "$phase_root/inventory.json")"
        jq -n --arg schema_version bloc-ec2-three-region-phase/v1 --arg experiment_id "$phase_id" --arg status invalid --arg invalid_reason "phase exit $original_status; cleanup exit $cleanup_status" --arg source_sha "$source_sha" --arg topology T2-three-region --argjson node_count "$node_count" --arg primary_region "$primary_region" --arg secondary_region "$secondary_region" --arg tertiary_region "$tertiary_region" --arg operator_instance_type "$operator_type" --arg controller_instance_type "$controller_type" --arg cpu_credits "$cpu_credits" --arg docker_image_digest "${phase_digest:-}" --argjson placement "$placement" '{schema_version:$schema_version,experiment_id:$experiment_id,status:$status,invalid_reason:$invalid_reason,source_sha:$source_sha,topology:$topology,node_count:$node_count,placement:$placement,primary_region:$primary_region,secondary_region:$secondary_region,tertiary_region:$tertiary_region,operator_instance_type:$operator_instance_type,controller_instance_type:$controller_instance_type,cpu_credits:$cpu_credits,docker_image_digest:$docker_image_digest}' >"$phase_root/manifest.json"
      fi
      jq -n --argjson node_count "$node_count" --arg status invalid --arg artifact_root "$phase_root" '{node_count:$node_count,status:$status,artifact_root:$artifact_root}' >>"$phases_file"
    elif [[ "$phase_record_ready" -eq 1 ]]; then
      jq -n --argjson node_count "$node_count" --arg status complete --arg artifact_root "$phase_root" --arg image_digest "$phase_digest" '{node_count:$node_count,status:$status,artifact_root:$artifact_root,image_digest:$image_digest}' >>"$phases_file"
    fi
    exit "$final_status"
  }
  trap cleanup_phase EXIT
  trap 'exit 130' INT; trap 'exit 143' TERM; trap 'exit 129' HUP

  admin_tf="$(printf '%s\n' "${admin_cidrs[@]}"|jq -R .|jq -s .)"
  {
    printf 'primary_region = "%s"\nsecondary_region = "%s"\ntertiary_region = "%s"\n' "$primary_region" "$secondary_region" "$tertiary_region"
    printf 'primary_availability_zone = "%s"\nsecondary_availability_zone = "%s"\ntertiary_availability_zone = "%s"\n' "$primary_az" "$secondary_az" "$tertiary_az"
    printf 'name_prefix = "%s"\nnode_count = %s\noperator_instance_type = "%s"\ncontroller_instance_type = "%s"\ncpu_credits = "%s"\n' "$phase_id" "$node_count" "$operator_type" "$controller_type" "$cpu_credits"
    printf 'primary_key_name = "%s"\nsecondary_key_name = "%s"\ntertiary_key_name = "%s"\nadmin_cidrs = %s\necr_repository_name = "%s"\n' "$primary_key_name" "$secondary_key_name" "$tertiary_key_name" "$admin_tf" "bloc-node-$phase_id"
  } >"$work/campaign.tfvars"
  (cd "$work" && terraform init -input=false && terraform fmt campaign.tfvars && terraform fmt -check -diff && terraform validate && AWS_PROFILE="$aws_profile" terraform plan -input=false -var-file=campaign.tfvars -out=campaign.tfplan && terraform show -no-color campaign.tfplan >"$phase_root/terraform-plan.txt" && terraform show -json campaign.tfplan >"$phase_root/terraform-plan.json")
  for forbidden in aws_nat_gateway aws_lb aws_eks_cluster aws_db_instance aws_eip aws_autoscaling_group aws_ec2_transit_gateway; do ! grep -q "$forbidden" "$phase_root/terraform-plan.txt" || bloc_die "forbidden resource in plan: $forbidden"; done
  allowed=' aws_vpc aws_subnet aws_internet_gateway aws_route_table aws_route_table_association aws_vpc_peering_connection aws_vpc_peering_connection_accepter aws_route aws_ecr_repository aws_iam_role aws_iam_role_policy aws_iam_instance_profile aws_security_group aws_instance '
  planned_types="$(sed -n 's/^[[:space:]]*#[[:space:]]*\(aws_[a-z0-9_]*\)\..* will be created$/\1/p' "$phase_root/terraform-plan.txt"|sort -u)"
  while IFS= read -r resource_type; do [[ -z "$resource_type" || "$allowed" == *" $resource_type "* ]] || bloc_die "unexpected resource type in plan: $resource_type"; done <<<"$planned_types"
  expected_adds=$((36 + node_count)); grep -Eq "Plan:[[:space:]]+$expected_adds to add, 0 to change, 0 to destroy" "$phase_root/terraform-plan.txt" || bloc_die "plan does not contain exactly $expected_adds additions"
  planned_instances="$(grep -Ec '^[[:space:]]*#[[:space:]]+aws_instance\..* will be created$' "$phase_root/terraform-plan.txt" || true)"; [[ "$planned_instances" -eq $((node_count + 1)) ]] || bloc_die "plan instance count mismatch"
  jq -e --arg instance_type "$operator_type" --arg cpu_credits "$cpu_credits" --argjson expected "$((node_count + 1))" '
    [.planned_values.root_module.resources[] | select(.type=="aws_instance")] as $instances |
    ($instances|length)==$expected and all($instances[];
      .values.instance_type==$instance_type and
      .values.monitoring==true and
      .values.metadata_options[0].http_tokens=="required" and
      .values.metadata_options[0].http_put_response_hop_limit==2 and
      .values.credit_specification[0].cpu_credits==$cpu_credits and
      .values.root_block_device[0].encrypted==true and
      .values.root_block_device[0].delete_on_termination==true
    )' "$phase_root/terraform-plan.json" >/dev/null || bloc_die "plan violates the IMDSv2, monitoring, T3 credit, or encrypted-EBS instance contract"
  [[ "$(grep -Ec '^[[:space:]]*#[[:space:]]+aws_vpc_peering_connection\..* will be created$' "$phase_root/terraform-plan.txt" || true)" -eq 3 ]] || bloc_die "plan must create three peering requests"
  [[ "$(grep -Ec '^[[:space:]]*#[[:space:]]+aws_vpc_peering_connection_accepter\..* will be created$' "$phase_root/terraform-plan.txt" || true)" -eq 3 ]] || bloc_die "plan must create three peering accepters"
  [[ "$(grep -Ec '^[[:space:]]*#[[:space:]]+aws_route\..* will be created$' "$phase_root/terraform-plan.txt" || true)" -eq 6 ]] || bloc_die "plan must create six peer routes"
  for type_and_count in 'aws_vpc 3' 'aws_subnet 3' 'aws_internet_gateway 3' 'aws_route_table 3' 'aws_route_table_association 3' 'aws_security_group 4' 'aws_ecr_repository 1' 'aws_iam_role 1' 'aws_iam_role_policy 1' 'aws_iam_instance_profile 1'; do
    resource_type="${type_and_count% *}"; expected_count="${type_and_count##* }"
    actual_count="$(grep -Ec "^[[:space:]]*#[[:space:]]+$resource_type\\..* will be created$" "$phase_root/terraform-plan.txt" || true)"
    [[ "$actual_count" -eq "$expected_count" ]] || bloc_die "plan must create exactly $expected_count $resource_type resources (found $actual_count)"
  done
  unexpected_instances="$(sed -n 's/^[[:space:]]*instance_type[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$phase_root/terraform-plan.txt"|grep -Ev "^($operator_type|$controller_type)$" || true)"; [[ -z "$unexpected_instances" ]] || bloc_die "plan contains unexpected instance type"
  if [[ "$plan_only" -eq 1 ]]; then jq -n --argjson node_count "$node_count" --arg status planned --arg artifact_root "$phase_root" '{node_count:$node_count,status:$status,artifact_root:$artifact_root}' >>"$phases_file"; phase_ok=1; return 0; fi
  if [[ "$auto_plan" -ne 1 ]]; then read -r -p "Type APPLY to create three-region phase n=$node_count: " answer; [[ "$answer" == APPLY ]] || bloc_die "operator declined apply"; fi

  aws ec2 create-key-pair --profile "$aws_profile" --region "$primary_region" --key-name "$primary_key_name" --key-type rsa --key-format pem --query KeyMaterial --output text >"$primary_key"
  aws ec2 create-key-pair --profile "$aws_profile" --region "$secondary_region" --key-name "$secondary_key_name" --key-type rsa --key-format pem --query KeyMaterial --output text >"$secondary_key"
  aws ec2 create-key-pair --profile "$aws_profile" --region "$tertiary_region" --key-name "$tertiary_key_name" --key-type rsa --key-format pem --query KeyMaterial --output text >"$tertiary_key"
  chmod 600 "$primary_key" "$secondary_key" "$tertiary_key"
  apply_attempted=1; (cd "$work" && AWS_PROFILE="$aws_profile" terraform apply -input=false -auto-approve campaign.tfplan && terraform output -json inventory >"$phase_root/inventory.json")
  inventory="$phase_root/inventory.json"
  jq -e --arg primary "$primary_region" --arg secondary "$secondary_region" --arg tertiary "$tertiary_region" --arg operator_type "$operator_type" --arg controller_type "$controller_type" --argjson count "$node_count" '.controller.region==$primary and .controller.instance_type==$controller_type and (.nodes|length)==$count and all(.nodes[]; .instance_type==$operator_type and .region==([ $primary,$secondary,$tertiary ][.id % 3]))' "$inventory" >/dev/null || bloc_die "inventory placement or instance-type acceptance failed"
  ecr_url="$(cd "$work" && terraform output -raw ecr_repository_url)"; registry="${ecr_url%%/*}"; image_uri="$ecr_url:$git_commit"
  aws ecr get-login-password --profile "$aws_profile" --region "$primary_region"|docker login --username AWS --password-stdin "$registry"
  docker tag "$image_tag" "$image_uri"; docker push "$image_uri"
  phase_digest="$(aws ecr describe-images --profile "$aws_profile" --region "$primary_region" --repository-name "bloc-node-$phase_id" --image-ids "imageTag=$git_commit" --query 'imageDetails[0].imageDigest' --output text)"
  if [[ -f "$campaign_root/image-digest.txt" ]]; then [[ "$(sed -n '1p' "$campaign_root/image-digest.txt")" == "$phase_digest" ]] || bloc_die "image digest changed between phases"; else printf '%s\n' "$phase_digest" >"$campaign_root/image-digest.txt"; fi
  runtime_image="$ecr_url@$phase_digest"

  cluster="$phase_root/generated/cluster.ec2.json"; remote="$phase_root/generated/remote-eval.ec2.json"; prometheus="$phase_root/generated/prometheus.ec2.yml"
  (cd "$repo_root/bloc-node" && go run ./cmd/bloc-node gen-ec2-config --inventory "$inventory" --cluster-out "$cluster" --crs-out "$phase_root/generated/cluster.ec2.crs" --secrets-dir "$phase_root/generated/secrets.ec2" --remote-eval-out "$remote" --cluster-id "$phase_id" --nodes "$node_count" --bmax 128)
  write_prometheus_config "$inventory" "$prometheus"
  while IFS= read -r host; do
    public="$(jq -r .public_ip <<<"$host")"; key="$(key_for "$host")"; ready=0
    for attempt in $(seq 1 60); do if ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 "ubuntu@$public" 'cloud-init status --wait >/dev/null && docker version >/dev/null && docker compose version >/dev/null'; then ready=1; break; fi; sleep 10; done
    [[ "$ready" -eq 1 ]] || bloc_die "host $public did not become ready"
  done < <(jq -c '[.controller]+.nodes|.[]' "$inventory")
  while IFS= read -r node; do
    id="$(jq -r .id <<<"$node")"; public="$(jq -r .public_ip <<<"$node")"; key="$(key_for "$node")"
    ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" 'sudo mkdir -p /etc/bloc /opt/bloc/ec2 && sudo chown -R ubuntu:ubuntu /opt/bloc /etc/bloc'
    scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$cluster" "ubuntu@$public:/etc/bloc/cluster.json"
    scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$phase_root/generated/cluster.ec2.crs" "ubuntu@$public:/etc/bloc/cluster.crs"
    scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$phase_root/generated/secrets.ec2/operator-$id.json" "ubuntu@$public:/etc/bloc/operator.json"
    ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" 'sudo chown 10001:10001 /etc/bloc/operator.json && sudo chmod 600 /etc/bloc/operator.json'
    scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$script_dir/operator-compose.yaml" "ubuntu@$public:/opt/bloc/ec2/operator-compose.yaml"
    scp -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$script_dir/sample-container-resources.sh" "ubuntu@$public:/opt/bloc/ec2/sample-container-resources.sh"
    ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" 'chmod 700 /opt/bloc/ec2/sample-container-resources.sh'
    ssh -n -i "$key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$public" "aws ecr get-login-password --region '$primary_region'|docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$id' BLOC_IMAGE='$runtime_image' docker compose -f operator-compose.yaml up -d"
  done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
  rm -rf "$phase_root/generated/secrets.ec2"
  controller="$(jq -c .controller "$inventory")"; controller_public="$(jq -r .public_ip <<<"$controller")"; controller_key="$(key_for "$controller")"
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" 'sudo mkdir -p /opt/bloc/ec2 /opt/bloc/docker-compose/grafana && sudo chown -R ubuntu:ubuntu /opt/bloc'
  scp -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$remote" "$prometheus" "$script_dir/controller-compose.yaml" "ubuntu@$controller_public:/opt/bloc/ec2/"
  scp -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -r "$repo_root/deploy/docker-compose/grafana/." "ubuntu@$controller_public:/opt/bloc/docker-compose/grafana/"
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" "aws ecr get-login-password --region '$primary_region'|docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; docker compose -f controller-compose.yaml up -d"
  while IFS= read -r private; do ready=0; for attempt in $(seq 1 30); do if ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" "curl --max-time 5 -fsS http://$private:8000/healthz"; then ready=1; break; fi; sleep 5; done; [[ "$ready" -eq 1 ]] || bloc_die "operator $private did not become healthy"; done < <(jq -r '.nodes|sort_by(.id)[]|.private_ip' "$inventory")
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" 'curl -fsS http://127.0.0.1:9090/api/v1/targets' >"$phase_root/prometheus-targets-before.json"
  assert_prometheus_targets "$phase_root/prometheus-targets-before.json" "$node_count"
  collect_pairwise_network "$inventory" pre "$phase_root/network-pre.csv"; assert_network_matrix "$phase_root/network-pre.csv" $((node_count * node_count))
  next_slot=1; specs=()
  for batch in "${batches[@]}"; do
    remote_dir="/opt/bloc/ec2/results/$phase_id/batch-$batch"
    ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" "sudo mkdir -p '$remote_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$runtime_image' eval-remote --config remote-eval.ec2.json --experiment-id '$phase_id-b$batch' --first-slot '$next_slot' --batch-size '$batch' --warmups '$warmups' --repetitions '$repetitions' --out-dir 'results/$phase_id/batch-$batch' --image-tag '$runtime_image' --git-commit '$git_commit' --timeout '$eval_timeout'"
    mkdir -p "$phase_root/scenarios/batch-$batch"; scp -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -r "ubuntu@$controller_public:$remote_dir" "$phase_root/scenarios/batch-$batch/results"
    bloc_python "$repo_root" assert-evaluator --csv "$phase_root/scenarios/batch-$batch/results/run_measurements.csv" --expected "$node_count/$batch=$repetitions"
    specs+=("1:$phase_root/scenarios/batch-$batch/results")
    next_slot=$((next_slot + warmups + repetitions))
  done
  bloc_python "$repo_root" merge-scenarios --root "$phase_root" "${specs[@]}"
  bloc_python "$repo_root" annotate-placement --phase-root "$phase_root" --inventory "$inventory"
  run_resource_phase
  collect_pairwise_network "$inventory" post "$phase_root/network-post.csv"; assert_network_matrix "$phase_root/network-post.csv" $((node_count * node_count))
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" 'curl -fsS http://127.0.0.1:9090/api/v1/targets' >"$phase_root/prometheus-targets.json"
  assert_prometheus_targets "$phase_root/prometheus-targets.json" "$node_count"
  while IFS= read -r node; do ssh -n -i "$(key_for "$node")" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$(jq -r .public_ip <<<"$node")" 'docker logs --timestamps ec2-bloc-node-1 2>&1' >"$phase_root/logs/operator-$(jq -r .id <<<"$node").log"; done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" 'docker logs --timestamps ec2-prometheus-1 2>&1' >"$phase_root/logs/prometheus.log" || true
  ssh -n -i "$controller_key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ubuntu@$controller_public" 'docker logs --timestamps ec2-grafana-1 2>&1' >"$phase_root/logs/grafana.log" || true
  peerings="$(cd "$work" && terraform output -json peering_connection_ids)"
  jq -n --arg schema_version bloc-ec2-three-region-phase/v1 --arg experiment_id "$phase_id" --arg status complete --arg source_sha "$source_sha" --arg topology T2-three-region --argjson node_count "$node_count" --arg primary_region "$primary_region" --arg secondary_region "$secondary_region" --arg tertiary_region "$tertiary_region" --arg operator_instance_type "$operator_type" --arg controller_instance_type "$controller_type" --arg cpu_credits "$cpu_credits" --arg docker_image "$runtime_image" --arg docker_image_digest "$phase_digest" --arg git_commit "$git_commit" --arg batch_sizes "$batch_sizes_csv" --argjson warmups "$warmups" --argjson repetitions "$repetitions" --arg eval_timeout "$eval_timeout" --argjson placement "$(jq '.nodes|sort_by(.id)|map({id,region,zone,instance_type})' "$inventory")" --argjson peering_connection_ids "$peerings" '{schema_version:$schema_version,experiment_id:$experiment_id,status:$status,source_sha:$source_sha,topology:$topology,node_count:$node_count,placement:$placement,primary_region:$primary_region,secondary_region:$secondary_region,tertiary_region:$tertiary_region,peering_connection_ids:$peering_connection_ids,operator_instance_type:$operator_instance_type,controller_instance_type:$controller_instance_type,cpu_credits:$cpu_credits,docker_image:$docker_image,docker_image_digest:$docker_image_digest,git_commit:$git_commit,batch_sizes:($batch_sizes|split(",")|map(tonumber)),warmups:$warmups,repetitions:$repetitions,eval_timeout:$eval_timeout}' >"$phase_root/manifest.json"
  bloc_python "$repo_root" assert-three-region-phase --phase-root "$phase_root"
  phase_ok=1; phase_record_ready=1
)

for node in "${nodes[@]}"; do
  run_phase "$node"
  if [[ "$auto_phases" -ne 1 && "$node" != "${nodes[${#nodes[@]}-1]}" ]]; then read -r -p "Type NEXT to continue: " answer; [[ "$answer" == NEXT ]] || bloc_die "operator stopped campaign"; fi
done
jq -s . "$phases_file" >"$campaign_root/phases.json"
if [[ "$plan_only" -ne 1 ]]; then
  for name in run_measurements.csv node_measurements.csv scenario_summary.csv network-pre.csv network-post.csv resource_timeseries.csv resource-summary.csv; do inputs=(); for node in "${nodes[@]}"; do inputs+=("$campaign_root/n$node/$name"); done; bloc_python "$repo_root" merge-csv --output "$campaign_root/$name" "${inputs[@]}"; done
  inputs=(); for node in "${nodes[@]}"; do inputs+=("$campaign_root/n$node/scenario_summary.json"); done; bloc_python "$repo_root" merge-json --output "$campaign_root/scenario_summary.json" "${inputs[@]}"
fi
write_campaign_manifest "$( [[ "$plan_only" -eq 1 ]] && echo planned || echo complete)" ""
if [[ "$skip_charts" -ne 1 && "$plan_only" -ne 1 ]]; then
  charts_root="$repo_root/results/charts/$campaign_id"
  (cd "$repo_root/latency-charts" && .venv/bin/python -m bloc_latency_charts.three_region "$campaign_root" --output "$charts_root")
fi
campaign_complete=1
