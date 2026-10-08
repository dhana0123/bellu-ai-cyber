output "ecr_repository_url" {
  description = "Push the global-intel image here, then set container_image (or deploy :latest)."
  value       = module.ecs.ecr_repository_url
}

output "raw_bucket" {
  description = "Immutable raw intelligence bucket."
  value       = module.s3.raw_bucket_name
}

output "releases_bucket" {
  description = "Signed release-artifact bucket."
  value       = module.s3.releases_bucket_name
}

output "database_endpoint" {
  description = "RDS PostgreSQL endpoint."
  value       = module.rds.endpoint
}

output "db_secret_arn" {
  description = "Secrets Manager ARN with DB credentials + DSN."
  value       = module.secrets.db_secret_arn
}

output "search_endpoint" {
  description = "OpenSearch HTTPS endpoint (VPC-internal)."
  value       = module.opensearch.endpoint
}

output "ecs_cluster_name" {
  description = "ECS cluster running the scheduled task."
  value       = module.ecs.cluster_name
}

output "task_definition_arn" {
  description = "Fargate task definition for the pipeline."
  value       = module.ecs.task_definition_arn
}

output "schedule_rule" {
  description = "EventBridge rule driving the daily run."
  value       = module.eventbridge.rule_name
}

output "vpc_id" {
  value = module.vpc.vpc_id
}

output "private_subnet_ids" {
  value = module.vpc.private_subnet_ids
}
