# EventBridge schedule: the only trigger for the pipeline. It RunTask's the
# Fargate task on schedule_expression. Infra schedules; global-intel fetches,
# normalizes, stores, indexes, and releases.

# Grant the (iam-module-owned) events role permission to run exactly this task.
resource "aws_iam_role_policy" "run_task" {
  name = "run-task"
  role = var.events_role_name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Action    = ["ecs:RunTask"]
        Resource  = var.task_definition_arn
        Condition = { ArnEquals = { "ecs:cluster" = var.cluster_arn } }
      },
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = [var.execution_role_arn, var.task_role_arn]
      }
    ]
  })
}

resource "aws_cloudwatch_event_rule" "daily_run" {
  name                = "${var.name}-daily-run"
  description         = "Scheduled global-intel full-schedule intelligence run"
  schedule_expression = var.schedule_expression
  state               = var.schedule_enabled ? "ENABLED" : "DISABLED"
}

resource "aws_cloudwatch_event_target" "fargate" {
  rule     = aws_cloudwatch_event_rule.daily_run.name
  arn      = var.cluster_arn
  role_arn = var.events_role_arn

  ecs_target {
    task_definition_arn = var.task_definition_arn
    task_count          = 1
    launch_type         = "FARGATE"
    platform_version    = "LATEST"

    network_configuration {
      assign_public_ip = false
      subnets          = var.subnet_ids
      security_groups  = [var.security_group_id]
    }
  }
}
