# Scenario: aws/public-ec2 (Requirement 4).
#
# A public EC2 instance with an instance-profile IAM role that can read an S3
# bucket, behind a security group open to 0.0.0.0/0. Exercises Compute,
# Identity, DataStore, and NetworkEndpoint collection end-to-end plus the
# USES / CAN_READ / EXPOSED_TO edges aws.go emits.
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

# A short random suffix keeps the globally-unique S3 bucket name collision-free.
variable "name_suffix" {
  type    = string
  default = ""
}

provider "aws" {
  region = var.region
  default_tags {
    tags = { "bellu-lab" = "true", "scenario" = "aws/public-ec2" }
  }
}

resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix      = var.name_suffix != "" ? var.name_suffix : random_id.suffix.hex
  bucket_name = "bellu-lab-public-ec2-${local.suffix}"
}

module "vpc" {
  source = "../../modules/vpc"
  name   = "public-ec2"
}

module "subnets" {
  source       = "../../modules/subnets"
  vpc_id       = module.vpc.vpc_id
  public_cidrs = ["10.42.1.0/24"]
  name         = "public-ec2"
}

module "igw" {
  source = "../../modules/igw"
  vpc_id = module.vpc.vpc_id
  name   = "public-ec2"
}

module "routes" {
  source     = "../../modules/route-tables"
  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.subnets.public_subnet_ids
  igw_id     = module.igw.igw_id
  name       = "public-ec2"
}

# Open to the world (0.0.0.0/0) so sgOpenToWorld fires in the normalizer.
module "sg" {
  source = "../../modules/security-group"
  vpc_id = module.vpc.vpc_id
  name   = "public-ec2"
  ingress_rules = [
    { from_port = 22, to_port = 22, protocol = "tcp", cidr = "0.0.0.0/0" },
    { from_port = 80, to_port = 80, protocol = "tcp", cidr = "0.0.0.0/0" },
  ]
}

module "bucket" {
  source      = "../../modules/s3"
  bucket_name = local.bucket_name
}

# Role the EC2 instance assumes via its instance profile; grants S3 read on the
# scenario bucket so the IAM evaluator derives a CAN_READ edge.
module "role" {
  source           = "../../modules/iam"
  role_name        = "public-ec2-role-${local.suffix}"
  assume_service   = "ec2.amazonaws.com"
  instance_profile = true
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

module "ec2" {
  source             = "../../modules/ec2"
  subnet_id          = module.subnets.public_subnet_ids[0]
  security_group_ids = [module.sg.security_group_id]
  instance_profile   = module.role.instance_profile_name
  name               = "public-ec2"
}

output "created_resource_ids" {
  value = concat(
    module.vpc.created_resource_ids,
    module.subnets.created_resource_ids,
    module.igw.created_resource_ids,
    module.routes.created_resource_ids,
    module.sg.created_resource_ids,
    module.bucket.created_resource_ids,
    module.role.created_resource_ids,
    module.ec2.created_resource_ids,
  )
}
