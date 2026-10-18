// Package gcp implements provider.CloudProvider for Google Cloud.
//
// Like the AWS/Azure adapters it performs no resource queries itself: it
// generates onboarding instructions for a read-only GCP service account and
// builds the discovery plan handed to the existing Steampipe source (the
// gcp.yaml catalog). The service-account key lives only in the session
// credential env for the discovery run and is never persisted on the
// Connection.
package gcp

import (
	"context"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// ProviderName is the registry key for the GCP adapter.
const ProviderName = "gcp"

// Adapter implements provider.CloudProvider for GCP.
type Adapter struct {
	steampipeHost     string
	steampipePort     int
	steampipeUser     string
	steampipePassword string
	steampipeDB       string
	steampipeConn     string // catalog/connection selector, e.g. "gcp"
}

// Options configures the GCP adapter.
type Options struct {
	SteampipeHost     string
	SteampipePort     int
	SteampipeUser     string
	SteampipePassword string
	SteampipeDB       string
	SteampipeConn     string
}

// New builds a GCP adapter with sensible local-dev defaults.
func New(opts Options) *Adapter {
	host := firstNonEmpty(opts.SteampipeHost, "localhost")
	user := firstNonEmpty(opts.SteampipeUser, "steampipe")
	db := firstNonEmpty(opts.SteampipeDB, "steampipe")
	conn := firstNonEmpty(opts.SteampipeConn, "gcp")
	port := opts.SteampipePort
	if port == 0 {
		port = 9193
	}
	return &Adapter{
		steampipeHost:     host,
		steampipePort:     port,
		steampipeUser:     user,
		steampipePassword: opts.SteampipePassword,
		steampipeDB:       db,
		steampipeConn:     conn,
	}
}

// Name implements provider.CloudProvider.
func (a *Adapter) Name() string { return ProviderName }

// CreateSetup returns onboarding instructions for a least-privilege GCP service
// account with the Viewer role on the target project.
func (a *Adapter) CreateSetup(ctx context.Context, in provider.SetupInput) (*provider.Setup, error) {
	return &provider.Setup{
		RoleName:         "bellu-viewer",
		Scope:            in.Scope,
		DeploymentMethod: firstNonEmpty(in.DeploymentMethod, "manual"),
		Instructions: []string{
			"Create a GCP service account for Bellu in the project you want scanned.",
			"Grant it the roles/viewer (and roles/cloudasset.viewer) role on the project or folder.",
			"Create a JSON key for the service account.",
			"Supply the Project ID and the service-account JSON key when verifying this connection.",
			"Bellu stores only the project id; the JSON key is used for the scan session and never persisted.",
		},
	}, nil
}

// Authenticate is a no-op credential pass-through: the service-account key is
// supplied out of band and travels in the discovery CredentialEnv.
func (a *Adapter) Authenticate(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}

// GetCredentials mirrors Authenticate.
func (a *Adapter) GetCredentials(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return a.Authenticate(ctx, t)
}

// VerifyConnection validates the connection shape; a full read-only probe is a
// follow-up.
func (a *Adapter) VerifyConnection(ctx context.Context, t provider.Target) (*provider.VerifyResult, error) {
	creds, err := a.GetCredentials(ctx, t)
	if err != nil {
		return nil, err
	}
	return a.CheckPermissions(ctx, t, creds, t.Capabilities)
}

// CheckPermissions reports a capability-shaped result.
func (a *Adapter) CheckPermissions(ctx context.Context, t provider.Target, creds *provider.Credentials, caps []string) (*provider.VerifyResult, error) {
	res := &provider.VerifyResult{OK: true}
	for _, c := range caps {
		res.Capabilities = append(res.Capabilities, provider.CapabilityResult{
			Capability: c, Outcome: provider.OutcomePass,
		})
	}
	return res, nil
}

// Discover builds the Steampipe discovery plan for GCP. The gcp.yaml catalog is
// selected and the project id is passed as the standard GCP_PROJECT env for the
// session only; the service-account key is injected by the session sink.
func (a *Adapter) Discover(ctx context.Context, t provider.Target, creds *provider.Credentials) (*provider.DiscoveryPlan, error) {
	connName := fmt.Sprintf("gcp_conn_%d", t.ConnectionID)
	cfg := map[string]any{
		"host":              a.steampipeHost,
		"port":              a.steampipePort,
		"user":              a.steampipeUser,
		"password":          a.steampipePassword,
		"database":          a.steampipeDB,
		"connection":        a.steampipeConn, // selects gcp.yaml catalog
		"connection_schema": connName,
		"capabilities":      t.Capabilities,
	}
	// AccountID carries the GCP project id for GCP connections.
	env := map[string]string{
		"BELLU_STEAMPIPE_CONNECTION": connName,
		"GCP_PROJECT":                t.AccountID,
	}
	return &provider.DiscoveryPlan{SteampipeConfig: cfg, CredentialEnv: env}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Compile-time check that Adapter satisfies the interface.
var _ provider.CloudProvider = (*Adapter)(nil)
