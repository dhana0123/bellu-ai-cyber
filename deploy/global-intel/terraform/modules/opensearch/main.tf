# Amazon OpenSearch Service: the fast search/index layer. PostgreSQL stays
# authoritative; this index is rebuildable from it. VPC-internal, encrypted at
# rest + node-to-node, HTTPS enforced, and reachable only from the task SG. The
# access policy grants the pipeline task role es:ESHttp*.
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}
data "aws_partition" "current" {}

locals {
  domain_name = "${var.name}-search"
  domain_arn  = "arn:${data.aws_partition.current.partition}:es:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:domain/${local.domain_name}"
  # Zone awareness needs >=2 subnets; otherwise pin to the first subnet.
  subnet_ids = var.zone_awareness ? slice(var.subnet_ids, 0, 2) : slice(var.subnet_ids, 0, 1)
}

resource "aws_opensearch_domain" "this" {
  domain_name    = local.domain_name
  engine_version = var.engine_version

  cluster_config {
    instance_type          = var.instance_type
    instance_count         = var.instance_count
    zone_awareness_enabled = var.zone_awareness

    dynamic "zone_awareness_config" {
      for_each = var.zone_awareness ? [1] : []
      content { availability_zone_count = 2 }
    }
  }

  ebs_options {
    ebs_enabled = true
    volume_type = "gp3"
    volume_size = var.volume_size
  }

  encrypt_at_rest {
    enabled    = true
    kms_key_id = var.kms_key_arn
  }

  node_to_node_encryption { enabled = true }

  domain_endpoint_options {
    enforce_https       = true
    tls_security_policy = "Policy-Min-TLS-1-2-2019-07"
  }

  vpc_options {
    subnet_ids         = local.subnet_ids
    security_group_ids = [var.security_group_id]
  }

  access_policies = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = var.task_role_arn }
      Action    = "es:ESHttp*"
      Resource  = "${local.domain_arn}/*"
    }]
  })

  tags = { Name = local.domain_name }
}
