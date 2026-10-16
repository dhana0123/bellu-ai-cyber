# Public route table with a default route to the IGW, associated to the given
# subnets.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "vpc_id" { type = string }
variable "subnet_ids" { type = list(string) }
variable "igw_id" { type = string }
variable "name" {
  type    = string
  default = "bellu-lab"
}
variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_route_table" "public" {
  vpc_id = var.vpc_id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = var.igw_id
  }
  tags = merge({ "bellu-lab" = "true", "Name" = "${var.name}-public" }, var.tags)
}

resource "aws_route_table_association" "public" {
  count          = length(var.subnet_ids)
  subnet_id      = var.subnet_ids[count.index]
  route_table_id = aws_route_table.public.id
}

output "route_table_ids" {
  value = [aws_route_table.public.id]
}

output "created_resource_ids" {
  value = [aws_route_table.public.id]
}
