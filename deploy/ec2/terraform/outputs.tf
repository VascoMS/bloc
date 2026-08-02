output "inventory" {
  value = {
    deployment = {
      environment = "ec2"
      region      = var.aws_region
      topology    = "one-ec2-per-sidecar"
    }
    controller = {
      instance_id   = aws_instance.controller.id
      ami_id        = aws_instance.controller.ami
      instance_type = aws_instance.controller.instance_type
      label         = "bloc-controller"
      private_ip    = aws_instance.controller.private_ip
      private_dns   = aws_instance.controller.private_dns
      public_ip     = aws_instance.controller.public_ip
      public_dns    = aws_instance.controller.public_dns
      region        = var.aws_region
      zone          = aws_instance.controller.availability_zone
    }
    nodes = [
      for instance in aws_instance.operator : {
        id            = tonumber(instance.tags.NodeID)
        instance_id   = instance.id
        ami_id        = instance.ami
        instance_type = instance.instance_type
        label         = instance.tags.Name
        private_ip    = instance.private_ip
        private_dns   = instance.private_dns
        public_ip     = instance.public_ip
        public_dns    = instance.public_dns
        region        = var.aws_region
        zone          = instance.availability_zone
      }
    ]
  }
}
