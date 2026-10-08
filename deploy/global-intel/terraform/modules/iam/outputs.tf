output "execution_role_arn" { value = aws_iam_role.execution.arn }
output "task_role_arn" { value = aws_iam_role.task.arn }
output "task_role_name" { value = aws_iam_role.task.name }
output "events_role_name" { value = aws_iam_role.events.name }
output "events_role_arn" { value = aws_iam_role.events.arn }
