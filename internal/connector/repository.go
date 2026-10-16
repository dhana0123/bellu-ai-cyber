package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// nullableParent maps a zero parent id to SQL NULL so the self-referential FK
// is satisfied for standalone and organization-root connections.
func nullableParent(id int64) any {
	if id == 0 {
		return nil
	}
	return id
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
		   (org_id, project_id, provider, scope, installation_type, deployment_method,
		    account_id, management_account_id, organizational_unit_id, parent_connection_id,
		    role_arn, external_id, region, capabilities, status, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 RETURNING id`,
		c.OrgID, c.ProjectID, c.Provider, string(c.Scope), string(c.InstallationType), string(c.DeploymentMethod),
		c.AccountID, c.ManagementAccountID, c.OrganizationalUnitID, nullableParent(c.ParentConnectionID),
		c.RoleARN, c.ExternalID, c.Region, c.Capabilities, string(c.Status), c.CreatedAt,
	).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrConflict
	}
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
	var status, scope, installation, deployment string
	var roleARN *string
	var parent *int64
	err := r.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, provider, scope, installation_type,
		        deployment_method, account_id, management_account_id, organizational_unit_id,
		        parent_connection_id, role_arn, external_id, region, capabilities, status,
		        created_at, last_verified_at
		   FROM connector_connections
		  WHERE id = $1 AND org_id = $2`,
		id, orgID,
	).Scan(
		&c.ID, &c.OrgID, &c.ProjectID, &c.Provider, &scope, &installation,
		&deployment, &c.AccountID, &c.ManagementAccountID, &c.OrganizationalUnitID,
		&parent, &roleARN, &c.ExternalID, &c.Region, &c.Capabilities, &status,
		&c.CreatedAt, &c.LastVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("connector: get connection: %w", err)
	}
	c.Status = Status(status)
	c.Scope = Scope(scope)
	c.InstallationType = InstallationType(installation)
	c.DeploymentMethod = DeploymentMethod(deployment)
	if roleARN != nil {
		c.RoleARN = *roleARN
	}
	if parent != nil {
		c.ParentConnectionID = *parent
	}
	return &c, nil
}

// ListByOrg returns all connections for a tenant, newest first.
func (r *Repository) ListByOrg(ctx context.Context, orgID int64) ([]Connection, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, org_id, project_id, provider, scope, installation_type,
		        deployment_method, account_id, management_account_id, organizational_unit_id,
		        parent_connection_id, role_arn, external_id, region, capabilities, status,
		        created_at, last_verified_at
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
		var status, scope, installation, deployment string
		var roleARN *string
		var parent *int64
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.ProjectID, &c.Provider, &scope, &installation,
			&deployment, &c.AccountID, &c.ManagementAccountID, &c.OrganizationalUnitID,
			&parent, &roleARN, &c.ExternalID, &c.Region, &c.Capabilities, &status,
			&c.CreatedAt, &c.LastVerifiedAt,
		); err != nil {
			return nil, fmt.Errorf("connector: scan connection: %w", err)
		}
		c.Status = Status(status)
		c.Scope = Scope(scope)
		c.InstallationType = InstallationType(installation)
		c.DeploymentMethod = DeploymentMethod(deployment)
		if roleARN != nil {
			c.RoleARN = *roleARN
		}
		if parent != nil {
			c.ParentConnectionID = *parent
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListDueForDiscovery returns the connections a scheduled re-scan should start:
// those that are idle (CONNECTED or COMPLETE, never mid-run or in ERROR) and
// have no discovery session that started after startedBefore. A connection
// never scanned has no session and is therefore always due. Oldest-scanned
// first, so a long backlog is worked through fairly.
func (r *Repository) ListDueForDiscovery(ctx context.Context, startedBefore time.Time) ([]ScheduleTarget, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.org_id, c.id
		   FROM connector_connections c
		  WHERE c.status IN ($1, $2)
		    AND NOT EXISTS (
		          SELECT 1 FROM connector_discovery_sessions s
		           WHERE s.connection_id = c.id AND s.started_at > $3)
		  ORDER BY (SELECT max(s.started_at) FROM connector_discovery_sessions s
		             WHERE s.connection_id = c.id) ASC NULLS FIRST, c.id ASC`,
		string(StatusConnected), string(StatusComplete), startedBefore)
	if err != nil {
		return nil, fmt.Errorf("connector: list due for discovery: %w", err)
	}
	defer rows.Close()

	var out []ScheduleTarget
	for rows.Next() {
		var t ScheduleTarget
		if err := rows.Scan(&t.OrgID, &t.ID); err != nil {
			return nil, fmt.Errorf("connector: scan due connection: %w", err)
		}
		out = append(out, t)
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

// SetAccountID stores the AWS account id for a tenant-scoped connection. For
// account-scope AWS connections the account is derived from the role ARN at
// verification time rather than supplied at create time.
func (r *Repository) SetAccountID(ctx context.Context, orgID, id int64, accountID string) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET account_id = $1 WHERE id = $2 AND org_id = $3`,
		accountID, id, orgID)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("connector: set account id: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetManagementAccountID stores the AWS Organizations management account id for
// an organization-scope connection, derived from the role ARN at verify.
func (r *Repository) SetManagementAccountID(ctx context.Context, orgID, id int64, accountID string) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET management_account_id = $1 WHERE id = $2 AND org_id = $3`,
		accountID, id, orgID)
	if err != nil {
		return fmt.Errorf("connector: set management account id: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRoleARN stores the role ARN the customer supplies at verification time for
// a tenant-scoped connection. The ARN is non-secret connection metadata.
func (r *Repository) SetRoleARN(ctx context.Context, orgID, id int64, roleARN string) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE connector_connections SET role_arn = $1 WHERE id = $2 AND org_id = $3`,
		roleARN, id, orgID)
	if err != nil {
		return fmt.Errorf("connector: set role arn: %w", err)
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
		`SELECT id, connection_id, run_id, status, started_at, ended_at, error, progress
		   FROM connector_discovery_sessions
		  WHERE connection_id = $1
		  ORDER BY started_at DESC
		  LIMIT 1`,
		connID,
	).Scan(&s.ID, &s.ConnectionID, &s.RunID, &s.Status, &s.StartedAt, &s.EndedAt, &s.Error, &s.Progress)
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
	// Progress is the raw JSON progress snapshot (discovery.Progress shape), or
	// nil when none has been recorded. Kept as raw JSON so the repository stays
	// decoupled from the discovery package.
	Progress []byte `json:"-"`
}

// SetCapabilities replaces a connection's enabled capability set, scoped to the
// org (tenant). It updates both the connector_connections.capabilities array
// and the connector_capabilities rows in one transaction, mirroring Insert's
// dual-write. Capabilities not in the new set are removed; present ones are
// upserted enabled. A missing connection returns ErrNotFound.
func (r *Repository) SetCapabilities(ctx context.Context, orgID, id int64, caps []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("connector: begin set capabilities: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE connector_connections SET capabilities = $3 WHERE id = $1 AND org_id = $2`,
		id, orgID, caps)
	if err != nil {
		return fmt.Errorf("connector: update capabilities: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	// Reconcile the connector_capabilities rows: delete those no longer present,
	// then upsert the current set as enabled.
	if _, err := tx.Exec(ctx,
		`DELETE FROM connector_capabilities WHERE connection_id = $1 AND NOT (capability = ANY($2))`,
		id, caps); err != nil {
		return fmt.Errorf("connector: prune capabilities: %w", err)
	}
	for _, cap := range caps {
		if _, err := tx.Exec(ctx,
			`INSERT INTO connector_capabilities (connection_id, capability, enabled)
			 VALUES ($1,$2,true)
			 ON CONFLICT (connection_id, capability) DO UPDATE SET enabled = true`,
			id, cap); err != nil {
			return fmt.Errorf("connector: upsert capability %q: %w", cap, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("connector: commit set capabilities: %w", err)
	}
	return nil
}

// SaveSessionProgress persists the latest progress snapshot (raw JSON) for a
// session. It is a lightweight UPDATE issued periodically during a run so the
// UI can rehydrate live progress after a reload or an API restart. A missing
// session row is a no-op (the row is created by RecordSession at run start).
func (r *Repository) SaveSessionProgress(ctx context.Context, sessionID string, progressJSON []byte) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE connector_discovery_sessions SET progress = $2 WHERE id = $1`,
		sessionID, progressJSON)
	if err != nil {
		return fmt.Errorf("connector: save session progress: %w", err)
	}
	return nil
}

// ReconcileRunningSessions marks any session still in 'running' as 'interrupted'.
// It is called at API startup: a running session with no owning in-process hub
// was orphaned by a crash/restart (collection cannot resume mid-flight), so it
// is moved to a terminal state and the UI shows it as interrupted rather than
// hanging forever. Returns the number of sessions reconciled.
func (r *Repository) ReconcileRunningSessions(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("connector: begin reconcile: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE connector_discovery_sessions
		    SET status = 'interrupted',
		        ended_at = COALESCE(ended_at, now()),
		        error = CASE WHEN error = '' THEN 'interrupted by service restart' ELSE error END
		  WHERE status = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("connector: reconcile running sessions: %w", err)
	}

	// Also move any connection stuck in DISCOVERY_RUNNING back to ERROR so it is
	// not wedged (the state machine permits a retry from ERROR). Without this,
	// the connection status would stay DISCOVERY_RUNNING forever after a crash.
	if _, err := tx.Exec(ctx,
		`UPDATE connector_connections SET status = 'ERROR' WHERE status = 'DISCOVERY_RUNNING'`); err != nil {
		return 0, fmt.Errorf("connector: reconcile running connections: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("connector: commit reconcile: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
