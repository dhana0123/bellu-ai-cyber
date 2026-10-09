package setup

import "fmt"

// renderTerraform produces main.tf and variables.tf that create the Bellu IAM
// role with its trust and least-privilege permission policies. The policy JSON
// is embedded verbatim from the backend-generated documents.
func renderTerraform(in Input) (*Artifact, error) {
	trustJSON, err := jsonIndent(in.TrustPolicy)
	if err != nil {
		return nil, err
	}
	permJSON, err := jsonIndent(in.PermissionPolicy)
	if err != nil {
		return nil, err
	}

	mainTF := fmt.Sprintf(`terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = var.region
}

# Bellu Cloud Connector cross-account role. The trust policy conditions
# sts:AssumeRole on the per-connection external id; the permission policy is
# least-privilege and read-only.
resource "aws_iam_role" "bellu_connector" {
  name               = var.role_name
  assume_role_policy = <<-TRUST
%s
  TRUST
}

resource "aws_iam_role_policy" "bellu_connector" {
  name   = "BelluConnectorPolicy"
  role   = aws_iam_role.bellu_connector.id
  policy = <<-PERM
%s
  PERM
}

output "role_arn" {
  description = "ARN of the Bellu connector role (paste back into Bellu)"
  value       = aws_iam_role.bellu_connector.arn
}
`, trustJSON, permJSON)

	varsTF := fmt.Sprintf(`variable "region" {
  type    = string
  default = %q
}

variable "role_name" {
  type    = string
  default = %q
}
`, defaultRegion(in.Region), in.RoleName)

	return &Artifact{
		Method: "terraform",
		Files: []File{
			{Name: "main.tf", Content: mainTF},
			{Name: "variables.tf", Content: varsTF},
		},
		Instructions: []string{
			"Download the Terraform files into a new directory.",
			"Run `terraform init`.",
			"Run `terraform apply` and review the plan.",
			"Copy the role_arn output and return to Bellu to verify.",
		},
	}, nil
}

func defaultRegion(r string) string {
	if r == "" {
		return "us-east-1"
	}
	return r
}
