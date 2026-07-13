output "inventory" {
  value = {
    region = var.aws_region
    hosts = [
      for label, instance in aws_instance.benchmark : {
        label         = label
        instance_id   = instance.id
        instance_type = instance.instance_type
        zone          = instance.availability_zone
        private_ip    = instance.private_ip
        public_ip     = instance.public_ip
      }
    ]
  }
}

output "ecr_repository_url" {
  value = aws_ecr_repository.benchmark.repository_url
}
