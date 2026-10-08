# CloudWatch Logs for the Fargate task. Uses CloudWatch's default server-side
# encryption; a customer CMK would require granting the logs service principal
# key access, which would couple this module to the KMS key policy — avoided
# deliberately so the log group stands alone.
resource "aws_cloudwatch_log_group" "task" {
  name              = "/ecs/${var.name}"
  retention_in_days = var.retention_days
  tags              = { Name = "${var.name}-logs" }
}
