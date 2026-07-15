output "inventory" {
  value = {
    deployment = {
      environment      = "ec2"
      topology         = "T2-cross-region"
      primary_region   = var.primary_region
      secondary_region = var.secondary_region
    }
    controller = {
      instance_id   = aws_instance.controller.id, ami_id = aws_instance.controller.ami,
      instance_type = aws_instance.controller.instance_type, label = "bloc-controller",
      private_ip    = aws_instance.controller.private_ip, public_ip = aws_instance.controller.public_ip,
      private_dns   = aws_instance.controller.private_dns, public_dns = aws_instance.controller.public_dns,
      region        = var.primary_region, zone = aws_instance.controller.availability_zone
    }
    nodes = concat(
      [for instance in aws_instance.primary_operator : {
        id            = tonumber(instance.tags.NodeID), instance_id = instance.id, ami_id = instance.ami,
        instance_type = instance.instance_type, label = instance.tags.Name,
        private_ip    = instance.private_ip, public_ip = instance.public_ip,
        private_dns   = instance.private_dns, public_dns = instance.public_dns,
        region        = var.primary_region, zone = instance.availability_zone
      }],
      [for instance in aws_instance.secondary_operator : {
        id            = tonumber(instance.tags.NodeID), instance_id = instance.id, ami_id = instance.ami,
        instance_type = instance.instance_type, label = instance.tags.Name,
        private_ip    = instance.private_ip, public_ip = instance.public_ip,
        private_dns   = instance.private_dns, public_dns = instance.public_dns,
        region        = var.secondary_region, zone = instance.availability_zone
      }]
    )
  }
}

output "ecr_repository_url" { value = aws_ecr_repository.bloc_node.repository_url }
output "peering_connection_id" { value = aws_vpc_peering_connection.cross_region.id }
output "primary_vpc_id" { value = aws_vpc.primary.id }
output "secondary_vpc_id" { value = aws_vpc.secondary.id }
