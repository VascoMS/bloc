#!/usr/bin/env bash
set -Eeuo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

scripts=(
  scripts/lib/campaign-common.sh scripts/lib/m3-matrix.sh
  bloc-node/scripts/demo-local.sh bloc-node/scripts/run-acs-safety-campaign.sh
  bloc-node/scripts/run-merge-plan-campaign.sh deploy/ec2/run-a1-pilot.sh
  deploy/ec2/rerun-a1-pilot-existing.sh deploy/ec2/run-m3-same-az.sh
  deploy/ec2/run-m3-cross-az.sh deploy/ec2/run-m3-three-region.sh
  deploy/ec2/run-merge-plan-attribution.sh
)
for script in "${scripts[@]}"; do bash -n "$script"; done

PYTHONPYCACHEPREFIX="${TMPDIR:-/tmp}/bloc-runner-pycache" python3 -m unittest scripts.tests.test_campaign_artifacts

bash bloc-node/scripts/run-acs-safety-campaign.sh --validate-only
bash bloc-node/scripts/run-merge-plan-campaign.sh --phase baseline --campaign-id validate-only --validate-only
bash deploy/ec2/run-a1-pilot.sh --admin-cidr 127.0.0.1/32 --validate-only
bash deploy/ec2/run-m3-same-az.sh --admin-cidr 127.0.0.1/32 --validate-only
bash deploy/ec2/run-m3-cross-az.sh --admin-cidr 127.0.0.1/32 --validate-only
bash deploy/ec2/run-m3-three-region.sh --admin-cidr 127.0.0.1/32 --validate-only
bash deploy/ec2/run-merge-plan-attribution.sh --admin-cidr 127.0.0.1/32 --validate-only

fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc runner fixture.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/generated"
printf '%s\n' '{"aws_region":"us-east-1","experiment_id":"bloc-ec2-fixture","terraform":{"ecr_repository_url":"example.invalid/bloc"},"docker_image":"example.invalid/bloc:test"}' >"$fixture/manifest.json"
printf '%s\n' '{"controller":{"public_ip":"127.0.0.1"},"nodes":[]}' >"$fixture/inventory.json"
printf '%s\n' fixture >"$fixture/generated/fixture.pem"; chmod 600 "$fixture/generated/fixture.pem"
bash deploy/ec2/rerun-a1-pilot-existing.sh --artifact-root "$fixture" --validate-only

fake_bin="$fixture/fake bin"; fake_calls="$fixture/fake-calls.txt"; mkdir -p "$fake_bin"; : >"$fake_calls"
for command in aws terraform docker ssh scp rsync; do
  printf '#!/bin/sh\nprintf "%%s\\n" "$0 $*" >>"%s"\nexit 99\n' "$fake_calls" >"$fake_bin/$command"
  chmod +x "$fake_bin/$command"
done
PATH="$fake_bin:$PATH" bash deploy/ec2/run-a1-pilot.sh --admin-cidr 127.0.0.1/32 --validate-only
[[ ! -s "$fake_calls" ]] || { echo "validation-only invoked an external lifecycle command" >&2; exit 1; }

set +e
bash deploy/ec2/run-a1-pilot.sh --admin-cidr 127.0.0.1/32 --node-count 0 --validate-only >/dev/null 2>&1
status=$?
set -e
[[ "$status" -eq 2 ]] || { echo "invalid CLI returned $status, expected 2" >&2; exit 1; }
set +e
bash deploy/ec2/run-a1-pilot.sh --admin-cidr >/dev/null 2>&1
status=$?
set -e
[[ "$status" -eq 2 ]] || { echo "missing option value returned $status, expected 2" >&2; exit 1; }
set +e
bash deploy/ec2/run-m3-three-region.sh --admin-cidr 127.0.0.1/32 --keep-resources-on-failure --validate-only >/dev/null 2>&1
status=$?
set -e
[[ "$status" -eq 2 ]] || { echo "three-region preserve-on-failure option returned $status, expected 2" >&2; exit 1; }
printf 'campaign runner portability checks passed\n'
