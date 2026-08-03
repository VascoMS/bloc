#!/usr/bin/env bash

final_same_az_repository_arn() {
  local image="$1" account repository
  account="${image%%.*}"; repository="${image#*.amazonaws.com/}"; repository="${repository%@*}"
  printf 'arn:aws:ecr:us-east-1:%s:repository/%s\n' "$account" "$repository"
}

final_same_az_prepare_files() {
  local artifact_root="$1" node_count="$2" work
  work="$artifact_root/generated-public/terraform"
  mkdir -p "$work"
  cp "$FINAL_REPO_ROOT/deploy/ec2/terraform/"*.tf "$work/"
  cp "$FINAL_REPO_ROOT/deploy/ec2/terraform/user-data.sh" "$work/"
  [[ ! -f "$FINAL_REPO_ROOT/deploy/ec2/terraform/.terraform.lock.hcl" ]] || cp "$FINAL_REPO_ROOT/deploy/ec2/terraform/.terraform.lock.hcl" "$work/"
  FINAL_SAME_AZ_KEY_NAME="$FINAL_EXPERIMENT_ID-key"
  FINAL_SAME_AZ_KEY_PATH="$FINAL_BUNDLE_ROOT/.campaign-keys/$FINAL_EXPERIMENT_ID.pem"
  export FINAL_SAME_AZ_KEY_NAME FINAL_SAME_AZ_KEY_PATH
  local bloc_arn mempool_arn
  bloc_arn="$(final_same_az_repository_arn "$FINAL_BLOC_IMAGE")"
  mempool_arn="$(final_same_az_repository_arn "$FINAL_MEMPOOL_IMAGE")"
  {
    printf 'aws_region = "us-east-1"\n'
    printf 'availability_zone = "us-east-1a"\n'
    printf 'name_prefix = "%s"\n' "$FINAL_EXPERIMENT_ID"
    printf 'node_count = %s\n' "$node_count"
    printf 'operator_instance_type = "t3.small"\ncontroller_instance_type = "t3.small"\n'
    printf 'cpu_credits = "unlimited"\n'
    printf 'key_name = "%s"\nadmin_cidrs = ["%s"]\n' "$FINAL_SAME_AZ_KEY_NAME" "$FINAL_ADMIN_CIDR"
    printf 'ecr_repository_arns = ["%s", "%s"]\n' "$bloc_arn" "$mempool_arn"
  } >"$work/campaign.auto.tfvars"
}

final_topology_prepare() {
  local artifact_root="$1" node_count="$2"
  final_same_az_prepare_files "$artifact_root" "$node_count"
  mkdir -p "$(dirname "$FINAL_SAME_AZ_KEY_PATH")"; chmod 700 "$(dirname "$FINAL_SAME_AZ_KEY_PATH")"
  aws ec2 create-key-pair --profile "$FINAL_AWS_PROFILE" --region us-east-1 --key-name "$FINAL_SAME_AZ_KEY_NAME" --query KeyMaterial --output text >"$FINAL_SAME_AZ_KEY_PATH"
  chmod 600 "$FINAL_SAME_AZ_KEY_PATH"
}

final_topology_apply() {
  local artifact_root="$1" work
  work="$artifact_root/generated-public/terraform"
  terraform -chdir="$work" init -input=false
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" apply -input=false -auto-approve
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" output -json inventory >"$artifact_root/inventory.json"
}

final_topology_key_for_host() { printf '%s\n' "$FINAL_SAME_AZ_KEY_PATH"; }

final_topology_destroy() {
  local artifact_root="$1" work status=0
  work="$artifact_root/generated-public/terraform"
  AWS_PROFILE="$FINAL_AWS_PROFILE" terraform -chdir="$work" destroy -input=false -auto-approve || status=1
  aws ec2 delete-key-pair --profile "$FINAL_AWS_PROFILE" --region us-east-1 --key-name "$FINAL_SAME_AZ_KEY_NAME" || status=1
  rm -f "$FINAL_SAME_AZ_KEY_PATH" || status=1
  return "$status"
}

final_same_az_query_array() {
  local query="$1"; shift
  local output
  output="$(aws "$@" --query "$query" --output text)" || return 1
  jq -n --arg value "$output" '$value|split(" ")|map(select(length>0 and .!="None"))'
}

final_topology_verify_absent() {
  local artifact_root="$1" work ok=true
  work="$artifact_root/generated-public/terraform"
  local instances volumes vpcs subnets groups routes keys peerings roles profiles state
  instances="$(final_same_az_query_array 'Reservations[].Instances[].InstanceId' ec2 describe-instances --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*" "Name=instance-state-name,Values=pending,running,stopping,stopped")" || ok=false
  volumes="$(final_same_az_query_array 'Volumes[].VolumeId' ec2 describe-volumes --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  vpcs="$(final_same_az_query_array 'Vpcs[].VpcId' ec2 describe-vpcs --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  subnets="$(final_same_az_query_array 'Subnets[].SubnetId' ec2 describe-subnets --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  groups="$(final_same_az_query_array 'SecurityGroups[].GroupId' ec2 describe-security-groups --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=group-name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  routes="$(final_same_az_query_array 'RouteTables[].RouteTableId' ec2 describe-route-tables --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  keys="$(final_same_az_query_array 'KeyPairs[].KeyName' ec2 describe-key-pairs --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=key-name,Values=$FINAL_SAME_AZ_KEY_NAME")" || ok=false
  peerings="$(final_same_az_query_array 'VpcPeeringConnections[?Status.Code!=`deleted`].VpcPeeringConnectionId' ec2 describe-vpc-peering-connections --profile "$FINAL_AWS_PROFILE" --region us-east-1 --filters "Name=tag:Name,Values=$FINAL_EXPERIMENT_ID-*")" || ok=false
  roles="$(final_same_az_query_array "Roles[?RoleName=='$FINAL_EXPERIMENT_ID-ec2-ecr-readonly'].RoleName" iam list-roles --profile "$FINAL_AWS_PROFILE")" || ok=false
  profiles="$(final_same_az_query_array "InstanceProfiles[?InstanceProfileName=='$FINAL_EXPERIMENT_ID-ec2-ecr-readonly'].InstanceProfileName" iam list-instance-profiles --profile "$FINAL_AWS_PROFILE")" || ok=false
  state="$(terraform -chdir="$work" state list 2>/dev/null | jq -R -s 'split("\n")|map(select(length>0))')" || ok=false
  jq -n --argjson query_succeeded "$ok" --argjson instances "${instances:-[]}" --argjson volumes "${volumes:-[]}" \
    --argjson vpcs "${vpcs:-[]}" --argjson subnets "${subnets:-[]}" --argjson security_groups "${groups:-[]}" \
    --argjson route_tables "${routes:-[]}" --argjson key_pairs "${keys:-[]}" --argjson peering_connections "${peerings:-[]}" --argjson roles "${roles:-[]}" \
    --argjson instance_profiles "${profiles:-[]}" --argjson terraform_state "${state:-[]}" \
    '{regions:{"us-east-1":{query_succeeded:$query_succeeded,instances:$instances,volumes:$volumes,vpcs:$vpcs,subnets:$subnets,security_groups:$security_groups,route_tables:$route_tables,key_pairs:$key_pairs,peering_connections:$peering_connections}},iam:{query_succeeded:$query_succeeded,roles:$roles,instance_profiles:$instance_profiles},terraform_state:$terraform_state}' \
    >"$artifact_root/cleanup-topology.json"
  FINAL_CLEANUP_REGIONS=us-east-1; export FINAL_CLEANUP_REGIONS
  [[ "$ok" == true ]] && final_assert_cleanup_empty "$artifact_root/cleanup-topology.json"
}
