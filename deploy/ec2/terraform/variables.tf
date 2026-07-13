variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "availability_zone" {
  type    = string
  default = "us-east-1a"
}

variable "availability_zones" {
  type    = list(string)
  default = []
}

variable "name_prefix" {
  type    = string
  default = "bloc-ec2"
}

variable "create_ecr_repository" {
  type    = bool
  default = true
}

variable "create_iam_instance_profile" {
  type    = bool
  default = true
}

variable "ecr_repository_name" {
  type    = string
  default = "bloc-node"
}

variable "node_count" {
  type    = number
  default = 4
}

variable "operator_instance_type" {
  type    = string
  default = "t3.small"
}

variable "controller_instance_type" {
  type    = string
  default = "t3.small"
}

variable "key_name" {
  type        = string
  description = "Existing EC2 key pair name for SSH access."
}

variable "admin_cidrs" {
  type        = list(string)
  description = "CIDR ranges allowed to reach SSH, Prometheus, and Grafana."
}

variable "assign_public_ip" {
  type    = bool
  default = true
}

variable "vpc_id" {
  type    = string
  default = ""
}

variable "subnet_ids" {
  type    = list(string)
  default = []
}

variable "vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "subnet_cidr" {
  type    = string
  default = "10.40.1.0/24"
}

variable "subnet_cidrs" {
  type    = list(string)
  default = []
}
