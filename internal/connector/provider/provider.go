// Package provider defines the Cloud_Provider adapter interface and the
// supporting non-secret value types the Connector Manager drives providers
// through. The Connector Manager depends only on this interface; all
// provider-specific implementation (STS, policy generation, permission
// probing) lives in per-provider sub-packages such as provider/aws.
package provider

import (
	"context"
	"time"
)

// CloudProvider is the common provider-adapter interface. New providers are
// added by implementing this interface and registering it; the Connector
// Manager never branches on provider type.
type CloudProvider interface {
	// Name returns the provider key, e.g. "aws".
	Name() string

	// CreateSetup returns onboarding artifacts (role ARN template, External Id,
	// trust and permission policies, human-readable steps) for the enabled
	// capabilities.
	CreateSetup(ctx context.Context, in SetupInput) (*Setup, error)

	// VerifyConnection authenticates and checks the required permissions of the
	// enabled capabilities.
	VerifyConnection(ctx context.Context, t Target) (*VerifyResult, error)

	// Authenticate obtains fresh temporary credentials for the connection.
	Authenticate(ctx context.Context, t Target) (*Credentials, error)

	// GetCredentials returns usable credentials, going through the credential
	// session cache where applicable.
	GetCredentials(ctx context.Context, t Target) (*Credentials, error)

	// CheckPermissions runs read-only probes for the given enabled capabilities.
	CheckPermissions(ctx context.Context, t Target, creds *Credentials, caps []string) (*VerifyResult, error)

	// Discover builds the collection plan handed to the existing Steampipe
	// source. It performs no provider-specific resource queries itself.
	Discover(ctx context.Context, t Target, creds *Credentials) (*DiscoveryPlan, error)
}

// Target is the provider-agnostic, non-secret view of a Connection an adapter
// needs. It carries no credentials.
type Target struct {
	ConnectionID int64
	AccountID    string
	RoleARN      string
	ExternalID   string
	Region       string
	Capabilities []string
}

// Credentials are short-lived credentials obtained from the provider (for AWS,
// from STS AssumeRole). They live in process memory only and are never
// persisted, logged, or returned over the API.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiry          time.Time
}

// Expired reports whether the credentials are at or past their expiry relative
// to now. Zero-expiry credentials are treated as non-expiring.
func (c *Credentials) Expired(now time.Time) bool {
	if c == nil {
		return true
	}
	if c.Expiry.IsZero() {
		return false
	}
	return !now.Before(c.Expiry)
}

// SetupInput carries the inputs needed to generate onboarding artifacts.
type SetupInput struct {
	ConnectionID         int64
	AccountID            string
	ManagementAccountID  string
	OrganizationalUnitID string
	ExternalID           string
	Region               string
	Scope                string // "account" | "organization"
	DeploymentMethod     string // "cloudformation" | "terraform" | "manual"
	Capabilities         []string
}

// SetupFile is one named artifact file (e.g. a Terraform file or a template).
type SetupFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Setup is the provider-specific onboarding artifact returned to the customer.
// For AWS it carries the role template, external id, trust/permission policies,
// the rendered deployment artifact for the chosen method, and human-readable
// steps. All fields are non-secret.
type Setup struct {
	RoleName         string         `json:"role_name"`
	RoleARNTemplate  string         `json:"role_arn_template"`
	ExternalID       string         `json:"external_id"`
	Scope            string         `json:"scope"`
	DeploymentMethod string         `json:"deployment_method"`
	TrustPolicy      map[string]any `json:"trust_policy"`
	PermissionPolicy map[string]any `json:"permission_policy"`
	Instructions     []string       `json:"instructions"`

	// Deployment-method-specific artifacts. Only the field matching
	// DeploymentMethod is populated.
	//
	// cloudformation: Template (YAML) + LaunchURL (console quick-create link).
	// terraform:      Files (main.tf, variables.tf).
	// manual:         TrustPolicy + PermissionPolicy above are the artifacts.
	Template       string `json:"template,omitempty"`
	TemplateFormat string `json:"template_format,omitempty"`
	LaunchURL      string `json:"launch_url,omitempty"`
	// TemplateURL is the (short-lived, presigned) S3 URL of the template.
	TemplateURL string      `json:"template_url,omitempty"`
	Files       []SetupFile `json:"files,omitempty"`
}

// Outcome is the result of a single permission check or an aggregate capability
// result.
type Outcome string

const (
	OutcomePass       Outcome = "PASS"
	OutcomeFail       Outcome = "FAIL"
	OutcomeNotEnabled Outcome = "NOT_ENABLED"
)

// PermissionResult is the outcome of one read-only permission probe.
type PermissionResult struct {
	Capability string  `json:"capability"`
	Permission string  `json:"permission"`
	Outcome    Outcome `json:"outcome"`
	Detail     string  `json:"detail,omitempty"`
}

// CapabilityResult aggregates the per-permission results for one capability.
type CapabilityResult struct {
	Capability string             `json:"capability"`
	Outcome    Outcome            `json:"outcome"` // PASS | FAIL | NOT_ENABLED
	Checks     []PermissionResult `json:"checks,omitempty"`
}

// VerifyResult is the structured output of permission verification: OK is true
// only when every checked permission across all enabled capabilities passed.
type VerifyResult struct {
	OK           bool               `json:"ok"`
	Capabilities []CapabilityResult `json:"capabilities"`
	Failures     []PermissionResult `json:"failures,omitempty"`
}

// DiscoveryPlan is what the Discovery Orchestrator feeds to the existing
// steampipe.Factory. SteampipeConfig is the config map (host/port/user/
// password/database and the catalog/connection selector); CredentialEnv holds
// the assumed-role AWS_* environment supplied to Steampipe for the session
// only. Neither is persisted.
type DiscoveryPlan struct {
	SteampipeConfig map[string]any
	CredentialEnv   map[string]string
}

// OrgAccount is one member account of a cloud organization.
type OrgAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// OrganizationLister is implemented by providers that can enumerate the member
// accounts of an organization-scope connection (AWS Organizations). The
// manager checks for it with a type assertion, so it stays provider-agnostic.
type OrganizationLister interface {
	ListOrganizationAccounts(ctx context.Context, t Target) ([]OrgAccount, error)
}

// MemberRoleARN returns the role ARN of a member account, built from a member
// account id. Providers that support organizations implement it.
type MemberRoleARNer interface {
	MemberRoleARN(accountID string) string
}
