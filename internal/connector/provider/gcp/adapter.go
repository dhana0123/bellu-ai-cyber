// Package gcp implements provider.CloudProvider for Google Cloud.
//
// Like the AWS/Azure adapters it performs no resource queries itself: it
// generates onboarding instructions for a read-only GCP service account and
// builds the discovery plan handed to the CloudQuery source (the gcp.yaml
// mapping). The service-account key lives only in the session credential env
// for the discovery run and is never persisted on the Connection.
package gcp

import (
	"context"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// ProviderName is the registry key for the GCP adapter.
const ProviderName = "gcp"

// Adapter implements provider.CloudProvider for GCP.
type Adapter struct {
	// cqStagingDSN is the customer PostgreSQL DSN the CloudQuery sync writes its
	// cq_staging destination into (and the cloudquery.Source reads from). It is a
	// connection string, not a source credential: the per-connection service
	// account credentials travel separately on DiscoveryPlan.CredentialEnv and
	// are never persisted.
	cqStagingDSN string
	// cqConnection is the mapping selector (e.g. "gcp") the cloudquery.Factory
	// resolves to mapping/<conn>.yaml.
	cqConnection string
}

// Options configures the GCP adapter.
type Options struct {
	// CQStagingDSN is the customer PostgreSQL DSN the CloudQuery sync writes its
	// cq_staging destination into and the cloudquery.Source reads from.
	CQStagingDSN string
	// CQConnection is the mapping selector (mapping/<conn>.yaml). Defaults to
	// "gcp".
	CQConnection string
}

// New builds a GCP adapter. The CloudQuery mapping selector defaults to "gcp".
func New(opts Options) *Adapter {
	conn := opts.CQConnection
	if conn == "" {
		conn = "gcp"
	}
	return &Adapter{
		cqStagingDSN: opts.CQStagingDSN,
		cqConnection: conn,
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

// Discover builds the CloudQuery discovery plan for GCP. It performs no
// provider-specific resource queries: it assembles the CloudQuery Factory
// config map (staging DSN + mapping selector + capability gating + the
// session-only credential env) exactly as the AWS adapter does. The gcp.yaml
// mapping is selected via the "connection" key.
//
// The plan carries the staging DSN and the mapping selector in SteampipeConfig
// (the field name is retained from the Steampipe era; it is just the generic
// Source-Factory config map). The service-account key travels in CredentialEnv
// and the CloudQuery Factory forwards it to the JobRunner for the run only; it
// is never persisted.
func (a *Adapter) Discover(ctx context.Context, t provider.Target, creds *provider.Credentials) (*provider.DiscoveryPlan, error) {
	// The session-only GCP_* credential environment the CloudQuery sync injects
	// as per-run environment variables. AccountID carries the GCP project id;
	// the service-account key is injected by the session sink.
	env := map[string]string{
		"GCP_PROJECT": t.AccountID,
	}
	cfg := map[string]any{
		"dsn":            a.cqStagingDSN,
		"connection":     a.cqConnection, // selects mapping/gcp.yaml
		"capabilities":   t.Capabilities,
		"credential_env": env,
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
