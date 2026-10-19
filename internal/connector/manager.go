package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// progressPersistInterval throttles how often live discovery progress is
// written to Postgres during a run, so a fast scan does not hammer the DB. The
// final snapshot is always written regardless of this interval.
const progressPersistInterval = time.Second

// ExternalIDFunc generates a per-connection external id. It is injectable so
// tests are deterministic; production uses the AWS adapter's crypto/rand
// generator.
type ExternalIDFunc func() (string, error)

// connRepo is the persistence surface the Manager depends on. *Repository
// satisfies it; tests inject an in-memory fake so manager logic is unit-tested
// without a database.
type connRepo interface {
	Insert(ctx context.Context, c *Connection) (int64, error)
	Get(ctx context.Context, orgID, id int64) (*Connection, error)
	ListByOrg(ctx context.Context, orgID int64) ([]Connection, error)
	UpdateStatus(ctx context.Context, orgID, id int64, s Status) error
	SetVerified(ctx context.Context, orgID, id int64, t time.Time) error
	SetRoleARN(ctx context.Context, orgID, id int64, roleARN string) error
	SetAccountID(ctx context.Context, orgID, id int64, accountID string) error
	SetManagementAccountID(ctx context.Context, orgID, id int64, accountID string) error
	Delete(ctx context.Context, orgID, id int64) error
	SaveVerification(ctx context.Context, connID int64, res *provider.VerifyResult) error
	RecordSession(ctx context.Context, connID int64, sessionID, runID, status string, started time.Time, ended *time.Time, errMsg string) error
	SaveSessionProgress(ctx context.Context, sessionID string, progressJSON []byte) error
	LatestSession(ctx context.Context, connID int64) (*DiscoverySessionRow, error)
	ReconcileRunningSessions(ctx context.Context) (int, error)
	SetCapabilities(ctx context.Context, orgID, id int64, caps []string) error

	// Git connector repository management.
	SaveRepositories(ctx context.Context, connID int64, repos []Repo) error
	ListRepositories(ctx context.Context, connID int64) ([]Repo, error)
	SetRepositorySelection(ctx context.Context, connID int64, repoIDs []int64, selected bool) error
	SetAllRepositoriesSelected(ctx context.Context, connID int64, selected bool) error
	GetRepoSelectionMode(ctx context.Context, connID int64) (RepoSelectionMode, error)
	SetRepoSelectionMode(ctx context.Context, connID int64, mode RepoSelectionMode) error

	// GitHub App installation + single-use install-state management.
	UpsertGitHubInstallation(ctx context.Context, inst GitHubInstallation) error
	GetGitHubInstallationByConnection(ctx context.Context, orgID, connID int64) (*GitHubInstallation, bool, error)
	GetGitHubInstallationByInstallationID(ctx context.Context, ghInstID int64) (*GitHubInstallation, bool, error)
	SaveInstallState(ctx context.Context, st InstallState) error
	ConsumeInstallState(ctx context.Context, state string, now time.Time) (InstallState, error)

	// GitHub webhook delivery dedup + lifecycle.
	InsertWebhookDelivery(ctx context.Context, d WebhookDelivery) error
	MarkWebhookDelivery(ctx context.Context, deliveryID, status, sanitizedErr string) error
	SetGitHubInstallationStatus(ctx context.Context, ghInstID int64, status string) error
	DeleteRepositoriesNotIn(ctx context.Context, connID int64, keepExternalIDs []string) error
}

// GitHubAppClient is the slice of the GitHub App API client the Manager needs
// to verify an installation during the install callback. *github.GitHubAppClient
// satisfies it; tests inject a fake. Declaring it here keeps the manager
// package free of an import on the ci/github package (and testable).
type GitHubAppClient interface {
	VerifyInstallation(ctx context.Context, installationID int64) (login string, accountID int64, accountType string, perms map[string]string, err error)
}

// Manager owns Connection lifecycle and the state machine. It is provider-
// agnostic: it depends on the provider registry and contains zero AWS SDK
// calls. All status changes go through Status.Next so invalid transitions are
// rejected.
type Manager struct {
	repo       connRepo
	registry   *provider.Registry
	disc       *discovery.Orchestrator
	externalID ExternalIDFunc
	now        func() time.Time
	// githubClient verifies GitHub App installations during the install
	// callback. Nil when no GitHub App config is present (dev-safe):
	// BeginGitHubInstall still works, but CompleteGitHubInstall returns a clear
	// error.
	githubClient GitHubAppClient
	// githubAppSlug is the GitHub App's URL slug used to build the install URL.
	githubAppSlug string
	// githubWebhookSecret authenticates inbound GitHub webhooks (HMAC-SHA256).
	// Empty when no webhook secret is configured; the webhook handler then
	// rejects all deliveries (fail-safe).
	githubWebhookSecret []byte
}

// Options configures a Manager.
type Options struct {
	Repo       connRepo
	Registry   *provider.Registry
	Discovery  *discovery.Orchestrator
	ExternalID ExternalIDFunc
	Now        func() time.Time
	// GitHubClient verifies GitHub App installations. Optional: nil in dev
	// builds without GitHub App config.
	GitHubClient GitHubAppClient
	// GitHubAppSlug is the GitHub App's URL slug (BELLU_GITHUB_APP_SLUG). Used
	// to build https://github.com/apps/<slug>/installations/new.
	GitHubAppSlug string
	// GitHubWebhookSecret authenticates inbound GitHub webhooks. Optional: when
	// empty, the webhook handler rejects all deliveries.
	GitHubWebhookSecret string
}

// NewManager builds a Manager.
func NewManager(opts Options) *Manager {
	m := &Manager{
		repo:          opts.Repo,
		registry:      opts.Registry,
		disc:          opts.Discovery,
		externalID:    opts.ExternalID,
		now:           opts.Now,
		githubClient:        opts.GitHubClient,
		githubAppSlug:       opts.GitHubAppSlug,
		githubWebhookSecret: []byte(opts.GitHubWebhookSecret),
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.externalID == nil {
		m.externalID = func() (string, error) {
			return fmt.Sprintf("BELLU-%d", time.Now().UnixNano()), nil
		}
	}
	return m
}

// CreateInput carries the fields needed to create a connection. RoleARN is
// intentionally absent: the customer deploys the role after setup and supplies
// its ARN at verification time.
type CreateInput struct {
	OrgID                int64
	ProjectID            int64
	Provider             string
	Scope                Scope
	InstallationType     InstallationType
	DeploymentMethod     DeploymentMethod
	AccountID            string
	ManagementAccountID  string
	OrganizationalUnitID string
	Region               string
	Capabilities         []string
	// The following are set internally when expanding an organization into
	// per-member-account connections; external callers leave them zero.
	parentConnectionID int64
	externalIDOverride string // reuse the organization's ExternalId
	roleARNOverride    string // the member role ARN the StackSet created
}

// Create validates the provider, generates an external id, defaults the
// capability set to core, and persists a PENDING connection.
func (m *Manager) Create(ctx context.Context, in CreateInput) (*Connection, error) {
	p, err := m.resolveProvider(in.Provider)
	if err != nil {
		return nil, err
	}
	// Git/CI providers (those that enumerate repositories) have no cloud
	// capabilities. Their "scope" is which repositories to scan, chosen on the
	// Repositories page — not the AWS capability catalog (core/eks/dspm/…). So
	// an empty capability set stays empty for them, and the cloud defaults are
	// skipped.
	_, isGit := p.(provider.RepoLister)

	caps := in.Capabilities
	if len(caps) == 0 && !isGit {
		caps = capability.DefaultSelection()
	}
	for _, c := range caps {
		if !capability.Known(c) {
			return nil, fmt.Errorf("%w: capability %q", ErrInvalidValue, c)
		}
	}

	scope := in.Scope
	if scope == "" {
		scope = ScopeAccount
	}
	if !scope.Valid() {
		return nil, fmt.Errorf("%w: scope %q", ErrInvalidValue, scope)
	}
	inst := in.InstallationType
	if inst == "" {
		inst = InstallationStandard
	}
	if inst != InstallationStandard {
		return nil, fmt.Errorf("%w: installation_type %q (only standard is supported)", ErrInvalidValue, inst)
	}
	method := in.DeploymentMethod
	if method == "" {
		// Git/CI connectors have no cloud role to deploy; default to manual.
		if isGit {
			method = DeploymentManual
		} else {
			method = DeploymentCloudFormation
		}
	}
	if !method.Valid() {
		return nil, fmt.Errorf("%w: deployment_method %q", ErrInvalidValue, method)
	}
	// Scope-specific inputs. Account scope takes no account or organization
	// inputs at create: the account id is derived from the role ARN at verify.
	// Organization scope keeps only what the StackSet deployment needs: the
	// management account (where the StackSet runs and where Bellu's org-level
	// role lives) and an optional deployment-target OU/root id.
	accountID := strings.TrimSpace(in.AccountID)
	mgmt := strings.TrimSpace(in.ManagementAccountID)
	ou := strings.TrimSpace(in.OrganizationalUnitID)
	switch scope {
	case ScopeAccount:
		// Organization-only inputs never apply to an account connection.
		mgmt, ou = "", ""
		// The 12-digit account-id rule is AWS-specific. Git/CI connectors use
		// account_id for the org/group/workspace login (free-form text), so the
		// AWS validation is skipped for them.
		if !isGit && accountID != "" && !accountIDRe.MatchString(accountID) {
			return nil, fmt.Errorf("%w: account_id must be a 12-digit AWS account id", ErrInvalidValue)
		}
	case ScopeOrganization:
		// The management account is optional at create: when omitted it is
		// derived from the role ARN at verify, like the account-scope flow.
		if mgmt != "" && !accountIDRe.MatchString(mgmt) {
			return nil, fmt.Errorf("%w: management_account_id must be a 12-digit AWS account id", ErrInvalidValue)
		}
		if ou != "" && !ouIDRe.MatchString(ou) {
			return nil, fmt.Errorf("%w: organizational_unit_id must be an OU id (ou-...) or root id (r-...)", ErrInvalidValue)
		}
		// The organization connection's own account is the management account,
		// so a role ARN from any other account is rejected at verify.
		if accountID != "" && mgmt != "" && accountID != mgmt {
			return nil, fmt.Errorf("%w: account_id must match management_account_id for organization scope", ErrInvalidValue)
		}
		if mgmt != "" {
			accountID = mgmt
		}
	}

	ext := in.externalIDOverride
	if ext == "" {
		var err error
		if ext, err = m.externalID(); err != nil {
			return nil, err
		}
	}

	c := &Connection{
		OrgID:                in.OrgID,
		ProjectID:            in.ProjectID,
		Provider:             in.Provider,
		Scope:                scope,
		InstallationType:     inst,
		DeploymentMethod:     method,
		AccountID:            accountID,
		ManagementAccountID:  mgmt,
		OrganizationalUnitID: ou,
		ParentConnectionID:   in.parentConnectionID,
		RoleARN:              in.roleARNOverride,
		ExternalID:           ext,
		Region:               in.Region,
		Capabilities:         caps,
		Status:               StatusPending,
		CreatedAt:            m.now(),
	}
	if _, err := m.repo.Insert(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Get returns a tenant-scoped connection.
func (m *Manager) Get(ctx context.Context, orgID, id int64) (*Connection, error) {
	return m.repo.Get(ctx, orgID, id)
}

// List returns a tenant's connections.
func (m *Manager) List(ctx context.Context, orgID int64) ([]Connection, error) {
	return m.repo.ListByOrg(ctx, orgID)
}

// ExpandOrganization creates a per-member-account child connection for each
// supplied member account id, parented to the given organization connection.
// It is how an organization-scope connection fans out into the account-level
// connections that discovery runs against (one role per member account, created
// by the StackSet). Each child is an account-scope connection sharing the
// parent's project, region, and capabilities, with its own external id.
//
// Member account ids are supplied by the caller. Live enumeration via the AWS
// Organizations API is the documented follow-up; this method is the account
// fan-out that both the manual and API-driven paths feed.
// ExpandOrganization creates (or reuses) a per-member-account child connection
// for each member account, parented to the organization connection. Each child
// reuses the organization's ExternalId (the StackSet deployed the same role
// everywhere) and carries the member role ARN the StackSet created, so it can
// be verified without the customer pasting anything. Accounts that already have
// a child connection are skipped, so expand is safe to call repeatedly.
//
// When memberAccountIDs is empty and the provider can list organization
// accounts, Bellu enumerates them automatically from the management account.
func (m *Manager) ExpandOrganization(ctx context.Context, orgID, id int64, memberAccountIDs []string) ([]*Connection, error) {
	parent, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if parent.Scope != ScopeOrganization {
		return nil, fmt.Errorf("%w: connection %d is not organization-scoped", ErrInvalidValue, id)
	}

	p, err := m.resolveProvider(parent.Provider)
	if err != nil {
		return nil, err
	}

	// Auto-discover member accounts from the management account when none were
	// supplied and the provider supports it.
	if len(memberAccountIDs) == 0 {
		if lister, ok := p.(provider.OrganizationLister); ok {
			accts, lerr := lister.ListOrganizationAccounts(ctx, m.targetFor(parent))
			if lerr != nil {
				return nil, lerr
			}
			for _, a := range accts {
				// Skip the management account itself (it is the parent).
				if a.ID == parent.ManagementAccountID {
					continue
				}
				memberAccountIDs = append(memberAccountIDs, a.ID)
			}
		}
	}

	// Index existing children by account id so repeated expands are idempotent.
	existing := map[string]bool{}
	all, _ := m.repo.ListByOrg(ctx, orgID)
	for i := range all {
		if all[i].ParentConnectionID == parent.ID {
			existing[all[i].AccountID] = true
		}
	}

	arner, _ := p.(provider.MemberRoleARNer)
	children := make([]*Connection, 0, len(memberAccountIDs))
	for _, acct := range memberAccountIDs {
		if acct == "" || existing[acct] {
			continue
		}
		roleARN := ""
		if arner != nil {
			roleARN = arner.MemberRoleARN(acct)
		}
		child, err := m.Create(ctx, CreateInput{
			OrgID:              parent.OrgID,
			ProjectID:          parent.ProjectID,
			Provider:           parent.Provider,
			Scope:              ScopeAccount,
			InstallationType:   parent.InstallationType,
			DeploymentMethod:   parent.DeploymentMethod,
			AccountID:          acct,
			Region:             parent.Region,
			Capabilities:       parent.Capabilities,
			parentConnectionID: parent.ID,
			externalIDOverride: parent.ExternalID,
			roleARNOverride:    roleARN,
		})
		if err != nil {
			return children, err
		}
		children = append(children, child)
	}
	return children, nil
}

// GenerateSetup moves the connection to VERIFYING and returns the provider's
// onboarding artifacts.
func (m *Manager) GenerateSetup(ctx context.Context, orgID, id int64) (*provider.Setup, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	setup, err := p.CreateSetup(ctx, provider.SetupInput{
		ConnectionID:         c.ID,
		AccountID:            c.AccountID,
		ManagementAccountID:  c.ManagementAccountID,
		OrganizationalUnitID: c.OrganizationalUnitID,
		ExternalID:           c.ExternalID,
		Region:               c.Region,
		Scope:                string(c.Scope),
		DeploymentMethod:     string(c.DeploymentMethod),
		Capabilities:         c.Capabilities,
	})
	if err != nil {
		return nil, err
	}
	// Generating setup does not change the connection status: the customer still
	// has to deploy the role in AWS. The connection stays PENDING until Verify
	// runs (which moves it PENDING -> VERIFYING -> CONNECTED/ERROR), so the list
	// never shows a connection as "verifying" before a real verification.
	return setup, nil
}

// Verify persists the role ARN the customer supplies (after deploying the role),
// runs permission verification, and moves the connection to CONNECTED on success
// or ERROR on failure. On success it stamps last_verified_at. A roleARN is
// required: it is supplied here rather than at create time.
func (m *Manager) Verify(ctx context.Context, orgID, id int64, roleARN string) (*provider.VerifyResult, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}

	// The customer supplies the role ARN at verification time. For AWS, the
	// authoritative account id is the one embedded in the ARN: derive it,
	// validate the ARN, reject a mismatch against any already-known account,
	// and persist both.
	if roleARN != "" {
		if c.Provider == "aws" {
			acct, aerr := accountIDFromRoleARN(roleARN)
			if aerr != nil {
				return nil, aerr
			}
			// Reject a role ARN whose account contradicts a previously set one
			// (e.g. the management account on an organization connection, or a
			// re-verify with a different ARN).
			if c.AccountID != "" && c.AccountID != acct {
				return nil, fmt.Errorf("%w: role_arn account %s does not match connection account %s",
					ErrInvalidValue, acct, c.AccountID)
			}
			if c.AccountID != acct {
				if err := m.repo.SetAccountID(ctx, orgID, id, acct); err != nil {
					return nil, err
				}
				c.AccountID = acct
			}
			// An organization connection's role lives in the management
			// account, so the ARN's account is the management account.
			if c.Scope == ScopeOrganization && c.ManagementAccountID == "" {
				if err := m.repo.SetManagementAccountID(ctx, orgID, id, acct); err != nil {
					return nil, err
				}
				c.ManagementAccountID = acct
			}
		}
		if err := m.repo.SetRoleARN(ctx, orgID, id, roleARN); err != nil {
			return nil, err
		}
		c.RoleARN = roleARN
	}
	if c.RoleARN == "" {
		return nil, fmt.Errorf("%w: role_arn is required to verify", ErrInvalidValue)
	}

	// Move to VERIFYING before running the check. This works the first time
	// (from PENDING) and on a re-verify (from CONNECTED/COMPLETE/ERROR); only a
	// verify already in flight is left as-is.
	if c.Status != StatusVerifying {
		if err := m.transition(ctx, c, EventBeginVerify); err != nil {
			return nil, err
		}
	}

	res, verr := p.VerifyConnection(ctx, m.targetFor(c))
	if verr != nil || res == nil || !res.OK {
		_ = m.transition(ctx, c, EventVerifyFailed)
		if verr != nil {
			return res, verr
		}
		return res, nil
	}

	_ = m.repo.SaveVerification(ctx, c.ID, res)
	if err := m.transition(ctx, c, EventVerifySucceeded); err != nil {
		return res, err
	}
	_ = m.repo.SetVerified(ctx, orgID, id, m.now())
	return res, nil
}

// Discover moves a CONNECTED connection to DISCOVERY_RUNNING and runs a
// discovery session, then records the terminal status.
func (m *Manager) Discover(ctx context.Context, orgID, id int64) (*discovery.Session, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	// Git/CI connectors have no Steampipe tables: a "scan" refreshes the
	// repository inventory from the provider rather than running cloud
	// discovery. Route them to the git scan path so clicking Scan on a GitHub
	// connection does not fail against the (cloud-only) Steampipe orchestrator.
	if m.isGitConnector(c.Provider) {
		return m.scanGit(ctx, c, EventDiscover)
	}
	return m.runDiscovery(ctx, c, EventDiscover)
}

// Retry restarts discovery for a connection in ERROR.
func (m *Manager) Retry(ctx context.Context, orgID, id int64) (*discovery.Session, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if m.isGitConnector(c.Provider) {
		return m.scanGit(ctx, c, EventRetry)
	}
	return m.runDiscovery(ctx, c, EventRetry)
}

// isGitConnector reports whether a provider enumerates repositories (git/CI),
// in which case "scan" means refreshing repositories, not cloud discovery.
func (m *Manager) isGitConnector(providerName string) bool {
	p, err := m.resolveProvider(providerName)
	if err != nil {
		return false
	}
	_, ok := p.(provider.RepoLister)
	return ok
}

// scanGit performs a "scan" for a git/CI connector: it transitions the
// connection to DISCOVERY_RUNNING, refreshes the repository inventory from the
// provider, records a completed (or failed) session whose progress is expressed
// per repository (so the progress sheet shows the repo list), and transitions
// to the terminal state. It runs synchronously because repo enumeration is a
// handful of API calls, not a long Steampipe crawl.
func (m *Manager) scanGit(ctx context.Context, c *Connection, start Event) (*discovery.Session, error) {
	if c.Status == StatusDiscoveryRunning {
		return nil, ErrConflict
	}
	if err := m.transition(ctx, c, start); err != nil {
		return nil, err
	}

	now := m.now()
	sess := &discovery.Session{
		ID:           fmt.Sprintf("gitscan-%d", now.UnixNano()),
		ConnectionID: c.ID,
		RunID:        fmt.Sprintf("run-%d", now.UnixNano()),
		Status:       discovery.SessRunning,
		StartedAt:    now,
		Progress:     &discovery.Progress{Tables: []discovery.TableProgress{}},
	}
	m.recordSession(ctx, c.ID, sess)

	view, rerr := m.RefreshRepositories(ctx, c.OrgID, c.ID)

	end := m.now()
	sess.EndedAt = &end
	if rerr != nil {
		sess.Status = discovery.SessFailed
		sess.Error = rerr.Error()
		m.recordSession(ctx, c.ID, sess)
		m.transitionByID(ctx, c.OrgID, c.ID, EventDiscoverFailed)
		return sess, rerr
	}

	// Express the result as per-repo progress so the progress UI (which renders
	// a "tables" list) shows each repository as a completed row.
	repos := view.Repos
	tables := make([]discovery.TableProgress, 0, len(repos))
	selected := 0
	for _, r := range repos {
		if r.Selected {
			selected++
		}
		tables = append(tables, discovery.TableProgress{
			Table: r.FullName, State: discovery.TableDone, Rows: 1,
		})
	}
	sess.Status = discovery.SessComplete
	sess.Progress = &discovery.Progress{
		TablesTotal:        len(repos),
		TablesDone:         len(repos),
		ResourcesCollected: selected,
		Tables:             tables,
	}
	m.recordSession(ctx, c.ID, sess)
	if data, err := json.Marshal(sess.Progress); err == nil {
		_ = m.repo.SaveSessionProgress(ctx, sess.ID, data)
	}
	m.transitionByID(ctx, c.OrgID, c.ID, EventDiscoverDone)
	return sess, nil
}

// runDiscovery starts discovery asynchronously: it resolves the provider,
// transitions the connection to DISCOVERY_RUNNING, then launches the run in the
// background and returns the running session immediately. When the run reaches a
// terminal state, the connection status is transitioned and the session is
// recorded on a background context (the request context is gone by then).
//
// A session already running for the connection is reported as ErrConflict.
func (m *Manager) runDiscovery(ctx context.Context, c *Connection, start Event) (*discovery.Session, error) {
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	// A connection already in DISCOVERY_RUNNING has a session in flight; starting
	// another is a conflict. Checking the status here (rather than relying on the
	// FSM to reject the transition) gives a clear ErrConflict instead of a
	// generic invalid-transition error.
	if c.Status == StatusDiscoveryRunning {
		return nil, ErrConflict
	}
	// Transition to DISCOVERY_RUNNING before launching the run so a concurrent
	// request observes the running status and conflicts.
	if err := m.transition(ctx, c, start); err != nil {
		return nil, err
	}

	// Capture the fields the terminal callback needs; the Connection pointer is
	// request-scoped and must not be touched from the background goroutine.
	orgID, connID := c.OrgID, c.ID
	onDone := func(sess *discovery.Session, derr error) {
		bg := context.Background()
		if sess != nil {
			m.recordSession(bg, connID, sess)
		}
		if derr != nil || (sess != nil && sess.Status == discovery.SessFailed) {
			m.transitionByID(bg, orgID, connID, EventDiscoverFailed)
			return
		}
		m.transitionByID(bg, orgID, connID, EventDiscoverDone)
	}

	sess, serr := m.disc.DiscoverAsync(p, m.targetFor(c), onDone)
	if serr != nil {
		// The orchestrator also guards against a running session (belt and
		// suspenders with the status check above). Roll the connection back out
		// of DISCOVERY_RUNNING so it is not stuck, and report the conflict.
		if errors.Is(serr, discovery.ErrSessionRunning) {
			m.transitionByID(ctx, c.OrgID, c.ID, EventDiscoverFailed)
			return nil, ErrConflict
		}
		m.transitionByID(ctx, c.OrgID, c.ID, EventDiscoverFailed)
		return nil, serr
	}

	// Durability: record the running session row immediately (so progress
	// UPDATEs have a row to hit) and persist progress snapshots throughout the
	// run. This is what lets the UI rehydrate live progress after a browser
	// reload or a bellu-api restart.
	bg := context.Background()
	m.recordSession(bg, connID, sess)
	m.persistProgress(connID, sess.ID)

	return sess, nil
}

// persistProgress subscribes to the live progress of a running session and
// writes throttled snapshots to Postgres, plus a final snapshot when the run
// ends. It runs in its own goroutine for the life of the session. Writes are
// coalesced to at most one per progressPersistInterval so a fast run does not
// hammer the database; the final snapshot is always written.
func (m *Manager) persistProgress(connID int64, sessionID string) {
	ch, cancel, ok := m.disc.Subscribe(connID)
	if !ok {
		return
	}
	go func() {
		defer cancel()
		bg := context.Background()
		var last *discovery.Progress
		var lastWrite time.Time
		for prog := range ch {
			last = prog
			if m.now().Sub(lastWrite) < progressPersistInterval {
				continue
			}
			lastWrite = m.now()
			if data, err := json.Marshal(prog); err == nil {
				_ = m.repo.SaveSessionProgress(bg, sessionID, data)
			}
		}
		// Channel closed: the session reached a terminal state. Persist the
		// final snapshot so the completed counts survive.
		if last != nil {
			if data, err := json.Marshal(last); err == nil {
				_ = m.repo.SaveSessionProgress(bg, sessionID, data)
			}
		}
	}()
}

// UpdateCapabilities replaces the enabled capability set on an existing
// connection. It validates each capability, requires that "core" stays enabled
// (it is always-on), and refuses to change capabilities while a scan is running
// (the running scan already captured its capability set). The new set takes
// effect on the next discovery run, since discovery reads the persisted set.
func (m *Manager) UpdateCapabilities(ctx context.Context, orgID, id int64, caps []string) (*Connection, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if c.Status == StatusDiscoveryRunning {
		return nil, fmt.Errorf("%w: cannot change capabilities while a scan is running", ErrConflict)
	}
	// Normalize: validate each, dedupe, and ensure core is present.
	seen := map[string]bool{}
	out := make([]string, 0, len(caps)+1)
	for _, cap := range caps {
		if !capability.Known(cap) {
			return nil, fmt.Errorf("%w: capability %q", ErrInvalidValue, cap)
		}
		if !seen[cap] {
			seen[cap] = true
			out = append(out, cap)
		}
	}
	if !seen[string(capability.Core)] {
		out = append([]string{string(capability.Core)}, out...)
	}
	if err := m.repo.SetCapabilities(ctx, orgID, id, out); err != nil {
		return nil, err
	}
	c.Capabilities = out
	return c, nil
}

// ReconcileInterrupted marks any session still 'running' (orphaned by a prior
// crash/restart) as interrupted. Call once at service startup. Returns the
// number of sessions reconciled.
func (m *Manager) ReconcileInterrupted(ctx context.Context) (int, error) {
	return m.repo.ReconcileRunningSessions(ctx)
}

// RepositoriesView is a git connection's repositories plus its selection mode.
type RepositoriesView struct {
	Mode  RepoSelectionMode `json:"mode"`
	Repos []Repo            `json:"repositories"`
}

// RefreshRepositories asks the connection's provider to enumerate the repos it
// can access and upserts them (new repos default to selected = collect-all).
// It is a no-op returning the stored list for providers that are not git/CI
// (do not implement provider.RepoLister). Returns the current view.
func (m *Manager) RefreshRepositories(ctx context.Context, orgID, id int64) (*RepositoriesView, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	if lister, ok := p.(provider.RepoLister); ok {
		tgt := m.targetFor(c)
		// For a GitHub connection, the installation id lives in the stored
		// installation record (bound to this tenant+connection). Pass it to the
		// adapter so it can mint an installation token and enumerate repos. The
		// id is never taken from a client-supplied value.
		if inst, ok, ierr := m.repo.GetGitHubInstallationByConnection(ctx, orgID, id); ierr == nil && ok && inst != nil {
			tgt.InstallationID = inst.GitHubInstallationID
		}
		repos, lerr := lister.ListRepositories(ctx, tgt)
		if lerr != nil {
			return nil, lerr
		}
		mapped := make([]Repo, 0, len(repos))
		for _, r := range repos {
			mapped = append(mapped, Repo{
				ExternalID: r.ExternalID, FullName: r.FullName, URL: r.URL,
				DefaultBranch: r.DefaultBranch, Private: r.Private,
			})
		}
		if err := m.repo.SaveRepositories(ctx, id, mapped); err != nil {
			return nil, err
		}
	}
	return m.Repositories(ctx, orgID, id)
}

// Repositories returns the stored repositories and selection mode for a git
// connection. Non-git connections return an empty list with mode "all".
func (m *Manager) Repositories(ctx context.Context, orgID, id int64) (*RepositoriesView, error) {
	if _, err := m.repo.Get(ctx, orgID, id); err != nil {
		return nil, err
	}
	mode, err := m.repo.GetRepoSelectionMode(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := m.repo.ListRepositories(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RepositoriesView{Mode: mode, Repos: repos}, nil
}

// SetRepositorySelection toggles collection for specific repos of a git
// connection. selected=true adds them to the collected set. Every requested
// repo id is validated against the connection's actual accessible set (the
// repos stored from the installation), so a caller cannot select a repository
// outside the installation's access (IDOR / out-of-scope guard).
func (m *Manager) SetRepositorySelection(ctx context.Context, orgID, id int64, repoIDs []int64, selected bool) error {
	if _, err := m.repo.Get(ctx, orgID, id); err != nil {
		return err
	}
	if len(repoIDs) == 0 {
		return nil
	}
	known, err := m.repo.ListRepositories(ctx, id)
	if err != nil {
		return err
	}
	valid := make(map[int64]bool, len(known))
	for _, r := range known {
		valid[r.ID] = true
	}
	for _, rid := range repoIDs {
		if !valid[rid] {
			return fmt.Errorf("%w: repository %d is not in this connection's accessible set", ErrInvalidValue, rid)
		}
	}
	return m.repo.SetRepositorySelection(ctx, id, repoIDs, selected)
}

// SetRepoSelectionMode switches a git connection between collecting all repos
// and only the selected ones. Switching to "all" also marks every repo
// selected so the stored set matches the mode.
func (m *Manager) SetRepoSelectionMode(ctx context.Context, orgID, id int64, mode RepoSelectionMode) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: repo selection mode %q", ErrInvalidValue, mode)
	}
	if _, err := m.repo.Get(ctx, orgID, id); err != nil {
		return err
	}
	if err := m.repo.SetRepoSelectionMode(ctx, id, mode); err != nil {
		return err
	}
	if mode == RepoModeAll {
		return m.repo.SetAllRepositoriesSelected(ctx, id, true)
	}
	return nil
}

// transitionByID loads the connection by id and applies an event. It is used by
// the async terminal callback, which runs after the request-scoped Connection
// pointer is no longer safe to use.
func (m *Manager) transitionByID(ctx context.Context, orgID, id int64, e Event) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return
	}
	_ = m.transition(ctx, c, e)
}

// SubscribeProgress subscribes to live progress for a connection's latest
// discovery session. It verifies the connection belongs to the org, then
// returns a channel of progress snapshots (first is the current snapshot), a
// cancel func, and ok=false when no live session exists. The caller must call
// cancel when done. Subscribing never affects the running session.
func (m *Manager) SubscribeProgress(ctx context.Context, orgID, id int64) (<-chan *discovery.Progress, func(), bool, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, nil, false, err
	}
	ch, cancel, ok := m.disc.Subscribe(c.ID)
	return ch, cancel, ok, nil
}

// Cancel stops a running discovery session for the connection.
func (m *Manager) Cancel(ctx context.Context, orgID, id int64) error {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	return m.disc.Cancel(c.ID)
}

// Delete removes a tenant-scoped connection.
func (m *Manager) Delete(ctx context.Context, orgID, id int64) error {
	return m.repo.Delete(ctx, orgID, id)
}

// StatusView is the connection status plus its latest discovery session.
type StatusView struct {
	ConnectionID  int64              `json:"connection_id"`
	Status        Status             `json:"status"`
	LatestSession *discovery.Session `json:"latest_session,omitempty"`
}

// Status returns the current connection status and the latest session state.
func (m *Manager) Status(ctx context.Context, orgID, id int64) (*StatusView, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	v := &StatusView{ConnectionID: c.ID, Status: c.Status}
	// Prefer the live in-memory session (this process is running the scan).
	// LatestView returns a race-free deep copy with a fresh progress snapshot.
	if sess, ok := m.disc.LatestView(c.ID); ok {
		v.LatestSession = sess
		return v, nil
	}
	// No live session in this process: rehydrate the last session from Postgres
	// so the UI still shows progress after a browser reload or an API restart.
	if row, err := m.repo.LatestSession(ctx, c.ID); err == nil && row != nil {
		v.LatestSession = sessionFromRow(row)
	}
	return v, nil
}

// sessionFromRow converts a persisted session row into a discovery.Session for
// the status view, decoding the stored progress JSON. A row still marked
// 'running' here means the owning process is gone (restart/crash) — but startup
// reconciliation already flips those to 'interrupted', so a 'running' row seen
// post-restart is defensively surfaced as interrupted too.
func sessionFromRow(row *DiscoverySessionRow) *discovery.Session {
	status := discovery.SessionStatus(row.Status)
	if status == discovery.SessRunning {
		status = discovery.SessInterrupted
	}
	s := &discovery.Session{
		ID:           row.ID,
		ConnectionID: row.ConnectionID,
		RunID:        row.RunID,
		Status:       status,
		StartedAt:    row.StartedAt,
		EndedAt:      row.EndedAt,
		Error:        row.Error,
	}
	if len(row.Progress) > 0 {
		var p discovery.Progress
		if err := json.Unmarshal(row.Progress, &p); err == nil {
			s.Progress = &p
		}
	}
	return s
}

// resolveProvider returns the registered provider or ErrUnsupportedProvider.
func (m *Manager) resolveProvider(name string) (provider.CloudProvider, error) {
	p, ok := m.registry.Resolve(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, name)
	}
	return p, nil
}

// transition applies an event to the connection's status (via Status.Next) and
// persists the new status, keeping the in-memory copy in sync.
func (m *Manager) transition(ctx context.Context, c *Connection, e Event) error {
	next, err := c.Status.Next(e)
	if err != nil {
		return err
	}
	if err := m.repo.UpdateStatus(ctx, c.OrgID, c.ID, next); err != nil {
		return err
	}
	c.Status = next
	return nil
}

func (m *Manager) targetFor(c *Connection) provider.Target {
	return provider.Target{
		ConnectionID: c.ID,
		OrgID:        c.OrgID,
		ProjectID:    c.ProjectID,
		AccountID:    c.AccountID,
		RoleARN:      c.RoleARN,
		ExternalID:   c.ExternalID,
		Region:       c.Region,
		Capabilities: c.Capabilities,
	}
}

func (m *Manager) recordSession(ctx context.Context, connID int64, s *discovery.Session) {
	_ = m.repo.RecordSession(ctx, connID, s.ID, s.RunID, string(s.Status), s.StartedAt, s.EndedAt, s.Error)
}
