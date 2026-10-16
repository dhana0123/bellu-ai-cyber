# S3 bucket for synthetic, non-sensitive data. No versioning or replication
# (least-cost). force_destroy lets `terraform destroy` remove a non-empty bucket
# so teardown never gets stuck.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "bucket_name" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_s3_bucket" "this" {
  bucket        = var.bucket_name
  force_destroy = true
  tags          = merge({ "bellu-lab" = "true" }, var.tags)
}

# A single synthetic object so the bucket is non-empty and clearly test data.
resource "aws_s3_object" "readme" {
  bucket  = aws_s3_bucket.this.id
  key     = "BELLU-LAB-SYNTHETIC.txt"
  content = "Synthetic non-sensitive data provisioned by bellu-cloud-lab. Safe to delete."
}

output "bucket_name" {
  value = aws_s3_bucket.this.id
}

output "bucket_arn" {
  value = aws_s3_bucket.this.arn
}

output "created_resource_ids" {
  value = [aws_s3_bucket.this.arn]
}
