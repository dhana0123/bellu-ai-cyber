# RDS PostgreSQL: the canonical system of record. Private subnets only, encrypted
# with the stack CMK, Multi-AZ + deletion protection in prod. The connection
# endpoint is written back into the DB secret so the pipeline resolves a single
# secret for the whole DSN.
resource "aws_db_subnet_group" "this" {
  name       = "${var.name}-db"
  subnet_ids = var.subnet_ids
  tags       = { Name = "${var.name}-db-subnet-group" }
}

resource "aws_db_instance" "this" {
  identifier     = "${var.name}-pg"
  engine         = "postgres"
  engine_version = var.engine_version
  db_name        = var.db_name
  username       = var.db_username
  password       = var.db_password

  instance_class         = var.instance_class
  allocated_storage      = var.allocated_storage
  storage_type           = "gp3"
  storage_encrypted      = true
  kms_key_id             = var.kms_key_arn
  multi_az               = var.multi_az
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [var.security_group_id]
  publicly_accessible    = false

  backup_retention_period      = var.backup_retention_days
  deletion_protection          = var.deletion_protection
  performance_insights_enabled = var.multi_az
  copy_tags_to_snapshot        = true
  auto_minor_version_upgrade   = true
  apply_immediately            = false

  # On teardown, snapshot prod and skip for ephemeral dev.
  skip_final_snapshot       = !var.deletion_protection
  final_snapshot_identifier = var.deletion_protection ? "${var.name}-pg-final" : null

  tags = { Name = "${var.name}-pg" }
}

# Write the full connection detail into the DB secret so global-intel resolves
# host/port/dbname/username/password from one place.
resource "aws_secretsmanager_secret_version" "db_conn" {
  secret_id = var.db_secret_id
  secret_string = jsonencode({
    username = var.db_username
    password = var.db_password
    host     = aws_db_instance.this.address
    port     = aws_db_instance.this.port
    dbname   = var.db_name
    dsn      = "postgres://${var.db_username}:${var.db_password}@${aws_db_instance.this.address}:${aws_db_instance.this.port}/${var.db_name}?sslmode=require"
  })
}
