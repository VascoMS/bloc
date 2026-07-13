terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }
}

resource "aws_vpc" "bloc" {
  count                = var.vpc_id == "" ? 1 : 0
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "${var.name_prefix}-vpc"
  }
}

data "aws_vpc" "selected" {
  id = var.vpc_id == "" ? aws_vpc.bloc[0].id : var.vpc_id
}

locals {
  generated_availability_zones = length(var.availability_zones) == 0 ? [var.availability_zone] : var.availability_zones
  generated_subnet_cidrs       = length(var.subnet_cidrs) == 0 ? [var.subnet_cidr] : var.subnet_cidrs
}

resource "aws_subnet" "bloc" {
  count                   = length(var.subnet_ids) == 0 ? length(local.generated_subnet_cidrs) : 0
  vpc_id                  = data.aws_vpc.selected.id
  cidr_block              = local.generated_subnet_cidrs[count.index]
  availability_zone       = local.generated_availability_zones[count.index % length(local.generated_availability_zones)]
  map_public_ip_on_launch = var.assign_public_ip

  tags = {
    Name = "${var.name_prefix}-subnet-${count.index}"
  }
}

resource "aws_internet_gateway" "bloc" {
  count  = var.vpc_id == "" ? 1 : 0
  vpc_id = data.aws_vpc.selected.id

  tags = {
    Name = "${var.name_prefix}-igw"
  }
}

resource "aws_route_table" "bloc" {
  count  = var.vpc_id == "" ? 1 : 0
  vpc_id = data.aws_vpc.selected.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.bloc[0].id
  }

  tags = {
    Name = "${var.name_prefix}-rt"
  }
}

resource "aws_route_table_association" "bloc" {
  count          = var.vpc_id == "" ? length(aws_subnet.bloc) : 0
  subnet_id      = aws_subnet.bloc[count.index].id
  route_table_id = aws_route_table.bloc[0].id
}

locals {
  subnet_ids                = length(var.subnet_ids) == 0 ? aws_subnet.bloc[*].id : var.subnet_ids
  iam_instance_profile_name = var.create_iam_instance_profile ? aws_iam_instance_profile.ec2_ecr_readonly[0].name : null
}

resource "aws_ecr_repository" "bloc_node" {
  count        = var.create_ecr_repository ? 1 : 0
  name         = var.ecr_repository_name
  force_delete = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = {
    Name = var.ecr_repository_name
  }
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
  count              = var.create_iam_instance_profile ? 1 : 0
  name               = "${var.name_prefix}-ec2-ecr-readonly"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
}

resource "aws_iam_role_policy_attachment" "ec2_ecr_readonly" {
  count      = var.create_iam_instance_profile ? 1 : 0
  role       = aws_iam_role.ec2_ecr_readonly[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_instance_profile" "ec2_ecr_readonly" {
  count = var.create_iam_instance_profile ? 1 : 0
  name  = "${var.name_prefix}-ec2-ecr-readonly"
  role  = aws_iam_role.ec2_ecr_readonly[0].name
}

resource "aws_security_group" "sidecar" {
  name        = "${var.name_prefix}-sidecar"
  description = "BLOC sidecar HTTP and libp2p traffic"
  vpc_id      = data.aws_vpc.selected.id

  ingress {
    description = "BLOC HTTP from controller"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  ingress {
    description = "BLOC libp2p between operators"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  ingress {
    description = "SSH admin access"
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
  name        = "${var.name_prefix}-controller"
  description = "BLOC evaluator, Prometheus, and Grafana controller"
  vpc_id      = data.aws_vpc.selected.id

  ingress {
    description = "SSH admin access"
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
  ami                         = data.aws_ami.ubuntu.id
  instance_type               = var.controller_instance_type
  subnet_id                   = local.subnet_ids[0]
  associate_public_ip_address = var.assign_public_ip
  key_name                    = var.key_name
  vpc_security_group_ids      = [aws_security_group.controller.id]
  iam_instance_profile        = local.iam_instance_profile_name
  user_data                   = file("${path.module}/user-data.sh")

  tags = {
    Name = "${var.name_prefix}-controller"
    Role = "bloc-controller"
  }

  volume_tags = {
    Name = "${var.name_prefix}-controller-volume"
    Role = "bloc-controller"
  }
}

resource "aws_instance" "operator" {
  count                       = var.node_count
  ami                         = data.aws_ami.ubuntu.id
  instance_type               = var.operator_instance_type
  subnet_id                   = local.subnet_ids[count.index % length(local.subnet_ids)]
  associate_public_ip_address = var.assign_public_ip
  key_name                    = var.key_name
  vpc_security_group_ids      = [aws_security_group.sidecar.id]
  iam_instance_profile        = local.iam_instance_profile_name
  user_data                   = file("${path.module}/user-data.sh")

  tags = {
    Name    = "${var.name_prefix}-operator-${count.index}"
    Role    = "bloc-operator"
    NodeID  = tostring(count.index)
    Cluster = var.name_prefix
  }

  volume_tags = {
    Name    = "${var.name_prefix}-operator-${count.index}-volume"
    Role    = "bloc-operator"
    NodeID  = tostring(count.index)
    Cluster = var.name_prefix
  }
}
