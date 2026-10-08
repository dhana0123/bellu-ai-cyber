package aws

import (
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// DefaultRoleName is the IAM role the customer creates for Bellu to assume.
const DefaultRoleName = "BelluConnectorRole"

// capabilitySid maps each known capability to its policy statement Sid.
var capabilitySid = map[string]string{
	string(capability.Core):     "BelluCoreReadOnly",
	string(capability.EKS):      "BelluEksReadOnly",
	string(capability.Workload): "BelluWorkloadReadOnly",
	string(capability.DSPM):     "BelluDspmReadOnly",
}

// sidForCapability maps a capability name to a policy statement Sid.
func sidForCapability(cap string) string {
	if sid, ok := capabilitySid[cap]; ok {
		return sid
	}
	return "BelluReadOnly"
}

// buildSetup generates the onboarding artifacts for an AWS connection: the role
// ARN template, the trust policy conditioned on the external id, and a
// least-privilege permission policy covering the enabled capabilities. The
// generated permission policy never includes AdministratorAccess and never uses
// a "*" action on "*" resource.
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

	return &provider.Setup{
		RoleName:         DefaultRoleName,
		RoleARNTemplate:  "arn:aws:iam::<ACCOUNT_ID>:role/" + DefaultRoleName,
		ExternalID:       in.ExternalID,
		TrustPolicy:      trust,
		PermissionPolicy: perm,
		Instructions:     instructions(in),
	}, nil
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
	for _, c := range []string{string(capability.EKS), string(capability.Workload), string(capability.DSPM)} {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// instructions returns the human-readable onboarding steps.
func instructions(in provider.SetupInput) []string {
	return []string{
		fmt.Sprintf("1. In AWS account %s, create an IAM role named %s.", in.AccountID, DefaultRoleName),
		"2. Set the role's trust policy to the provided trust_policy document (it conditions sts:AssumeRole on the external id).",
		"3. Attach the provided permission_policy document as an inline or managed policy (least-privilege, read-only).",
		fmt.Sprintf("4. Copy the role ARN (arn:aws:iam::%s:role/%s) back into Bellu and run Verify.", in.AccountID, DefaultRoleName),
	}
}
