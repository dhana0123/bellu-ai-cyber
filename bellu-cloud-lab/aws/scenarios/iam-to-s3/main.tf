# Scenario: aws/iam-to-s3 (Requirement 5).
#
# A focused IAM-to-S3 fixture: an IAM role with an attached S3-read policy plus
# the bucket it targets. Validates the approximate IAM evaluator's permission
# edge (Identity -> DataStore) end-to-end.
terraform {
  required_version = ">= 1.5"
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 5.0" }
    random = { source = "hashicorp/random", version = "~> 3.0" }
  }
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "name_suffix" {
  type    = string
  default = ""
}

provider "aws" {
  region = var.region
  default_tags {
    tags = { "bellu-lab" = "true", "scenario" = "aws/iam-to-s3" }
  }
}

resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix      = var.name_suffix != "" ? var.name_suffix : random_id.suffix.hex
  bucket_name = "bellu-lab-iam-to-s3-${local.suffix}"
}

module "bucket" {
  source      = "../../modules/s3"
  bucket_name = local.bucket_name
}

module "role" {
  source         = "../../modules/iam"
  role_name      = "lab-reader-${local.suffix}"
  assume_service = "ec2.amazonaws.com"
  inline_policies = {
    s3-read = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:ListBucket"]
        Resource = [module.bucket.bucket_arn, "${module.bucket.bucket_arn}/*"]
      }]
    })
  }
}

output "created_resource_ids" {
  value = concat(
    module.bucket.created_resource_ids,
    module.role.created_resource_ids,
  )
}
