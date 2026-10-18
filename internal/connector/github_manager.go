package connector

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

// installStateTTL bounds how long a begin-install state is valid before the
// callback must consume it. Short-lived per the security boundary (<= 10 min).
const installStateTTL = 10 * time.Minute

// BeginGitHubInstall starts a GitHub App install for a tenant's connection. It
// requires the connection to exist (tenant-scoped) and be a GitHub provider,
// generates a 32-byte base64url single-use state bound to the org/user/
// connection, persists it with a 10-minute expiry, and returns the state plus
// the GitHub install URL. The URL is built from the configured App slug, not
// from any client input.
func (m *Manager) BeginGitHubInstall(ctx context.Context, orgID, userID, connID int64) (state, installURL string, err error) {
	c, err := m.repo.Get(ctx, orgID, connID)
	if err != nil {
		return "", "", err
	}
	if c.Provider != "github" {
		return "", "", fmt.Errorf("%w: connection %d is not a github connection", ErrInvalidValue, connID)
	}
	if m.githubAppSlug == "" {
		return "", "", fmt.Errorf("%w: GitHub App slug is not configured", ErrInvalidValue)
	}

	state, err = randomState()
	if err != nil {
		return "", "", err
	}
	now := m.now()
	if err := m.repo.SaveInstallState(ctx, InstallState{
		State:        state,
		OrgID:        orgID,
		UserID:       userID,
		ConnectionID: connID,
		ExpiresAt:    now.Add(installStateTTL),
	}); err != nil {
		return "", "", err
	}

	installURL = fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s", m.githubAppSlug, state)
	return state, installURL, nil
}

// CompleteGitHubInstall finishes a GitHub App install from the callback. It
// consumes the single-use state (rejecting a missing/expired/replayed one),
// verifies the installation against GitHub via the App client, upserts the
// installation bound to the state's org_id/connection_id (never a browser-
// supplied tenant), records the account login on the connection, and
// transitions the connection to CONNECTED through the FSM. The tenant comes
// solely from the consumed state.
func (m *Manager) CompleteGitHubInstall(ctx context.Context, state string, installationID int64) (*Connection, error) {
	if m.githubClient == nil {
		return nil, fmt.Errorf("%w: GitHub App client is not configured", ErrInvalidValue)
	}
	if installationID <= 0 {
		return nil, fmt.Errorf("%w: installation_id is required", ErrInvalidValue)
	}

	// Consume the state first: single-use + tenant binding. A replayed or forged
	// state is rejected here before any GitHub call.
	st, err := m.repo.ConsumeInstallState(ctx, state, m.now())
	if err != nil {
		return nil, err
	}

	// Load the connection using the TENANT from the state, not from the browser.
	c, err := m.repo.Get(ctx, st.OrgID, st.ConnectionID)
	if err != nil {
		return nil, err
	}
	if c.Provider != "github" {
		return nil, fmt.Errorf("%w: connection %d is not a github connection", ErrInvalidValue, c.ID)
	}

	// Verify the installation server-side (confirms it exists and returns the
	// account + permissions).
	login, accountID, accountType, perms, verr := m.githubClient.VerifyInstallation(ctx, installationID)
	if verr != nil {
		return nil, verr
	}

	now := m.now()
	if err := m.repo.UpsertGitHubInstallation(ctx, GitHubInstallation{
		OrgID:                st.OrgID,
		ConnectionID:         st.ConnectionID,
		GitHubInstallationID: installationID,
		GitHubAccountID:      accountID,
		GitHubAccountLogin:   login,
		GitHubAccountType:    accountType,
		Status:               "active",
		RepoSelectionMode:    string(RepoModeAll),
		GrantedPermissions:   perms,
		LastVerifiedAt:       &now,
	}); err != nil {
		return nil, err
	}

	// Record the account login as the connection's account id (free-form org
	// login for git connectors) so the UI shows which org is connected.
	if login != "" && c.AccountID != login {
		if err := m.repo.SetAccountID(ctx, st.OrgID, st.ConnectionID, login); err != nil {
			return nil, err
		}
		c.AccountID = login
	}

	// Drive the connection to CONNECTED via the FSM. Reuse the existing verify
	// events: a PENDING/VERIFYING connection goes BeginVerify -> VerifySucceeded.
	// If it is already CONNECTED (re-install), leave it; otherwise run the two
	// transitions. SetVerified stamps last_verified_at on success.
	if c.Status != StatusConnected {
		if c.Status != StatusVerifying {
			if err := m.transition(ctx, c, EventBeginVerify); err != nil {
				return nil, err
			}
		}
		if err := m.transition(ctx, c, EventVerifySucceeded); err != nil {
			return nil, err
		}
		_ = m.repo.SetVerified(ctx, st.OrgID, st.ConnectionID, now)
	}

	// Populate the repository list immediately so the UI shows the installation's
	// repos (all selected by default). Best-effort: a transient GitHub error here
	// must not fail the install — the user can Refresh on the Repositories page.
	if _, rerr := m.RefreshRepositories(ctx, st.OrgID, st.ConnectionID); rerr != nil {
		// Not fatal; the connection is CONNECTED regardless.
		_ = rerr
	}

	return c, nil
}

// randomState returns 32 cryptographically-random bytes encoded as base64url
// (no padding), suitable for a single-use CSRF state token.
func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("connector: generate install state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// installationIDFromString parses a GitHub installation id from the callback
// query string, returning ErrInvalidValue on a malformed value. Exposed as a
// helper so the handler maps the error cleanly.
func installationIDFromString(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: installation_id", ErrInvalidValue)
	}
	return id, nil
}
