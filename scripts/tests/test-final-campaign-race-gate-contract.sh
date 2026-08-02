#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="$repo_root/scripts/tests/run-final-campaign-race-gate.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/bloc-final-race-contract.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/fake-bin"
call_log="$fixture/calls.log"
: >"$call_log"
printf '#!/usr/bin/env bash\nprintf "%%s|%%s\\n" "$PWD" "$*" >>"$FINAL_RACE_CALL_LOG"\n' >"$fixture/fake-bin/go"
chmod +x "$fixture/fake-bin/go"

PATH="$fixture/fake-bin:$PATH" FINAL_RACE_CALL_LOG="$call_log" bash "$runner" >"$fixture/stdout"

skip_tests='^(TestBuildAndLoadCampaignBundle|TestVerifyCampaignBundleWritesOnceAndChecksExpectedIdentities|TestBuildCampaignBundleRejectsInvalidFrozenInputs|TestLoadCampaignBundleRejectsMutations|TestBuildCampaignBundleRejectsBadSecretAndEscapingSymlink|TestBuildCampaignIdentityContainsNoDeploymentAddresses|TestGenCampaignIdentityWritesPrivateSecretsAndRefusesOverwrite|TestVerifyCampaignSecretsRejectsWrongP2PIdentity|TestVerifyCampaignSecretsRejectsWrongShareSet|TestMaterializeCampaignConfigPreservesFrozenInputsAcrossTopologies|TestMaterializeCampaignConfigRejectsInvalidPlacement)$'
expected="$fixture/expected.log"
printf '%s\n' \
  "$repo_root/bloc-node|test ./internal/app" \
  "$repo_root/bte/btd-impl-main|test -race ./be" \
  "$repo_root/mempool-il|test -race ./internal/mempool ./internal/api" \
  "$repo_root/bloc-node|test -race ./internal/app -skip $skip_tests" \
  >"$expected"

cmp "$expected" "$call_log"
grep -Fxq 'final campaign race gate passed' "$fixture/stdout"
if [[ TestFinalCampaignRejectsSyntheticBeforeExecution =~ $skip_tests ]]; then
  echo "skip expression hides a concurrency-relevant final-campaign test" >&2
  exit 1
fi

echo "final campaign race gate contract passed"
