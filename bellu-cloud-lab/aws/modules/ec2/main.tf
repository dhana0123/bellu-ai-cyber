# EC2 instance, defaulting to the smallest practical type (t3.micro). Uses the
# latest Amazon Linux 2023 AMI so no AMI id needs hardcoding.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "subnet_id" { type = string }
variable "security_group_ids" { type = list(string) }
variable "instance_profile" {
  type    = string
  default = ""
}
variable "instance_type" {
  type    = string
  default = "t3.micro"
}
variable "name" {
  type    = string
  default = "bellu-lab-ec2"
}
variable "tags" {
  type    = map(string)
  default = {}
}

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }
  filter {
    name   = "architecture"
    values = ["x86_64"]
  }
}

resource "aws_instance" "this" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.instance_type
  subnet_id              = var.subnet_id
  vpc_security_group_ids = var.security_group_ids
  iam_instance_profile   = var.instance_profile != "" ? var.instance_profile : null
  tags                   = merge({ "bellu-lab" = "true", "Name" = var.name }, var.tags)
}

output "instance_id" {
  value = aws_instance.this.id
}

output "instance_arn" {
  value = aws_instance.this.arn
}

output "created_resource_ids" {
  value = [aws_instance.this.id]
}
