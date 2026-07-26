#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; repo_root="$(cd "$script_dir/../.." && pwd)"; source "$repo_root/scripts/lib/campaign-common.sh"
usage() { cat <<'EOF'
Usage: bash deploy/ec2/run-merge-plan-attribution.sh --admin-cidr CIDR [options]
  --aws-profile NAME --aws-region REGION --availability-zone AZ
  --compute-flex-instance-type TYPE --compute-flex-fallback-instance-type TYPE
  --burstable-instance-type TYPE --controller-instance-type TYPE
  --campaign-id ID --prebuilt-campaign-image-tag TAG --cost-ceiling-usd VALUE
  --auto-approve-plan --resume-completed-phases --keep-resources-on-failure --validate-only
EOF
}
admin_cidrs=(); aws_profile=bloc; aws_region=us-east-1; availability_zone=us-east-1a; compute_type=c7i-flex.large; fallback_type=m7i-flex.large; burst_type=t3.small; controller_type=t3.small; campaign_id=""; prebuilt=""; cost_ceiling=5.00; auto_plan=0; resume=0; keep_failure=0; validate_only=0
bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do case "$1" in
  --admin-cidr) admin_cidrs+=("$2"); shift 2;; --aws-profile) aws_profile="$2"; shift 2;; --aws-region) aws_region="$2"; shift 2;; --availability-zone) availability_zone="$2"; shift 2;;
  --compute-flex-instance-type) compute_type="$2"; shift 2;; --compute-flex-fallback-instance-type) fallback_type="$2"; shift 2;; --burstable-instance-type) burst_type="$2"; shift 2;; --controller-instance-type) controller_type="$2"; shift 2;;
  --campaign-id) campaign_id="$2"; shift 2;; --prebuilt-campaign-image-tag) prebuilt="$2"; shift 2;; --cost-ceiling-usd) cost_ceiling="$2"; shift 2;;
  --auto-approve-plan) auto_plan=1; shift;; --resume-completed-phases) resume=1; shift;; --keep-resources-on-failure) keep_failure=1; shift;; --validate-only) validate_only=1; shift;;
  -h|--help) usage; exit 0;; *) usage; bloc_usage_error "unknown argument: $1";; esac; done
[[ "${#admin_cidrs[@]}" -gt 0 ]] || bloc_usage_error "at least one --admin-cidr is required"; [[ -n "$campaign_id" ]] || campaign_id="merge-plan-attribution-$(bloc_utc_stamp)"; [[ "$campaign_id" =~ ^[a-z0-9-]+$ ]] || bloc_usage_error "invalid campaign id"
python3 - "$cost_ceiling" <<'PY' || exit 2
import sys
try: value=float(sys.argv[1])
except ValueError: raise SystemExit("cost ceiling must be numeric")
if value < 4.71: raise SystemExit("cost ceiling must cover the conservative 4.71 USD projection")
PY
for command in aws terraform docker git jq go python3; do bloc_require_cmd "$command"; done
if [[ "$validate_only" -eq 1 ]]; then args=(--admin-cidr "${admin_cidrs[0]}" --node-count 4 --repetition-blocks 3 --batch-order-block 32,8,128 --batch-order-block 128,32,8 --batch-order-block 8,128,32 --validate-only); bash "$script_dir/run-a1-pilot.sh" "${args[@]}"; bloc_validate_only_message "run-merge-plan-attribution.sh"; exit 0; fi

campaign_root="$repo_root/results/ec2/$campaign_id"; mkdir -p "$campaign_root"; phases_jsonl="$campaign_root/phases.jsonl"; commands="$campaign_root/commands.txt"; touch "$phases_jsonl" "$commands"; started_at="$(bloc_utc_iso)"; git_commit="$(git -C "$repo_root" rev-parse --short=12 HEAD)"
blocked="$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all | awk '{print substr($0,4)}' | grep -E '^(bloc-node|bte|sbc|latency-charts|deploy/ec2|docs)/' || true)"; [[ -z "$blocked" ]] || bloc_die "campaign sources must be committed: $blocked"
aws sts get-caller-identity --profile "$aws_profile" >/dev/null; quota="$(aws service-quotas get-service-quota --profile "$aws_profile" --region "$aws_region" --service-code ec2 --quota-code L-1216C47A --query Quota.Value --output text)"; python3 - "$quota" <<'PY' || bloc_die "vCPU quota is below 16"
import sys; raise SystemExit(0 if float(sys.argv[1]) >= 16 else 1)
PY
offered() { aws ec2 describe-instance-type-offerings --profile "$aws_profile" --region "$aws_region" --location-type availability-zone --filters "Name=location,Values=$availability_zone" "Name=instance-type,Values=$1" --query 'length(InstanceTypeOfferings)' --output text | grep -qv '^0$'; }
if ! offered "$compute_type"; then offered "$fallback_type" || bloc_die "neither compute-flex type is offered"; compute_type="$fallback_type"; fi; offered "$burst_type" || bloc_die "$burst_type is not offered"
if [[ -z "$prebuilt" ]]; then image_tag="bloc-node:$campaign_id-$git_commit"; docker build --platform linux/amd64 -f "$repo_root/bloc-node/Dockerfile" -t "$image_tag" "$repo_root"; else image_tag="$prebuilt"; docker image inspect "$image_tag" >/dev/null; fi
[[ "$(docker image inspect "$image_tag" --format '{{.Architecture}}')" == amd64 ]] || bloc_die "campaign image is not amd64"; local_image_id="$(docker image inspect "$image_tag" --format '{{.Id}}')"; expected_digest=""; ecr_tag="$git_commit-$campaign_id"
write_manifest() { local status="$1" reason="${2:-}"; jq -s . "$phases_jsonl" >"$campaign_root/phases.json"; jq -n --arg schema_version bloc-merge-plan-attribution/v1 --arg campaign_id "$campaign_id" --arg status "$status" --arg reason "$reason" --arg started_at "$started_at" --arg finished_at "$(bloc_utc_iso)" --arg git_commit "$git_commit" --arg image_tag "$image_tag" --arg local_image_id "$local_image_id" --slurpfile phases "$campaign_root/phases.json" '{schema_version:$schema_version,campaign_id:$campaign_id,status:$status,invalid_reason:(if $reason=="" then null else $reason end),started_at:$started_at,finished_at:$finished_at,git_commit:$git_commit,image_tag:$image_tag,local_image_id:$local_image_id,phases:$phases[0]}' >"$campaign_root/manifest.json"; }
trap 'status=$?; [[ $status -eq 0 ]] || write_manifest invalid "runner failed with exit code $status"' EXIT
phase_ids=(compute-flex-n4 compute-flex-n7 burstable-n7); phase_nodes=(4 7 7); phase_types=("$compute_type" "$compute_type" "$burst_type")
for index in 0 1 2; do id="${phase_ids[$index]}"; nodes="${phase_nodes[$index]}"; operator="${phase_types[$index]}"; phase_path="$campaign_root/$id"
  if [[ "$resume" -eq 1 && -f "$phase_path/manifest.json" && "$(jq -r .status "$phase_path/manifest.json")" == complete ]]; then digest="$(jq -r '.terraform.docker_image_digest' "$phase_path/manifest.json")"; else
    experiment_id="bloc-ec2-mpa-$(date -u +%Y%m%d%H%M%S)-$id"; source_path="$repo_root/results/ec2/$experiment_id"; args=(--aws-profile "$aws_profile" --aws-region "$aws_region" --availability-zone "$availability_zone" --node-count "$nodes" --operator-instance-type "$operator" --controller-instance-type "$controller_type" --batch-sizes 8,32,128 --warmups 5 --repetitions 30 --repetition-blocks 3 --batch-order-block 32,8,128 --batch-order-block 128,32,8 --batch-order-block 8,128,32 --prebuilt-image-tag "$image_tag" --ecr-image-tag "$ecr_tag" --campaign-label "Merge-Plan-attribution-$id" --topology T0-same-az --experiment-id "$experiment_id" --max-runtime-minutes 90 --skip-chart-generation)
    for cidr in "${admin_cidrs[@]}"; do args+=(--admin-cidr "$cidr"); done; [[ "$auto_plan" -eq 1 ]] && args+=(--auto-approve-plan); [[ "$keep_failure" -eq 1 ]] && args+=(--keep-resources-on-failure)
    bloc_append_command "$commands" bash "$script_dir/run-a1-pilot.sh" "${args[@]}"; bash "$script_dir/run-a1-pilot.sh" "${args[@]}"; mv "$source_path" "$phase_path"; digest="$(jq -r '.terraform.docker_image_digest' "$phase_path/manifest.json")"
  fi
  [[ -n "$digest" && "$digest" != null ]] || bloc_die "$id has no image digest"; [[ -z "$expected_digest" || "$digest" == "$expected_digest" ]] || bloc_die "$id image digest differs"; expected_digest="$digest"
  bloc_python "$repo_root" assert-evaluator --require-success --csv "$phase_path/run_measurements.csv" --expected "$nodes/8=30" --expected "$nodes/32=30" --expected "$nodes/128=30"
  jq -n --arg id "$id" --arg path "$id" --argjson nodes "$nodes" --arg operator_instance_type "$operator" --arg controller_instance_type "$controller_type" --arg image_digest "$digest" '{id:$id,path:$path,nodes:$nodes,operator_instance_type:$operator_instance_type,controller_instance_type:$controller_instance_type,image_digest:$image_digest}' >>"$phases_jsonl"; write_manifest in-progress
done
write_manifest analysis-pending; python_bin="$repo_root/latency-charts/.venv/bin/python"; [[ -x "$python_bin" ]] || python_bin=python3; (cd "$repo_root/latency-charts" && "$python_bin" -m bloc_latency_charts.merge_plan_campaign "$campaign_root"); write_manifest complete; trap - EXIT
printf 'campaign complete: %s\n' "$campaign_root"
