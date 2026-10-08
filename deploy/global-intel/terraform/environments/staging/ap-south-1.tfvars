environment = "staging"
region      = "ap-south-1"

vpc_cidr           = "10.45.0.0/16"
az_count           = 2
single_nat_gateway = true

db_instance_class        = "db.t3.small"
db_allocated_storage     = 50
db_multi_az              = false
db_backup_retention_days = 7
db_deletion_protection   = false

opensearch_instance_type  = "t3.medium.search"
opensearch_instance_count = 1
opensearch_zone_awareness = false
opensearch_volume_size    = 50

task_cpu    = 512
task_memory = 1024

schedule_expression = "cron(0 6 * * ? *)"
schedule_enabled    = true
log_retention_days  = 30
