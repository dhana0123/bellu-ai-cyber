output "cluster_arn" { value = aws_ecs_cluster.this.arn }
output "cluster_name" { value = aws_ecs_cluster.this.name }
output "task_definition_arn" { value = aws_ecs_task_definition.intel.arn }
output "ecr_repository_url" { value = aws_ecr_repository.this.repository_url }
