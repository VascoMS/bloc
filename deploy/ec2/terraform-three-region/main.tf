data "aws_ami" "primary" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }
}

data "aws_ami" "secondary" {
  provider    = aws.secondary
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }
}

data "aws_ami" "tertiary" {
  provider    = aws.tertiary
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }
}

locals {
  regional_vpc_cidrs     = [var.primary_vpc_cidr, var.secondary_vpc_cidr, var.tertiary_vpc_cidr]
  primary_operator_ids   = [for id in range(var.node_count) : id if id % 3 == 0]
  secondary_operator_ids = [for id in range(var.node_count) : id if id % 3 == 1]
  tertiary_operator_ids  = [for id in range(var.node_count) : id if id % 3 == 2]
  primary_operator_map   = { for id in local.primary_operator_ids : tostring(id) => id }
  secondary_operator_map = { for id in local.secondary_operator_ids : tostring(id) => id }
  tertiary_operator_map  = { for id in local.tertiary_operator_ids : tostring(id) => id }
}

resource "aws_vpc" "primary" {
  cidr_block           = var.primary_vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = { Name = "${var.name_prefix}-primary-vpc" }
}

resource "aws_vpc" "secondary" {
  provider             = aws.secondary
  cidr_block           = var.secondary_vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = { Name = "${var.name_prefix}-secondary-vpc" }
}

resource "aws_vpc" "tertiary" {
  provider             = aws.tertiary
  cidr_block           = var.tertiary_vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = { Name = "${var.name_prefix}-tertiary-vpc" }
}

resource "aws_subnet" "primary" {
  vpc_id                  = aws_vpc.primary.id
  cidr_block              = var.primary_subnet_cidr
  availability_zone       = var.primary_availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name_prefix}-primary-subnet" }
}

resource "aws_subnet" "secondary" {
  provider                = aws.secondary
  vpc_id                  = aws_vpc.secondary.id
  cidr_block              = var.secondary_subnet_cidr
  availability_zone       = var.secondary_availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name_prefix}-secondary-subnet" }
}

resource "aws_subnet" "tertiary" {
  provider                = aws.tertiary
  vpc_id                  = aws_vpc.tertiary.id
  cidr_block              = var.tertiary_subnet_cidr
  availability_zone       = var.tertiary_availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name_prefix}-tertiary-subnet" }
}

resource "aws_internet_gateway" "primary" {
  vpc_id = aws_vpc.primary.id
  tags   = { Name = "${var.name_prefix}-primary-igw" }
}

resource "aws_internet_gateway" "secondary" {
  provider = aws.secondary
  vpc_id   = aws_vpc.secondary.id
  tags     = { Name = "${var.name_prefix}-secondary-igw" }
}

resource "aws_internet_gateway" "tertiary" {
  provider = aws.tertiary
  vpc_id   = aws_vpc.tertiary.id
  tags     = { Name = "${var.name_prefix}-tertiary-igw" }
}

resource "aws_route_table" "primary" {
  vpc_id = aws_vpc.primary.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.primary.id
  }
  tags = { Name = "${var.name_prefix}-primary-rt" }
}

resource "aws_route_table" "secondary" {
  provider = aws.secondary
  vpc_id   = aws_vpc.secondary.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.secondary.id
  }
  tags = { Name = "${var.name_prefix}-secondary-rt" }
}

resource "aws_route_table" "tertiary" {
  provider = aws.tertiary
  vpc_id   = aws_vpc.tertiary.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.tertiary.id
  }
  tags = { Name = "${var.name_prefix}-tertiary-rt" }
}

resource "aws_route_table_association" "primary" {
  subnet_id      = aws_subnet.primary.id
  route_table_id = aws_route_table.primary.id
}

resource "aws_route_table_association" "secondary" {
  provider       = aws.secondary
  subnet_id      = aws_subnet.secondary.id
  route_table_id = aws_route_table.secondary.id
}

resource "aws_route_table_association" "tertiary" {
  provider       = aws.tertiary
  subnet_id      = aws_subnet.tertiary.id
  route_table_id = aws_route_table.tertiary.id
}

resource "aws_vpc_peering_connection" "primary_secondary" {
  vpc_id      = aws_vpc.primary.id
  peer_vpc_id = aws_vpc.secondary.id
  peer_region = var.secondary_region
  auto_accept = false
  tags        = { Name = "${var.name_prefix}-primary-secondary-peering" }
}

resource "aws_vpc_peering_connection_accepter" "primary_secondary" {
  provider                  = aws.secondary
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_secondary.id
  auto_accept               = true
  tags                      = { Name = "${var.name_prefix}-primary-secondary-peering" }
}

resource "aws_vpc_peering_connection" "primary_tertiary" {
  vpc_id      = aws_vpc.primary.id
  peer_vpc_id = aws_vpc.tertiary.id
  peer_region = var.tertiary_region
  auto_accept = false
  tags        = { Name = "${var.name_prefix}-primary-tertiary-peering" }
}

resource "aws_vpc_peering_connection_accepter" "primary_tertiary" {
  provider                  = aws.tertiary
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_tertiary.id
  auto_accept               = true
  tags                      = { Name = "${var.name_prefix}-primary-tertiary-peering" }
}

resource "aws_vpc_peering_connection" "secondary_tertiary" {
  provider    = aws.secondary
  vpc_id      = aws_vpc.secondary.id
  peer_vpc_id = aws_vpc.tertiary.id
  peer_region = var.tertiary_region
  auto_accept = false
  tags        = { Name = "${var.name_prefix}-secondary-tertiary-peering" }
}

resource "aws_vpc_peering_connection_accepter" "secondary_tertiary" {
  provider                  = aws.tertiary
  vpc_peering_connection_id = aws_vpc_peering_connection.secondary_tertiary.id
  auto_accept               = true
  tags                      = { Name = "${var.name_prefix}-secondary-tertiary-peering" }
}

resource "aws_route" "primary_to_secondary" {
  route_table_id            = aws_route_table.primary.id
  destination_cidr_block    = var.secondary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_secondary.id
  depends_on                = [aws_vpc_peering_connection_accepter.primary_secondary]
}

resource "aws_route" "secondary_to_primary" {
  provider                  = aws.secondary
  route_table_id            = aws_route_table.secondary.id
  destination_cidr_block    = var.primary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_secondary.id
  depends_on                = [aws_vpc_peering_connection_accepter.primary_secondary]
}

resource "aws_route" "primary_to_tertiary" {
  route_table_id            = aws_route_table.primary.id
  destination_cidr_block    = var.tertiary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_tertiary.id
  depends_on                = [aws_vpc_peering_connection_accepter.primary_tertiary]
}

resource "aws_route" "tertiary_to_primary" {
  provider                  = aws.tertiary
  route_table_id            = aws_route_table.tertiary.id
  destination_cidr_block    = var.primary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_tertiary.id
  depends_on                = [aws_vpc_peering_connection_accepter.primary_tertiary]
}

resource "aws_route" "secondary_to_tertiary" {
  provider                  = aws.secondary
  route_table_id            = aws_route_table.secondary.id
  destination_cidr_block    = var.tertiary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.secondary_tertiary.id
  depends_on                = [aws_vpc_peering_connection_accepter.secondary_tertiary]
}

resource "aws_route" "tertiary_to_secondary" {
  provider                  = aws.tertiary
  route_table_id            = aws_route_table.tertiary.id
  destination_cidr_block    = var.secondary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.secondary_tertiary.id
  depends_on                = [aws_vpc_peering_connection_accepter.secondary_tertiary]
}

data "aws_iam_policy_document" "ec2_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ec2_ecr_readonly" {
  name               = "${var.name_prefix}-ec2-ecr-readonly"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
}

data "aws_iam_policy_document" "ec2_ecr_pull" {
  statement {
    sid       = "ECRAuthorization"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid = "CampaignRepositoryPull"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
    ]
    resources = var.ecr_repository_arns
  }
}

resource "aws_iam_role_policy" "ec2_ecr_pull" {
  name   = "campaign-repository-pull"
  role   = aws_iam_role.ec2_ecr_readonly.id
  policy = data.aws_iam_policy_document.ec2_ecr_pull.json
}

resource "aws_iam_instance_profile" "ec2_ecr_readonly" {
  name = "${var.name_prefix}-ec2-ecr-readonly"
  role = aws_iam_role.ec2_ecr_readonly.name
}

resource "aws_security_group" "primary_sidecar" {
  name   = "${var.name_prefix}-primary-sidecar"
  vpc_id = aws_vpc.primary.id
  ingress {
    description = "BLOC HTTP measurement and control from all campaign VPCs"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "BLOC libp2p from all campaign VPCs"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "SSH administration"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "secondary_sidecar" {
  provider = aws.secondary
  name     = "${var.name_prefix}-secondary-sidecar"
  vpc_id   = aws_vpc.secondary.id
  ingress {
    description = "BLOC HTTP measurement and control from all campaign VPCs"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "BLOC libp2p from all campaign VPCs"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "SSH administration"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "tertiary_sidecar" {
  provider = aws.tertiary
  name     = "${var.name_prefix}-tertiary-sidecar"
  vpc_id   = aws_vpc.tertiary.id
  ingress {
    description = "BLOC HTTP measurement and control from all campaign VPCs"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "BLOC libp2p from all campaign VPCs"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = local.regional_vpc_cidrs
  }
  ingress {
    description = "SSH administration"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "controller" {
  name   = "${var.name_prefix}-controller"
  vpc_id = aws_vpc.primary.id
  ingress {
    description = "SSH administration"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  ingress {
    description = "Prometheus UI"
    from_port   = 9090
    to_port     = 9090
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  ingress {
    description = "Grafana UI"
    from_port   = 3000
    to_port     = 3000
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "controller" {
  ami                         = data.aws_ami.primary.id
  instance_type               = var.controller_instance_type
  subnet_id                   = aws_subnet.primary.id
  associate_public_ip_address = true
  key_name                    = var.primary_key_name
  vpc_security_group_ids      = [aws_security_group.controller.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  monitoring                  = true

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  credit_specification { cpu_credits = var.cpu_credits }
  root_block_device {
    volume_size           = var.controller_root_volume_size
    encrypted             = true
    delete_on_termination = true
  }
  tags        = { Name = "${var.name_prefix}-controller", Role = "bloc-controller" }
  volume_tags = { Name = "${var.name_prefix}-controller-volume", Role = "bloc-controller" }
}

resource "aws_instance" "primary_operator" {
  for_each                    = local.primary_operator_map
  ami                         = data.aws_ami.primary.id
  instance_type               = var.operator_instance_type
  subnet_id                   = aws_subnet.primary.id
  associate_public_ip_address = true
  key_name                    = var.primary_key_name
  vpc_security_group_ids      = [aws_security_group.primary_sidecar.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  monitoring                  = true

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  credit_specification { cpu_credits = var.cpu_credits }
  root_block_device {
    encrypted             = true
    delete_on_termination = true
  }
  tags        = { Name = "${var.name_prefix}-operator-${each.value}", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
  volume_tags = { Name = "${var.name_prefix}-operator-${each.value}-volume", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
}

resource "aws_instance" "secondary_operator" {
  provider                    = aws.secondary
  for_each                    = local.secondary_operator_map
  ami                         = data.aws_ami.secondary.id
  instance_type               = var.operator_instance_type
  subnet_id                   = aws_subnet.secondary.id
  associate_public_ip_address = true
  key_name                    = var.secondary_key_name
  vpc_security_group_ids      = [aws_security_group.secondary_sidecar.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  monitoring                  = true

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  credit_specification { cpu_credits = var.cpu_credits }
  root_block_device {
    encrypted             = true
    delete_on_termination = true
  }
  tags        = { Name = "${var.name_prefix}-operator-${each.value}", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
  volume_tags = { Name = "${var.name_prefix}-operator-${each.value}-volume", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
}

resource "aws_instance" "tertiary_operator" {
  provider                    = aws.tertiary
  for_each                    = local.tertiary_operator_map
  ami                         = data.aws_ami.tertiary.id
  instance_type               = var.operator_instance_type
  subnet_id                   = aws_subnet.tertiary.id
  associate_public_ip_address = true
  key_name                    = var.tertiary_key_name
  vpc_security_group_ids      = [aws_security_group.tertiary_sidecar.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  monitoring                  = true

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  credit_specification { cpu_credits = var.cpu_credits }
  root_block_device {
    encrypted             = true
    delete_on_termination = true
  }
  tags        = { Name = "${var.name_prefix}-operator-${each.value}", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
  volume_tags = { Name = "${var.name_prefix}-operator-${each.value}-volume", Role = "bloc-operator", NodeID = tostring(each.value), Cluster = var.name_prefix }
}
