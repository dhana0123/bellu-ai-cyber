# Reusable VPC module. Single VPC, no extras. Least-cost by construction.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "cidr_block" {
  type    = string
  default = "10.42.0.0/16"
}

variable "name" {
  type    = string
  default = "bellu-lab"
}

variable "tags" {
  type    = map(string)
  default = {}
}

locals {
  tags = merge({ "bellu-lab" = "true", "Name" = var.name }, var.tags)
}

resource "aws_vpc" "this" {
  cidr_block           = var.cidr_block
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = local.tags
}

output "vpc_id" {
  value = aws_vpc.this.id
}

output "created_resource_ids" {
  value = [aws_vpc.this.id]
}
