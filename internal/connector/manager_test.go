package connector

import (
	"context"
	"errors"
	"fmt"
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
func (r *memRepo) SetAccountID(ctx context.Context, orgID, id int64, accountID string) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	c.AccountID = accountID
	return nil
}
func (r *memRepo) SetManagementAccountID(ctx context.Context, orgID, id int64, accountID string) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	c.ManagementAccountID = accountID
	return nil
}
func (r *memRepo) SetRoleARN(ctx context.Context, orgID, id int64, roleARN string) error {
	c, ok := r.conns[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	c.RoleARN = roleARN
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
		ExternalID: newExtIDGen(),
		Now:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
}

// newExtIDGen returns a deterministic but unique external-id generator for
// tests (production uses crypto/rand).
func newExtIDGen() func() (string, error) {
	n := 0
	return func() (string, error) {
		n++
		return fmt.Sprintf("BELLU-TEST-%d", n), nil
	}
}

func createInput() CreateInput {
	return CreateInput{
		OrgID:     1,
		ProjectID: 2,
		Provider:  "aws",
		AccountID: "123456789012",
		Region:    "us-east-1",
	}
}

const testRoleARN = "arn:aws:iam::123456789012:role/BelluConnectorRole"

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
	// Default selection is core plus the read-only compute capabilities so
	// discovery can query EKS/ECS/Lambda without AccessDenied.
	wantCaps := map[string]bool{"core": true, "eks": true, "workload": true, "ai": true}
	if len(c.Capabilities) != len(wantCaps) {
		t.Errorf("capabilities = %v, want %d", c.Capabilities, len(wantCaps))
	}
	for _, cap := range c.Capabilities {
		if !wantCaps[cap] {
			t.Errorf("unexpected default capability %q", cap)
		}
	}
	if c.ExternalID == "" || c.CreatedAt.IsZero() {
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
// connection at PENDING (the role is not deployed yet).
func TestGenerateSetupKeepsPending(t *testing.T) {
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
	if got.Status != StatusPending {
		t.Errorf("status = %s, want PENDING (role not deployed yet)", got.Status)
	}
	// Re-running setup is still safe and keeps PENDING.
	if _, err := m.GenerateSetup(context.Background(), c.OrgID, c.ID); err != nil {
		t.Fatalf("re-generate setup: %v", err)
	}
	got, _ = repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusPending {
		t.Errorf("after re-generate status = %s, want PENDING", got.Status)
	}
}

// TestVerifySuccess asserts a passing verify moves to CONNECTED and stamps
// last_verified_at.
func TestVerifySuccess(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())

	res, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN)
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

	res, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN)
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
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
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

// TestCreateDefaultsScopeAndDeployment asserts create defaults scope=account,
// installation=standard, deployment=cloudformation, and no role ARN yet.
func TestCreateDefaultsScopeAndDeployment(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{})
	c, err := m.Create(context.Background(), createInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Scope != ScopeAccount || c.InstallationType != InstallationStandard || c.DeploymentMethod != DeploymentCloudFormation {
		t.Errorf("defaults wrong: scope=%s inst=%s method=%s", c.Scope, c.InstallationType, c.DeploymentMethod)
	}
	if c.RoleARN != "" {
		t.Errorf("role arn should be empty at create, got %q", c.RoleARN)
	}
}

// TestCreateRejectsBadDeployment asserts an unknown deployment method is
// rejected.
func TestCreateRejectsBadDeployment(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{})
	in := createInput()
	in.DeploymentMethod = DeploymentMethod("ansible")
	if _, err := m.Create(context.Background(), in); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("want ErrInvalidValue for bad deployment, got %v", err)
	}
}

// TestVerifyRequiresRoleARN asserts verify without a stored or supplied ARN
// fails.
func TestVerifyRequiresRoleARN(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, ""); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("verify without role arn should fail, got %v", err)
	}
}

// TestVerifyPersistsRoleARN asserts the ARN supplied at verify is stored.
func TestVerifyPersistsRoleARN(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.RoleARN != testRoleARN {
		t.Errorf("role arn not persisted: %q", got.RoleARN)
	}
	if got.Status != StatusConnected {
		t.Errorf("status = %s, want CONNECTED", got.Status)
	}
}

// TestOrgCreateRequiresManagementAccount asserts organization scope needs a
// management account id.
func TestOrgCreateWithoutManagementAccount(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	in := createInputNoAccount()
	in.Scope = ScopeOrganization
	c, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("org create without management account should succeed: %v", err)
	}
	if c.ManagementAccountID != "" || c.AccountID != "" {
		t.Fatalf("expected empty account ids at create, got %+v", c)
	}
	arn := "arn:aws:iam::111111111111:role/BelluConnectorRole"
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, arn); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.AccountID != "111111111111" || got.ManagementAccountID != "111111111111" {
		t.Errorf("want account+management derived from ARN, got account=%q mgmt=%q", got.AccountID, got.ManagementAccountID)
	}
}

// TestExpandOrganization asserts an org connection fans out into per-member
// account connections parented to it.
func TestExpandOrganization(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{})
	in := createInputNoAccount()
	in.Scope = ScopeOrganization
	in.ManagementAccountID = "111111111111"
	parent, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	children, err := m.ExpandOrganization(context.Background(), parent.OrgID, parent.ID,
		[]string{"222222222222", "333333333333"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("want 2 children, got %d", len(children))
	}
	for _, ch := range children {
		if ch.Scope != ScopeAccount {
			t.Errorf("child %d scope = %s, want account", ch.ID, ch.Scope)
		}
		if ch.ParentConnectionID != parent.ID {
			t.Errorf("child %d parent = %d, want %d", ch.ID, ch.ParentConnectionID, parent.ID)
		}
		if ch.ExternalID != parent.ExternalID {
			t.Error("child must reuse the organization's external id")
		}
	}
}

// TestExpandNonOrgRejected asserts expanding an account connection fails.
func TestExpandNonOrgRejected(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{})
	c, _ := m.Create(context.Background(), createInput()) // account scope
	if _, err := m.ExpandOrganization(context.Background(), c.OrgID, c.ID, []string{"222222222222"}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("expanding non-org connection should fail, got %v", err)
	}
}

// TestVerifyDerivesAccountFromARN asserts an account-scope connection created
// without an account id gets it from the role ARN at verify.
func TestVerifyDerivesAccountFromARN(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	in := createInput()
	in.AccountID = "" // account scope: no account id at create
	c, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.AccountID != "" {
		t.Fatalf("account id should be empty at create, got %q", c.AccountID)
	}
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.AccountID != "123456789012" {
		t.Errorf("account id = %q, want 123456789012 (derived from ARN)", got.AccountID)
	}
}

// TestVerifyRejectsAccountMismatch asserts a role ARN whose account contradicts
// a connection's existing account is rejected.
func TestVerifyRejectsAccountMismatch(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	in := createInput()
	in.AccountID = "999999999999" // pre-set a different account
	c, _ := m.Create(context.Background(), in)
	_, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN) // ARN account 123456789012
	if !errors.Is(err, ErrInvalidValue) {
		t.Errorf("want ErrInvalidValue on account mismatch, got %v", err)
	}
}

// TestVerifyRejectsMalformedARN asserts a non-ARN string is rejected at verify.
func TestVerifyRejectsMalformedARN(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInputNoAccount())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, "not-an-arn"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("want ErrInvalidValue on malformed ARN, got %v", err)
	}
}

func createInputNoAccount() CreateInput {
	in := createInput()
	in.AccountID = ""
	return in
}

// TestAccountScopeDropsOrgInputs asserts organization-only inputs are discarded
// for account scope, so a stray value can't leak into the single-account flow.
func TestAccountScopeDropsOrgInputs(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{})
	in := createInputNoAccount()
	in.ManagementAccountID = "111111111111"
	in.OrganizationalUnitID = "ou-abcd-12345678"
	c, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ManagementAccountID != "" || c.OrganizationalUnitID != "" || c.AccountID != "" {
		t.Errorf("account scope kept org/account inputs: %+v", c)
	}
}

// TestOrgScopeValidation pins the organization-scope input rules.
func TestOrgScopeValidation(t *testing.T) {
	cases := []struct {
		name string
		mgmt string
		ou   string
		ok   bool
	}{
		{"valid mgmt, no ou", "111111111111", "", true},
		{"valid mgmt, ou", "111111111111", "ou-abcd-12345678", true},
		{"valid mgmt, root", "111111111111", "r-ab12", true},
		{"missing mgmt (derived at verify)", "", "", true},
		{"short mgmt", "1111", "", false},
		{"bad ou", "111111111111", "not-an-ou", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newManager(t, newMemRepo(), &mgrFakeProvider{})
			in := createInputNoAccount()
			in.Scope = ScopeOrganization
			in.ManagementAccountID = tc.mgmt
			in.OrganizationalUnitID = tc.ou
			c, err := m.Create(context.Background(), in)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("want ErrInvalidValue, got %v", err)
			}
			if tc.ok && tc.mgmt != "" && c.AccountID != tc.mgmt {
				t.Errorf("org account = %q, want management account %q", c.AccountID, tc.mgmt)
			}
		})
	}
}

// TestOrgVerifyRejectsNonManagementARN asserts an organization connection only
// accepts a role ARN from its management account.
func TestOrgVerifyRejectsNonManagementARN(t *testing.T) {
	m := newManager(t, newMemRepo(), &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	in := createInputNoAccount()
	in.Scope = ScopeOrganization
	in.ManagementAccountID = "111111111111"
	c, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// testRoleARN is in account 123456789012, not the management account.
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("want ErrInvalidValue for non-management ARN, got %v", err)
	}
	ok := "arn:aws:iam::111111111111:role/BelluConnectorRole"
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, ok); err != nil {
		t.Errorf("management-account ARN should verify, got %v", err)
	}
}

// orgFakeProvider is a mgrFakeProvider that also lists org accounts.
type orgFakeProvider struct {
	mgrFakeProvider
	accounts []provider.OrgAccount
}

func (p *orgFakeProvider) ListOrganizationAccounts(context.Context, provider.Target) ([]provider.OrgAccount, error) {
	return p.accounts, nil
}
func (p *orgFakeProvider) MemberRoleARN(acct string) string {
	return "arn:aws:iam::" + acct + ":role/BelluConnectorRole"
}

// TestExpandReusesExternalIDAndSetsRoleARN asserts children reuse the parent's
// ExternalId and carry the member role ARN.
func TestExpandReusesExternalIDAndSetsRoleARN(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{})
	in := createInputNoAccount()
	in.Scope = ScopeOrganization
	in.ManagementAccountID = "111111111111"
	parent, err := m.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	kids, err := m.ExpandOrganization(context.Background(), parent.OrgID, parent.ID, []string{"222222222222"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(kids) != 1 {
		t.Fatalf("want 1 child, got %d", len(kids))
	}
	if kids[0].ExternalID != parent.ExternalID {
		t.Errorf("child ExternalId %q != parent %q", kids[0].ExternalID, parent.ExternalID)
	}
	// mgrFakeProvider is not a MemberRoleARNer, so no ARN is set here.
	// Idempotency: expanding the same account again adds nothing.
	again, err := m.ExpandOrganization(context.Background(), parent.OrgID, parent.ID, []string{"222222222222"})
	if err != nil {
		t.Fatalf("expand again: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("re-expand should be idempotent, created %d", len(again))
	}
}

// TestExpandAutoDiscovers asserts an empty member list triggers provider-driven
// account enumeration, skipping the management account, with member role ARNs.
func TestExpandAutoDiscovers(t *testing.T) {
	repo := newMemRepo()
	p := &orgFakeProvider{accounts: []provider.OrgAccount{
		{ID: "111111111111", Name: "mgmt", State: "ACTIVE"},
		{ID: "222222222222", Name: "prod", State: "ACTIVE"},
		{ID: "333333333333", Name: "dev", State: "ACTIVE"},
	}}
	m := newManager(t, repo, p)
	in := createInputNoAccount()
	in.Scope = ScopeOrganization
	in.ManagementAccountID = "111111111111"
	parent, _ := m.Create(context.Background(), in)

	kids, err := m.ExpandOrganization(context.Background(), parent.OrgID, parent.ID, nil)
	if err != nil {
		t.Fatalf("expand auto: %v", err)
	}
	if len(kids) != 2 {
		t.Fatalf("want 2 members (mgmt skipped), got %d", len(kids))
	}
	for _, k := range kids {
		if k.AccountID == "111111111111" {
			t.Error("management account should be skipped")
		}
		if k.RoleARN != "arn:aws:iam::"+k.AccountID+":role/BelluConnectorRole" {
			t.Errorf("member %s missing role ARN, got %q", k.AccountID, k.RoleARN)
		}
		if k.ExternalID != parent.ExternalID {
			t.Error("member must reuse parent ExternalId")
		}
	}
}

// TestReVerifyFromConnected asserts an already-CONNECTED connection can be
// verified again without an invalid-transition error, staying CONNECTED.
func TestReVerifyFromConnected(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusConnected {
		t.Fatalf("after first verify status = %s, want CONNECTED", got.Status)
	}
	// Re-verify must not error and must settle back to CONNECTED.
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
		t.Fatalf("re-verify: %v", err)
	}
	got, _ = repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusConnected {
		t.Errorf("after re-verify status = %s, want CONNECTED", got.Status)
	}
}

// TestReDiscoverFromComplete asserts discovery can run again on a COMPLETE
// connection (CONNECTED -> discover -> COMPLETE -> discover again).
func TestReDiscoverFromComplete(t *testing.T) {
	repo := newMemRepo()
	m := newManager(t, repo, &mgrFakeProvider{verifyRes: &provider.VerifyResult{OK: true}})
	c, _ := m.Create(context.Background(), createInput())
	if _, err := m.Verify(context.Background(), c.OrgID, c.ID, testRoleARN); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := m.Discover(context.Background(), c.OrgID, c.ID); err != nil {
		t.Fatalf("first discover: %v", err)
	}
	got, _ := repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusComplete {
		t.Fatalf("after first discover status = %s, want COMPLETE", got.Status)
	}
	// Re-discover from COMPLETE must not error.
	if _, err := m.Discover(context.Background(), c.OrgID, c.ID); err != nil {
		t.Fatalf("re-discover: %v", err)
	}
	got, _ = repo.Get(context.Background(), c.OrgID, c.ID)
	if got.Status != StatusComplete {
		t.Errorf("after re-discover status = %s, want COMPLETE", got.Status)
	}
}
