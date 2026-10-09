package setup

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// StackName is the CloudFormation stack name used in launch links.
const StackName = "BelluConnector"

// renderCloudFormation produces a single-account YAML template that creates the
// Bellu IAM role with its trust policy and an inline least-privilege permission
// policy. The template embeds the authoritative policies the backend generated.
// The one-click launch link is added by the provider once the template is
// published to S3 (CloudFormation can only pre-load templates hosted in S3).
func renderCloudFormation(in Input) (*Artifact, error) {
	tmpl := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              fmt.Sprintf("Bellu Cloud Connector cross-account role (external id %s)", in.ExternalID),
		"Resources": map[string]any{
			"BelluConnectorRole": roleResource(in.RoleName, in.TrustPolicy, in.PermissionPolicy, nil),
		},
		"Outputs": roleARNOutput("ARN of the Bellu connector role (paste back into Bellu)"),
	}
	body, err := yaml.Marshal(tmpl)
	if err != nil {
		return nil, err
	}
	return &Artifact{
		Method:         "cloudformation",
		Template:       string(body),
		TemplateFormat: "yaml",
		Instructions: []string{
			"Click Launch CloudFormation (or upload the template under CloudFormation → Create stack).",
			"Tick \"I acknowledge that AWS CloudFormation might create IAM resources with custom names\".",
			"Click Create stack and wait for CREATE_COMPLETE.",
			"Copy the RoleArn value from the stack's Outputs tab and paste it into Bellu to verify.",
		},
	}, nil
}

// roleResource builds an AWS::IAM::Role resource with the trust policy, the
// least-privilege permission policy, and any extra inline policies.
func roleResource(name string, trust, perm map[string]any, extra []map[string]any) map[string]any {
	policies := []map[string]any{{
		"PolicyName": "BelluConnectorPolicy",
		"PolicyDocument": map[string]any{
			"Version":   "2012-10-17",
			"Statement": permStatements(perm),
		},
	}}
	policies = append(policies, extra...)
	return map[string]any{
		"Type": "AWS::IAM::Role",
		"Properties": map[string]any{
			"RoleName":                 name,
			"AssumeRolePolicyDocument": trust,
			"Policies":                 policies,
		},
	}
}

func roleARNOutput(desc string) map[string]any {
	return map[string]any{
		"RoleArn": map[string]any{
			"Description": desc,
			"Value":       map[string]any{"Fn::GetAtt": []string{"BelluConnectorRole", "Arn"}},
		},
	}
}

// permStatements extracts the Statement list from a permission policy document.
func permStatements(policy map[string]any) any {
	if s, ok := policy["Statement"]; ok {
		return s
	}
	return []any{}
}

// compactJSON marshals v without indentation (used for nested template bodies).
func compactJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
