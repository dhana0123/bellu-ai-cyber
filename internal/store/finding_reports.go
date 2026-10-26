package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// FindingReport is the agent's actual generated report artifact for one
// (finding_id, team), carried through and stored (not re-derived): structured
// columns back the list/badges, the JSONB Sections field holds the long-form
// structured content, and RenderedMarkdown is the plane's rendered artifact for
// the detail view (Req 14.1).
//
// SECURITY (Property P6, Req 16.2/16.3): the weaponized proof-of-concept body
// is NEVER a field here. PoCAvailable is a flag only, and the store defensively
// strips any poc_script_code key from Sections at any nesting level before
// persisting (see UpsertFindingReport) so a weaponized body can never be stored
// even if a caller sends one.
type FindingReport struct {
	FindingID        int64           // findings.id surrogate key the report attaches to
	RunID            string          // "" when unlinked (run_id is nullable)
	Team             string          // red|blue
	Title            string          // short title for list/badges
	Severity         string          // severity bucket for list/badges
	PoCAvailable     bool            // flag only; never the body (P6)
	Sections         json.RawMessage // long-form structured fields; poc_script_code stripped (P6)
	RenderedMarkdown string          // the plane's rendered markdown artifact
}

// pocScriptCodeKey is the JSON key whose value is the weaponized
// proof-of-concept body. It is stripped from Sections at every nesting level
// before persistence (Property P6, Req 16.2) and there is no column for it.
const pocScriptCodeKey = "poc_script_code"

// UpsertFindingReport writes (or refreshes) the report for a (finding_id, team)
// pair. It is idempotent per (finding_id, team): the first report for a pair
// inserts a row, and a re-report for the same pair updates that row in place —
// replacing title, severity, poc_available, sections, rendered markdown, and
// run linkage — and never inserts a second row (Req 14.1, 14.2), relying on the
// finding_reports_uniq (finding_id, team) index.
//
// Org scoping (tenant isolation / P1, Req 19.1): the write is gated through the
// findings table via INSERT ... SELECT, so a row is produced only when the
// target finding_id actually exists. The findings table is NOT org-partitioned
// (it has no org_id column — see internal/finding/ingest_store.go), so, exactly
// as the findings:ingest seam does, tenant scoping of a finding is carried in
// the synthetic asset_id the handler resolves before calling in; the org
// argument is resolved from the authenticated principal (never the request
// body) and is threaded here for API consistency and so this seam can tighten
// to a real join the day findings become org-partitioned, with no signature
// change. A finding_id that does not exist matches no row in the SELECT, so
// nothing is inserted or updated — the call is a silent no-op with no existence
// disclosure, matching GetAgentRun's no-disclosure approach.
//
// SECURITY (Property P6, Req 16.2/16.3): before persisting, every occurrence of
// the poc_script_code key is stripped from Sections at any nesting level (via
// stripPoCScriptCode) while every other field is preserved, so the stored
// JSONB can never carry a weaponized body even if the caller supplied one.
func (s *Store) UpsertFindingReport(ctx context.Context, org int64, r FindingReport) error {
	// Defensively redact the weaponized PoC body from the long-form sections at
	// every nesting level before it can reach the database (P6). This is a
	// belt-and-suspenders strip: the plane already omits the body, but the store
	// must guarantee it is never persisted regardless of what a caller sends.
	sections, err := stripPoCScriptCode(r.Sections)
	if err != nil {
		return fmt.Errorf("store: upsert finding report: redact sections: %w", err)
	}

	// run_id is nullable: an unlinked report ("") stores SQL NULL rather than an
	// empty string so the run_id FK stays valid.
	var runArg any
	if r.RunID != "" {
		runArg = r.RunID
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO finding_reports
		     (finding_id, run_id, team, title, severity, poc_available,
		      sections, rendered_markdown)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8
		   FROM findings
		  WHERE findings.id = $1
		 ON CONFLICT (finding_id, team) DO UPDATE
		    SET run_id            = EXCLUDED.run_id,
		        title             = EXCLUDED.title,
		        severity          = EXCLUDED.severity,
		        poc_available     = EXCLUDED.poc_available,
		        sections          = EXCLUDED.sections,
		        rendered_markdown = EXCLUDED.rendered_markdown`,
		r.FindingID, runArg, r.Team, r.Title, r.Severity, r.PoCAvailable,
		sections, r.RenderedMarkdown)
	if err != nil {
		return fmt.Errorf("store: upsert finding report: %w", err)
	}
	return nil
}

// GetFindingReport returns the report(s) stored for a finding, scoped to the
// caller's org. A finding may carry up to two reports (a red and a blue one),
// so the result is a slice ordered by team for a stable read.
//
// When the finding has no reports (or does not exist), it returns an empty
// slice and a nil error — NOT an error — so the finding detail read can degrade
// gracefully to reports: [] and still render the header/intel/evidence
// (Property P7, supports task 20). Org scoping mirrors UpsertFindingReport: the
// org argument is resolved from the authenticated principal and threaded for
// API consistency (findings are not org-partitioned, so the handler-resolved
// finding_id carries the tenant boundary); a cross-org or nonexistent finding
// simply yields no rows, disclosing no existence (P1, Req 19.1).
func (s *Store) GetFindingReport(ctx context.Context, org, findingID int64) ([]FindingReport, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT finding_id, COALESCE(run_id, ''), team, title, severity,
		        poc_available, sections, rendered_markdown
		   FROM finding_reports
		  WHERE finding_id = $1
		  ORDER BY team`,
		findingID)
	if err != nil {
		return nil, fmt.Errorf("store: get finding report: %w", err)
	}
	defer rows.Close()

	out := make([]FindingReport, 0, 2)
	for rows.Next() {
		var r FindingReport
		if err := rows.Scan(&r.FindingID, &r.RunID, &r.Team, &r.Title, &r.Severity,
			&r.PoCAvailable, &r.Sections, &r.RenderedMarkdown); err != nil {
			return nil, fmt.Errorf("store: get finding report: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get finding report: %w", err)
	}
	return out, nil
}

// stripPoCScriptCode returns the sections JSON with every poc_script_code key
// removed at ANY nesting level, while preserving all other fields (Property P6,
// Req 16.2). It walks the decoded JSON tree (objects and arrays, recursively),
// deletes the key wherever it appears, and re-marshals.
//
// An empty/nil input returns the canonical empty object ({}) matching the
// column default. Non-object JSON (e.g. a top-level array) is still walked so a
// poc_script_code key nested anywhere inside it is removed. Input that is not
// valid JSON is rejected with an error rather than persisted verbatim, so a
// malformed body can never slip an un-walked payload past the strip.
func stripPoCScriptCode(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("invalid sections json: %w", err)
	}
	cleaned := redactPoC(v)
	out, err := json.Marshal(cleaned)
	if err != nil {
		return nil, fmt.Errorf("re-marshal sections: %w", err)
	}
	return out, nil
}

// redactPoC recursively removes every poc_script_code key from a decoded JSON
// value. For objects it deletes the key and recurses into the remaining values;
// for arrays it recurses into each element; scalars pass through unchanged. The
// recursion reaches the key at any nesting depth (P6).
func redactPoC(v any) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, pocScriptCodeKey)
		for k, child := range t {
			t[k] = redactPoC(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactPoC(child)
		}
		return t
	default:
		return v
	}
}
