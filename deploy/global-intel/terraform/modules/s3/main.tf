# Private, encrypted, versioned S3 storage. The raw bucket is immutable
# (Object Lock GOVERNANCE) for content-addressed upstream payloads; the
# releases bucket holds signed .tar.zst artifacts. Both deny non-TLS access and
# block all public access.
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  raw_bucket      = "${var.name}-raw-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.name}"
  releases_bucket = "${var.name}-releases-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.name}"
}

# ---- raw (immutable) ------------------------------------------------------
resource "aws_s3_bucket" "raw" {
  bucket              = local.raw_bucket
  object_lock_enabled = true
  tags                = { Name = "${var.name}-raw" }
}

resource "aws_s3_bucket_versioning" "raw" {
  bucket = aws_s3_bucket.raw.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_object_lock_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id
  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = var.raw_object_lock_days
    }
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "raw" {
  bucket                  = aws_s3_bucket.raw.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id
  rule {
    id     = "expire-noncurrent"
    status = "Enabled"
    # Apply to all objects (empty filter) — required by the current provider.
    filter {}
    noncurrent_version_expiration { noncurrent_days = 90 }
  }
}

# ---- releases -------------------------------------------------------------
resource "aws_s3_bucket" "releases" {
  bucket = local.releases_bucket
  tags   = { Name = "${var.name}-releases" }
}

resource "aws_s3_bucket_versioning" "releases" {
  bucket = aws_s3_bucket.releases.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "releases" {
  bucket = aws_s3_bucket.releases.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "releases" {
  bucket                  = aws_s3_bucket.releases.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ---- TLS-only policies ----------------------------------------------------
data "aws_iam_policy_document" "deny_insecure" {
  for_each = {
    raw      = aws_s3_bucket.raw.arn
    releases = aws_s3_bucket.releases.arn
  }
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [each.value, "${each.value}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "raw" {
  bucket = aws_s3_bucket.raw.id
  policy = data.aws_iam_policy_document.deny_insecure["raw"].json
}

resource "aws_s3_bucket_policy" "releases" {
  bucket = aws_s3_bucket.releases.id
  policy = data.aws_iam_policy_document.deny_insecure["releases"].json
}
