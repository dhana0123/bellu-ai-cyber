# ---------------------------------------------------------------------------
# Core identity: environment + region drive naming, state keys, and sizing.
# ---------------------------------------------------------------------------
variable "environment" {
  type        = string
  description = "Deployment environment (dev | staging | prod)."
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

variable "region" {
  type        = string
  description = "AWS region to deploy into (e.g. us-east-1, ap-south-1)."
}

variable "name_prefix" {
  type        = string
  default     = "bellu-global-intel"
  description = "Prefix for all resource names; combined with environment."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Extra tags merged into the provider default_tags."
}

# ---------------------------------------------------------------------------
# Networking
# ---------------------------------------------------------------------------
variable "vpc_cidr" {
  type        = string
  default     = "10.42.0.0/16"
  description = "CIDR block for the stack VPC."
}

variable "az_count" {
  type        = number
  default     = 2
  description = "Number of AZs to spread subnets across (>=2 for prod HA)."
  validation {
    condition     = var.az_count >= 1 && var.az_count <= 3
    error_message = "az_count must be between 1 and 3."
  }
}

variable "single_nat_gateway" {
  type        = bool
  default     = true
  description = "true = one shared NAT GW (cost). false = one NAT GW per AZ (HA)."
}

# ---------------------------------------------------------------------------
# Database (RDS PostgreSQL)
# ---------------------------------------------------------------------------
variable "db_name" {
  type        = string
  default     = "bellu"
  description = "Initial PostgreSQL database name."
}

variable "db_username" {
  type        = string
  default     = "bellu"
  description = "PostgreSQL master username (password generated into Secrets Manager)."
}

variable "db_engine_version" {
  type        = string
  default     = "16.4"
  description = "PostgreSQL engine version."
}

variable "db_instance_class" {
  type        = string
  default     = "db.t3.micro"
  description = "RDS instance class (sized per environment)."
}

variable "db_allocated_storage" {
  type        = number
  default     = 20
  description = "RDS allocated storage in GB."
}

variable "db_multi_az" {
  type        = bool
  default     = false
  description = "Run RDS Multi-AZ (prod)."
}

variable "db_backup_retention_days" {
  type        = number
  default     = 7
  description = "Automated backup retention in days."
}

variable "db_deletion_protection" {
  type        = bool
  default     = false
  description = "Protect the RDS instance from deletion (prod)."
}

# ---------------------------------------------------------------------------
# Search (Amazon OpenSearch Service)
# ---------------------------------------------------------------------------
variable "opensearch_engine_version" {
  type        = string
  default     = "OpenSearch_2.13"
  description = "OpenSearch engine version."
}

variable "opensearch_instance_type" {
  type        = string
  default     = "t3.small.search"
  description = "OpenSearch data node instance type."
}

variable "opensearch_instance_count" {
  type        = number
  default     = 1
  description = "OpenSearch data node count (>=2 with zone awareness for HA)."
}

variable "opensearch_zone_awareness" {
  type        = bool
  default     = false
  description = "Enable OpenSearch multi-AZ zone awareness (prod)."
}

variable "opensearch_volume_size" {
  type        = number
  default     = 20
  description = "Per-node EBS volume size in GB."
}

# ---------------------------------------------------------------------------
# Compute (ECS Fargate) + scheduling
# ---------------------------------------------------------------------------
variable "container_image" {
  type        = string
  default     = ""
  description = "bellu-intel image URI (repo:tag). Empty = use the created ECR repo :latest."
}

variable "task_cpu" {
  type        = number
  default     = 512
  description = "Fargate task CPU units."
}

variable "task_memory" {
  type        = number
  default     = 1024
  description = "Fargate task memory (MiB)."
}

variable "run_command" {
  type        = list(string)
  default     = ["run", "--mode", "daily", "--raw-dir", "/app/intel-raw"]
  description = "bellu-intel CLI args the scheduled task runs."
}

variable "schedule_expression" {
  type        = string
  default     = "cron(0 6 * * ? *)"
  description = "EventBridge schedule for the daily run (UTC)."
}

variable "schedule_enabled" {
  type        = bool
  default     = true
  description = "Enable the scheduled intelligence run."
}

variable "log_retention_days" {
  type        = number
  default     = 30
  description = "CloudWatch Logs retention for the task."
}
