package setup

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func sampleInput(method string) Input {
	return Input{
		RoleName:   "BelluConnectorRole",
		ExternalID: "BELLU-TEST123",
		AccountID:  "123456789012",
		Region:     "us-east-1",
		Scope:      "account",
		TrustPolicy: map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":    "Allow",
				"Principal": map[string]any{"AWS": "arn:aws:iam::999999999999:role/BelluService"},
				"Action":    "sts:AssumeRole",
				"Condition": map[string]any{"StringEquals": map[string]any{"sts:ExternalId": "BELLU-TEST123"}},
			}},
		},
		PermissionPolicy: map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Sid":      "BelluCoreReadOnly",
				"Effect":   "Allow",
				"Action":   []string{"ec2:DescribeInstances", "s3:ListAllMyBuckets"},
				"Resource": "*",
			}},
		},
	}
}

func TestGenerateUnknownMethod(t *testing.T) {
	if _, err := Generate("bogus", sampleInput("bogus")); err == nil {
		t.Error("unknown deployment method should error")
	}
}

func TestCloudFormationRender(t *testing.T) {
	art, err := Generate("cloudformation", sampleInput("cloudformation"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if art.TemplateFormat != "yaml" {
		t.Errorf("template format = %q, want yaml", art.TemplateFormat)
	}
	for _, want := range []string{"AWS::IAM::Role", "BelluConnectorRole", "BELLU-TEST123", "AssumeRolePolicyDocument", "Outputs:"} {
		if !strings.Contains(art.Template, want) {
			t.Errorf("cloudformation template missing %q", want)
		}
	}
	// No wildcard full access leaked in.
	if strings.Contains(art.Template, "\"*\"") && strings.Contains(art.Template, "AdministratorAccess") {
		t.Error("template must not grant AdministratorAccess")
	}
}

func TestTerraformRender(t *testing.T) {
	art, err := Generate("terraform", sampleInput("terraform"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	names := map[string]string{}
	for _, f := range art.Files {
		names[f.Name] = f.Content
	}
	if _, ok := names["main.tf"]; !ok {
		t.Error("terraform output missing main.tf")
	}
	if _, ok := names["variables.tf"]; !ok {
		t.Error("terraform output missing variables.tf")
	}
	if !strings.Contains(names["main.tf"], "aws_iam_role") || !strings.Contains(names["main.tf"], "assume_role_policy") {
		t.Error("main.tf missing the IAM role resource")
	}
}

func TestManualRender(t *testing.T) {
	art, err := Generate("manual", sampleInput("manual"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(art.Instructions) < 4 {
		t.Errorf("manual should have step-by-step instructions, got %d", len(art.Instructions))
	}
}

func TestCloudFormationStackSetRender(t *testing.T) {
	in := sampleInput("cloudformation")
	in.Scope = "organization"
	art, err := Generate("cloudformation", in)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var tmpl map[string]any
	if err := yaml.Unmarshal([]byte(art.Template), &tmpl); err != nil {
		t.Fatalf("org template is not valid YAML: %v", err)
	}
	res := tmpl["Resources"].(map[string]any)
	if res["BelluConnectorRole"].(map[string]any)["Type"] != "AWS::IAM::Role" {
		t.Error("missing management-account role")
	}
	ss := res["BelluMemberRoles"].(map[string]any)
	if ss["Type"] != "AWS::CloudFormation::StackSet" {
		t.Fatal("missing StackSet resource")
	}
	props := ss["Properties"].(map[string]any)
	if props["PermissionModel"] != "SERVICE_MANAGED" {
		t.Errorf("PermissionModel = %v", props["PermissionModel"])
	}
	if props["AutoDeployment"].(map[string]any)["Enabled"] != true {
		t.Error("auto-deployment must be enabled")
	}
	var member map[string]any
	if err := json.Unmarshal([]byte(props["TemplateBody"].(string)), &member); err != nil {
		t.Fatalf("member TemplateBody is not valid JSON: %v", err)
	}
	if !strings.Contains(props["TemplateBody"].(string), "BELLU-TEST123") {
		t.Error("member role must trust the same ExternalId")
	}
	if strings.Contains(props["TemplateBody"].(string), "organizations:") {
		t.Error("member roles must not get Organizations permissions")
	}
	if !strings.Contains(art.Template, "organizations:ListAccounts") {
		t.Error("management role needs organizations:ListAccounts")
	}
	if _, ok := tmpl["Parameters"].(map[string]any)[OrgTargetParameter]; !ok {
		t.Errorf("missing %s parameter", OrgTargetParameter)
	}
}

func TestAccountTemplateIsValidYAML(t *testing.T) {
	art, err := Generate("cloudformation", sampleInput("cloudformation"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var tmpl map[string]any
	if err := yaml.Unmarshal([]byte(art.Template), &tmpl); err != nil {
		t.Fatalf("account template is not valid YAML: %v", err)
	}
	if strings.Contains(art.Template, "StackSet") {
		t.Error("account scope must not create a StackSet")
	}
}

func TestStackSetWithoutManagementOrOU(t *testing.T) {
	in := sampleInput("cloudformation")
	in.Scope = "organization"
	art, err := Generate("cloudformation", in)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	joined := strings.Join(art.Instructions, " ")
	if strings.Contains(joined, "<") {
		t.Errorf("instructions should not contain placeholders: %v", art.Instructions)
	}
}
