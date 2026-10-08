output "vpc_id" { value = aws_vpc.this.id }
output "private_subnet_ids" { value = aws_subnet.private[*].id }
output "public_subnet_ids" { value = aws_subnet.public[*].id }
output "task_security_group_id" { value = aws_security_group.task.id }
output "db_security_group_id" { value = aws_security_group.database.id }
output "search_security_group_id" { value = aws_security_group.search.id }
