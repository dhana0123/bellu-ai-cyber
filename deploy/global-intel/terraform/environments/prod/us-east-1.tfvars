environment = "prod"
region      = "us-east-1"

vpc_cidr           = "10.46.0.0/16"
az_count           = 2
single_nat_gateway = false

db_instance_class        = "db.m6g.large"
db_allocated_storage     = 100
db_multi_az              = true
db_backup_retention_days = 14
db_deletion_protection   = true

opensearch_instance_type  = "m6g.large.search"
opensearch_instance_count = 2
opensearch_zone_awareness = true
opensearch_volume_size    = 100

task_cpu    = 1024
task_memory = 2048

schedule_expression = "cron(0 6 * * ? *)"
schedule_enabled    = true
log_retention_days  = 30
