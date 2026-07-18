#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; repo_root="$(cd "$script_dir/../.." && pwd)"
source "$repo_root/scripts/lib/campaign-common.sh"; source "$repo_root/scripts/lib/m3-matrix.sh"
bloc_m3_matrix_usage() { printf 'Usage: bash deploy/ec2/run-m3-same-az.sh --admin-cidr CIDR [--node-counts 4,7,10] [--batch-sizes 8,32,128] [--validate-only]\n'; }
M3_REPO_ROOT="$repo_root"; M3_ENTRY_NAME=run-m3-same-az.sh; M3_NODE_COUNTS=4,7,10; M3_CAMPAIGN_PREFIX=m3-same-az-synthetic; M3_PHASE_PREFIX=m3-same-az; M3_CAMPAIGN_LABEL=M3-same-az-synthetic; M3_TOPOLOGY=T0-same-az; M3_AWS_REGION=us-east-1; M3_PRIMARY_AZ=us-east-1a; M3_AVAILABILITY_ZONES=""; M3_SUBNET_CIDRS=""; M3_OPERATOR_TYPE=t3.small; M3_CONTROLLER_TYPE=t3.small
bloc_m3_matrix_main "$@"
