package connector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// memRepo is an in-memory connRepo for manager unit tests.
type memRepo struct {
	seq      int64
	conns    map[int64]*Connection
	saved    map[int64]*provider.VerifyResult
	verified map[int64]time.Time
}

func newMemRepo() *memRepo {
	return &memRepo{conns: map[int64]*Connection{}, saved: map[int64]*provider.VerifyResult{}, verified: map[int64]time.Time{}}
}

func (r *memRepo) Insert(ctx context.Context, c *Connection) (int64, error) {
	r.seq++
	c.ID = r.seq
	cp := *c
	r.conns[c.ID] = &cp
	return c.ID, nil
}
func (r *memRepo) Get(ctx context.Context, orgID, id int64) (*Connection, error) {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}
func (r *memRepo) ListByOrg(ctx context.Context, orgID int64) ([]Connection, error) {
	var out []Connection
	for _, c := range r.conns {
		if c.OrgID == orgID {
			out = append(out, *c)
		}
	}
	return out, nil
}
func (r *memRepo) UpdateStatus(ctx context.Context, orgID, id int64, s Status) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	c.Status = s
	return nil
}
func (r *memRepo) SetVerified(ctx context.Context, orgID, id int64, t time.Time) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	r.verified[id] = t
	c.LastVerifiedAt = &t
	return nil
}
func (r *memRepo) Delete(ctx context.Context, orgID, id int64) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	delete(r.conns, id)
	return nil
}
func (r *memRepo) SaveVerification(ctx context.Context, connID int64, res *provider.VerifyResult) error {
	r.saved[connID] = res
	return nil
}
func (r *memRepo) RecordSession(ctx context.Context, connID int64, sessionID, runID, status string, started time.Time, ended *time.Time, errMsg string) error {
	return nil
}

// mgrFakeProvider is a configurable CloudProvider for manager tests.
type mgrFakeProvider struct {
	name      string
	verifyRes *provider.VerifyResult
	verifyErr error
}

func (p *mgrFakeProvider) Name() string {
	if p.name == "" {
		return "aws"
	}
	return p.name
}
func (p *mgrFakeProvider) CreateSetup(context.Context, provider.SetupInput) (*provider.Setup, error) {
	return &provider.Setup{RoleName: "BelluConnectorRole"}, nil
}
func (p *mgrFakeProvider) VerifyConnection(context.Context, provider.Target) (*provider.VerifyResult, error) {
	return p.verifyRes, p.verifyErr
}
func (p *mgrFakeProvider) Authenticate(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *mgrFakeProvider) GetCredentials(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *mgrFakeProvider) CheckPermissions(context.Context, provider.Target, *provider.Credentials, []string) (*provider.VerifyResult, error) {
	return p.verifyRes, p.verifyErr
}
func (p *mgrFakeProvider) Discover(context.Context, provider.Target, *provider.Credentials) (*provider.DiscoveryPlan, error) {
	return &provider.DiscoveryPlan{SteampipeConfig: map[string]any{}, CredentialEnv: map[string]string{}}, nil
}

// discCreds satisfies discovery.Credentialer.
type discCreds struct{}

func (discCreds) Get(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (discCreds) Revoke(int64) {}

// okRunner is a PipelineRunner that always succeeds.
type okRunner struct{}

func (okRunner) Run(context.Context, source.Source, string) error { return nil }

func newManager(t *testing.T, repo connRepo, p provider.CloudProvider) *Manager {
	reg := provider.NewRegistry()
	if p != nil {
		reg.Register(p)
	}
	orch := discovery.New(discovery.Options{
		Factory: func(map[string]any) (source.Source, error) { return nil, nil },
		Runner:  okRunner{},
		Creds:   discCreds{},
	})
	return NewManager(Options{
		Repo:       repo,
		Registry:   reg,
		Discovery:  orch,
		ExternalID: func() (string, error) { return "BELLU-FIXED", nil },
		Now:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
}

func createInput() CreateInput {
	return CreateInput{
		OrgID:     1,
		ProjectID: 2,
		Provider:  "aws",
		AccountID: "123456789012",
		RoleARN:   "arn:aws:iam::123456789012:role/BelluConnectorRole",
		Region:    "us-east-1",
	}
}

// TestCreateDefaultsCoreAndPending asserts a created connection defaults to the
// core capability, PENDING status, and a stamped created_at + external id.
func TestCreateDefaultsCoreAndPending(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{})
	c, err := m.Create(context.Background(), createInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Status != StatusPending {
		t.Errorf("status = %s, want PENDING", c.Status)
	}
	if len(c.Capabilities) != 1 || c.Capabilities[0] != "core" {
		t.Errorf("capabilities = %v, want [core]", c.Capabilities)
	}
	if c.ExternalID != "BELLU-FIXED" || c.CreatedAt.IsZero() {
		t.Errorf("external id / created_at not set: %+v", c)
	}
}

// TestCreateUnsupportedProvider asserts an unregistered provider is rejected.
func TestCreateUnsupportedProvider(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{name: "aws"})
	in := createInput()
	in.Provider = "gcp"
	_, err := m.Create(context.Background(), in)
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Errorf("want ErrUnsupportedProvider, got %v", err)
	}
}

// TestGenerateSetupMovesToVerifying asserts setup generation transitions the
// connection PENDING -> VERIFYING.
func TestGenerateSetupMovesToVerifying(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{})
	c, _ := m.Create(context.Background(), createInput())

	setup, err := m.GenerateSetup(context.Background(), c.OrgID, c.ID)
	if err != nil {
		t.Fatalf("generate setup: %v", err)
	}
	if setup.RoleName != "BelluConnectorRole" {
		t.Errorf("unexpected setup %+v", setup)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusVerifying {
		t.Errorf("status = %s, want VERIFYING", got.Status)
	}
}

// TestVerifySuccess asserts a passing verify moves to CONNECTED and stamps
// last_verified_at.
func TestVerifySuccess(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())

	res, err := m.Verify(context.Background(), c.OrgID, c.ID)
	if err != nil || !res.OK {
		t.Fatalf("verify: res=%+v err=%v", res, err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusConnected {
		t.Errorf("status = %s, want CONNECTED", got.Status)
	}
	if got.LastVerifiedAt == nil {
		t.Error("last_verified_at should be set on verify success")
	}
	if repo.saved[c.ID] == nil {
		t.Error("verification results should be saved on success")
	}
}

// TestVerifyFailureMovesToError asserts a failing verify moves to ERROR.
func TestVerifyFailureMovesToError(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: false}})
	c, _ := m.Create(context.Background(), createInput())

	res, err := m.Verify(context.Background(), c.OrgID, c.ID)
	if err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
	if res.OK {
		t.Error("verify result should be not OK")
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusError {
		t.Errorf("status = %s, want ERROR", got.Status)
	}
}

// TestDiscoverLifecycle asserts a CONNECTED connection runs discovery to
// COMPLETE.
func TestDiscoverLifecycle(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}

	sess, err := m.Discover(context.Background(), c.OrgID, c.ID)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if sess.Status != discovery.SessComplete {
		t.Errorf("session status = %s, want complete", sess.Status)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusComplete {
		t.Errorf("connection status = %s, want COMPLETE", got.Status)
	}
}

// TestGetTenantScoped asserts Get is tenant-scoped.
func TestGetTenantScoped(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Get(context.Background(), 999, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get: want ErrNotFound, got %v", err)
	}
}
