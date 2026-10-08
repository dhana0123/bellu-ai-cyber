package connector

import (
	"context"
	"fmt"
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

// CreateInput carries the fields needed to create a connection.
type CreateInput struct {
	OrgID        int64
	ProjectID    int64
	Provider     string
	AccountID    string
	RoleARN      string
	Region       string
	Capabilities []string
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

	ext, err := m.externalID()
	if err != nil {
		return nil, err
	}

	c := &Connection{
		OrgID:        in.OrgID,
		ProjectID:    in.ProjectID,
		Provider:     in.Provider,
		AccountID:    in.AccountID,
		RoleARN:      in.RoleARN,
		ExternalID:   ext,
		Region:       in.Region,
		Capabilities: caps,
		Status:       StatusPending,
		CreatedAt:    m.now(),
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
		ConnectionID: c.ID,
		AccountID:    c.AccountID,
		ExternalID:   c.ExternalID,
		Region:       c.Region,
		Capabilities: c.Capabilities,
	})
	if err != nil {
		return nil, err
	}
	if err := m.transition(ctx, c, EventBeginVerify); err != nil {
		return nil, err
	}
	return setup, nil
}

// Verify runs permission verification and moves the connection to CONNECTED on
// success or ERROR on failure. On success it stamps last_verified_at.
func (m *Manager) Verify(ctx context.Context, orgID, id int64) (*provider.VerifyResult, error) {
	c, err := m.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}

	// Allow verify from PENDING by first moving to VERIFYING if needed.
	if c.Status == StatusPending {
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

func (m *Manager) runDiscovery(ctx context.Context, c *Connection, start Event) (*discovery.Session, error) {
	p, err := m.resolveProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	if err := m.transition(ctx, c, start); err != nil {
		return nil, err
	}

	sess, derr := m.disc.Discover(ctx, p, m.targetFor(c))
	if sess != nil {
		m.recordSession(ctx, c.ID, sess)
	}
	if derr != nil || (sess != nil && sess.Status == discovery.SessFailed) {
		_ = m.transition(ctx, c, EventDiscoverFailed)
		return sess, derr
	}
	_ = m.transition(ctx, c, EventDiscoverDone)
	return sess, nil
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
	if sess, ok := m.disc.Latest(c.ID); ok {
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
