package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

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
}

// Options configures a Manager.
type Options struct {
	Repo       connRepo
	Registry   *provider.Registry
	Discovery  *discovery.Orchestrator
	ExternalID ExternalIDFunc
	Now        func() time.Time
}

// NewManager builds a Manager.
func NewManager(opts Options) *Manager {
	m := &Manager{
		repo:       opts.Repo,
		registry:   opts.Registry,
		disc:       opts.Discovery,
		externalID: opts.ExternalID,
		now:        opts.Now,
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
	if _, err := m.resolveProvider(in.Provider); err != nil {
		return nil, err
	}

	caps := in.Capabilities
	if len(caps) == 0 {
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
		method = DeploymentCloudFormation
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
		if accountID != "" && !accountIDRe.MatchString(accountID) {
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
	return m.runDiscovery(ctx, c, EventDiscover)
}

// Retry restarts discovery for a connection in ERROR.
func (m *Manager) Retry(ctx context.Context, orgID, id int64) (*discovery.Session, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	return m.runDiscovery(ctx, c, EventRetry)
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
	return sess, nil
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
	// LatestView returns a race-free deep copy of the latest session with a
	// fresh progress snapshot (live while running, frozen once terminal), so the
	// status poll can serialize it without racing the collector goroutine.
	if sess, ok := m.disc.LatestView(c.ID); ok {
		v.LatestSession = sess
	}
	return v, nil
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
