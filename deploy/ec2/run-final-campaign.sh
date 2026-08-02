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
printf 'live lifecycle implementation is not complete; no AWS action was taken\n' >&2
exit 3
