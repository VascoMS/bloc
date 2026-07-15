variable "primary_region" {
  type    = string
  default = "us-east-1"
}

variable "secondary_region" {
  type    = string
  default = "eu-west-1"
}

variable "primary_availability_zone" {
  type    = string
  default = "us-east-1a"
}

variable "secondary_availability_zone" {
  type    = string
  default = "eu-west-1a"
}

variable "primary_vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "secondary_vpc_cidr" {
  type    = string
  default = "10.50.0.0/16"
}

variable "primary_subnet_cidr" {
  type    = string
  default = "10.40.1.0/24"
}

variable "secondary_subnet_cidr" {
  type    = string
  default = "10.50.1.0/24"
}

variable "name_prefix" {
  type    = string
  default = "bloc-ec2-cross-region"
}

variable "node_count" {
  type    = number
  default = 4

  validation {
    condition     = contains([4, 7, 10], var.node_count)
    error_message = "node_count must be 4, 7, or 10."
  }
}

variable "operator_instance_type" {
  type    = string
  default = "t3.medium"
}

variable "controller_instance_type" {
  type    = string
  default = "t3.medium"
}

variable "primary_key_name" {
  type = string
}

variable "secondary_key_name" {
  type = string
}

variable "admin_cidrs" {
  type = list(string)
}

variable "ecr_repository_name" {
  type    = string
  default = "bloc-node-cross-region"
}
