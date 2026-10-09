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

	// steampipe connection parameters used to build the discovery plan.
	steampipeHost     string
	steampipePort     int
	steampipeUser     string
	steampipePassword string
	steampipeDB       string
	steampipeConn     string // provider catalog/connection selector, e.g. "aws"
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

	SteampipeHost     string
	SteampipePort     int
	SteampipeUser     string
	SteampipePassword string
	SteampipeDB       string
	SteampipeConn     string
}

// New builds an AWS adapter. Sessions defaults to a fresh in-memory manager;
// Steampipe defaults match the local dev stack.
func New(opts Options) *Adapter {
	sess := opts.Sessions
	if sess == nil {
		sess = session.New()
	}
	host := opts.SteampipeHost
	if host == "" {
		host = "localhost"
	}
	port := opts.SteampipePort
	if port == 0 {
		port = 9193
	}
	user := opts.SteampipeUser
	if user == "" {
		user = "steampipe"
	}
	db := opts.SteampipeDB
	if db == "" {
		db = "steampipe"
	}
	conn := opts.SteampipeConn
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
		u, err := a.templates.Publish(ctx, templateKey(in.ConnectionID, body), body)
		if err != nil {
			// Publishing is best-effort: if the template bucket is unreachable
			// (e.g. missing credentials), setup still works via download/upload.
			// The one-click launch link is simply omitted.
			log.Printf("connector: template publish failed, launch link disabled: %v", err)
		} else {
			s.TemplateURL = u
			s.LaunchURL = quickCreateURL(in.Region, u)
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

// Discover builds the discovery plan handed to the existing Steampipe source.
// It performs no provider-specific resource queries: it only assembles the
// steampipe config map and the session-only AWS_* credential environment.
func (a *Adapter) Discover(ctx context.Context, t provider.Target, creds *provider.Credentials) (*provider.DiscoveryPlan, error) {
	if creds == nil {
		return nil, fmt.Errorf("aws: discover requires credentials")
	}
	cfg := map[string]any{
		"host":       a.steampipeHost,
		"port":       a.steampipePort,
		"user":       a.steampipeUser,
		"password":   a.steampipePassword,
		"database":   a.steampipeDB,
		"connection": a.steampipeConn,
	}
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_SESSION_TOKEN":     creds.SessionToken,
	}
	if t.Region != "" {
		env["AWS_REGION"] = t.Region
	}
	return &provider.DiscoveryPlan{SteampipeConfig: cfg, CredentialEnv: env}, nil
}

// Compile-time check that Adapter satisfies the interface.
var _ provider.CloudProvider = (*Adapter)(nil)
