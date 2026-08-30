#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
source "$repo_root/scripts/lib/final-campaign-contract.sh"

status=0
final_parse_campaign_args "$@" || status=$?
if [[ "$status" -eq 64 ]]; then
  exit 0
elif [[ "$status" -ne 0 ]]; then
  final_campaign_usage >&2
  exit "$status"
fi
final_validate_campaign_contract "$repo_root"
final_print_campaign_contract
if [[ "$FINAL_VALIDATE_ONLY" -eq 1 ]]; then
  exit 0
fi

adapter="$script_dir/final-topology-$FINAL_TOPOLOGY.sh"
[[ -f "$adapter" ]] || { printf 'live topology adapter is not implemented: %s\n' "$adapter" >&2; exit 3; }
source "$repo_root/scripts/lib/final-campaign-lifecycle.sh"
source "$adapter"
FINAL_REPO_ROOT="$repo_root"
export FINAL_REPO_ROOT FINAL_BUNDLE_ROOT FINAL_NODE_COUNT FINAL_TOPOLOGY FINAL_PHASE
export FINAL_EXPERIMENT_ID FINAL_SOURCE_SHA FINAL_BLOC_IMAGE FINAL_MEMPOOL_IMAGE
export FINAL_AWS_PROFILE FINAL_ADMIN_CIDR FINAL_WARMUPS FINAL_REPETITIONS FINAL_BLOCKS
export FINAL_SAMPLER FINAL_BATCHES FINAL_SEED FINAL_DEADLINE
export FINAL_ACS_TRACE_SCHEMA FINAL_STREAM_MODE
final_run_campaign_lifecycle "$repo_root/results/ec2/$FINAL_EXPERIMENT_ID"
