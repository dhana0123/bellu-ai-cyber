package aws

import (
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/connector/setup"
)

// DefaultRoleName is the IAM role the customer creates for Bellu to assume.
const DefaultRoleName = "BelluConnectorRole"

// capabilitySid maps each known capability to its policy statement Sid.
var capabilitySid = map[string]string{
	string(capability.Core):     "BelluCoreReadOnly",
	string(capability.EKS):      "BelluEksReadOnly",
	string(capability.Workload): "BelluWorkloadReadOnly",
	string(capability.DSPM):     "BelluDspmReadOnly",
	string(capability.AI):       "BelluAiReadOnly",
}

// sidForCapability maps a capability name to a policy statement Sid.
func sidForCapability(cap string) string {
	if sid, ok := capabilitySid[cap]; ok {
		return sid
	}
	return "BelluReadOnly"
}

// buildSetup generates the onboarding artifacts for an AWS connection: the role
// ARN template, the trust policy conditioned on the external id, a
// least-privilege permission policy covering the enabled capabilities, and the
// deployment-method-specific artifact (CloudFormation template + launch URL,
// Terraform files, or manual steps). The permission policy never includes
// AdministratorAccess and never uses a "*" action on "*" resource.
func buildSetup(belluPrincipalARN string, in provider.SetupInput) (*provider.Setup, error) {
	caps := in.Capabilities
	if len(caps) == 0 {
		caps = capability.DefaultSelection()
	}

	// Core first (if enabled), then the rest in catalog-stable order, so the
	// policy is deterministic.
	ordered := orderCapabilities(caps)

	var statements []map[string]any
	for _, c := range ordered {
		def, ok := capability.Lookup(c)
		if !ok {
			return nil, fmt.Errorf("aws setup: unknown capability %q", c)
		}
		statements = append(statements, permissionStatement(sidForCapability(c), def.Permissions))
	}

	perm := policyDocument(statements...)
	trust := trustPolicy(belluPrincipalARN, in.ExternalID)

	method := in.DeploymentMethod
	if method == "" {
		method = "cloudformation"
	}

	art, err := setup.Generate(method, setup.Input{
		RoleName:             DefaultRoleName,
		ExternalID:           in.ExternalID,
		AccountID:            in.AccountID,
		Region:               in.Region,
		Scope:                in.Scope,
		ManagementAccountID:  in.ManagementAccountID,
		OrganizationalUnitID: in.OrganizationalUnitID,
		TrustPolicy:          trust,
		PermissionPolicy:     perm,
	})
	if err != nil {
		return nil, err
	}

	s := &provider.Setup{
		RoleName:         DefaultRoleName,
		RoleARNTemplate:  "arn:aws:iam::<ACCOUNT_ID>:role/" + DefaultRoleName,
		ExternalID:       in.ExternalID,
		Scope:            in.Scope,
		DeploymentMethod: method,
		TrustPolicy:      trust,
		PermissionPolicy: perm,
		Instructions:     art.Instructions,
		Template:         art.Template,
		TemplateFormat:   art.TemplateFormat,
		LaunchURL:        art.LaunchURL,
	}
	for _, f := range art.Files {
		s.Files = append(s.Files, provider.SetupFile{Name: f.Name, Content: f.Content})
	}
	return s, nil
}

// orderCapabilities returns the enabled capabilities with core first, then the
// others in a stable order.
func orderCapabilities(caps []string) []string {
	seen := map[string]bool{}
	for _, c := range caps {
		seen[c] = true
	}
	var out []string
	if seen[string(capability.Core)] {
		out = append(out, string(capability.Core))
	}
	for _, c := range []string{
		string(capability.EKS),
		string(capability.Workload),
		string(capability.AI),
		string(capability.DSPM),
	} {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}
