# ECS Fargate: the cluster + task definition that run the bellu-intel CLI, plus
# the ECR repo the image is pushed to. Security groups live in the vpc module to
# keep the module graph acyclic. The DB DSN fields are injected as container
# secrets from Secrets Manager, so no credential appears in the task definition.

# ---- ECR ------------------------------------------------------------------
resource "aws_ecr_repository" "this" {
  name                 = var.name
  image_tag_mutability = "MUTABLE"
  image_scanning_configuration { scan_on_push = true }
  encryption_configuration { encryption_type = "AES256" }
}

resource "aws_ecr_lifecycle_policy" "this" {
  repository = aws_ecr_repository.this.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the 10 most recent images"
      selection    = { tagStatus = "any", countType = "imageCountMoreThan", countNumber = 10 }
      action       = { type = "expire" }
    }]
  })
}

# ---- cluster + task definition -------------------------------------------
resource "aws_ecs_cluster" "this" {
  name = var.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

# Resolve the image: explicit var wins; otherwise the stack's own ECR :latest.
locals {
  image = var.container_image != "" ? var.container_image : "${aws_ecr_repository.this.repository_url}:latest"
}

resource "aws_ecs_task_definition" "intel" {
  family                   = "${var.name}-intel"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = tostring(var.task_cpu)
  memory                   = tostring(var.task_memory)
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.task_role_arn

  runtime_platform {
    cpu_architecture        = "X86_64"
    operating_system_family = "LINUX"
  }

  container_definitions = jsonencode([{
    name      = "bellu-intel"
    image     = local.image
    essential = true
    command   = var.run_command
    environment = [
      { name = "BELLU_RAW_BUCKET", value = var.raw_bucket },
      { name = "BELLU_RELEASES_BUCKET", value = var.releases_bucket },
      { name = "BELLU_SEARCH_ENDPOINT", value = var.search_endpoint },
      { name = "BELLU_ENVIRONMENT", value = var.environment },
      { name = "AWS_REGION", value = var.region },
    ]
    # DB connection fields pulled from the Secrets Manager JSON — never in
    # plaintext in the task definition.
    secrets = [
      { name = "BELLU_DB_HOST", valueFrom = "${var.db_secret_arn}:host::" },
      { name = "BELLU_DB_PORT", valueFrom = "${var.db_secret_arn}:port::" },
      { name = "BELLU_DB_NAME", valueFrom = "${var.db_secret_arn}:dbname::" },
      { name = "BELLU_DB_USER", valueFrom = "${var.db_secret_arn}:username::" },
      { name = "BELLU_DB_PASSWORD", valueFrom = "${var.db_secret_arn}:password::" },
      { name = "BELLU_POSTGRES_DSN", valueFrom = "${var.db_secret_arn}:dsn::" },
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = var.log_group_name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "intel"
      }
    }
  }])
}
