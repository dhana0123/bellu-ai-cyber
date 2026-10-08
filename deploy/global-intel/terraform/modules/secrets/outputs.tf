output "kms_key_arn" { value = aws_kms_key.data.arn }
output "db_secret_arn" { value = aws_secretsmanager_secret.db.arn }
output "db_secret_id" { value = aws_secretsmanager_secret.db.id }

output "db_password" {
  value     = random_password.db.result
  sensitive = true
}
