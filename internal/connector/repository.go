package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Repository persists Connection operational state in the customer Postgres. It
// stores only non-secret metadata (role ARN, account id, external id, status,
// capabilities, timestamps) — never credentials. Every read and write is
// tenant-scoped by org id.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository builds a Repository over the shared store's pool.
func NewRepository(s *store.Store) *Repository {
	return &Repository{pool: s.Pool()}
}

// Insert creates a connection and returns its id. Capabilities are written both
// as the connections.capabilities array and as connector_capabilities rows.
func (r *Repository) Insert(ctx context.Context, c *Connection) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("connector: begin insert: %w", err)
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO connector_connections
		   (org_id, project_id, provider, account_id, role_arn, external_id,
		    region, capabilities, status, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 RETURNING id`,
		c.OrgID, c.ProjectID, c.Provider, c.AccountID, c.RoleARN, c.ExternalID,
		c.Region, c.Capabilities, string(c.Status), c.CreatedAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("connector: insert connection: %w", err)
	}

	for _, cap := range c.Capabilities {
		if _, err := tx.Exec(ctx,
			`INSERT INTO connector_capabilities (connection_id, capability, enabled)
			 VALUES ($1,$2,true)
			 ON CONFLICT (connection_id, capability) DO NOTHING`,
			id, cap); err != nil {
			return 0, fmt.Errorf("connector: insert capability %q: %w", cap, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("connector: commit insert: %w", err)
	}
	c.ID = id
	return id, nil
}

// Get returns a connection by id, scoped to the given org (tenant). A missing
// row returns ErrNotFound.
func (r *Repository) Get(ctx context.Context, orgID, id int64) (*Connection, error) {
	var c Connection
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, provider, account_id, role_arn,
		        external_id, region, capabilities, status, created_at, last_verified_at
		   FROM connector_connections
		  WHERE id = $1 AND org_id = $2`,
		id, orgID,
	).Scan(
		&c.ID, &c.OrgID, &c.ProjectID, &c.Provider, &c.AccountID, &c.RoleARN,
		&c.ExternalID, &c.Region, &c.Capabilities, &status, &c.CreatedAt, &c.LastVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("connector: get connection: %w", err)
	}
	c.Status = Status(status)
	return &c, nil
}

// ListByOrg returns all connections for a tenant, newest first.
func (r *Repository) ListByOrg(ctx context.Context, orgID int64) ([]Connection, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, org_id, project_id, provider, account_id, role_arn,
		        external_id, region, capabilities, status, created_at, last_verified_at
		   FROM connector_connections
		  WHERE org_id = $1
		  ORDER BY created_at DESC, id DESC`,
		orgID)
	if err != nil {
		return nil, fmt.Errorf("connector: list connections: %w", err)
	}
	defer rows.Close()

	var out []Connection
	for rows.Next() {
		var c Connection
		var status string
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.ProjectID, &c.Provider, &c.AccountID, &c.RoleARN,
			&c.ExternalID, &c.Region, &c.Capabilities, &status, &c.CreatedAt, &c.LastVerifiedAt,
		); err != nil {
			return nil, fmt.Errorf("connector: scan connection: %w", err)
		}
		c.Status = Status(status)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateStatus sets the status for a tenant-scoped connection.
func (r *Repository) UpdateStatus(ctx context.Context, orgID, id int64, s Status) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET status = $1 WHERE id = $2 AND org_id = $3`,
		string(s), id, orgID)
	if err != nil {
		return fmt.Errorf("connector: update status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetVerified sets last_verified_at for a tenant-scoped connection.
func (r *Repository) SetVerified(ctx context.Context, orgID, id int64, t time.Time) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET last_verified_at = $1 WHERE id = $2 AND org_id = $3`,
		t, id, orgID)
	if err != nil {
		return fmt.Errorf("connector: set verified: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a tenant-scoped connection (cascading to capabilities,
// verification results, and discovery sessions).
func (r *Repository) Delete(ctx context.Context, orgID, id int64) error {
	ct, err := r.pool.Exec(ctx,
		`DELETE FROM connector_connections WHERE id = $1 AND org_id = $2`, id, orgID)
	if err != nil {
		return fmt.Errorf("connector: delete connection: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveVerification replaces the stored verification results for a connection
// with the latest run's per-permission outcomes.
func (r *Repository) SaveVerification(ctx context.Context, connID int64, res *provider.VerifyResult) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("connector: begin save verification: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM connector_verification_results WHERE connection_id = $1`, connID); err != nil {
		return fmt.Errorf("connector: clear verification: %w", err)
	}
	for _, cr := range res.Capabilities {
		if len(cr.Checks) == 0 {
			// NOT_ENABLED capabilities: record the aggregate outcome.
			if _, err := tx.Exec(ctx,
				`INSERT INTO connector_verification_results (connection_id, capability, permission, outcome)
				 VALUES ($1,$2,$3,$4)`,
				connID, cr.Capability, "", string(cr.Outcome)); err != nil {
				return fmt.Errorf("connector: insert capability result: %w", err)
			}
			continue
		}
		for _, pr := range cr.Checks {
			if _, err := tx.Exec(ctx,
				`INSERT INTO connector_verification_results (connection_id, capability, permission, outcome)
				 VALUES ($1,$2,$3,$4)`,
				connID, pr.Capability, pr.Permission, string(pr.Outcome)); err != nil {
				return fmt.Errorf("connector: insert permission result: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}

// RecordSession upserts a discovery session row for status tracking.
func (r *Repository) RecordSession(ctx context.Context, connID int64, sessionID, runID, status string, started time.Time, ended *time.Time, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO connector_discovery_sessions
		   (id, connection_id, run_id, status, started_at, ended_at, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (id) DO UPDATE SET
		   status = EXCLUDED.status,
		   ended_at = EXCLUDED.ended_at,
		   error = EXCLUDED.error`,
		sessionID, connID, runID, status, started, ended, errMsg)
	if err != nil {
		return fmt.Errorf("connector: record session: %w", err)
	}
	return nil
}

// LatestSession returns the most recent discovery session for a connection, or
// (nil, nil) when none exists.
func (r *Repository) LatestSession(ctx context.Context, connID int64) (*DiscoverySessionRow, error) {
	var s DiscoverySessionRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, connection_id, run_id, status, started_at, ended_at, error
		   FROM connector_discovery_sessions
		  WHERE connection_id = $1
		  ORDER BY started_at DESC
		  LIMIT 1`,
		connID,
	).Scan(&s.ID, &s.ConnectionID, &s.RunID, &s.Status, &s.StartedAt, &s.EndedAt, &s.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("connector: latest session: %w", err)
	}
	return &s, nil
}

// DiscoverySessionRow is the persisted form of a discovery session.
type DiscoverySessionRow struct {
	ID           string     `json:"id"`
	ConnectionID int64      `json:"connection_id"`
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	Error        string     `json:"error,omitempty"`
}
