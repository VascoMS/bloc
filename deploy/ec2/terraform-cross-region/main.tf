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

resource "aws_internet_gateway" "primary" {
  vpc_id = aws_vpc.primary.id
  tags   = { Name = "${var.name_prefix}-primary-igw" }
}

resource "aws_internet_gateway" "secondary" {
  provider = aws.secondary
  vpc_id   = aws_vpc.secondary.id
  tags     = { Name = "${var.name_prefix}-secondary-igw" }
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

resource "aws_route_table_association" "primary" {
  subnet_id      = aws_subnet.primary.id
  route_table_id = aws_route_table.primary.id
}

resource "aws_route_table_association" "secondary" {
  provider       = aws.secondary
  subnet_id      = aws_subnet.secondary.id
  route_table_id = aws_route_table.secondary.id
}

resource "aws_vpc_peering_connection" "cross_region" {
  vpc_id      = aws_vpc.primary.id
  peer_vpc_id = aws_vpc.secondary.id
  peer_region = var.secondary_region
  auto_accept = false
  tags        = { Name = "${var.name_prefix}-peering" }
}

resource "aws_vpc_peering_connection_accepter" "cross_region" {
  provider                  = aws.secondary
  vpc_peering_connection_id = aws_vpc_peering_connection.cross_region.id
  auto_accept               = true
  tags                      = { Name = "${var.name_prefix}-peering" }
}

resource "aws_route" "primary_to_secondary" {
  route_table_id            = aws_route_table.primary.id
  destination_cidr_block    = var.secondary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.cross_region.id
  depends_on                = [aws_vpc_peering_connection_accepter.cross_region]
}

resource "aws_route" "secondary_to_primary" {
  provider                  = aws.secondary
  route_table_id            = aws_route_table.secondary.id
  destination_cidr_block    = var.primary_vpc_cidr
  vpc_peering_connection_id = aws_vpc_peering_connection.cross_region.id
  depends_on                = [aws_vpc_peering_connection_accepter.cross_region]
}

resource "aws_ecr_repository" "bloc_node" {
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

resource "aws_iam_role" "ec2_ecr_readonly" {
  name               = "${var.name_prefix}-ec2-ecr-readonly"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
}

resource "aws_iam_role_policy_attachment" "ec2_ecr_readonly" {
  role       = aws_iam_role.ec2_ecr_readonly.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_instance_profile" "ec2_ecr_readonly" {
  name = "${var.name_prefix}-ec2-ecr-readonly"
  role = aws_iam_role.ec2_ecr_readonly.name
}

locals {
  primary_operator_count   = ceil(var.node_count / 2)
  secondary_operator_count = floor(var.node_count / 2)
}

resource "aws_security_group" "primary_sidecar" {
  name   = "${var.name_prefix}-primary-sidecar"
  vpc_id = aws_vpc.primary.id
  ingress {
    description = "BLOC HTTP measurement and control from both VPCs"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = [var.primary_vpc_cidr, var.secondary_vpc_cidr]
  }
  ingress {
    description = "BLOC libp2p from both regional VPCs"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = [var.primary_vpc_cidr, var.secondary_vpc_cidr]
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
    description = "BLOC HTTP measurement and control from both VPCs"
    from_port   = 8000
    to_port     = 8000
    protocol    = "tcp"
    cidr_blocks = [var.primary_vpc_cidr, var.secondary_vpc_cidr]
  }
  ingress {
    description = "BLOC libp2p from both regional VPCs"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    cidr_blocks = [var.primary_vpc_cidr, var.secondary_vpc_cidr]
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
  tags                        = { Name = "${var.name_prefix}-controller", Role = "bloc-controller" }
  volume_tags                 = { Name = "${var.name_prefix}-controller-volume", Role = "bloc-controller" }
}

resource "aws_instance" "primary_operator" {
  count                       = local.primary_operator_count
  ami                         = data.aws_ami.primary.id
  instance_type               = var.operator_instance_type
  subnet_id                   = aws_subnet.primary.id
  associate_public_ip_address = true
  key_name                    = var.primary_key_name
  vpc_security_group_ids      = [aws_security_group.primary_sidecar.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  tags                        = { Name = "${var.name_prefix}-operator-${count.index * 2}", Role = "bloc-operator", NodeID = tostring(count.index * 2), Cluster = var.name_prefix }
  volume_tags                 = { Name = "${var.name_prefix}-operator-${count.index * 2}-volume", Role = "bloc-operator", NodeID = tostring(count.index * 2), Cluster = var.name_prefix }
}

resource "aws_instance" "secondary_operator" {
  provider                    = aws.secondary
  count                       = local.secondary_operator_count
  ami                         = data.aws_ami.secondary.id
  instance_type               = var.operator_instance_type
  subnet_id                   = aws_subnet.secondary.id
  associate_public_ip_address = true
  key_name                    = var.secondary_key_name
  vpc_security_group_ids      = [aws_security_group.secondary_sidecar.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2_ecr_readonly.name
  user_data                   = file("${path.module}/user-data.sh")
  tags                        = { Name = "${var.name_prefix}-operator-${count.index * 2 + 1}", Role = "bloc-operator", NodeID = tostring(count.index * 2 + 1), Cluster = var.name_prefix }
  volume_tags                 = { Name = "${var.name_prefix}-operator-${count.index * 2 + 1}-volume", Role = "bloc-operator", NodeID = tostring(count.index * 2 + 1), Cluster = var.name_prefix }
}
