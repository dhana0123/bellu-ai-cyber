package setup

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// StackName is the CloudFormation stack name used in launch links.
const StackName = "BelluConnector"

// ExternalIdParameter is the CloudFormation parameter that carries the
// per-connection external id. Making it a parameter (rather than embedding the
// value) keeps the template BODY identical across connections, so S3 stores a
// single reusable object; the unique value rides in the quick-create URL
// (param_ExternalId=...) and auto-fills on the launch page.
const ExternalIdParameter = "ExternalId"

// externalIdParamDef is the parameter definition injected into every
// CloudFormation template. NoEcho masks the shared-secret value in the console.
func externalIdParamDef() map[string]any {
	return map[string]any{
		"Type":        "String",
		"NoEcho":      true,
		"Description": "Bellu-provided external id (auto-filled from the Bellu launch link; conditions the role's AssumeRole trust).",
		"MinLength":   1,
	}
}

// templateTrustPolicy rewrites the backend trust policy so the sts:ExternalId
// condition references the ExternalId parameter ({"Ref":"ExternalId"}) instead
// of the literal value. This is what makes the template body connection-
// independent. The literal trust policy is still used elsewhere (Terraform,
// manual, Bellu's own AssumeRole), so only the CloudFormation body is affected.
func templateTrustPolicy(trust map[string]any) map[string]any {
	// Deep-ish copy: we only need to replace the sts:ExternalId leaf, so walk the
	// known shape and swap it, leaving everything else intact.
	out := map[string]any{"Version": trust["Version"]}
	stmts, _ := trust["Statement"].([]map[string]any)
	newStmts := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		ns := map[string]any{}
		for k, v := range s {
			ns[k] = v
		}
		if cond, ok := ns["Condition"].(map[string]any); ok {
			if se, ok := cond["StringEquals"].(map[string]any); ok {
				if _, has := se["sts:ExternalId"]; has {
					newSE := map[string]any{}
					for k, v := range se {
						newSE[k] = v
					}
					newSE["sts:ExternalId"] = map[string]any{"Ref": ExternalIdParameter}
					newCond := map[string]any{}
					for k, v := range cond {
						newCond[k] = v
					}
					newCond["StringEquals"] = newSE
					ns["Condition"] = newCond
				}
			}
		}
		newStmts = append(newStmts, ns)
	}
	out["Statement"] = newStmts
	return out
}

// renderCloudFormation produces a single-account YAML template that creates the
// Bellu IAM role. The external id is a CloudFormation Parameter (not embedded),
// so the template body is identical for every connection — S3 stores one
// reusable object and the value auto-fills from the quick-create link. The one-
// click launch link is added by the provider once the template is published.
func renderCloudFormation(in Input) (*Artifact, error) {
	tmpl := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              "Bellu Cloud Connector cross-account role",
		"Parameters":               map[string]any{ExternalIdParameter: externalIdParamDef()},
		"Resources": map[string]any{
			"BelluConnectorRole": roleResource(in.RoleName, templateTrustPolicy(in.TrustPolicy), in.PermissionPolicy, nil),
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
			"Click Launch CloudFormation (the external id is pre-filled from Bellu). Or upload the template under CloudFormation → Create stack and paste the external id from Bellu into the ExternalId field.",
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
