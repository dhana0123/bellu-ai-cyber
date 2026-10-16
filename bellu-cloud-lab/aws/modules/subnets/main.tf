# Public/private subnets in a single AZ (least-cost). Public subnets auto-assign
# public IPs so an EC2 instance is reachable without an EIP.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "vpc_id" { type = string }
variable "public_cidrs" {
  type    = list(string)
  default = ["10.42.1.0/24"]
}
variable "private_cidrs" {
  type    = list(string)
  default = []
}
variable "az" {
  type    = string
  default = ""
}
variable "name" {
  type    = string
  default = "bellu-lab"
}
variable "tags" {
  type    = map(string)
  default = {}
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  az   = var.az != "" ? var.az : data.aws_availability_zones.available.names[0]
  tags = merge({ "bellu-lab" = "true" }, var.tags)
}

resource "aws_subnet" "public" {
  count                   = length(var.public_cidrs)
  vpc_id                  = var.vpc_id
  cidr_block              = var.public_cidrs[count.index]
  availability_zone       = local.az
  map_public_ip_on_launch = true
  tags                    = merge(local.tags, { "Name" = "${var.name}-public-${count.index}" })
}

resource "aws_subnet" "private" {
  count             = length(var.private_cidrs)
  vpc_id            = var.vpc_id
  cidr_block        = var.private_cidrs[count.index]
  availability_zone = local.az
  tags              = merge(local.tags, { "Name" = "${var.name}-private-${count.index}" })
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  value = aws_subnet.private[*].id
}

output "created_resource_ids" {
  value = concat(aws_subnet.public[*].id, aws_subnet.private[*].id)
}
