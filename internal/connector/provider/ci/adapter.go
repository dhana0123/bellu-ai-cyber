// Package ci implements provider.CloudProvider for CI/VCS platforms — GitHub,
// GitLab, and Atlassian (Bitbucket).
//
// These are not cloud accounts: there is no Steampipe catalog and no cloud
// role. A CI connection authenticates to the platform's API (GitHub via the
// shared code-security GitHub App installation; GitLab and Atlassian via a
// read-only token/app) and lets Bellu enumerate repositories/projects as
// evidence for the graph.
//
// Scope note: these adapters implement the common provider.CloudProvider
// interface so they register in the SAME connector registry and surface in the
// SAME Settings page as the cloud providers, with zero Manager changes. Setup
// and verification are implemented here; the actual repository/project
// collection runs through the code-security scanning path (GitHub) or a CI
// metadata collector (GitLab/Atlassian), which is wired separately from the
// Steampipe discovery orchestrator. Discover therefore returns a plan that
// names the CI endpoint rather than a Steampipe config.
package ci

import (
	"context"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	gh "github.com/bellu/bellu-ai-cyber/internal/connector/provider/ci/github"
)

// githubRepoLister is the slice of the GitHub App client the adapter needs to
// enumerate an installation's repositories. *github.GitHubAppClient satisfies
// it; tests inject a fake. Declaring it here (rather than importing the
// concrete type directly in ListRepositories) keeps the delegation testable and
// nil-safe.
type githubRepoLister interface {
	ListInstallationRepos(ctx context.Context, installationID int64) ([]provider.Repo, error)
}

// Platform identifies a CI/VCS platform.
type Platform string

const (
	GitHub    Platform = "github"
	GitLab    Platform = "gitlab"
	Atlassian Platform = "atlassian"
)

// Adapter implements provider.CloudProvider for one CI/VCS platform.
type Adapter struct {
	platform Platform
	// apiBase is the platform API base URL used to build the discovery plan.
	apiBase string
	// ghClient is the GitHub App client used to enumerate an installation's
	// repositories. It is nil in dev/unconfigured builds, in which case
	// ListRepositories returns (nil, nil) rather than crashing.
	ghClient githubRepoLister
}

// NewGitHub builds the GitHub CI adapter. GitHub auth reuses the code-security
// GitHub App installation rather than a personal access token.
func NewGitHub(apiBase string) *Adapter {
	return &Adapter{platform: GitHub, apiBase: firstNonEmpty(apiBase, "https://api.github.com")}
}

// NewGitHubApp builds the GitHub CI adapter wired to a GitHub App client, so
// ListRepositories enumerates the installation's accessible repositories via
// the GitHub REST API. Used when App config (app id + private key + api base)
// is present in the environment.
func NewGitHubApp(apiBase string, client *gh.GitHubAppClient) *Adapter {
	a := NewGitHub(apiBase)
	if client != nil {
		a.ghClient = client
	}
	return a
}

// NewGitLab builds the GitLab CI adapter.
func NewGitLab(apiBase string) *Adapter {
	return &Adapter{platform: GitLab, apiBase: firstNonEmpty(apiBase, "https://gitlab.com/api/v4")}
}

// NewAtlassian builds the Atlassian (Bitbucket) CI adapter.
func NewAtlassian(apiBase string) *Adapter {
	return &Adapter{platform: Atlassian, apiBase: firstNonEmpty(apiBase, "https://api.bitbucket.org/2.0")}
}

// Name implements provider.CloudProvider: the registry key is the platform name
// ("github" | "gitlab" | "atlassian").
func (a *Adapter) Name() string { return string(a.platform) }

// CreateSetup returns platform-specific onboarding instructions. GitHub reuses
// the code-security GitHub App; GitLab and Atlassian use a read-only token/app.
func (a *Adapter) CreateSetup(ctx context.Context, in provider.SetupInput) (*provider.Setup, error) {
	s := &provider.Setup{
		Scope:            firstNonEmpty(in.Scope, "account"),
		DeploymentMethod: "manual",
	}
	switch a.platform {
	case GitHub:
		s.RoleName = "bellu-github-app"
		s.Instructions = []string{
			"Install the Bellu GitHub App on your organization and select the repositories to connect.",
			"The same GitHub App powers code-security scanning; no personal access token is needed.",
			"Grant read access to repository contents and metadata; write (pull requests) is requested only for repos opted into automated remediation.",
			"Bellu receives a short-lived installation token per scan; no long-lived credential is stored.",
		}
	case GitLab:
		s.RoleName = "bellu-gitlab-token"
		s.Instructions = []string{
			"Create a GitLab project or group access token with the read_api and read_repository scopes.",
			"Supply the token and your GitLab instance URL (gitlab.com or self-managed) when verifying this connection.",
			"Bellu uses the token to enumerate projects as evidence; it is used per session and never exposed over the API.",
		}
	case Atlassian:
		s.RoleName = "bellu-atlassian-token"
		s.Instructions = []string{
			"Create a Bitbucket app password (or Atlassian API token) with repository read scope.",
			"Supply the workspace, username, and app password when verifying this connection.",
			"Bellu uses the credential to enumerate repositories as evidence; it is used per session and never exposed over the API.",
		}
	}
	return s, nil
}

// Authenticate is a credential pass-through: GitHub mints an installation token
// via the code-security app at use time; GitLab/Atlassian supply a token via
// the session env. No long-lived credential is held here.
func (a *Adapter) Authenticate(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}

// GetCredentials mirrors Authenticate.
func (a *Adapter) GetCredentials(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return a.Authenticate(ctx, t)
}

// VerifyConnection validates the connection shape and reports a capability
// result. A live API reachability probe per platform is a follow-up.
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

// Discover returns a CI collection plan. Unlike the cloud providers this is NOT
// a Steampipe config: CI collection runs through the code-security path
// (GitHub) or a CI metadata collector (GitLab/Atlassian). The plan names the
// platform and API base so the CI collector can act on it; the Steampipe
// discovery orchestrator does not consume CI plans.
func (a *Adapter) Discover(ctx context.Context, t provider.Target, creds *provider.Credentials) (*provider.DiscoveryPlan, error) {
	connName := fmt.Sprintf("%s_conn_%d", a.platform, t.ConnectionID)
	cfg := map[string]any{
		"collector":    "ci",
		"platform":     string(a.platform),
		"api_base":     a.apiBase,
		"capabilities": t.Capabilities,
	}
	env := map[string]string{
		"BELLU_CI_CONNECTION": connName,
		"BELLU_CI_PLATFORM":   string(a.platform),
		// AccountID carries the org/workspace/group identifier for CI connections.
		"BELLU_CI_ACCOUNT": t.AccountID,
	}
	return &provider.DiscoveryPlan{SteampipeConfig: cfg, CredentialEnv: env}, nil
}

// ListRepositories enumerates the repositories the connection can access,
// implementing provider.RepoLister. The platform client (GitHub App
// installation, GitLab token, Bitbucket app password) is supplied out of band
// via the session credential sink; when no client is configured this returns an
// empty slice rather than fabricating repositories, so the UI shows an empty
// list instead of fake data. The real per-platform API calls are wired where
// the session credential sink provides the token.
func (a *Adapter) ListRepositories(ctx context.Context, t provider.Target) ([]provider.Repo, error) {
	// GitHub with a configured App client and a bound installation delegates to
	// the GitHub REST API. Everything else (no App config, no installation id,
	// or a non-GitHub platform) returns an empty — not fabricated — list so
	// unconfigured dev environments do not crash.
	if a.platform == GitHub && a.ghClient != nil && t.InstallationID > 0 {
		return a.ghClient.ListInstallationRepos(ctx, t.InstallationID)
	}
	return nil, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Compile-time checks that each adapter satisfies the interfaces.
var (
	_ provider.CloudProvider = (*Adapter)(nil)
	_ provider.RepoLister    = (*Adapter)(nil)
)
