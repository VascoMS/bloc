#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
# shellcheck source=../../scripts/lib/campaign-common.sh
source "$repo_root/scripts/lib/campaign-common.sh"

usage() {
  cat <<'EOF'
Usage:
  bash deploy/ec2/run-a1-pilot.sh --admin-cidr CIDR [options]

Required:
  --admin-cidr CIDR              CIDR allowed to SSH/Prometheus/Grafana, e.g. 1.2.3.4/32.

Options:
  --aws-profile NAME             AWS profile to use. Default: bloc
  --aws-region REGION            AWS region. Default: us-east-1
  --availability-zone AZ         Single-AZ pilot placement. Default: us-east-1a
  --availability-zones LIST      Comma-separated multi-AZ placement.
  --subnet-cidrs LIST            Comma-separated generated subnet CIDRs.
  --node-count N                 Operator count. Default: 4
  --operator-instance-type TYPE  Operator instance type. Default: t3.small
  --controller-instance-type TYPE Controller instance type. Default: t3.small
  --batch-sizes LIST             Comma-separated batch sizes. Default: 8,32,128
  --warmups N                    Warmups per batch. Default: 1
  --repetitions N                Measured repetitions per batch. Default: 3
  --repetition-blocks N          Split measurements into N balanced blocks. Default: 1
  --batch-order-block LIST       Repeat once per block; each list is a permutation of batches.
  --prebuilt-image-tag TAG       Reuse an existing local image.
  --ecr-image-tag TAG            ECR tag; defaults to the Git commit.
  --max-runtime-minutes N        Fail the phase after this many minutes; 0 disables.
  --campaign-label LABEL         Manifest campaign label.
  --topology LABEL               Manifest topology label.
  --experiment-id ID             Stable experiment id. Default: bloc-ec2-a1-pilot-same-az-n<N>-<UTC stamp>
                                  ECR mode requires a bloc-ec2-* id to match the scoped IAM policy.
  --image-distribution MODE      ecr or ssh-load. Default: ecr
  --auto-approve-plan            Apply Terraform without interactive APPLY prompt.
  --keep-resources-on-failure    Do not destroy Terraform resources after a failure.
  --keep-resources-after-run     Keep resources after a successful run.
  --skip-chart-generation        Do not run latency chart generation.
  --validate-only                Validate arguments/dependencies without writing or provisioning.
  -h, --help                     Show this help.

This script creates AWS resources. By default it destroys them in a cleanup trap.
EOF
}

aws_profile="bloc"
aws_region="us-east-1"
availability_zone="us-east-1a"
availability_zones_csv=""
subnet_cidrs_csv=""
node_count="4"
operator_instance_type="t3.small"
controller_instance_type="t3.small"
batch_sizes_csv="8,32,128"
warmups="1"
repetitions="3"
repetition_blocks="1"
batch_order_blocks=()
prebuilt_image_tag=""
ecr_image_tag=""
max_runtime_minutes="0"
campaign_label="A1-pilot-same-az"
topology="T0-same-az"
experiment_id=""
auto_approve_plan="0"
keep_resources_on_failure="0"
keep_resources_after_run="0"
skip_chart_generation="0"
validate_only="0"
image_distribution="ecr"
admin_cidrs=()

bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --admin-cidr) admin_cidrs+=("$2"); shift 2 ;;
    --aws-profile) aws_profile="$2"; shift 2 ;;
    --aws-region) aws_region="$2"; shift 2 ;;
    --availability-zone) availability_zone="$2"; shift 2 ;;
    --availability-zones) availability_zones_csv="$2"; shift 2 ;;
    --subnet-cidrs) subnet_cidrs_csv="$2"; shift 2 ;;
    --node-count) node_count="$2"; shift 2 ;;
    --operator-instance-type) operator_instance_type="$2"; shift 2 ;;
    --controller-instance-type) controller_instance_type="$2"; shift 2 ;;
    --batch-sizes) batch_sizes_csv="$2"; shift 2 ;;
    --warmups) warmups="$2"; shift 2 ;;
    --repetitions) repetitions="$2"; shift 2 ;;
    --repetition-blocks) repetition_blocks="$2"; shift 2 ;;
    --batch-order-block) batch_order_blocks+=("$2"); shift 2 ;;
    --prebuilt-image-tag) prebuilt_image_tag="$2"; shift 2 ;;
    --ecr-image-tag) ecr_image_tag="$2"; shift 2 ;;
    --max-runtime-minutes) max_runtime_minutes="$2"; shift 2 ;;
    --campaign-label) campaign_label="$2"; shift 2 ;;
    --topology) topology="$2"; shift 2 ;;
    --experiment-id) experiment_id="$2"; shift 2 ;;
    --image-distribution) image_distribution="$2"; shift 2 ;;
    --auto-approve-plan) auto_approve_plan="1"; shift ;;
    --keep-resources-on-failure) keep_resources_on_failure="1"; shift ;;
    --keep-resources-after-run) keep_resources_after_run="1"; shift ;;
    --skip-chart-generation) skip_chart_generation="1"; shift ;;
    --validate-only) validate_only="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ ${#admin_cidrs[@]} -eq 0 ]]; then
  echo "--admin-cidr is required; use a /32 for your current admin IP." >&2
  exit 2
fi
if [[ "$image_distribution" != "ecr" && "$image_distribution" != "ssh-load" ]]; then
  echo "--image-distribution must be ecr or ssh-load" >&2
  exit 2
fi

bloc_is_positive_int "$node_count" || bloc_usage_error "--node-count must be positive"
[[ "$node_count" -le 10 ]] || bloc_usage_error "--node-count must be at most 10"
bloc_validate_csv_positive "$batch_sizes_csv" BatchSizes
bloc_is_uint "$warmups" || bloc_usage_error "--warmups must be non-negative"
bloc_is_positive_int "$repetitions" || bloc_usage_error "--repetitions must be positive"
bloc_is_positive_int "$repetition_blocks" || bloc_usage_error "--repetition-blocks must be positive"
[[ $((repetitions % repetition_blocks)) -eq 0 ]] || bloc_usage_error "--repetitions must be divisible by --repetition-blocks"
bloc_is_uint "$max_runtime_minutes" || bloc_usage_error "--max-runtime-minutes must be non-negative"
[[ -z "$availability_zones_csv" ]] || bloc_csv_each "$availability_zones_csv" ':' AvailabilityZones
[[ -z "$subnet_cidrs_csv" ]] || bloc_csv_each "$subnet_cidrs_csv" ':' SubnetCidrs
if [[ "${#batch_order_blocks[@]}" -eq 0 ]]; then
  i=0; while [[ "$i" -lt "$repetition_blocks" ]]; do batch_order_blocks+=("$batch_sizes_csv"); i=$((i+1)); done
fi
[[ -n "$availability_zones_csv" ]] || availability_zones_csv="$availability_zone"
[[ "${#batch_order_blocks[@]}" -eq "$repetition_blocks" ]] || bloc_usage_error "provide exactly one --batch-order-block per repetition block"
python3 - "$batch_sizes_csv" "${batch_order_blocks[@]}" <<'PY' || exit 2
import sys
expected=sorted(sys.argv[1].split(','))
for order in sys.argv[2:]:
    if sorted(order.split(',')) != expected:
        raise SystemExit("each --batch-order-block must contain every configured batch exactly once")
PY

stamp="$(date -u +%Y%m%dt%H%M%sz)"
if [[ -z "$experiment_id" ]]; then experiment_id="bloc-ec2-a1-pilot-same-az-n${node_count}-${stamp}"; fi
bloc_validate_id "$experiment_id" ExperimentId
if [[ "$image_distribution" == "ecr" && "$experiment_id" != bloc-ec2-* ]]; then
  bloc_usage_error "--experiment-id must start with bloc-ec2- when --image-distribution=ecr"
fi
bloc_require_dir "$script_dir/terraform"

for command in aws terraform docker git jq ssh scp gzip rsync python3 go; do command -v "$command" >/dev/null 2>&1 || bloc_usage_error "required command not found: $command"; done
if [[ "$validate_only" == 1 ]]; then bloc_validate_only_message "run-a1-pilot.sh"; exit 0; fi

IFS=',' read -r -a batch_sizes <<< "$batch_sizes_csv"
terraform_dir="$script_dir/terraform"
ecr_repository_name="$(printf 'bloc-node-%s' "$experiment_id" | tr '[:upper:]' '[:lower:]' | sed 's#[^a-z0-9._/-]#-#g')"

artifact_root="$repo_root/results/ec2/$experiment_id"
key_name="${experiment_id}-key"
terraform_work_dir="$artifact_root/generated/terraform-work"
key_path="${TMPDIR:-/tmp}/${key_name}.pem"
tfvars_path="$terraform_work_dir/a1-pilot.tfvars"
plan_path="$terraform_work_dir/a1-pilot.tfplan"
image_tar_path="$artifact_root/generated/bloc-node-image.tar"
docker_context_dir="${TMPDIR:-/tmp}/${experiment_id}-docker-context"
campaign_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
campaign_started_epoch="$(date +%s)"

terraform_applied="0"
terraform_started="0"
key_created="0"
git_commit=""
local_image_uri=""
ecr_repository_url=""
image_uri=""
registry=""
invalid_reason=""
campaign_status="invalid"

mkdir -p "$artifact_root"/{generated,logs,scenarios}

log() {
  printf '\n==> %s\n' "$*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

json_escape_array() {
  local first="1"
  printf '['
  for item in "$@"; do
    if [[ "$first" == "0" ]]; then printf ', '; fi
    first="0"
    printf '"%s"' "$item"
  done
  printf ']'
}

ssh_ec2() {
  local host="$1"
  shift
  ssh -i "$key_path"     -o StrictHostKeyChecking=no     -o UserKnownHostsFile=/dev/null     -o ConnectTimeout=10     "ubuntu@$host" "$@"
}

scp_ec2() {
  scp -i "$key_path"     -o StrictHostKeyChecking=no     -o UserKnownHostsFile=/dev/null     "$@"
}

retry_ssh_ec2() {
  local host="$1"
  local attempts="$2"
  local delay="$3"
  local description="$4"
  shift 4

  local attempt
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if ssh_ec2 "$host" "$@"; then
      return 0
    fi
    if [[ "$attempt" -lt "$attempts" ]]; then
      printf 'waiting for %s (%d/%d)
' "$description" "$attempt" "$attempts" >&2
      sleep "$delay"
    fi
  done

  printf 'timed out waiting for %s after %d attempts
' "$description" "$attempts" >&2
  return 1
}

write_prometheus_config() {
  local inventory="$1"
  local out="$2"
  {
    echo "global:"
    echo "  scrape_interval: 2s"
    echo "scrape_configs:"
    echo "  - job_name: 'bloc-sidecars'"
    echo "    metrics_path: /metrics"
    echo "    static_configs:"
    echo "      - targets:"
    jq -r '.nodes | sort_by(.id)[] | "          - " + .private_ip + ":8000"' "$inventory"
  } > "$out"
}

collect_network_matrix() {
  local phase="$1"
  local out="$2"
  local controller_public_ip="$3"
  local inventory="$4"

  echo "phase,source,target_node_id,target_private_ip,transmitted,received,loss_percent,avg_rtt_ms" > "$out"
  jq -c '.nodes | sort_by(.id)[]' "$inventory" | while read -r node; do
    local node_id private_ip
    node_id="$(jq -r '.id' <<< "$node")"
    private_ip="$(jq -r '.private_ip' <<< "$node")"
    if ! ping_output="$(ssh_ec2 "$controller_public_ip" "ping -c 5 '$private_ip' || true")"; then
      line="$phase,controller,$node_id,$private_ip,error,error,error,error"
    elif [[ -z "$ping_output" ]]; then
      line="$phase,controller,$node_id,$private_ip,error,error,error,error"
    else
      line="$(awk -v phase="$phase" -v node_id="$node_id" -v ip="$private_ip" '
        /packets transmitted/ { tx=$1; rx=$4; loss=$6; gsub(/%/, "", loss) }
        /rtt/ { split($4, a, "/"); avg=a[2] }
        END { printf "%s,controller,%s,%s,%s,%s,%s,%s", phase, node_id, ip, tx, rx, loss, avg }
      ' <<< "$ping_output")"
    fi
    echo "$line" >> "$out"
  done
}

collect_resource_sample() {
  local phase="$1" batch="$2" inventory="$3" out="$4" timestamp node node_id public_ip private_ip sample
  timestamp="$(bloc_utc_iso)"
  if [[ ! -f "$out" ]]; then printf 'timestamp,phase,batch_size,node_id,private_ip,container,cpu_percent,mem_usage,mem_percent,net_io,block_io,pids\n' >"$out"; fi
  while IFS= read -r node; do
    node_id="$(jq -r .id <<<"$node")"; public_ip="$(jq -r .public_ip <<<"$node")"; private_ip="$(jq -r .private_ip <<<"$node")"
    sample="$(ssh_ec2 "$public_ip" "docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}},{{.PIDs}}' ec2-bloc-node-1" 2>/dev/null || true)"
    if [[ -z "$sample" ]]; then sample='error,error,error,error,error,error,error'; fi
    printf '%s,%s,%s,%s,%s,%s\n' "$timestamp" "$phase" "$batch" "$node_id" "$private_ip" "$sample" >>"$out"
  done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
}

run_resource_phase() {
  # This separate pass is deliberately after all primary latency measurements:
  # the 250 ms sampler is never present during latency/p99 collection.
  local batch node node_id public_ip region scenario remote_csv stop_file pid_file remote_dir part_dir next_resource_slot=100000
  local minimum_resource_rows=4 sampler_iteration_max_seconds=8 sampler_stop_timeout_seconds=10
  local parts=() summary_args=(resource-summary --input "$artifact_root/resource_timeseries.csv" --output "$artifact_root/resource-summary.csv")
  mkdir -p "$artifact_root/resource-parts"
  for node_id in $(jq -r '.nodes|sort_by(.id)[]|.id' "$inventory_path"); do summary_args+=(--expected-node "$node_id"); done
  for batch in "${batch_sizes[@]}"; do
    scenario="n${node_count}-b${batch}"; part_dir="$artifact_root/resource-parts/$scenario"; mkdir -p "$part_dir"
    summary_args+=(--expected-configuration "$scenario:resource-measured")
    while IFS= read -r node; do
      node_id="$(jq -r .id <<<"$node")"; public_ip="$(jq -r .public_ip <<<"$node")"; region="$(jq -r .region <<<"$node")"
      remote_csv="/opt/bloc/ec2/resources/$scenario.csv"; stop_file="/opt/bloc/ec2/resources/$scenario.stop"; pid_file="/opt/bloc/ec2/resources/$scenario.sampler.pid"
      ssh_ec2 "$public_ip" "mkdir -p /opt/bloc/ec2/resources; rm -f '$stop_file' '$pid_file'; nohup /opt/bloc/ec2/sample-container-resources.sh run --container ec2-bloc-node-1 --output '$remote_csv' --stop-file '$stop_file' --node '$node_id' --region '$region' --scenario '$scenario' --phase resource-measured >/opt/bloc/ec2/resources/$scenario.log 2>&1 & echo \$! >'$pid_file'"
    done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory_path")
    remote_dir="/opt/bloc/ec2/results/$experiment_id/resource-$scenario"
    ssh_ec2 "$controller_public_ip" "sudo mkdir -p '$remote_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '$experiment_id-resource-$scenario' --first-slot '$next_resource_slot' --batch-size '$batch' --warmups 0 --repetitions '$repetitions' --out-dir 'results/$experiment_id/resource-$scenario' --image-tag '$image_uri' --git-commit '$git_commit' --timeout 30s"
    while IFS= read -r node; do
      node_id="$(jq -r .id <<<"$node")"; public_ip="$(jq -r .public_ip <<<"$node")"; remote_csv="/opt/bloc/ec2/resources/$scenario.csv"; stop_file="/opt/bloc/ec2/resources/$scenario.stop"; pid_file="/opt/bloc/ec2/resources/$scenario.sampler.pid"
      ssh_ec2 "$public_ip" "set -e; test -s '$pid_file' || exit 1; kill -0 \$(cat '$pid_file') || exit 1; for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do test -s '$pid_file' || exit 1; kill -0 \$(cat '$pid_file') || exit 1; data_rows=\$(( \$(wc -l < '$remote_csv') - 1 )); [ \"\$data_rows\" -ge '$minimum_resource_rows' ] && break; sleep 0.25; done; data_rows=\$(( \$(wc -l < '$remote_csv') - 1 )); [ \"\$data_rows\" -ge '$minimum_resource_rows' ] || { echo 'resource sampler did not produce four data rows within ${sampler_stop_timeout_seconds}s (iteration bound ${sampler_iteration_max_seconds}s)' >&2; exit 1; }; test -s '$pid_file' || exit 1; kill -0 \$(cat '$pid_file') || exit 1; touch '$stop_file'; for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do if ! kill -0 \$(cat '$pid_file') 2>/dev/null; then test -s '$remote_csv'; exit 0; fi; sleep 0.25; done; exit 1"
      scp_ec2 "ubuntu@$public_ip:$remote_csv" "$part_dir/node-$node_id.csv"; parts+=("$part_dir/node-$node_id.csv")
    done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory_path")
    next_resource_slot=$((next_resource_slot + repetitions))
  done
  bloc_python "$repo_root" merge-csv --output "$artifact_root/resource_timeseries.csv" "${parts[@]}"
  bloc_python "$repo_root" "${summary_args[@]}"
}

merge_csv_outputs() {
  local root="$1"
  local args=(merge-scenarios --root "$root")
  [[ "$repetition_blocks" -gt 1 ]] && args+=(--multiple-blocks)
  args+=("${scenario_specs[@]}")
  bloc_python "$repo_root" "${args[@]}"
}

write_manifest() {
  local status="$1"
  local reason="${2:-}"
  local inventory_json="null"
  local cleanup_json="{}"
  local terraform_json="{}"

  [[ -f "$artifact_root/inventory.json" ]] && inventory_json="$(cat "$artifact_root/inventory.json")"
  [[ -f "$artifact_root/cleanup-verification.json" ]] && cleanup_json="$(cat "$artifact_root/cleanup-verification.json")"
  [[ -f "$artifact_root/terraform-metadata.json" ]] && terraform_json="$(cat "$artifact_root/terraform-metadata.json")"

  jq -n \
    --arg schema "bloc-ec2-campaign/v1" \
    --arg experiment_id "$experiment_id" \
    --arg campaign "$campaign_label" \
    --arg status "$status" \
    --arg invalid_reason "$reason" \
    --arg started_at "$campaign_started_at" \
    --arg finished_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg git_commit "$git_commit" \
    --arg docker_image "$image_uri" \
    --arg ecr_repository_name "$ecr_repository_name" \
    --arg aws_region "$aws_region" \
    --arg availability_zone "$availability_zone" \
    --arg availability_zones "$availability_zones_csv" \
    --arg node_count "$node_count" \
    --arg operator_instance_type "$operator_instance_type" \
    --arg controller_instance_type "$controller_instance_type" \
    --arg warmups "$warmups" \
    --arg repetitions "$repetitions" \
    --arg repetition_blocks "$repetition_blocks" \
    --arg topology "$topology" \
    --argjson batch_order_blocks "$(printf '%s\n' "${batch_order_blocks[@]}" | jq -R 'split(",")' | jq -s '.')" \
    --argjson batch_sizes "$(printf '%s\n' "${batch_sizes[@]}" | jq -R 'tonumber' | jq -s '.')" \
    --argjson inventory "$inventory_json" \
    --argjson terraform "$terraform_json" \
    --argjson cleanup "$cleanup_json" \
    '{
      schema_version: $schema,
      experiment_id: $experiment_id,
      campaign: $campaign,
      status: $status,
      invalid_reason: (if $invalid_reason == "" then null else $invalid_reason end),
      started_at: $started_at,
      finished_at: $finished_at,
      git_commit: $git_commit,
      docker_image: $docker_image,
      ecr_repository_name: $ecr_repository_name,
      aws_region: $aws_region,
      availability_zone: $availability_zone,
      node_count: ($node_count | tonumber),
      operator_instance_type: $operator_instance_type,
      controller_instance_type: $controller_instance_type,
      availability_zones: ($availability_zones|split(",")|map(select(length>0))),
      topology: $topology,
      tx_source: "synthetic",
      batch_sizes: $batch_sizes,
      warmups: ($warmups | tonumber),
      repetitions: ($repetitions | tonumber),
      repetition_blocks: ($repetition_blocks | tonumber),
      batch_order_blocks: $batch_order_blocks,
      terraform: $terraform,
      inventory: $inventory,
      cleanup_checks: $cleanup
    }' > "$artifact_root/manifest.json"
}

cleanup() {
  local exit_code=$?
  local cleanup_failed=0
  set +e

  if [[ "$exit_code" -ne 0 ]]; then
    invalid_reason="${invalid_reason:-runner failed with exit code $exit_code}"
    campaign_status="invalid"
  fi

  local should_keep="0"
  if [[ "$keep_resources_after_run" == "1" || ("$exit_code" -ne 0 && "$keep_resources_on_failure" == "1") ]]; then should_keep="1"; fi
  if [[ ("$terraform_applied" == "1" || "$terraform_started" == "1") && "$should_keep" != "1" ]]; then
    log "terraform destroy"
    (
      cd "$terraform_work_dir"
      if [[ -f "$tfvars_path" ]]; then
        AWS_PROFILE="$aws_profile" terraform destroy -var-file="$tfvars_path" -auto-approve
      else
        AWS_PROFILE="$aws_profile" terraform destroy -auto-approve
      fi
      terraform state list > "$artifact_root/terraform-state-after-destroy.txt" 2>/dev/null || true
    ) || cleanup_failed=1
  elif [[ ("$terraform_applied" == "1" || "$terraform_started" == "1") && "$should_keep" == "1" ]]; then
    echo "keeping AWS resources because an explicit keep switch was supplied" >&2
  fi

  if [[ "$key_created" == "1" && "$should_keep" != "1" ]]; then
    aws ec2 delete-key-pair --profile "$aws_profile" --region "$aws_region" --key-name "$key_name" >/dev/null 2>&1 || cleanup_failed=1
  fi
  if [[ "$should_keep" != "1" ]]; then rm -f "$key_path"; fi
  rm -f "$tfvars_path" "$plan_path"
  rm -rf "$docker_context_dir"

  local instance_check volume_check vpc_check key_check ecr_check iam_role_check instance_profile_check
  instance_check="$(aws ec2 describe-instances --profile "$aws_profile" --region "$aws_region" \
    --filters "Name=tag:Name,Values=$experiment_id-controller,$experiment_id-operator-*" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
    --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null)"
  vpc_check="$(aws ec2 describe-vpcs --profile "$aws_profile" --region "$aws_region" \
    --filters "Name=tag:Name,Values=$experiment_id-vpc" \
    --query 'Vpcs[].VpcId' --output text 2>/dev/null)"
  key_check="$(aws ec2 describe-key-pairs --profile "$aws_profile" --region "$aws_region" \
    --key-names "$key_name" --query 'KeyPairs[].KeyName' --output text 2>/dev/null)"
  ecr_check="$(aws ecr describe-repositories --profile "$aws_profile" --region "$aws_region" \
    --repository-names "$ecr_repository_name" --query 'repositories[].repositoryUri' --output text 2>/dev/null)"
  volume_check="$(aws ec2 describe-volumes --profile "$aws_profile" --region "$aws_region" --filters "Name=tag:Name,Values=$experiment_id-*" --query 'Volumes[].VolumeId' --output text 2>/dev/null)"
  iam_role_check="$(aws iam get-role --profile "$aws_profile" --role-name "$experiment_id-ec2-ecr-readonly" --query 'Role.RoleName' --output text 2>/dev/null)"
  instance_profile_check="$(aws iam get-instance-profile --profile "$aws_profile" --instance-profile-name "$experiment_id-ec2-ecr-readonly" --query 'InstanceProfile.InstanceProfileName' --output text 2>/dev/null)"

  jq -n \
    --arg instances "$instance_check" \
    --arg vpc "$vpc_check" \
    --arg volumes "$volume_check" \
    --arg key_pair "$key_check" \
    --arg ecr_repository "$ecr_check" \
    --arg iam_role "$iam_role_check" --arg instance_profile "$instance_profile_check" \
    '{instances:$instances,volumes:$volumes,vpc:$vpc,key_pair:$key_pair,ecr_repository:$ecr_repository,iam_role:$iam_role,instance_profile:$instance_profile}' \
    > "$artifact_root/cleanup-verification.json"

  if [[ "$should_keep" != "1" ]] && ! jq -e '[.[]|select(length>0 and .!="None")]|length == 0' "$artifact_root/cleanup-verification.json" >/dev/null; then
    cleanup_failed=1
  fi
  if [[ "$cleanup_failed" -ne 0 ]]; then
    exit_code=1
    campaign_status="invalid"
    invalid_reason="${invalid_reason:-AWS cleanup or cleanup verification failed}"
  fi

  write_manifest "$campaign_status" "$invalid_reason"
  exit "$exit_code"
}
trap cleanup EXIT

require_cmd aws
require_cmd terraform
require_cmd docker
require_cmd git
require_cmd jq
require_cmd ssh
require_cmd scp
require_cmd gzip
require_cmd rsync

cd "$repo_root"

log "local preflight"
git_commit="$(git rev-parse --short=12 HEAD)"
local_image_uri="bloc-node:${git_commit}"
aws sts get-caller-identity --profile "$aws_profile" --output json > "$artifact_root/aws-caller-identity.json"

log "prebuild Docker image"
docker version --format '{{.Server.Version}}' >/dev/null
if [[ -z "$prebuilt_image_tag" ]]; then
  rm -rf "$docker_context_dir"
  mkdir -p "$docker_context_dir"
  rsync -a --delete \
    --exclude '.git' --exclude '.gocache' --exclude '.cache' --exclude 'bin' \
    --exclude 'results' --exclude 'results-*' --exclude '.terraform' \
    --exclude 'terraform.tfstate*' --exclude '*.tfplan' --exclude '*.pem' \
    "$repo_root/bte" "$repo_root/sbc" "$repo_root/bloc-node" "$docker_context_dir/"
  docker build --platform linux/amd64 -f "$docker_context_dir/bloc-node/Dockerfile" -t "$local_image_uri" "$docker_context_dir"
else
  local_image_uri="$prebuilt_image_tag"
  docker image inspect "$local_image_uri" >/dev/null
fi
image_arch="$(docker image inspect "$local_image_uri" --format '{{.Architecture}}')"
[[ "$image_arch" == amd64 ]] || bloc_die "EC2 image architecture is $image_arch, expected amd64"

log "prepare isolated Terraform workdir"
rm -rf "$terraform_work_dir"
mkdir -p "$terraform_work_dir"
cp "$terraform_dir"/{main.tf,outputs.tf,variables.tf,user-data.sh} "$terraform_work_dir/"
if [[ -f "$terraform_dir/.terraform.lock.hcl" ]]; then
  cp "$terraform_dir/.terraform.lock.hcl" "$terraform_work_dir/"
fi

admin_cidr_tf="$(json_escape_array "${admin_cidrs[@]}")"
availability_zones_tf='[]'
subnet_cidrs_tf='[]'
if [[ -n "$availability_zones_csv" ]]; then IFS=',' read -r -a availability_zones <<< "$availability_zones_csv"; availability_zones_tf="$(json_escape_array "${availability_zones[@]}")"; fi
if [[ -n "$subnet_cidrs_csv" ]]; then IFS=',' read -r -a subnet_cidrs <<< "$subnet_cidrs_csv"; subnet_cidrs_tf="$(json_escape_array "${subnet_cidrs[@]}")"; fi
create_ecr_repository="true"
create_iam_instance_profile="true"
if [[ "$image_distribution" == "ssh-load" ]]; then
  create_ecr_repository="false"
  create_iam_instance_profile="false"
fi
cat > "$tfvars_path" <<EOF
aws_region               = "$aws_region"
availability_zone        = "$availability_zone"
availability_zones       = $availability_zones_tf
subnet_cidrs              = $subnet_cidrs_tf
name_prefix              = "$experiment_id"
node_count               = $node_count
operator_instance_type   = "$operator_instance_type"
controller_instance_type = "$controller_instance_type"
create_ecr_repository    = $create_ecr_repository
create_iam_instance_profile = $create_iam_instance_profile
ecr_repository_name      = "$ecr_repository_name"
key_name                 = "$key_name"
admin_cidrs              = $admin_cidr_tf
EOF

log "terraform plan"
(
  cd "$terraform_work_dir"
  AWS_PROFILE="$aws_profile" terraform init -input=false
  terraform fmt
  terraform fmt -check -diff
  terraform validate
  AWS_PROFILE="$aws_profile" terraform plan -var-file="$tfvars_path" -out="$plan_path" -input=false
  terraform show -no-color "$plan_path" > "$artifact_root/terraform-plan.txt"
)

for forbidden in aws_nat_gateway aws_lb aws_eks_cluster aws_db_instance aws_eip aws_autoscaling_group; do
  if grep -q "$forbidden" "$artifact_root/terraform-plan.txt"; then
    invalid_reason="Terraform plan contains forbidden expensive resource: $forbidden"
    echo "$invalid_reason" >&2
    exit 1
  fi
done
if grep -Eq '^[[:space:]]*\+[[:space:]]*market_type[[:space:]]*=[[:space:]]*"spot"|^[[:space:]]*\+[[:space:]]*monitoring[[:space:]]*=[[:space:]]*true' "$artifact_root/terraform-plan.txt"; then
  bloc_die "Terraform plan contains a forbidden Spot or detailed-monitoring setting"
fi
allowed_types=' aws_vpc aws_subnet aws_internet_gateway aws_route_table aws_route_table_association aws_ecr_repository aws_iam_role aws_iam_role_policy aws_iam_role_policy_attachment aws_iam_instance_profile aws_security_group aws_instance '
planned_types="$(sed -n 's/^[[:space:]]*#[[:space:]]*\(aws_[a-z0-9_]*\)\..* will be created$/\1/p' "$artifact_root/terraform-plan.txt"|sort -u)"
while IFS= read -r resource_type; do
  [[ -z "$resource_type" || "$allowed_types" == *" $resource_type "* ]] || bloc_die "Terraform plan contains a resource outside the campaign allowlist: $resource_type"
done <<<"$planned_types"

if [[ "$auto_approve_plan" != "1" ]]; then
  echo "Terraform plan saved at: $artifact_root/terraform-plan.txt"
  echo "Expected only low-cost EC2/VPC/ECR/IAM resources."
  read -r -p "Type APPLY to create AWS resources for this A1 pilot: " answer
  if [[ "$answer" != "APPLY" ]]; then
    invalid_reason="operator declined terraform apply"
    exit 1
  fi
fi

log "create temporary EC2 key pair"
aws ec2 create-key-pair \
  --profile "$aws_profile" \
  --region "$aws_region" \
  --key-name "$key_name" \
  --key-type rsa \
  --key-format pem \
  --output json \
  | jq -r '.KeyMaterial' > "$key_path"
chmod 600 "$key_path"
key_created="1"
if [[ "$keep_resources_on_failure" == 1 || "$keep_resources_after_run" == 1 ]]; then cp "$key_path" "$artifact_root/generated/$key_name.pem"; chmod 600 "$artifact_root/generated/$key_name.pem"; fi

log "terraform apply"
terraform_started="1"
(
  cd "$terraform_work_dir"
  AWS_PROFILE="$aws_profile" terraform apply -input=false "$plan_path"
  AWS_PROFILE="$aws_profile" terraform output -json inventory > "$script_dir/inventory.json"
  cp "$script_dir/inventory.json" "$artifact_root/inventory.json"
  AWS_PROFILE="$aws_profile" terraform state pull > "$artifact_root/terraform-state-after-apply.json"
)
terraform_applied="1"
if [[ "$image_distribution" == "ecr" ]]; then
  ecr_repository_url="$(cd "$terraform_work_dir" && AWS_PROFILE="$aws_profile" terraform output -raw ecr_repository_url)"
fi

if [[ "$image_distribution" == "ecr" ]]; then
  [[ -n "$ecr_image_tag" ]] || ecr_image_tag="$git_commit"
  [[ "$ecr_image_tag" =~ ^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$ ]] || bloc_die "invalid ECR image tag: $ecr_image_tag"
  image_uri="${ecr_repository_url}:${ecr_image_tag}"
  log "tag Docker image"
  docker tag "$local_image_uri" "$image_uri"
else
  image_uri="$local_image_uri"
fi

if [[ "$image_distribution" == "ecr" ]]; then
  log "push Docker image"
  registry="${ecr_repository_url%%/*}"
  aws ecr get-login-password --profile "$aws_profile" --region "$aws_region" \
    | docker login --username AWS --password-stdin "$registry"
  docker push "$image_uri"
  image_digest="$(aws ecr describe-images --profile "$aws_profile" --region "$aws_region" \
    --repository-name "$ecr_repository_name" \
    --image-ids imageTag="$ecr_image_tag" \
    --query 'imageDetails[0].imageDigest' --output text)"
  jq -n \
    --arg image_distribution "$image_distribution" \
    --arg ecr_repository_url "$ecr_repository_url" \
    --arg docker_image_digest "$image_digest" \
    '{image_distribution:$image_distribution,ecr_repository_url:$ecr_repository_url,docker_image_digest:$docker_image_digest}' \
    > "$artifact_root/terraform-metadata.json"
else
  log "export Docker image"
  docker save -o "$image_tar_path" "$image_uri"
  jq -n \
    --arg image_distribution "$image_distribution" \
    --arg docker_image "$image_uri" \
    '{image_distribution:$image_distribution,docker_image:$docker_image}' \
    > "$artifact_root/terraform-metadata.json"
fi

log "generate deployment configs"
inventory_path="$script_dir/inventory.json"
controller_private_ip="$(jq -r '.controller.private_ip' "$inventory_path")"
controller_public_ip="$(jq -r '.controller.public_ip' "$inventory_path")"
write_prometheus_config "$inventory_path" "$script_dir/prometheus.ec2.yml"
(
  cd "$repo_root/bloc-node"
  GOCACHE="$PWD/.gocache" go run ./cmd/bloc-node gen-ec2-config \
    --inventory ../deploy/ec2/inventory.json \
    --cluster-out ../deploy/ec2/cluster.ec2.json \
    --remote-eval-out ../deploy/ec2/remote-eval.ec2.json \
    --cluster-id "$experiment_id" \
    --nodes "$node_count" \
    --bmax 128 \
    --prometheus-url "http://${controller_private_ip}:9090" \
    --grafana-url "http://${controller_private_ip}:3000" \
    --controller-url "$controller_private_ip"
)
cp "$script_dir/cluster.ec2.json" "$artifact_root/generated/cluster.ec2.json"
cp "$script_dir/cluster.ec2.crs" "$artifact_root/generated/cluster.ec2.crs"
cp "$script_dir/remote-eval.ec2.json" "$artifact_root/generated/remote-eval.ec2.json"
cp "$script_dir/prometheus.ec2.yml" "$artifact_root/generated/prometheus.ec2.yml"

log "copy configs and start services"
public_hosts=()
while IFS= read -r host; do public_hosts+=("$host"); done < <(jq -r '.controller.public_ip, (.nodes | sort_by(.id)[] | .public_ip)' "$inventory_path")
for host in "${public_hosts[@]}"; do
  ssh_ec2 "$host" "sudo mkdir -p /etc/bloc /opt/bloc/ec2 /opt/bloc/docker-compose/grafana && sudo chown -R ubuntu:ubuntu /opt/bloc /etc/bloc"
done

if [[ "$image_distribution" == "ssh-load" ]]; then
  log "load Docker image on EC2 hosts"
  for host in "${public_hosts[@]}"; do
    scp_ec2 "$image_tar_path" "ubuntu@$host:/opt/bloc/ec2/bloc-node-image.tar"
    ssh_ec2 "$host" "docker load -i /opt/bloc/ec2/bloc-node-image.tar"
  done
fi

jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  public_ip="$(jq -r '.public_ip' <<< "$node")"
  scp_ec2 "$script_dir/cluster.ec2.json" "ubuntu@$public_ip:/etc/bloc/cluster.json"
  scp_ec2 "$script_dir/cluster.ec2.crs" "ubuntu@$public_ip:/etc/bloc/cluster.crs"
  scp_ec2 "$script_dir/secrets.ec2/operator-${node_id}.json" "ubuntu@$public_ip:/etc/bloc/operator.json"
  ssh_ec2 "$public_ip" "sudo chown 10001:10001 /etc/bloc/operator.json && sudo chmod 600 /etc/bloc/operator.json"
  scp_ec2 "$script_dir/operator-compose.yaml" "ubuntu@$public_ip:/opt/bloc/ec2/operator-compose.yaml"
  scp_ec2 "$script_dir/sample-container-resources.sh" "ubuntu@$public_ip:/opt/bloc/ec2/sample-container-resources.sh"
  ssh_ec2 "$public_ip" "chmod 700 /opt/bloc/ec2/sample-container-resources.sh"
  if [[ "$image_distribution" == "ecr" ]]; then
    ssh_ec2 "$public_ip" "set -e; aws ecr get-login-password --region '$aws_region' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$node_id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml up -d"
  else
    ssh_ec2 "$public_ip" "set -e; cd /opt/bloc/ec2; NODE_ID='$node_id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml up -d"
  fi
done

scp_ec2 "$script_dir/controller-compose.yaml" "ubuntu@$controller_public_ip:/opt/bloc/ec2/controller-compose.yaml"
scp_ec2 "$script_dir/prometheus.ec2.yml" "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus.ec2.yml"
scp_ec2 "$script_dir/remote-eval.ec2.json" "ubuntu@$controller_public_ip:/opt/bloc/ec2/remote-eval.ec2.json"
scp_ec2 -r "$repo_root/deploy/docker-compose/grafana/." "ubuntu@$controller_public_ip:/opt/bloc/docker-compose/grafana/"
ssh_ec2 "$controller_public_ip" "set -e; cd /opt/bloc/ec2; docker compose -f controller-compose.yaml up -d"

log "readiness checks"
jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  private_ip="$(jq -r '.private_ip' <<< "$node")"
  retry_ssh_ec2 "$controller_public_ip" 30 5 "operator $node_id /healthz"     "curl -fsS 'http://$private_ip:8000/healthz'"
  retry_ssh_ec2 "$controller_public_ip" 12 5 "operator $node_id /metrics"     "curl -fsS 'http://$private_ip:8000/metrics' | head -n 5"
done
retry_ssh_ec2 "$controller_public_ip" 12 5 "Prometheus targets API"   "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-before.json"
scp_ec2 "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus-targets-before.json" "$artifact_root/prometheus-targets-before.json"

log "pre-campaign network characterization"
collect_network_matrix "pre" "$artifact_root/network-pre.csv" "$controller_public_ip" "$inventory_path"

log "run A1 pilot scenarios"
block_repetitions=$((repetitions / repetition_blocks))
scenario_specs=()
warmed_batches=","; next_slot=1; block_number=0
for batch_order in "${batch_order_blocks[@]}"; do
  block_number=$((block_number + 1))
  IFS=',' read -r -a ordered_batches <<< "$batch_order"
  for batch in "${ordered_batches[@]}"; do
  if [[ "$max_runtime_minutes" -gt 0 && $((($(date +%s)-campaign_started_epoch)/60)) -ge "$max_runtime_minutes" ]]; then bloc_die "phase runtime reached the configured ceiling"; fi
  scenario_warmups=0
  case "$warmed_batches" in *",$batch,"*) ;; *) scenario_warmups="$warmups"; warmed_batches="${warmed_batches}${batch}," ;; esac
  if [[ "$repetition_blocks" -eq 1 ]]; then relative_path="batch-$batch"; else relative_path="block-$block_number/batch-$batch"; fi
  scenario_dir="/opt/bloc/ec2/results/$experiment_id/$relative_path"
  remote_experiment_id="$experiment_id-block$block_number-b$batch"
  if [[ "$image_distribution" == "ecr" ]]; then
    ssh_ec2 "$controller_public_ip" \
      "set -e; aws ecr get-login-password --region '$aws_region' | docker login --username AWS --password-stdin '$registry'; sudo mkdir -p '$scenario_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '$remote_experiment_id' --first-slot '$next_slot' --batch-size '$batch' --warmups '$scenario_warmups' --repetitions '$block_repetitions' --out-dir 'results/$experiment_id/$relative_path' --image-tag '$image_uri' --git-commit '$git_commit' --timeout 30s"
  else
    ssh_ec2 "$controller_public_ip" \
      "set -e; sudo mkdir -p '$scenario_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '$remote_experiment_id' --first-slot '$next_slot' --batch-size '$batch' --warmups '$scenario_warmups' --repetitions '$block_repetitions' --out-dir 'results/$experiment_id/$relative_path' --image-tag '$image_uri' --git-commit '$git_commit' --timeout 30s"
  fi
  mkdir -p "$artifact_root/scenarios/$relative_path"
  scp_ec2 -r "ubuntu@$controller_public_ip:$scenario_dir" "$artifact_root/scenarios/$relative_path/results"
  scenario_specs+=("$block_number:$artifact_root/scenarios/$relative_path/results")
  next_slot=$((next_slot + scenario_warmups + block_repetitions))
  done
done

log "run dedicated resource evidence phase"
run_resource_phase

log "post-campaign artifact collection"
collect_network_matrix "post" "$artifact_root/network-post.csv" "$controller_public_ip" "$inventory_path"
ssh_ec2 "$controller_public_ip" "curl -fsS http://127.0.0.1:9090/api/v1/targets > /opt/bloc/ec2/prometheus-targets-after.json"
scp_ec2 "ubuntu@$controller_public_ip:/opt/bloc/ec2/prometheus-targets-after.json" "$artifact_root/prometheus-targets.json"

jq -c '.nodes | sort_by(.id)[]' "$inventory_path" | while read -r node; do
  node_id="$(jq -r '.id' <<< "$node")"
  public_ip="$(jq -r '.public_ip' <<< "$node")"
  ssh_ec2 "$public_ip" "docker logs --tail=500 ec2-bloc-node-1 2>&1" > "$artifact_root/logs/operator-${node_id}.log"
done
ssh_ec2 "$controller_public_ip" "docker logs --tail=500 ec2-prometheus-1 2>&1" > "$artifact_root/logs/prometheus.log" || true
ssh_ec2 "$controller_public_ip" "docker logs --tail=500 ec2-grafana-1 2>&1" > "$artifact_root/logs/grafana.log" || true

merge_csv_outputs "$artifact_root"

expected=()
IFS=',' read -r -a accepted_batches <<< "$batch_sizes_csv"
for batch in "${accepted_batches[@]}"; do expected+=("$node_count/$batch=$repetitions"); done
acceptance_args=(assert-evaluator --csv "$artifact_root/run_measurements.csv")
for value in "${expected[@]}"; do acceptance_args+=(--expected "$value"); done
bloc_python "$repo_root" "${acceptance_args[@]}"
jq -e --argjson expected "$node_count" \
  '(.data.activeTargets | length) == $expected and ([.data.activeTargets[] | select(.health == "up")] | length) == $expected' \
  "$artifact_root/prometheus-targets.json" >/dev/null || bloc_die "Prometheus target acceptance failed"

if [[ "$skip_chart_generation" != "1" ]]; then
  log "generate charts"
  if [[ -x "$repo_root/latency-charts/.venv/bin/python" ]]; then
    (cd "$repo_root/latency-charts" && .venv/bin/python -m bloc_latency_charts "$artifact_root")
  else
    echo "latency-charts .venv not found; skipping chart generation" >&2
  fi
fi

campaign_status="complete"
write_manifest "complete" ""
