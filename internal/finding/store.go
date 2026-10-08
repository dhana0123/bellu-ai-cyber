package finding

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Store persists findings into the findings table using the shared
// *store.Store connection pool, mirroring the intel_* persistence layer. The
// caller retains ownership of the Store (and its pool lifecycle).
type Store struct {
	store *store.Store
}

// NewStore returns a findings Store backed by the given *store.Store.
func NewStore(s *store.Store) *Store {
	return &Store{store: s}
}

// Key identifies a finding by its deduplication tuple (asset_id, vuln_id).
// Correlation runs collect the set of currently-matched keys and hand it to
// ResolveMissing so stale findings can be transitioned to resolved.
type Key struct {
	AssetID string
	VulnID  string
}

// Upsert idempotently writes one matched finding (Requirement 12.2, 12.5).
//
// The row is deduped on the findings_uniq (asset_id, vuln_id) constraint
// (Requirement 12.1). ON CONFLICT it refreshes risk_score, severity, intel_ref,
// asset_kind and last_seen = now(), and re-asserts status = 'open' so a finding
// that was previously resolved re-opens on a fresh match. first_seen is never
// written in the UPDATE branch, so the original timestamp is preserved
// (Requirement 12.4). On a no-op re-run with unchanged inputs the only
// observable change is last_seen (correctness Property 3).
func (s *Store) Upsert(ctx context.Context, f Finding) error {
	_, err := s.store.Pool().Exec(ctx,
		`INSERT INTO findings
		   (asset_id, asset_kind, vuln_id, intel_ref, risk_score, severity,
		    status, first_seen, last_seen)
		 VALUES ($1,$2,$3,$4,$5,$6,'open', now(), now())
		 ON CONFLICT (asset_id, vuln_id) DO UPDATE SET
		   asset_kind = EXCLUDED.asset_kind,
		   intel_ref  = EXCLUDED.intel_ref,
		   risk_score = EXCLUDED.risk_score,
		   severity   = EXCLUDED.severity,
		   status     = 'open',
		   last_seen  = now()`,
		f.AssetID, string(f.AssetKind), f.VulnID, f.IntelRef,
		f.RiskScore, string(f.Severity),
	)
	if err != nil {
		return fmt.Errorf("finding: upsert (%s/%s): %w", f.AssetID, f.VulnID, err)
	}
	return nil
}

// ResolveMissing transitions open findings that no longer match to resolved
// (Requirement 12.3). matched is the set of (asset_id, vuln_id) keys that are
// still open-and-matched as of the current correlation run; every open finding
// NOT in that set is set to status = 'resolved' with last_seen = now().
//
// Resolving is scoped to currently-open findings, so re-running with the same
// matched set leaves already-resolved findings untouched and is idempotent
// except for last_seen on rows that transition this run (correctness
// Property 3). An empty matched set means nothing matched this run, so every
// open finding is resolved.
func (s *Store) ResolveMissing(ctx context.Context, matched []Key) error {
	// Pass the matched keys as parallel asset_id/vuln_id arrays and keep an open
	// finding only when its tuple appears in the set. ANY over empty arrays is
	// false, so every open finding resolves when nothing matched this run.
	assetIDs := make([]string, 0, len(matched))
	vulnIDs := make([]string, 0, len(matched))
	for _, k := range matched {
		assetIDs = append(assetIDs, k.AssetID)
		vulnIDs = append(vulnIDs, k.VulnID)
	}

	_, err := s.store.Pool().Exec(ctx,
		`UPDATE findings
		    SET status = 'resolved', last_seen = now()
		  WHERE status = 'open'
		    AND NOT EXISTS (
		        SELECT 1
		          FROM unnest($1::text[], $2::text[]) AS m(asset_id, vuln_id)
		         WHERE m.asset_id = findings.asset_id
		           AND m.vuln_id  = findings.vuln_id
		    )`,
		assetIDs, vulnIDs,
	)
	if err != nil {
		return fmt.Errorf("finding: resolve missing: %w", err)
	}
	return nil
}

// ErrNotFound is returned by Get when no finding has the requested id.
var ErrNotFound = errors.New("finding: not found")

// Get loads a single finding by its findings.id surrogate key. It is used by
// the finding-detail API (Requirement 13.2) to resolve the finding a request
// cites before loading its intel and evidence trail. It returns ErrNotFound
// when no row matches.
func (s *Store) Get(ctx context.Context, id int64) (Finding, error) {
	var (
		f         Finding
		assetKind string
		severity  string
		status    string
	)
	err := s.store.Pool().QueryRow(ctx,
		`SELECT id, asset_id, asset_kind, vuln_id, intel_ref,
		        risk_score, severity, status, first_seen, last_seen
		   FROM findings
		  WHERE id = $1`,
		id,
	).Scan(
		&f.ID, &f.AssetID, &assetKind, &f.VulnID, &f.IntelRef,
		&f.RiskScore, &severity, &status, &f.FirstSeen, &f.LastSeen,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	}
	if err != nil {
		return Finding{}, fmt.Errorf("finding: get %d: %w", id, err)
	}
	f.AssetKind = model.Kind(assetKind)
	f.Severity = canonical.Severity(severity)
	f.Status = Status(status)
	return f, nil
}

// Filter narrows a List query. A zero-value field is omitted from the WHERE
// clause; Limit <= 0 applies no limit.
type Filter struct {
	Severity  canonical.Severity
	Status    Status
	AssetKind model.Kind
	Limit     int
}

// List returns findings matching the optional filter, newest last_seen first.
// It is used by the findings API (Requirement 13.1) to serve severity/status/
// asset_kind/limit queries.
func (s *Store) List(ctx context.Context, filter Filter) ([]Finding, error) {
	// Build the WHERE clause from the non-empty filter fields, collecting
	// positional args in lockstep so the query stays parameterized.
	query := `SELECT id, asset_id, asset_kind, vuln_id, intel_ref,
	                 risk_score, severity, status, first_seen, last_seen
	            FROM findings`
	var (
		conds []string
		args  []any
	)
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if filter.Severity != "" {
		add("severity = $%d", string(filter.Severity))
	}
	if filter.Status != "" {
		add("status = $%d", string(filter.Status))
	}
	if filter.AssetKind != "" {
		add("asset_kind = $%d", string(filter.AssetKind))
	}
	for i, c := range conds {
		if i == 0 {
			query += " WHERE " + c
		} else {
			query += " AND " + c
		}
	}
	query += " ORDER BY last_seen DESC, id DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.store.Pool().Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("finding: list: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var (
			f         Finding
			assetKind string
			severity  string
			status    string
		)
		if err := rows.Scan(
			&f.ID, &f.AssetID, &assetKind, &f.VulnID, &f.IntelRef,
			&f.RiskScore, &severity, &status, &f.FirstSeen, &f.LastSeen,
		); err != nil {
			return nil, fmt.Errorf("finding: scan: %w", err)
		}
		f.AssetKind = model.Kind(assetKind)
		f.Severity = canonical.Severity(severity)
		f.Status = Status(status)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finding: list rows: %w", err)
	}
	return out, nil
}
