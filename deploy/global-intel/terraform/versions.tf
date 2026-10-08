# Terraform + provider version constraints for the bellu-global-intel stack.
#
# The S3 backend is configured partially here and completed per environment via
# `-backend-config=environments/<env>/<region>.backend.hcl` so the same root
# module targets dev/staging/prod and any region without edits. State locking
# uses S3-native conditional writes (use_lockfile) — no DynamoDB table.
terraform {
  required_version = ">= 1.11.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.70"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  backend "s3" {
    # Supplied per env+region via -backend-config:
    #   bucket, key, region, use_lockfile = true, encrypt = true
    use_lockfile = true
    encrypt      = true
  }
}
