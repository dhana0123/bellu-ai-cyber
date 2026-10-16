# Internet gateway only — no NAT gateway (Requirement 13.4), which keeps the
# scenario free of hourly NAT charges.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "vpc_id" { type = string }
variable "name" {
  type    = string
  default = "bellu-lab"
}
variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_internet_gateway" "this" {
  vpc_id = var.vpc_id
  tags   = merge({ "bellu-lab" = "true", "Name" = var.name }, var.tags)
}

output "igw_id" {
  value = aws_internet_gateway.this.id
}

output "created_resource_ids" {
  value = [aws_internet_gateway.this.id]
}
