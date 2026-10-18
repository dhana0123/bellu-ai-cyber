package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// fakeGitHubClient implements the manager's GitHubAppClient interface.
type fakeGitHubClient struct {
	login       string
	accountID   int64
	accountType string
	perms       map[string]string
	err         error
	calls       int
}

func (f *fakeGitHubClient) VerifyInstallation(context.Context, int64) (string, int64, string, map[string]string, error) {
	f.calls++
	if f.err != nil {
		return "", 0, "", nil, f.err
	}
	return f.login, f.accountID, f.accountType, f.perms, nil
}

// newGitHubManager builds a Manager wired for the GitHub install flow: a
// registered "github" provider, the App slug, a fake App client, and a fixed
// clock so state expiry is deterministic.
func newGitHubManager(t *testing.T, repo connRepo, gh GitHubAppClient, now time.Time) *Manager {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register(&mgrFakeProvider{name: "github"})
	orch := discovery.New(discovery.Options{
		Factory: func(map[string]any) (source.Source, error) { return nil, nil },
		Runner:  okRunner{},
		Creds:   discCreds{},
	})
	return NewManager(Options{
		Repo:          repo,
		Registry:      reg,
		Discovery:     orch,
		ExternalID:    newExtIDGen(),
		Now:           func() time.Time { return now },
		GitHubClient:  gh,
		GitHubAppSlug: "bellu-security",
	})
}

func seedGitHubConn(t *testing.T, repo *memRepo, orgID int64) *Connection {
	t.Helper()
	c := &Connection{OrgID: orgID, ProjectID: 1, Provider: "github", Status: StatusPending}
	if _, err := repo.Insert(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBeginGitHubInstallBuildsURLAndState(t *testing.T) {
	repo := newMemRepo()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newGitHubManager(t, repo, &fakeGitHubClient{}, now)
	c := seedGitHubConn(t, repo, 10)

	state, url, err := m.BeginGitHubInstall(context.Background(), 10, 99, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Fatal("expected a non-empty state")
	}
	if !strings.Contains(url, "github.com/apps/bellu-security/installations/new") || !strings.Contains(url, "state="+state) {
		t.Fatalf("install url = %q", url)
	}
	// State persisted, bound to org/user/connection, unexpired.
	st, err := repo.ConsumeInstallState(context.Background(), state, now)
	if err != nil {
		t.Fatalf("state should be consumable: %v", err)
	}
	if st.OrgID != 10 || st.UserID != 99 || st.ConnectionID != c.ID {
		t.Errorf("state binding = %+v", st)
	}
}

func TestBeginGitHubInstallRejectsNonGitHub(t *testing.T) {
	repo := newMemRepo()
	m := newGitHubManager(t, repo, &fakeGitHubClient{}, time.Now())
	// An AWS connection must not start a GitHub install.
	c := &Connection{OrgID: 10, ProjectID: 1, Provider: "aws", Status: StatusPending}
	_, _ = repo.Insert(context.Background(), c)
	if _, _, err := m.BeginGitHubInstall(context.Background(), 10, 1, c.ID); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("want ErrInvalidValue for non-github, got %v", err)
	}
}

func TestCompleteGitHubInstallBindsTenantAndConnects(t *testing.T) {
	repo := newMemRepo()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	gh := &fakeGitHubClient{login: "bellu-ai", accountID: 555, accountType: "Organization", perms: map[string]string{"contents": "read"}}
	m := newGitHubManager(t, repo, gh, now)
	c := seedGitHubConn(t, repo, 10)

	state, _, err := m.BeginGitHubInstall(context.Background(), 10, 99, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.CompleteGitHubInstall(context.Background(), state, 7001)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusConnected {
		t.Errorf("status = %s, want CONNECTED", out.Status)
	}
	if out.AccountID != "bellu-ai" {
		t.Errorf("account id = %q, want org login bellu-ai", out.AccountID)
	}
	inst, ok, err := repo.GetGitHubInstallationByInstallationID(context.Background(), 7001)
	if err != nil || !ok {
		t.Fatalf("installation not upserted: ok=%v err=%v", ok, err)
	}
	if inst.OrgID != 10 || inst.ConnectionID != c.ID {
		t.Errorf("installation tenant binding wrong: %+v", inst)
	}
	if inst.GitHubAccountLogin != "bellu-ai" {
		t.Errorf("installation account = %+v", inst)
	}
}

func TestCompleteGitHubInstallStateIsSingleUse(t *testing.T) {
	repo := newMemRepo()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	gh := &fakeGitHubClient{login: "bellu-ai", accountID: 1, accountType: "Organization"}
	m := newGitHubManager(t, repo, gh, now)
	c := seedGitHubConn(t, repo, 10)

	state, _, _ := m.BeginGitHubInstall(context.Background(), 10, 99, c.ID)
	if _, err := m.CompleteGitHubInstall(context.Background(), state, 7001); err != nil {
		t.Fatal(err)
	}
	// Replaying the same state must fail.
	if _, err := m.CompleteGitHubInstall(context.Background(), state, 7001); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replay err = %v, want ErrInvalidState", err)
	}
}

func TestCompleteGitHubInstallRejectsUnknownState(t *testing.T) {
	repo := newMemRepo()
	m := newGitHubManager(t, repo, &fakeGitHubClient{login: "x", accountType: "User"}, time.Now())
	if _, err := m.CompleteGitHubInstall(context.Background(), "forged-state", 7001); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("forged state err = %v, want ErrInvalidState", err)
	}
}

func TestSetRepositorySelectionRejectsOutOfScope(t *testing.T) {
	repo := newMemRepo()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newGitHubManager(t, repo, &fakeGitHubClient{}, now)
	c := seedGitHubConn(t, repo, 10)

	// Seed two accessible repos for the connection.
	if err := repo.SaveRepositories(context.Background(), c.ID, []Repo{
		{ExternalID: "1", FullName: "bellu-ai/a"},
		{ExternalID: "2", FullName: "bellu-ai/b"},
	}); err != nil {
		t.Fatal(err)
	}
	known, _ := repo.ListRepositories(context.Background(), c.ID)
	validID := known[0].ID

	// A valid id succeeds.
	if err := m.SetRepositorySelection(context.Background(), 10, c.ID, []int64{validID}, false); err != nil {
		t.Fatalf("valid selection failed: %v", err)
	}
	// An id outside the connection's accessible set is rejected.
	if err := m.SetRepositorySelection(context.Background(), 10, c.ID, []int64{validID, 99999}, true); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("out-of-scope selection err = %v, want ErrInvalidValue", err)
	}
}

func TestCompleteGitHubInstallRequiresClient(t *testing.T) {
	repo := newMemRepo()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Manager without a GitHub client (dev build).
	m := newGitHubManager(t, repo, nil, now)
	c := seedGitHubConn(t, repo, 10)
	state, _, _ := m.BeginGitHubInstall(context.Background(), 10, 99, c.ID)
	if _, err := m.CompleteGitHubInstall(context.Background(), state, 7001); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("want ErrInvalidValue when client unconfigured, got %v", err)
	}
}
