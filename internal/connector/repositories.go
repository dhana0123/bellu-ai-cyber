package connector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RepoSelectionMode controls which of a git connection's repositories Bellu
// collects: "all" (every accessible repo, including ones added later) or
// "selected" (only repos with Selected=true).
type RepoSelectionMode string

const (
	RepoModeAll      RepoSelectionMode = "all"
	RepoModeSelected RepoSelectionMode = "selected"
)

// Valid reports whether m is a known selection mode.
func (m RepoSelectionMode) Valid() bool { return m == RepoModeAll || m == RepoModeSelected }

// Repo is one repository a git connector can access. Non-secret metadata only;
// no tokens are ever stored here. ExternalID is the provider's stable id
// (GitHub repo id) used for dedup; names/urls may change but the id does not.
type Repo struct {
	ID            int64  `json:"id"`
	ConnectionID  int64  `json:"connection_id"`
	ExternalID    string `json:"external_id"`
	FullName      string `json:"full_name"`
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Selected      bool   `json:"selected"`
	// GitHub-specific metadata (zero/empty for other providers).
	Owner      string `json:"owner,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	SyncStatus string `json:"sync_status,omitempty"`
}

// ---- store methods (connector_repositories + repo_selection_mode) ----

// SaveRepositories upserts the given repositories for a connection, preserving
// the operator's prior per-repo Selected choice on an existing row and
// defaulting a newly-seen repo to selected=true (collect-all-by-default).
func (r *Repository) SaveRepositories(ctx context.Context, connID int64, repos []Repo) error {
	if len(repos) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, repo := range repos {
		// github_repo_id is the numeric GitHub id parsed from ExternalID when
		// present (stable dedup key); owner is derived from full_name's prefix.
		ghID := githubRepoID(repo.ExternalID)
		owner := repo.Owner
		if owner == "" {
			owner = ownerFromFullName(repo.FullName)
		}
		vis := repo.Visibility
		if vis == "" && repo.Private {
			vis = "private"
		} else if vis == "" {
			vis = "public"
		}
		batch.Queue(
			`INSERT INTO connector_repositories
			   (connection_id, external_id, full_name, url, default_branch, private, selected,
			    github_repo_id, owner, visibility)
			 VALUES ($1,$2,$3,$4,$5,$6,true,$7,$8,$9)
			 ON CONFLICT (connection_id, external_id) DO UPDATE SET
			   full_name = EXCLUDED.full_name,
			   url = EXCLUDED.url,
			   default_branch = EXCLUDED.default_branch,
			   private = EXCLUDED.private,
			   github_repo_id = EXCLUDED.github_repo_id,
			   owner = EXCLUDED.owner,
			   visibility = EXCLUDED.visibility,
			   updated_at = now()`,
			connID, repo.ExternalID, repo.FullName, repo.URL, repo.DefaultBranch, repo.Private,
			ghID, owner, vis)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range repos {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("connector: save repository: %w", err)
		}
	}
	return nil
}

// ListRepositories returns a connection's repositories, ordered by full name.
func (r *Repository) ListRepositories(ctx context.Context, connID int64) ([]Repo, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, connection_id, external_id, full_name, url, default_branch, private, selected,
		        owner, visibility, sync_status
		   FROM connector_repositories
		  WHERE connection_id = $1
		  ORDER BY full_name`, connID)
	if err != nil {
		return nil, fmt.Errorf("connector: list repositories: %w", err)
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		var repo Repo
		if err := rows.Scan(&repo.ID, &repo.ConnectionID, &repo.ExternalID, &repo.FullName,
			&repo.URL, &repo.DefaultBranch, &repo.Private, &repo.Selected,
			&repo.Owner, &repo.Visibility, &repo.SyncStatus); err != nil {
			return nil, fmt.Errorf("connector: scan repository: %w", err)
		}
		out = append(out, repo)
	}
	return out, rows.Err()
}

// githubRepoID parses the numeric GitHub repo id from a provider ExternalID
// (which for GitHub is the stringified repo id). Returns nil (SQL NULL) when the
// id is not numeric, so non-GitHub providers leave github_repo_id empty.
func githubRepoID(externalID string) any {
	if n, err := strconv.ParseInt(externalID, 10, 64); err == nil && n > 0 {
		return n
	}
	return nil
}

// ownerFromFullName returns the "owner" segment of an "owner/repo" full name.
func ownerFromFullName(fullName string) string {
	if i := strings.IndexByte(fullName, '/'); i > 0 {
		return fullName[:i]
	}
	return ""
}

// SetRepositorySelection sets selected on the given repo ids for a connection.
// Ids not belonging to the connection are ignored.
func (r *Repository) SetRepositorySelection(ctx context.Context, connID int64, repoIDs []int64, selected bool) error {
	if len(repoIDs) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE connector_repositories SET selected = $1, updated_at = now()
		  WHERE connection_id = $2 AND id = ANY($3)`,
		selected, connID, repoIDs)
	if err != nil {
		return fmt.Errorf("connector: set repository selection: %w", err)
	}
	return nil
}

// SetAllRepositoriesSelected sets selected on every repo of a connection, used
// when the operator flips the whole connection on/off.
func (r *Repository) SetAllRepositoriesSelected(ctx context.Context, connID int64, selected bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE connector_repositories SET selected = $1, updated_at = now()
		  WHERE connection_id = $2`,
		selected, connID)
	if err != nil {
		return fmt.Errorf("connector: set all repositories selected: %w", err)
	}
	return nil
}

// GetRepoSelectionMode returns the connection's repo selection mode.
func (r *Repository) GetRepoSelectionMode(ctx context.Context, connID int64) (RepoSelectionMode, error) {
	var mode string
	err := r.pool.QueryRow(ctx,
		`SELECT repo_selection_mode FROM connector_connections WHERE id = $1`, connID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("connector: get repo selection mode: %w", err)
	}
	return RepoSelectionMode(mode), nil
}

// SetRepoSelectionMode updates the connection's repo selection mode.
func (r *Repository) SetRepoSelectionMode(ctx context.Context, connID int64, mode RepoSelectionMode) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET repo_selection_mode = $1 WHERE id = $2`,
		string(mode), connID)
	if err != nil {
		return fmt.Errorf("connector: set repo selection mode: %w", err)
	}
	return nil
}
