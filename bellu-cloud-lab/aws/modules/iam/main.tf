# IAM module: an assumable role with optional inline policies and an optional
# instance profile (for EC2 attachment). Emits the identifiers aws.go keys on.
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

variable "role_name" { type = string }

# Service principal that may assume the role (ec2 by default).
variable "assume_service" {
  type    = string
  default = "ec2.amazonaws.com"
}

# Inline policies: map of policy-name => JSON policy document string.
variable "inline_policies" {
  type    = map(string)
  default = {}
}

# When true, also create an instance profile wrapping the role.
variable "instance_profile" {
  type    = bool
  default = false
}

variable "tags" {
  type    = map(string)
  default = {}
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = [var.assume_service]
    }
  }
}

resource "aws_iam_role" "this" {
  name               = var.role_name
  assume_role_policy = data.aws_iam_policy_document.assume.json
  tags               = merge({ "bellu-lab" = "true" }, var.tags)
}

resource "aws_iam_role_policy" "inline" {
  for_each = var.inline_policies
  name     = each.key
  role     = aws_iam_role.this.id
  policy   = each.value
}

resource "aws_iam_instance_profile" "this" {
  count = var.instance_profile ? 1 : 0
  name  = "${var.role_name}-profile"
  role  = aws_iam_role.this.name
  tags  = merge({ "bellu-lab" = "true" }, var.tags)
}

output "role_arn" {
  value = aws_iam_role.this.arn
}

output "role_name" {
  value = aws_iam_role.this.name
}

output "instance_profile_name" {
  value = var.instance_profile ? aws_iam_instance_profile.this[0].name : ""
}

output "created_resource_ids" {
  value = compact(concat(
    [aws_iam_role.this.arn],
    var.instance_profile ? [aws_iam_instance_profile.this[0].arn] : [],
  ))
}
