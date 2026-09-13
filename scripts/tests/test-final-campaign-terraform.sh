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

  console_args=(
    -var=node_count=10
    -var='admin_cidrs=["127.0.0.1/32"]'
    -var='ecr_repository_arns=["arn:aws:ecr:us-east-1:123456789012:repository/bloc-node","arn:aws:ecr:us-east-1:123456789012:repository/mempool-il"]'
  )
  if [[ "$(basename "$module")" == terraform-three-region ]]; then
    console_args+=(
      -var=primary_key_name=test-primary
      -var=secondary_key_name=test-secondary
      -var=tertiary_key_name=test-tertiary
    )
  else
    console_args+=(-var=key_name=test-key)
  fi
  n10_output="$(printf 'var.node_count\n' | terraform -chdir="$module" console -no-color "${console_args[@]}" 2>&1)"
  if grep -Fq 'Error:' <<<"$n10_output" || ! grep -Fxq '10' <<<"$n10_output"; then
    echo "$module rejected the supported n=10 topology" >&2
    printf '%s\n' "$n10_output" >&2
    exit 1
  fi
  invalid_output="$(printf 'var.node_count\n' | terraform -chdir="$module" console -no-color "${console_args[@]/-var=node_count=10/-var=node_count=11}" 2>&1)"
  grep -Fq 'Invalid value for variable' <<<"$invalid_output" || {
    echo "$module accepted unsupported n=11 topology" >&2
    exit 1
  }
done

echo "final campaign Terraform contract tests passed"
