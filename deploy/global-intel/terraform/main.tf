# Root module: composes the global-intel stack from the child modules. The
# same code deploys to any environment+region; sizing/HA come entirely from the
# per-env/region tfvars. Dependency order and the IAM<->OpenSearch policy cycle
# are handled here (IAM references the OpenSearch domain by its deterministic
# name, so it does not depend on the opensearch resource).

locals {
  # Every resource name is prefixed with "<name_prefix>-<environment>", so dev,
  # staging, and prod never collide and are obvious in the console.
  name               = "${var.name_prefix}-${var.environment}"
  search_domain_name = "${local.name}-search"
}

# ---- networking -----------------------------------------------------------
module "vpc" {
  source             = "./modules/vpc"
  name               = local.name
  vpc_cidr           = var.vpc_cidr
  az_count           = var.az_count
  single_nat_gateway = var.single_nat_gateway
  region             = var.region
}

# ---- secrets + KMS (no deps) ---------------------------------------------
module "secrets" {
  source      = "./modules/secrets"
  name        = local.name
  db_username = var.db_username
}

# ---- S3 (needs KMS) -------------------------------------------------------
module "s3" {
  source      = "./modules/s3"
  name        = local.name
  kms_key_arn = module.secrets.kms_key_arn
}

# ---- CloudWatch -----------------------------------------------------------
module "cloudwatch" {
  source         = "./modules/cloudwatch"
  name           = local.name
  retention_days = var.log_retention_days
}

# ---- IAM (name-based OpenSearch ARN breaks the policy cycle) --------------
module "iam" {
  source              = "./modules/iam"
  name                = local.name
  raw_bucket_arn      = module.s3.raw_bucket_arn
  releases_bucket_arn = module.s3.releases_bucket_arn
  db_secret_arn       = module.secrets.db_secret_arn
  kms_key_arn         = module.secrets.kms_key_arn
  search_domain_name  = local.search_domain_name
}

# ---- ECS (owns the security groups the data stores reference) -------------
module "ecs" {
  source             = "./modules/ecs"
  name               = local.name
  container_image    = var.container_image
  task_cpu           = var.task_cpu
  task_memory        = var.task_memory
  run_command        = var.run_command
  execution_role_arn = module.iam.execution_role_arn
  task_role_arn      = module.iam.task_role_arn
  log_group_name     = module.cloudwatch.log_group_name
  region             = var.region
  environment        = var.environment
  raw_bucket         = module.s3.raw_bucket_name
  releases_bucket    = module.s3.releases_bucket_name
  # OpenSearch endpoint is injected after it exists; see note below.
  search_endpoint = module.opensearch.endpoint
  db_secret_arn   = module.secrets.db_secret_arn
}

# ---- RDS (needs KMS, subnets, the task SG, and the DB secret) -------------
module "rds" {
  source                = "./modules/rds"
  name                  = local.name
  db_name               = var.db_name
  db_username           = var.db_username
  db_password           = module.secrets.db_password
  engine_version        = var.db_engine_version
  instance_class        = var.db_instance_class
  allocated_storage     = var.db_allocated_storage
  multi_az              = var.db_multi_az
  backup_retention_days = var.db_backup_retention_days
  deletion_protection   = var.db_deletion_protection
  kms_key_arn           = module.secrets.kms_key_arn
  subnet_ids            = module.vpc.private_subnet_ids
  security_group_id     = module.vpc.db_security_group_id
  db_secret_id          = module.secrets.db_secret_id
}

# ---- OpenSearch (needs KMS, subnets, search SG, and task role ARN) --------
module "opensearch" {
  source            = "./modules/opensearch"
  name              = local.name
  engine_version    = var.opensearch_engine_version
  instance_type     = var.opensearch_instance_type
  instance_count    = var.opensearch_instance_count
  zone_awareness    = var.opensearch_zone_awareness
  volume_size       = var.opensearch_volume_size
  kms_key_arn       = module.secrets.kms_key_arn
  subnet_ids        = module.vpc.private_subnet_ids
  security_group_id = module.vpc.search_security_group_id
  task_role_arn     = module.iam.task_role_arn
}

# ---- EventBridge schedule (needs the cluster, task def, subnets, SG, role) -
module "eventbridge" {
  source              = "./modules/eventbridge"
  name                = local.name
  schedule_expression = var.schedule_expression
  schedule_enabled    = var.schedule_enabled
  cluster_arn         = module.ecs.cluster_arn
  task_definition_arn = module.ecs.task_definition_arn
  subnet_ids          = module.vpc.private_subnet_ids
  security_group_id   = module.vpc.task_security_group_id
  events_role_name    = module.iam.events_role_name
  events_role_arn     = module.iam.events_role_arn
  execution_role_arn  = module.iam.execution_role_arn
  task_role_arn       = module.iam.task_role_arn
}
