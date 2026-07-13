terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 5.100.0"
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

resource "aws_vpc" "benchmark" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = { Name = "${var.name_prefix}-vpc" }
}

resource "aws_internet_gateway" "benchmark" {
  vpc_id = aws_vpc.benchmark.id
  tags   = { Name = "${var.name_prefix}-igw" }
}

resource "aws_subnet" "benchmark" {
  count                   = length(var.availability_zones)
  vpc_id                  = aws_vpc.benchmark.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index + 1)
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name_prefix}-subnet-${count.index}" }
}

resource "aws_route_table" "benchmark" {
  vpc_id = aws_vpc.benchmark.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.benchmark.id
  }
  tags = { Name = "${var.name_prefix}-rt" }
}

resource "aws_route_table_association" "benchmark" {
  count          = length(aws_subnet.benchmark)
  subnet_id      = aws_subnet.benchmark[count.index].id
  route_table_id = aws_route_table.benchmark.id
}

resource "aws_security_group" "benchmark" {
  name        = "${var.name_prefix}-benchmark"
  description = "SSH access for the BTE attribution campaign"
  vpc_id      = aws_vpc.benchmark.id

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

resource "aws_ecr_repository" "benchmark" {
  name         = var.ecr_repository_name
  force_delete = true
  image_scanning_configuration { scan_on_push = true }
  tags = { Name = var.ecr_repository_name }
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

resource "aws_iam_role" "ecr_readonly" {
  name               = "${var.name_prefix}-ec2-ecr-readonly"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
}

resource "aws_iam_role_policy_attachment" "ecr_readonly" {
  role       = aws_iam_role.ecr_readonly.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_instance_profile" "ecr_readonly" {
  name = "${var.name_prefix}-ec2-ecr-readonly"
  role = aws_iam_role.ecr_readonly.name
}

locals {
  hosts = {
    for host in var.hosts : host.label => host
  }
}

resource "aws_instance" "benchmark" {
  for_each                    = local.hosts
  ami                         = data.aws_ami.ubuntu.id
  instance_type               = each.value.instance_type
  subnet_id                   = aws_subnet.benchmark[each.value.zone_index].id
  associate_public_ip_address = true
  key_name                    = var.key_name
  vpc_security_group_ids      = [aws_security_group.benchmark.id]
  iam_instance_profile        = aws_iam_instance_profile.ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")

  dynamic "credit_specification" {
    for_each = startswith(each.value.instance_type, "t3.") ? [1] : []
    content {
      cpu_credits = "unlimited"
    }
  }

  tags = {
    Name       = "${var.name_prefix}-${each.key}"
    Role       = "bte-attribution"
    HostLabel  = each.key
    Experiment = var.name_prefix
  }

  volume_tags = {
    Name       = "${var.name_prefix}-${each.key}-volume"
    Role       = "bte-attribution"
    Experiment = var.name_prefix
  }
}
