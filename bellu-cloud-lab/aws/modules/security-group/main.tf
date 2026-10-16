# Security group. Supports ingress open to 0.0.0.0/0 so a scenario can produce
# the sgOpenToWorld NetworkEndpoint + EXPOSED_TO edge that aws.go emits.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "vpc_id" { type = string }
variable "name" {
  type    = string
  default = "bellu-lab-sg"
}

# Each ingress rule: { from_port, to_port, protocol, cidr }.
variable "ingress_rules" {
  type = list(object({
    from_port = number
    to_port   = number
    protocol  = string
    cidr      = string
  }))
  default = []
}

variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_security_group" "this" {
  name_prefix = "${var.name}-"
  vpc_id      = var.vpc_id
  tags        = merge({ "bellu-lab" = "true", "Name" = var.name }, var.tags)

  dynamic "ingress" {
    for_each = var.ingress_rules
    content {
      from_port   = ingress.value.from_port
      to_port     = ingress.value.to_port
      protocol    = ingress.value.protocol
      cidr_blocks = [ingress.value.cidr]
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

output "security_group_id" {
  value = aws_security_group.this.id
}

output "created_resource_ids" {
  value = [aws_security_group.this.id]
}
