#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
modules=(
  "$repo_root/deploy/ec2/terraform"
  "$repo_root/deploy/ec2/terraform-three-region"
)
expected_actions=$'ecr:BatchCheckLayerAvailability\necr:BatchGetImage\necr:GetAuthorizationToken\necr:GetDownloadUrlForLayer'

for module in "${modules[@]}"; do
  terraform -chdir="$module" fmt -check -diff
  if [[ "${FINAL_CAMPAIGN_SKIP_TERRAFORM_VALIDATE:-0}" != 1 ]]; then
    terraform -chdir="$module" validate >/dev/null
  fi

  grep -Fq 'variable "ecr_repository_arns"' "$module/variables.tf" || {
    echo "$module does not require pre-existing ECR repository ARNs" >&2
    exit 1
  }
  ! grep -Rq 'resource "aws_ecr_repository"' "$module" --include='*.tf' || {
    echo "$module still creates an ECR repository" >&2
    exit 1
  }
  ! grep -Rq 'output "ecr_repository_url"' "$module" --include='*.tf' || {
    echo "$module still exports a campaign-created ECR repository" >&2
    exit 1
  }
  actual_actions="$(grep -Eho '"ecr:[A-Z][A-Za-z]+"' "$module"/*.tf | tr -d '"' | sort -u)"
  [[ "$actual_actions" == "$expected_actions" ]] || {
    echo "$module ECR actions differ from the pull-only contract" >&2
    printf 'actual:\n%s\n' "$actual_actions" >&2
    exit 1
  }
  grep -Fq 'resources = var.ecr_repository_arns' "$module/main.tf" || {
    echo "$module does not scope image pulls to the supplied repositories" >&2
    exit 1
  }
done

echo "final campaign Terraform contract tests passed"
