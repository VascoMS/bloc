#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
# shellcheck source=../../scripts/lib/campaign-common.sh
source "$repo_root/scripts/lib/campaign-common.sh"

usage() { cat <<'EOF'
Usage: bash deploy/ec2/rerun-a1-pilot-existing.sh --artifact-root PATH [options]
  --aws-profile NAME       Default: bloc
  --batch-sizes LIST       Default: 8,32,128
  --warmups N              Default: 1
  --repetitions N          Default: 3
  --first-slot N           Default: 1000
  --eval-timeout DURATION  Default: 30s
  --key-path PATH          Infer the only generated/*.pem when omitted
  --skip-image-build
  --regenerate-config
  --validate-only
EOF
}

artifact_root=""; aws_profile=bloc; batch_sizes_csv=8,32,128; warmups=1; repetitions=3
first_slot=1000; eval_timeout=30s; key_path=""; skip_image_build=0; regenerate_config=0; validate_only=0
bloc_validate_flag_values "$@"
while [[ $# -gt 0 ]]; do case "$1" in
  --artifact-root) artifact_root="$2"; shift 2;; --aws-profile) aws_profile="$2"; shift 2;;
  --batch-sizes) batch_sizes_csv="$2"; shift 2;; --warmups) warmups="$2"; shift 2;;
  --repetitions) repetitions="$2"; shift 2;; --first-slot) first_slot="$2"; shift 2;;
  --eval-timeout) eval_timeout="$2"; shift 2;; --key-path) key_path="$2"; shift 2;;
  --skip-image-build) skip_image_build=1; shift;; --regenerate-config) regenerate_config=1; shift;;
  --validate-only) validate_only=1; shift;; -h|--help) usage; exit 0;; *) usage; bloc_usage_error "unknown argument: $1";; esac; done
[[ -n "$artifact_root" ]] || bloc_usage_error "--artifact-root is required"
bloc_require_dir "$artifact_root"; bloc_require_file "$artifact_root/manifest.json"; bloc_require_file "$artifact_root/inventory.json"
bloc_csv_contains_only "$batch_sizes_csv" 8,32,128,512 BatchSizes
bloc_is_uint "$warmups" || bloc_usage_error "--warmups must be non-negative"; bloc_is_positive_int "$repetitions" || bloc_usage_error "--repetitions must be positive"
bloc_is_uint "$first_slot" || bloc_usage_error "--first-slot must be non-negative"; bloc_validate_go_duration "$eval_timeout" EvalTimeout
for command in aws docker jq ssh scp go git python3; do bloc_require_cmd "$command"; done
if [[ -z "$key_path" ]]; then
  keys=("$artifact_root"/generated/*.pem); [[ "${#keys[@]}" -eq 1 && -f "${keys[0]}" ]] || bloc_usage_error "could not infer exactly one generated/*.pem; pass --key-path"; key_path="${keys[0]}"
fi
bloc_require_file "$key_path"
if [[ "$validate_only" -eq 1 ]]; then bloc_validate_only_message "rerun-a1-pilot-existing.sh"; exit 0; fi

manifest="$artifact_root/manifest.json"; inventory="$artifact_root/inventory.json"
aws_region="$(jq -er .aws_region "$manifest")"; experiment_id="$(jq -er .experiment_id "$manifest")"
ecr_url="$(jq -er '.terraform.ecr_repository_url' "$manifest")"; registry="${ecr_url%%/*}"
stamp="$(bloc_utc_stamp)"; rerun_id="$experiment_id-rerun-$stamp"; rerun_root="$artifact_root/reruns/$rerun_id"
mkdir -p "$rerun_root"/{generated,scenarios,logs}; chmod 600 "$key_path"; commands="$rerun_root/commands.txt"; : >"$commands"
ssh_run() { local host="$1"; shift; bloc_append_command "$commands" ssh "ubuntu@$host" "$*"; ssh -i "$key_path" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 "ubuntu@$host" "$*"; }
scp_run() { bloc_append_command "$commands" scp "$@"; scp -i "$key_path" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$@"; }

git_commit="$(git -C "$repo_root" rev-parse --short=12 HEAD)"; image_uri=""
if [[ "$skip_image_build" -eq 0 ]]; then
  image_tag="rerun-$git_commit-$stamp"; local_image="bloc-node:$image_tag"; image_uri="$ecr_url:$image_tag"
  docker build --platform linux/amd64 -f "$repo_root/bloc-node/Dockerfile" -t "$local_image" "$repo_root"
  [[ "$(docker image inspect "$local_image" --format '{{.Architecture}}')" == amd64 ]] || bloc_die "rerun image is not amd64"
  docker tag "$local_image" "$image_uri"; aws ecr get-login-password --profile "$aws_profile" --region "$aws_region" | docker login --username AWS --password-stdin "$registry"; docker push "$image_uri"
else
  image_uri="$(jq -er .docker_image "$manifest")"
fi

IFS=',' read -r -a batches <<<"$batch_sizes_csv"
bmax=0
for batch in "${batches[@]}"; do
  if (( batch > bmax )); then bmax="$batch"; fi
done
cluster="$rerun_root/generated/cluster.ec2.json"; remote="$rerun_root/generated/remote-eval.ec2.json"
if [[ "$regenerate_config" -eq 1 ]]; then
  (cd "$repo_root/bloc-node" && go run ./cmd/bloc-node gen-ec2-config \
    --inventory "$inventory" --cluster-out "$cluster" \
    --crs-out "$rerun_root/generated/cluster.ec2.crs" \
    --secrets-dir "$rerun_root/generated/secrets.ec2" \
    --remote-eval-out "$remote" --cluster-id "$experiment_id" \
    --nodes "$(jq '.nodes|length' "$inventory")" --bmax "$bmax")
else
  cp "$artifact_root/generated/cluster.ec2.json" "$cluster"; cp "$artifact_root/generated/remote-eval.ec2.json" "$remote"
fi

while IFS= read -r node; do
  id="$(jq -r .id <<<"$node")"; public="$(jq -r .public_ip <<<"$node")"
  scp_run "$cluster" "ubuntu@$public:/etc/bloc/cluster.json"
  if [[ -f "$rerun_root/generated/secrets.ec2/operator-$id.json" ]]; then scp_run "$rerun_root/generated/secrets.ec2/operator-$id.json" "ubuntu@$public:/etc/bloc/operator.json"; fi
  ssh_run "$public" "set -e; aws ecr get-login-password --region '$aws_region' | docker login --username AWS --password-stdin '$registry'; cd /opt/bloc/ec2; NODE_ID='$id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml pull; docker compose -f operator-compose.yaml down; NODE_ID='$id' BLOC_IMAGE='$image_uri' docker compose -f operator-compose.yaml up -d"
done < <(jq -c '.nodes|sort_by(.id)[]' "$inventory")
rm -rf "$rerun_root/generated/secrets.ec2"
controller="$(jq -r .controller.public_ip "$inventory")"; scp_run "$remote" "ubuntu@$controller:/opt/bloc/ec2/remote-eval.ec2.json"
while IFS= read -r private; do ssh_run "$controller" "for attempt in \$(seq 1 24); do curl --max-time 3 -fsS http://$private:8000/healthz && exit 0; sleep 5; done; exit 1"; done < <(jq -r '.nodes|sort_by(.id)[]|.private_ip' "$inventory")

next_slot="$first_slot"; scenario_specs=()
for batch in "${batches[@]}"; do
  remote_dir="/opt/bloc/ec2/results/$rerun_id/batch-$batch"
  ssh_run "$controller" "set -e; sudo mkdir -p '$remote_dir'; sudo chown -R 10001:10001 /opt/bloc/ec2/results; cd /opt/bloc/ec2; docker run --rm -v /opt/bloc/ec2:/work -w /work '$image_uri' eval-remote --config remote-eval.ec2.json --experiment-id '$rerun_id-b$batch' --first-slot '$next_slot' --batch-size '$batch' --warmups '$warmups' --repetitions '$repetitions' --out-dir 'results/$rerun_id/batch-$batch' --image-tag '$image_uri' --git-commit '$git_commit' --timeout '$eval_timeout'"
  mkdir -p "$rerun_root/scenarios/batch-$batch"; scp_run -r "ubuntu@$controller:$remote_dir" "$rerun_root/scenarios/batch-$batch/results"
  bloc_python "$repo_root" assert-evaluator --csv "$rerun_root/scenarios/batch-$batch/results/run_measurements.csv" --expected "$(jq '.nodes|length' "$inventory")/$batch=$repetitions"
  scenario_specs+=("1:$rerun_root/scenarios/batch-$batch/results"); next_slot=$((next_slot+warmups+repetitions))
done
bloc_python "$repo_root" merge-scenarios --root "$rerun_root" "${scenario_specs[@]}"
jq -n --arg schema_version 'bloc-ec2-rerun/v1' --arg experiment_id "$rerun_id" --arg parent_experiment_id "$experiment_id" --arg status complete --arg docker_image "$image_uri" --arg git_commit "$git_commit" --arg created_at "$(bloc_utc_iso)" --argjson batch_sizes "$(printf '%s\n' "${batches[@]}"|jq -R tonumber|jq -s .)" --argjson warmups "$warmups" --argjson repetitions "$repetitions" '{schema_version:$schema_version,experiment_id:$experiment_id,parent_experiment_id:$parent_experiment_id,status:$status,docker_image:$docker_image,git_commit:$git_commit,batch_sizes:$batch_sizes,warmups:$warmups,repetitions:$repetitions,created_at:$created_at}' >"$rerun_root/manifest.json"
printf 'rerun complete: %s\n' "$rerun_root"
