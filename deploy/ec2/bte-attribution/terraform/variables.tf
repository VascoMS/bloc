variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "availability_zones" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]

  validation {
    condition     = length(var.availability_zones) >= 2
    error_message = "At least two availability zones are required."
  }
}

variable "name_prefix" {
  type    = string
  default = "bloc-ec2-bte-attribution"
}

variable "ecr_repository_name" {
  type = string
}

variable "key_name" {
  type        = string
  description = "Existing EC2 key pair name used for artifact collection."
}

variable "admin_cidrs" {
  type        = list(string)
  description = "CIDR ranges allowed to reach SSH."
}

variable "vpc_cidr" {
  type    = string
  default = "10.60.0.0/16"
}

variable "hosts" {
  type = list(object({
    label         = string
    instance_type = string
    zone_index    = number
  }))

  default = [
    { label = "t3-small-a", instance_type = "t3.small", zone_index = 0 },
    { label = "t3-small-b", instance_type = "t3.small", zone_index = 1 },
    { label = "c7a-large-a", instance_type = "c7a.large", zone_index = 0 },
    { label = "c7a-large-b", instance_type = "c7a.large", zone_index = 1 }
  ]

  validation {
    condition     = alltrue([for host in var.hosts : host.zone_index >= 0 && host.zone_index < length(var.availability_zones)])
    error_message = "Every host zone_index must address availability_zones."
  }
}
