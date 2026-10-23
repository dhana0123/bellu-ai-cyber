package finding

import (
	"context"
	"fmt"
)

// UpsertResult reports the outcome of an UpsertFinding call. Created is true
// when the upsert inserted a brand-new row and false when it updated an
// existing one — the agents-plane findings:ingest seam uses this to tally the
// per-batch created vs updated counts (Requirement 6.10).
type UpsertResult struct {
	// Created is true when a new findings row was inserted, false when an
	// existing (asset_id, vuln_id) row was updated instead.
	Created bool
}

// UpsertFinding idempotently writes one agent-ingested finding, returning
// whether the row was created or updated (Requirements 6.8, 6.10).
//
// It mirrors Upsert's dedup-on-findings_uniq (asset_id, vuln_id) behavior: a
// repeated ingest of the same key updates the existing row (advancing
// last_seen, refreshing the mutable columns, re-asserting status='open') rather
// than creating a duplicate, so ingesting the same finding N times leaves
// exactly one row (correctness Property 2). first_seen is written only on
// INSERT, so the original timestamp is preserved across updates.
//
// org is accepted so the seam resolves the tenant from the authenticated
// principal and never from the request body; the findings table is not itself
// org-partitioned, so org-scoping of unlinked targets is carried in the
// synthetic asset_id the handler builds (Requirement 6.5). Created vs updated
// is detected via the system column xmax: a fresh INSERT leaves xmax = 0 on the
// returned row, whereas an ON CONFLICT UPDATE sets it non-zero.
func (s *Store) UpsertFinding(ctx context.Context, org int64, f Finding) (UpsertResult, error) {
	var inserted bool
	err := s.store.Pool().QueryRow(ctx,
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
		   last_seen  = now()
		 RETURNING (xmax = 0)`,
		f.AssetID, string(f.AssetKind), f.VulnID, f.IntelRef,
		f.RiskScore, string(f.Severity),
	).Scan(&inserted)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("finding: upsert ingest (%s/%s): %w", f.AssetID, f.VulnID, err)
	}
	return UpsertResult{Created: inserted}, nil
}
