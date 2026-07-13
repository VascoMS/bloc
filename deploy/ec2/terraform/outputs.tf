output "inventory" {
  value = {
    deployment = {
      environment = "ec2"
      region      = var.aws_region
      topology    = "one-ec2-per-sidecar"
    }
    controller = {
      label       = "bloc-controller"
      private_ip  = aws_instance.controller.private_ip
      private_dns = aws_instance.controller.private_dns
      public_ip   = aws_instance.controller.public_ip
      public_dns  = aws_instance.controller.public_dns
      region      = var.aws_region
      zone        = aws_instance.controller.availability_zone
    }
    nodes = [
      for instance in aws_instance.operator : {
        id          = tonumber(instance.tags.NodeID)
        label       = instance.tags.Name
        private_ip  = instance.private_ip
        private_dns = instance.private_dns
        public_ip   = instance.public_ip
        public_dns  = instance.public_dns
        region      = var.aws_region
        zone        = instance.availability_zone
      }
    ]
  }
}

output "ecr_repository_url" {
  value       = var.create_ecr_repository ? aws_ecr_repository.bloc_node[0].repository_url : ""
  description = "ECR repository URL for the bloc-node image when Terraform creates the repository."
}
