package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/connector/session"
)

// ProviderName is the registry key for the AWS adapter.
const ProviderName = "aws"

// Adapter implements provider.CloudProvider for AWS by composing the auth
// layer, credential session cache, permission verifier, and setup generator.
type Adapter struct {
	auth              *AuthLayer
	sessions          *session.Manager
	verifier          *Verifier
	belluPrincipalARN string
	templates         TemplateStore    // nil: no one-click launch link (dev)
	orgClient         OrgClientFactory // builds Organizations clients for listing

	// cqStagingDSN is the customer PostgreSQL DSN the CloudQuery sync writes its
	// cq_staging destination into (and the cloudquery.Source reads from). It is a
	// connection string, not a source credential: the per-connection STS session
	// credentials travel separately on DiscoveryPlan.CredentialEnv and are never
	// persisted. Empty is tolerated so verify/setup still work without a staging
	// DB configured; discovery then relies on the Factory's host/port defaults.
	cqStagingDSN string
	// cqConnection is the mapping selector (e.g. "aws") the cloudquery.Factory
	// resolves to mapping/<conn>.yaml.
	cqConnection string
}

// Options configures the AWS adapter.
type Options struct {
	STS               STSAPI
	Sessions          *session.Manager
	Probe             ProbeFunc
	BelluPrincipalARN string
	// Templates publishes CloudFormation templates to S3 for one-click launch.
	// When nil, setup still works but no launch link is returned.
	Templates TemplateStore
	// OrgClient overrides the Organizations client factory (tests).
	OrgClient OrgClientFactory

	// CQStagingDSN is the customer PostgreSQL DSN the CloudQuery sync writes its
	// cq_staging destination into and the cloudquery.Source reads from.
	CQStagingDSN string
	// CQConnection is the mapping selector (mapping/<conn>.yaml). Defaults to
	// "aws".
	CQConnection string
}

// New builds an AWS adapter. Sessions defaults to a fresh in-memory manager;
// the CloudQuery mapping selector defaults to "aws".
func New(opts Options) *Adapter {
	sess := opts.Sessions
	if sess == nil {
		sess = session.New()
	}
	conn := opts.CQConnection
	if conn == "" {
		conn = "aws"
	}
	orgClient := opts.OrgClient
	if orgClient == nil {
		orgClient = defaultOrgClient
	}
	return &Adapter{
		auth:              NewAuthLayer(opts.STS),
		sessions:          sess,
		verifier:          NewVerifier(opts.Probe),
		belluPrincipalARN: opts.BelluPrincipalARN,
		templates:         opts.Templates,
		orgClient:         orgClient,
		cqStagingDSN:      opts.CQStagingDSN,
		cqConnection:      conn,
	}
}

// Name implements provider.CloudProvider.
func (a *Adapter) Name() string { return ProviderName }

// CreateSetup generates the onboarding artifacts. For CloudFormation, when a
// template store is configured, the template is published to S3 and a
// quick-create launch link is returned; otherwise LaunchURL is empty and the
// customer downloads/uploads the template themselves.
func (a *Adapter) CreateSetup(ctx context.Context, in provider.SetupInput) (*provider.Setup, error) {
	s, err := buildSetup(a.belluPrincipalARN, in)
	if err != nil {
		return nil, err
	}
	if s.DeploymentMethod == "cloudformation" && a.templates != nil && s.Template != "" {
		body := []byte(s.Template)
		u, err := a.templates.Publish(ctx, templateKey(body), body)
		if err != nil {
			// Publishing is best-effort: if the template bucket is unreachable
			// (e.g. missing credentials), setup still works via download/upload.
			// The one-click launch link is simply omitted.
			log.Printf("connector: template publish failed, launch link disabled: %v", err)
		} else {
			s.TemplateURL = u
			s.LaunchURL = quickCreateURL(in.Region, u, in.ExternalID)
			s.UpdateURL = quickUpdateURL(in.Region, u, in.ExternalID)
		}
	}
	return s, nil
}

// Authenticate obtains fresh temporary credentials via STS AssumeRole.
func (a *Adapter) Authenticate(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return a.auth.Authenticate(ctx, t)
}

// GetCredentials returns credentials through the session cache, authenticating
// on a miss or expiry.
func (a *Adapter) GetCredentials(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return a.sessions.Obtain(ctx, t.ConnectionID, func(ctx context.Context) (*provider.Credentials, error) {
		return a.auth.Authenticate(ctx, t)
	})
}

// Revoke discards any cached session credentials for the connection. It is
// called by the discovery orchestrator when a session ends.
func (a *Adapter) Revoke(connID int64) { a.sessions.Revoke(connID) }

// VerifyConnection authenticates (through the session cache) and then checks the
// permissions of the enabled capabilities.
func (a *Adapter) VerifyConnection(ctx context.Context, t provider.Target) (*provider.VerifyResult, error) {
	creds, err := a.GetCredentials(ctx, t)
	if err != nil {
		return nil, err
	}
	return a.CheckPermissions(ctx, t, creds, t.Capabilities)
}

// CheckPermissions runs the read-only verifier for the given capabilities.
func (a *Adapter) CheckPermissions(ctx context.Context, t provider.Target, creds *provider.Credentials, caps []string) (*provider.VerifyResult, error) {
	return a.verifier.Verify(ctx, creds, caps), nil
}

// Discover builds the discovery plan handed to the CloudQuery source. It
// performs no provider-specific resource queries: it assembles the CloudQuery
// Factory config map (staging DSN + mapping selector + capability gating) and
// the session-only AWS_* credential environment the CloudQuery job injects.
//
// The plan carries the staging DSN and the mapping selector in SteampipeConfig
// (the field name is retained from the Steampipe era; it is just the generic
// Source-Factory config map the orchestrator feeds to the wired Factory). The
// per-connection STS session credentials travel in CredentialEnv and the
// CloudQuery Factory forwards them to the JobRunner for the run only; they are
// never persisted to the staging DB, the on-disk spec, logs, or API responses
// (Requirements 1.2, 1.3, 16.4). This preserves the credential-env contract the
// Steampipe sink enforced, now carried end-to-end to the CloudQuery runner.
func (a *Adapter) Discover(ctx context.Context, t provider.Target, creds *provider.Credentials) (*provider.DiscoveryPlan, error) {
	if creds == nil {
		return nil, fmt.Errorf("aws: discover requires credentials")
	}
	// The session-only AWS_* credential environment the CloudQuery sync injects
	// as per-run environment variables. The CloudQuery Factory passes this
	// through to the JobRunner, which places it ONLY in the sync subprocess
	// environment and discards it after the run on every path.
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_SESSION_TOKEN":     creds.SessionToken,
		"BELLU_ROLE_ARN":        t.RoleARN,
		"BELLU_EXTERNAL_ID":     t.ExternalID,
	}
	if t.Region != "" {
		env["AWS_REGION"] = t.Region
	}

	cfg := map[string]any{
		// dsn is the customer PostgreSQL the CloudQuery sync writes cq_staging
		// into and the cloudquery.Source reads from. It is not a source
		// credential; the session credentials travel in CredentialEnv below.
		"dsn":        a.cqStagingDSN,
		"connection": a.cqConnection, // selects mapping/aws.yaml
		// Enabled capabilities gate which mapping tables the collector reads and
		// which the generated sync Spec selects: a table whose capability is not
		// in this set is skipped, so an ungranted capability never asks the
		// plugin for resources it cannot read. An empty/nil set means "no gating
		// configured" and every table is collected (backward-compatible).
		"capabilities": t.Capabilities,
		// credential_env carries the run's STS session credentials to the
		// CloudQuery Factory, which forwards them to the JobRunner for this run
		// only. Placing it in the (in-memory, never-persisted) config map is how
		// the credential-env contract reaches the runner now that the collector
		// is a subprocess rather than a long-lived Steampipe service.
		"credential_env": env,
	}
	return &provider.DiscoveryPlan{SteampipeConfig: cfg, CredentialEnv: env}, nil
}

// Compile-time check that Adapter satisfies the interface.
var _ provider.CloudProvider = (*Adapter)(nil)
