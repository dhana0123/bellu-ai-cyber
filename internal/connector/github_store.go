package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GitHubInstallation binds a verified GitHub App installation to a tenant
// (OrgID) and a connector connection. It carries only non-secret facts: a
// GitHub App stores no token (the installation token is minted on demand).
type GitHubInstallation struct {
	ID                   int64             `json:"id"`
	OrgID                int64             `json:"-"`
	ConnectionID         int64             `json:"connection_id"`
	GitHubInstallationID int64             `json:"github_installation_id"`
	GitHubAccountID      int64             `json:"github_account_id"`
	GitHubAccountLogin   string            `json:"github_account_login"`
	GitHubAccountType    string            `json:"github_account_type"` // User | Organization
	Status               string            `json:"status"`              // active|suspended|deleted|pending
	RepoSelectionMode    string            `json:"repo_selection_mode"`
	GrantedPermissions   map[string]string `json:"granted_permissions"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	LastVerifiedAt       *time.Time        `json:"last_verified_at,omitempty"`
}

// InstallState is a CSRF-safe, single-use GitHub install state. It is created
// at begin-install and consumed (marked used) in the callback transaction. The
// resulting installation's tenant comes from OrgID recorded here, never from
// the browser.
type InstallState struct {
	State        string    `json:"state"`
	OrgID        int64     `json:"-"`
	UserID       int64     `json:"user_id"`
	ConnectionID int64     `json:"connection_id"`
	Used         bool      `json:"used"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ---- github_installations ----

// UpsertGitHubInstallation inserts or updates an installation keyed on the
// globally-unique github_installation_id, so repeated callbacks for the same
// installation are idempotent. The tenant (org_id) and connection binding come
// from the caller (derived from the consumed single-use state).
func (r *Repository) UpsertGitHubInstallation(ctx context.Context, inst GitHubInstallation) error {
	perms := inst.GrantedPermissions
	if perms == nil {
		perms = map[string]string{}
	}
	permsJSON, err := json.Marshal(perms)
	if err != nil {
		return fmt.Errorf("connector: marshal granted permissions: %w", err)
	}
	status := inst.Status
	if status == "" {
		status = "active"
	}
	mode := inst.RepoSelectionMode
	if mode == "" {
		mode = string(RepoModeAll)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO github_installations
		   (org_id, connection_id, github_installation_id, github_account_id,
		    github_account_login, github_account_type, status, repo_selection_mode,
		    granted_permissions, last_verified_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (github_installation_id) DO UPDATE SET
		   org_id = EXCLUDED.org_id,
		   connection_id = EXCLUDED.connection_id,
		   github_account_id = EXCLUDED.github_account_id,
		   github_account_login = EXCLUDED.github_account_login,
		   github_account_type = EXCLUDED.github_account_type,
		   status = EXCLUDED.status,
		   granted_permissions = EXCLUDED.granted_permissions,
		   last_verified_at = EXCLUDED.last_verified_at,
		   updated_at = now()`,
		inst.OrgID, inst.ConnectionID, inst.GitHubInstallationID, inst.GitHubAccountID,
		inst.GitHubAccountLogin, inst.GitHubAccountType, status, mode,
		permsJSON, inst.LastVerifiedAt)
	if err != nil {
		return fmt.Errorf("connector: upsert github installation: %w", err)
	}
	return nil
}

// GetGitHubInstallationByConnection returns the installation bound to a
// connection, tenant-scoped by org. found=false (nil error) when none exists.
func (r *Repository) GetGitHubInstallationByConnection(ctx context.Context, orgID, connID int64) (*GitHubInstallation, bool, error) {
	return r.scanInstallation(ctx,
		`WHERE connection_id = $1 AND org_id = $2`, connID, orgID)
}

// GetGitHubInstallationByInstallationID returns the installation with the given
// GitHub installation id. It is not org-scoped (the installation id is the
// lookup key during a callback, before the tenant is loaded from state); the
// returned OrgID is the authoritative tenant. found=false when none exists.
func (r *Repository) GetGitHubInstallationByInstallationID(ctx context.Context, ghInstID int64) (*GitHubInstallation, bool, error) {
	return r.scanInstallation(ctx,
		`WHERE github_installation_id = $1`, ghInstID)
}

// scanInstallation runs a single-row installation query with the given WHERE
// clause and args, returning found=false (nil error) when no row matches.
func (r *Repository) scanInstallation(ctx context.Context, where string, args ...any) (*GitHubInstallation, bool, error) {
	var inst GitHubInstallation
	var permsJSON []byte
	q := `SELECT id, org_id, connection_id, github_installation_id, github_account_id,
	             github_account_login, github_account_type, status, repo_selection_mode,
	             granted_permissions, created_at, updated_at, last_verified_at
	        FROM github_installations ` + where
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&inst.ID, &inst.OrgID, &inst.ConnectionID, &inst.GitHubInstallationID, &inst.GitHubAccountID,
		&inst.GitHubAccountLogin, &inst.GitHubAccountType, &inst.Status, &inst.RepoSelectionMode,
		&permsJSON, &inst.CreatedAt, &inst.UpdatedAt, &inst.LastVerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("connector: get github installation: %w", err)
	}
	if len(permsJSON) > 0 {
		_ = json.Unmarshal(permsJSON, &inst.GrantedPermissions)
	}
	return &inst, true, nil
}

// ---- github_install_states ----

// SaveInstallState persists a single-use install state (CSRF token) with its
// tenant/connection binding and expiry.
func (r *Repository) SaveInstallState(ctx context.Context, st InstallState) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO github_install_states
		   (state, org_id, user_id, connection_id, used, expires_at)
		 VALUES ($1,$2,$3,$4,false,$5)`,
		st.State, st.OrgID, st.UserID, st.ConnectionID, st.ExpiresAt)
	if err != nil {
		return fmt.Errorf("connector: save install state: %w", err)
	}
	return nil
}

// ConsumeInstallState atomically validates and consumes a state in one
// transaction: it selects an unused, unexpired state FOR UPDATE, marks it used,
// and returns it. A missing/expired/already-used state returns ErrInvalidState,
// so a replayed or forged callback is rejected. Single-use is enforced by the
// used flag flipped inside the same transaction.
func (r *Repository) ConsumeInstallState(ctx context.Context, state string, now time.Time) (InstallState, error) {
	var out InstallState
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("connector: begin consume state: %w", err)
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx,
		`SELECT state, org_id, user_id, connection_id, used, created_at, expires_at
		   FROM github_install_states
		  WHERE state = $1 AND used = false AND expires_at > $2
		  FOR UPDATE`,
		state, now,
	).Scan(&out.State, &out.OrgID, &out.UserID, &out.ConnectionID, &out.Used, &out.CreatedAt, &out.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return InstallState{}, ErrInvalidState
	}
	if err != nil {
		return InstallState{}, fmt.Errorf("connector: select install state: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE github_install_states SET used = true WHERE state = $1`, state); err != nil {
		return InstallState{}, fmt.Errorf("connector: mark install state used: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstallState{}, fmt.Errorf("connector: commit consume state: %w", err)
	}
	out.Used = true
	return out, nil
}
