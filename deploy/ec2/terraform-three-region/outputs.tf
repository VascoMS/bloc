output "inventory" {
  value = {
    deployment = {
      environment      = "ec2"
      topology         = "T2-three-region"
      primary_region   = var.primary_region
      secondary_region = var.secondary_region
      tertiary_region  = var.tertiary_region
    }
    controller = {
      instance_id   = aws_instance.controller.id
      ami_id        = aws_instance.controller.ami
      instance_type = aws_instance.controller.instance_type
      label         = "bloc-controller"
      private_ip    = aws_instance.controller.private_ip
      public_ip     = aws_instance.controller.public_ip
      private_dns   = aws_instance.controller.private_dns
      public_dns    = aws_instance.controller.public_dns
      region        = var.primary_region
      zone          = aws_instance.controller.availability_zone
    }
    nodes = concat(
      [for instance in values(aws_instance.primary_operator) : {
        id            = tonumber(instance.tags.NodeID)
        instance_id   = instance.id
        ami_id        = instance.ami
        instance_type = instance.instance_type
        label         = instance.tags.Name
        private_ip    = instance.private_ip
        public_ip     = instance.public_ip
        private_dns   = instance.private_dns
        public_dns    = instance.public_dns
        region        = var.primary_region
        zone          = instance.availability_zone
      }],
      [for instance in values(aws_instance.secondary_operator) : {
        id            = tonumber(instance.tags.NodeID)
        instance_id   = instance.id
        ami_id        = instance.ami
        instance_type = instance.instance_type
        label         = instance.tags.Name
        private_ip    = instance.private_ip
        public_ip     = instance.public_ip
        private_dns   = instance.private_dns
        public_dns    = instance.public_dns
        region        = var.secondary_region
        zone          = instance.availability_zone
      }],
      [for instance in values(aws_instance.tertiary_operator) : {
        id            = tonumber(instance.tags.NodeID)
        instance_id   = instance.id
        ami_id        = instance.ami
        instance_type = instance.instance_type
        label         = instance.tags.Name
        private_ip    = instance.private_ip
        public_ip     = instance.public_ip
        private_dns   = instance.private_dns
        public_dns    = instance.public_dns
        region        = var.tertiary_region
        zone          = instance.availability_zone
      }]
    )
  }
}

output "ecr_repository_url" {
  value = aws_ecr_repository.bloc_node.repository_url
}

output "peering_connection_ids" {
  value = {
    primary_secondary  = aws_vpc_peering_connection.primary_secondary.id
    primary_tertiary   = aws_vpc_peering_connection.primary_tertiary.id
    secondary_tertiary = aws_vpc_peering_connection.secondary_tertiary.id
  }
}

output "vpc_ids" {
  value = {
    primary   = aws_vpc.primary.id
    secondary = aws_vpc.secondary.id
    tertiary  = aws_vpc.tertiary.id
  }
}
