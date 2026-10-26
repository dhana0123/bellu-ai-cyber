package finding

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// MonitorEmitter is the minimal monitor-event sink the vulnerability delta tap
// needs. It is injected (never imported as a concrete server type) so the
// finding package stays free of transport/server dependencies and no import
// cycle is created; *store.Store satisfies it via its InsertMonitorEvent
// method. A nil emitter disables the tap (the upsert still works unchanged), so
// code paths that have no monitor wiring keep compiling and running.
type MonitorEmitter interface {
	InsertMonitorEvent(ctx context.Context, org int64, in store.MonitorEventInput) (int64, error)
}

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
	res := UpsertResult{Created: inserted}
	// Vulnerability delta tap: fire exactly one monitor event ONLY on a
	// nothing->open transition (Created == true); a re-upsert of a still-open
	// finding reports Created == false and emits nothing, so a re-correlation
	// sweep over unchanged findings produces no events — no re-trigger storm
	// (Requirements 4.5, 4.6, 5.2, 5.3). The emit is best-effort: a monitor-emit
	// failure must never fail the upsert the caller depends on, and even if a
	// delta fired twice the store's unique (org, source, delivery_id) key folds
	// the second into a no-op.
	if res.Created && s.emitter != nil {
		s.emitVulnerabilityDelta(ctx, org, f)
	}
	return res, nil
}

// emitVulnerabilityDelta persists one vulnerability monitor event for a
// nothing->open finding delta. source = "correlate", event_kind =
// "vulnerability", delivery_id = asset_id + ":" + vuln_id (Requirement 2.6).
// The org is the caller's authenticated tenant (never read from a payload), so
// the event lands in the right tenant's queue (tenant isolation). It is
// best-effort: all errors (including the duplicate sentinel) are swallowed so
// the upsert path is never disturbed.
func (s *Store) emitVulnerabilityDelta(ctx context.Context, org int64, f Finding) {
	payload, err := json.Marshal(map[string]string{
		"asset_id": f.AssetID,
		"vuln_id":  f.VulnID,
	})
	if err != nil {
		return
	}
	_, _ = s.emitter.InsertMonitorEvent(ctx, org, store.MonitorEventInput{
		Source:     "correlate",
		DeliveryID: f.AssetID + ":" + f.VulnID,
		EventKind:  "vulnerability",
		Payload:    payload,
	})
}
