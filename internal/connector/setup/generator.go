// Package setup renders deployment-method-specific onboarding artifacts
// (CloudFormation, Terraform, Manual) from an authoritative, backend-generated
// IAM policy set. The deployment method selects a renderer, not a different
// connector implementation; the trust and permission policies are identical
// across methods. No artifact contains any credential.
package setup

import (
	"encoding/json"
	"fmt"
)

// Artifact is the rendered, non-secret result for a deployment method.
type Artifact struct {
	Method         string
	Template       string // cloudformation: YAML template
	TemplateFormat string // "yaml" for cloudformation
	LaunchURL      string // cloudformation: console quick-create URL
	Files          []File // terraform: main.tf, variables.tf
	Instructions   []string
}

// File is one named artifact file.
type File struct {
	Name    string
	Content string
}

// Input is the authoritative, backend-owned input to every renderer.
type Input struct {
	RoleName         string
	ExternalID       string
	AccountID        string
	Region           string
	Scope            string         // "account" | "organization"
	TrustPolicy      map[string]any // Bellu principal + sts:ExternalId condition
	PermissionPolicy map[string]any // least-privilege, read-only

	// Organization-scope inputs (only used when Scope == "organization").
	ManagementAccountID  string
	OrganizationalUnitID string // target OU id; empty means the whole org root
}

// IsOrganization reports whether the input targets an AWS Organization.
func (in Input) IsOrganization() bool { return in.Scope == "organization" }

// Generate renders the artifact for the given deployment method.
func Generate(method string, in Input) (*Artifact, error) {
	switch method {
	case "cloudformation":
		if in.IsOrganization() {
			return renderCloudFormationStackSet(in)
		}
		return renderCloudFormation(in)
	case "terraform":
		return renderTerraform(in)
	case "manual":
		return renderManual(in)
	default:
		return nil, fmt.Errorf("setup: unknown deployment method %q", method)
	}
}

// jsonIndent marshals a policy document to indented JSON for embedding in
// templates and manual instructions.
func jsonIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
