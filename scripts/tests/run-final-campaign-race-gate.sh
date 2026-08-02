#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
skip_tests='^(TestBuildAndLoadCampaignBundle|TestVerifyCampaignBundleWritesOnceAndChecksExpectedIdentities|TestBuildCampaignBundleRejectsInvalidFrozenInputs|TestLoadCampaignBundleRejectsMutations|TestBuildCampaignBundleRejectsBadSecretAndEscapingSymlink|TestBuildCampaignIdentityContainsNoDeploymentAddresses|TestGenCampaignIdentityWritesPrivateSecretsAndRefusesOverwrite|TestVerifyCampaignSecretsRejectsWrongP2PIdentity|TestVerifyCampaignSecretsRejectsWrongShareSet|TestMaterializeCampaignConfigPreservesFrozenInputsAcrossTopologies|TestMaterializeCampaignConfigRejectsInvalidPlacement)$'

(
  cd "$repo_root/bloc-node"
  env GOCACHE="${BLOC_NODE_GOCACHE:-/tmp/bloc-node-final-race}" go test ./internal/app
)
(
  cd "$repo_root/bte/btd-impl-main"
  env GOCACHE="${BTE_GOCACHE:-/tmp/bloc-bte-final-race}" go test -race ./be
)
(
  cd "$repo_root/mempool-il"
  env GOCACHE="${MEMPOOL_GOCACHE:-/tmp/bloc-mempool-final-race}" go test -race ./internal/mempool ./internal/api
)
(
  cd "$repo_root/bloc-node"
  env GOCACHE="${BLOC_NODE_GOCACHE:-/tmp/bloc-node-final-race}" go test -race ./internal/app -skip "$skip_tests"
)

echo "final campaign race gate passed"
