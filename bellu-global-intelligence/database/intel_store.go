// Package database persists canonical Global Intelligence into the intel_*
// Postgres tables defined by migration 00005_intel.sql. It reuses the existing
// pgx v5 connection pool exposed by *store.Store.Pool() and the project's
// INSERT ... ON CONFLICT upsert idioms (see internal/store/normalized.go), so
// intelligence persistence shares the same database access path as the rest of
// the pipeline.
//
// Idempotence (Requirement 6.2 / correctness Property 3) is the central
// guarantee: re-running UpsertVuln with the same canonical record leaves every
// intel_* table in the same state except for the last_seen timestamp on
// intel_vulnerabilities. Each canonical vulnerability is written atomically in
// a single transaction so a partial failure never leaves a vuln without its
// aliases, affected ranges, or EPSS history.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// IntelStore persists canonical intelligence into the intel_* tables using the
// shared *store.Store connection pool.
type IntelStore struct {
	store *store.Store
}

// New returns an IntelStore backed by the given *store.Store. The caller
// retains ownership of the Store (and its pool lifecycle).
func New(s *store.Store) *IntelStore {
	return &IntelStore{store: s}
}

// FetchRun describes one source fetch for the intel_fetch_runs audit table.
// The counts and cursors are accumulated by the pipeline as a run progresses;
// RecordFetchRun writes the final accounting for the run.
type FetchRun struct {
	Source         string
	Cursor         string // next incremental cursor (== CursorAfter)
	RawCount       int
	RecordsAdded   int
	RecordsUpdated int
	RecordsDeleted int
	CursorBefore   string
	CursorAfter    string
	Status         string // SUCCESS | FETCH_FAILED | PARSE_FAILED | ...
	Error          string // failure detail; empty when SUCCESS
	StartedAt      time.Time
	FinishedAt     time.Time
}

// UpsertVuln idempotently persists one canonical vulnerability and all of its
// dependent rows (aliases, affected ranges, and optionally a daily EPSS history
// row) in a single transaction.
//
// On a no-op re-run the only observable change is intel_vulnerabilities.last_seen,
// which is refreshed to now(); first_seen is preserved. Aliases are re-pointed
// at this vuln id; affected ranges are inserted once per unique
// (vuln_id, purl, introduced, fixed) tuple and left untouched thereafter.
//
// If scoredOn is non-zero and the record carries an EPSS score, a daily
// intel_epss_history row keyed by (vuln_id, scored_on) is appended (Requirement
// 6.3); re-running on the same date overwrites that day's epss/percentile only.
// A zero scoredOn skips history entirely.
func (s *IntelStore) UpsertVuln(ctx context.Context, v canonical.CanonicalVuln, scoredOn time.Time, percentile float64) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("database: invalid canonical vuln %q: %w", v.ID, err)
	}

	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := upsertVulnRow(ctx, tx, v); err != nil {
		return err
	}
	if err := upsertAliases(ctx, tx, v); err != nil {
		return err
	}
	if err := upsertAffected(ctx, tx, v); err != nil {
		return err
	}
	if !scoredOn.IsZero() {
		if err := upsertEPSSHistory(ctx, tx, v.ID, scoredOn, v.EPSS, percentile); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit vuln %q: %w", v.ID, err)
	}
	return nil
}

// nullableScore converts a canonical CVSS/EPSS score into a value suitable for
// a nullable DOUBLE PRECISION column: a zero score (no data) is stored as NULL
// rather than a misleading 0.0.
func nullableScore(score float64) *float64 {
	if score == 0 {
		return nil
	}
	s := score
	return &s
}

// nullableTime converts a zero time.Time into NULL for a nullable TIMESTAMPTZ
// column.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	tt := t
	return &tt
}

// upsertVulnRow writes the canonical vulnerability row. ON CONFLICT (id) it
// refreshes the mutable intelligence fields and last_seen = now() while
// preserving first_seen, so a no-op re-run only moves last_seen forward.
func upsertVulnRow(ctx context.Context, tx pgx.Tx, v canonical.CanonicalVuln) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO intel_vulnerabilities
		   (id, summary, cvss_score, cvss_vector, cvss_version, severity,
		    known_exploited, epss, published, modified, sources,
		    first_seen, last_seen)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now(), now())
		 ON CONFLICT (id) DO UPDATE SET
		   summary         = EXCLUDED.summary,
		   cvss_score      = EXCLUDED.cvss_score,
		   cvss_vector     = EXCLUDED.cvss_vector,
		   cvss_version    = EXCLUDED.cvss_version,
		   severity        = EXCLUDED.severity,
		   known_exploited = EXCLUDED.known_exploited,
		   epss            = EXCLUDED.epss,
		   published       = EXCLUDED.published,
		   modified        = EXCLUDED.modified,
		   sources         = EXCLUDED.sources,
		   last_seen       = now()`,
		v.ID,
		v.Summary,
		nullableScore(v.CVSS.Score),
		v.CVSS.Vector,
		v.CVSS.Version,
		string(v.Severity),
		v.KnownExploited,
		nullableScore(v.EPSS),
		nullableTime(v.Published),
		nullableTime(v.Modified),
		v.Sources,
	)
	if err != nil {
		return fmt.Errorf("database: upsert vuln %q: %w", v.ID, err)
	}
	return nil
}

// upsertAliases points every alias at this vuln id. ON CONFLICT (alias) the
// vuln_id is re-pointed, which is a no-op when the alias already resolves here.
func upsertAliases(ctx context.Context, tx pgx.Tx, v canonical.CanonicalVuln) error {
	for _, alias := range v.Aliases {
		_, err := tx.Exec(ctx,
			`INSERT INTO intel_aliases (alias, vuln_id)
			 VALUES ($1, $2)
			 ON CONFLICT (alias) DO UPDATE SET vuln_id = EXCLUDED.vuln_id`,
			alias, v.ID)
		if err != nil {
			return fmt.Errorf("database: upsert alias %q -> %q: %w", alias, v.ID, err)
		}
	}
	return nil
}

// upsertAffected inserts one row per affected (purl, introduced, fixed) range.
// A package with no explicit ranges still records a single open range so the
// affected purl is captured. ON CONFLICT DO NOTHING keeps the row immutable, so
// re-runs add nothing (Requirement 6.2).
func upsertAffected(ctx context.Context, tx pgx.Tx, v canonical.CanonicalVuln) error {
	for _, ap := range v.Affected {
		ranges := ap.Ranges
		if len(ranges) == 0 {
			// Record the affected purl even when no bounded range is known.
			ranges = []canonical.SemverRange{{}}
		}
		for _, r := range ranges {
			_, err := tx.Exec(ctx,
				`INSERT INTO intel_affected_packages
				   (vuln_id, purl, ecosystem, introduced, fixed)
				 VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (vuln_id, purl, introduced, fixed) DO NOTHING`,
				v.ID, ap.Purl, ap.Ecosystem, r.Introduced, r.Fixed)
			if err != nil {
				return fmt.Errorf("database: upsert affected %q/%q: %w", v.ID, ap.Purl, err)
			}
		}
	}
	return nil
}

// upsertEPSSHistory appends a daily EPSS data point keyed by (vuln_id,
// scored_on). ON CONFLICT it overwrites only that day's epss/percentile, so the
// history series stays one row per day (Requirement 6.3) and re-running on the
// same date is idempotent.
func upsertEPSSHistory(ctx context.Context, tx pgx.Tx, vulnID string, scoredOn time.Time, epss, percentile float64) error {
	// scored_on is a DATE; truncate to the UTC calendar day for a stable key.
	day := scoredOn.UTC().Truncate(24 * time.Hour)
	_, err := tx.Exec(ctx,
		`INSERT INTO intel_epss_history (vuln_id, scored_on, epss, percentile)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (vuln_id, scored_on) DO UPDATE SET
		   epss       = EXCLUDED.epss,
		   percentile = EXCLUDED.percentile`,
		vulnID, day, epss, nullableScore(percentile))
	if err != nil {
		return fmt.Errorf("database: upsert epss history %q@%s: %w", vulnID, day.Format("2006-01-02"), err)
	}
	return nil
}

// UpsertVulns persists a batch of canonical vulnerabilities, each in its own
// transaction via UpsertVuln. scoredOn/percentile apply to every record in the
// batch; pass a zero scoredOn to skip EPSS history. It stops at the first error.
func (s *IntelStore) UpsertVulns(ctx context.Context, vulns []canonical.CanonicalVuln, scoredOn time.Time) error {
	for _, v := range vulns {
		if err := s.UpsertVuln(ctx, v, scoredOn, 0); err != nil {
			return err
		}
	}
	return nil
}

// RecordEPSSHistory records the daily EPSS data point for each vuln (keyed by
// vuln_id, scored_on) and refreshes ONLY the current epss score plus last_seen
// on intel_vulnerabilities. Crucially it never overwrites summary, cvss_*,
// severity, known_exploited, published, modified, or sources — so a canonical
// row that was merged from OSV/KEV (carrying CVSS, a summary, and the KEV flag)
// keeps those attributes when an EPSS-only record for the same CVE records its
// daily score. Using the full UpsertVuln here would clobber the merged
// attributes with the EPSS-only record's empty values.
//
// Each vuln is written in its own transaction. A zero scoredOn is a no-op
// (nothing to record for a day that is not known). The per-day history row is
// idempotent: re-running the same day overwrites only that day's epss/percentile,
// and the vuln UPDATE touches only epss/last_seen. An UPDATE that matches no row
// (the vuln is not yet persisted) is a harmless no-op, since run() upserts the
// deduped canonical set before recording EPSS history.
func (s *IntelStore) RecordEPSSHistory(ctx context.Context, vulns []canonical.CanonicalVuln, scoredOn time.Time) error {
	if scoredOn.IsZero() {
		return nil
	}
	for _, v := range vulns {
		if err := s.recordEPSSHistoryOne(ctx, v, scoredOn); err != nil {
			return err
		}
	}
	return nil
}

// recordEPSSHistoryOne writes one vuln's daily EPSS history row and refreshes
// only its current epss + last_seen, atomically.
func (s *IntelStore) recordEPSSHistoryOne(ctx context.Context, v canonical.CanonicalVuln, scoredOn time.Time) error {
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: begin epss-history tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Daily series row (idempotent per (vuln_id, scored_on)).
	if err := upsertEPSSHistory(ctx, tx, v.ID, scoredOn, v.EPSS, 0); err != nil {
		return err
	}

	// Refresh only the current epss and last_seen; never touch the merged
	// attributes. A no-match UPDATE (unknown vuln) is a harmless no-op.
	if _, err := tx.Exec(ctx,
		`UPDATE intel_vulnerabilities
		    SET epss = $2, last_seen = now()
		  WHERE id = $1`,
		v.ID, nullableScore(v.EPSS),
	); err != nil {
		return fmt.Errorf("database: refresh epss for %q: %w", v.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit epss-history %q: %w", v.ID, err)
	}
	return nil
}

// LastCursor reads the most recent successful incremental cursor for a source
// into dst. It returns the cursor_after of the newest intel_fetch_runs row for
// the source whose status is SUCCESS; when the source has no successful run yet
// dst is left as the empty string (a full fetch on the next incremental run).
//
// This is the resume marker the bellu-intel pipeline writes on every run via
// RecordFetchRun, so reads and writes of the cursor share one table and stay in
// lock step. (intel_sources.last_cursor remains the scheduler's authoritative
// marker.)
func (s *IntelStore) LastCursor(ctx context.Context, source string, dst *string) error {
	var cursor string
	err := s.store.Pool().QueryRow(ctx,
		`SELECT cursor_after
		   FROM intel_fetch_runs
		  WHERE source = $1 AND status = $2
		  ORDER BY id DESC
		  LIMIT 1`,
		source, "SUCCESS",
	).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		*dst = ""
		return nil
	}
	if err != nil {
		return fmt.Errorf("database: last cursor (%s): %w", source, err)
	}
	*dst = cursor
	return nil
}

// RecordFetchRun records one source fetch in intel_fetch_runs with its source,
// cursor, counts, and status (Requirement 6.4). Failed runs are written just
// like successful ones so the audit trail is never lost. It returns the new
// run id.
func (s *IntelStore) RecordFetchRun(ctx context.Context, run FetchRun) (int64, error) {
	status := run.Status
	if status == "" {
		status = "running"
	}
	var id int64
	err := s.store.Pool().QueryRow(ctx,
		`INSERT INTO intel_fetch_runs
		   (source, cursor, raw_count, records_added, records_updated,
		    records_deleted, cursor_before, cursor_after, status, error,
		    started_at, finished_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
		         COALESCE($11, now()), $12)
		 RETURNING id`,
		run.Source, run.Cursor, run.RawCount, run.RecordsAdded, run.RecordsUpdated,
		run.RecordsDeleted, run.CursorBefore, run.CursorAfter, status, run.Error,
		nullableTime(run.StartedAt), nullableTime(run.FinishedAt),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("database: record fetch run (%s): %w", run.Source, err)
	}
	return id, nil
}
