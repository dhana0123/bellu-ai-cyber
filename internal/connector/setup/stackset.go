package setup

import (
	"gopkg.in/yaml.v3"
)

// OrgTargetParameter is the template parameter where the customer enters the
// organization root id (r-xxxx) or OU ids the StackSet deploys to.
const OrgTargetParameter = "OrganizationRootOrOUId"

// orgTargetPattern matches a comma-separated list of root / OU ids.
const orgTargetPattern = `^(r-[0-9a-z]{4,32}|ou-[0-9a-z]{4,32}-[a-z0-9]{8,32})(,(r-[0-9a-z]{4,32}|ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}))*$`

// renderCloudFormationStackSet produces the organization template, launched as
// a normal stack in the management account. It creates:
//
//  1. BelluConnectorRole in the management account, with read-only
//     Organizations permissions so Bellu can list member accounts.
//  2. A service-managed AWS::CloudFormation::StackSet that deploys the same role
//     (same trust policy and ExternalId) into every member account under the
//     chosen root/OU, and automatically into accounts added later.
//
// Service-managed StackSets do not deploy to the management account itself,
// which is why the management role is a separate resource.
func renderCloudFormationStackSet(in Input) (*Artifact, error) {
	member := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              "Bellu Cloud Connector member-account role",
		"Parameters":               map[string]any{ExternalIdParameter: externalIdParamDef()},
		"Resources": map[string]any{
			"BelluConnectorRole": roleResource(in.RoleName, templateTrustPolicy(in.TrustPolicy), in.PermissionPolicy, nil),
		},
		"Outputs": roleARNOutput("ARN of the Bellu connector role in this member account"),
	}
	memberBody, err := compactJSON(member)
	if err != nil {
		return nil, err
	}

	orgRead := []map[string]any{{
		"PolicyName": "BelluOrganizationsReadOnly",
		"PolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":   "Allow",
				"Action":   []string{"organizations:ListAccounts", "organizations:DescribeOrganization"},
				"Resource": "*",
			}},
		},
	}}

	param := map[string]any{
		"Type":                  "String",
		"Description":           "Organization root ID (r-xxxx) or OU IDs (ou-xxxx-xxxxxxxx), comma-separated. Find it in AWS Organizations → AWS accounts.",
		"AllowedPattern":        orgTargetPattern,
		"ConstraintDescription": "Must be r-xxxx or ou-xxxx-xxxxxxxx, comma-separated.",
	}
	if in.OrganizationalUnitID != "" {
		param["Default"] = in.OrganizationalUnitID
	}

	tmpl := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              "Bellu Cloud Connector for an AWS Organization",
		"Parameters": map[string]any{
			OrgTargetParameter:  param,
			ExternalIdParameter: externalIdParamDef(),
		},
		"Resources": map[string]any{
			"BelluConnectorRole": roleResource(in.RoleName, templateTrustPolicy(in.TrustPolicy), in.PermissionPolicy, orgRead),
			"BelluMemberRoles": map[string]any{
				"Type": "AWS::CloudFormation::StackSet",
				"Properties": map[string]any{
					"StackSetName":    "BelluConnectorMembers",
					"Description":     "Deploys the Bellu connector role into every member account",
					"PermissionModel": "SERVICE_MANAGED",
					"AutoDeployment": map[string]any{
						"Enabled":                      true,
						"RetainStacksOnAccountRemoval": false,
					},
					"ManagedExecution": map[string]any{"Active": true},
					"Capabilities":     []string{"CAPABILITY_NAMED_IAM"},
					"OperationPreferences": map[string]any{
						"FailureTolerancePercentage": 100,
						"MaxConcurrentPercentage":    100,
					},
					// Forward the external id into every member stack instance so
					// the member role trusts the same id.
					"Parameters": []map[string]any{{
						"ParameterKey":   ExternalIdParameter,
						"ParameterValue": map[string]any{"Ref": ExternalIdParameter},
					}},
					"StackInstancesGroup": []map[string]any{{
						"DeploymentTargets": map[string]any{
							"OrganizationalUnitIds": map[string]any{
								"Fn::Split": []any{",", map[string]any{"Ref": OrgTargetParameter}},
							},
						},
						"Regions": []any{map[string]any{"Ref": "AWS::Region"}},
					}},
					"TemplateBody": memberBody,
				},
			},
		},
		"Outputs": roleARNOutput("ARN of the Bellu connector role in the management account (paste back into Bellu)"),
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
			"Sign in to your AWS Organization management account.",
			"One time: in AWS Organizations → Services → CloudFormation StackSets, enable trusted access.",
			"Click Launch CloudFormation, enter your organization root ID (r-xxxx) or OU IDs in " + OrgTargetParameter + ".",
			"Tick \"I acknowledge that AWS CloudFormation might create IAM resources with custom names\", then Create stack.",
			"When the stack is CREATE_COMPLETE, copy RoleArn from the Outputs tab and paste it into Bellu. Bellu finds your member accounts automatically.",
		},
	}, nil
}
