variable "primary_region" {
  type    = string
  default = "us-east-1"
}

variable "secondary_region" {
  type    = string
  default = "eu-west-1"
}

variable "tertiary_region" {
  type    = string
  default = "eu-central-1"
}

variable "primary_availability_zone" {
  type    = string
  default = "us-east-1a"
}

variable "secondary_availability_zone" {
  type    = string
  default = "eu-west-1a"
}

variable "tertiary_availability_zone" {
  type    = string
  default = "eu-central-1a"
}

variable "primary_vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "secondary_vpc_cidr" {
  type    = string
  default = "10.50.0.0/16"
}

variable "tertiary_vpc_cidr" {
  type    = string
  default = "10.60.0.0/16"
}

variable "primary_subnet_cidr" {
  type    = string
  default = "10.40.1.0/24"
}

variable "secondary_subnet_cidr" {
  type    = string
  default = "10.50.1.0/24"
}

variable "tertiary_subnet_cidr" {
  type    = string
  default = "10.60.1.0/24"
}

variable "name_prefix" {
  type    = string
  default = "bloc-ec2-three-region"
}

variable "node_count" {
  type    = number
  default = 4

  validation {
    condition     = contains([4, 7], var.node_count)
    error_message = "node_count must be 4 or 7."
  }
}

variable "operator_instance_type" {
  type    = string
  default = "t3.small"

  validation {
    condition     = var.operator_instance_type == "t3.small"
    error_message = "The accepted three-region campaign requires t3.small operators."
  }
}

variable "controller_instance_type" {
  type    = string
  default = "t3.small"

  validation {
    condition     = var.controller_instance_type == "t3.small"
    error_message = "The accepted three-region campaign requires a t3.small controller."
  }
}

variable "controller_root_volume_size" {
  type    = number
  default = 16

  validation {
    condition     = var.controller_root_volume_size == 16
    error_message = "The final campaign controller requires a 16 GiB root volume."
  }
}

variable "cpu_credits" {
  type    = string
  default = "unlimited"

  validation {
    condition     = contains(["standard", "unlimited"], var.cpu_credits)
    error_message = "cpu_credits must be standard or unlimited."
  }
}

variable "primary_key_name" {
  type = string
}

variable "secondary_key_name" {
  type = string
}

variable "tertiary_key_name" {
  type = string
}

variable "admin_cidrs" {
  type = list(string)

  validation {
    condition = length(var.admin_cidrs) > 0 && alltrue([
      for cidr in var.admin_cidrs : can(cidrhost(cidr, 0)) && can(regex("/32$", cidr))
    ])
    error_message = "admin_cidrs must contain one or more valid administration /32 CIDRs."
  }
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
