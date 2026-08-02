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

variable "create_iam_instance_profile" {
  type    = bool
  default = true
}

variable "ecr_repository_arns" {
  type        = list(string)
  description = "The pre-existing us-east-1 bloc-node and mempool-il ECR repository ARNs."

  validation {
    condition = length(var.ecr_repository_arns) == 2 && alltrue([
      for arn in var.ecr_repository_arns : can(regex("^arn:aws:ecr:us-east-1:[0-9]{12}:repository/[a-z0-9][a-z0-9._/-]*$", arn))
    ])
    error_message = "ecr_repository_arns must contain exactly two us-east-1 private ECR repository ARNs."
  }
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

variable "cpu_credits" {
  type    = string
  default = "unlimited"

  validation {
    condition     = contains(["standard", "unlimited"], var.cpu_credits)
    error_message = "cpu_credits must be standard or unlimited."
  }
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
