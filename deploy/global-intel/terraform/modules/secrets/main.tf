# KMS CMK encrypting RDS, OpenSearch, S3, and this secret at rest, plus the RDS
# master credential secret. The password is generated here and never written to
# tfvars or state in plaintext input — only Terraform's own state holds it, so
# the state backend is encrypted (see root versions.tf).
data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

resource "aws_kms_key" "data" {
  description             = "${var.name} data-at-rest encryption key"
  enable_key_rotation     = true
  deletion_window_in_days = 14

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "EnableRoot"
      Effect    = "Allow"
      Principal = { AWS = "arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:root" }
      Action    = "kms:*"
      Resource  = "*"
    }]
  })
  tags = { Name = "${var.name}-data-key" }
}

resource "aws_kms_alias" "data" {
  name          = "alias/${var.name}-data"
  target_key_id = aws_kms_key.data.key_id
}

# Random master password (generated, not supplied).
resource "random_password" "db" {
  length           = 32
  special          = true
  override_special = "!#%*-_=+:?"
}

resource "aws_secretsmanager_secret" "db" {
  name       = "${var.name}/db/credentials"
  kms_key_id = aws_kms_key.data.arn
  tags       = { Name = "${var.name}-db-credentials" }
}

# Initial value holds username + password; the rds module augments it with the
# connection endpoint via a secret version once the instance exists.
resource "aws_secretsmanager_secret_version" "db_initial" {
  secret_id = aws_secretsmanager_secret.db.id
  secret_string = jsonencode({
    username = var.db_username
    password = random_password.db.result
  })
  lifecycle {
    # The rds module writes the authoritative version with host/port/dbname;
    # don't fight it on subsequent applies.
    ignore_changes = [secret_string]
  }
}
