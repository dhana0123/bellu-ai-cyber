package steampipe

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// recordingQuerier is a fake querier that records the table name of every query
// the collector attempts (parsed from the "FROM <table> t" clause) and returns
// an empty, immediately-exhausted result set. It lets tests observe exactly
// which tables the collector issues a query for, with no live Steampipe/DB.
type recordingQuerier struct {
	mu       sync.Mutex
	attempts []string
}

func (r *recordingQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	r.mu.Lock()
	r.attempts = append(r.attempts, tableFromSQL(sql))
	r.mu.Unlock()
	return emptyRows{}, nil
}

func (r *recordingQuerier) queried(table string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.attempts {
		if a == table {
			return true
		}
	}
	return false
}

// tableFromSQL extracts the (possibly schema-qualified) table from the
// collector's "SELECT to_jsonb(t) AS row FROM <table> t ..." query and returns
// the bare table name.
func tableFromSQL(sql string) string {
	const marker = "FROM "
	i := strings.Index(sql, marker)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(sql[i+len(marker):])
	// rest looks like "<table> t" or "<schema>.<table> t ...".
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	tbl := fields[0]
	if dot := strings.LastIndex(tbl, "."); dot >= 0 {
		tbl = tbl[dot+1:]
	}
	return tbl
}

// emptyRows is a pgx.Rows that yields no rows and no error. It embeds the
// interface so only the methods collectTable actually calls (Next, Close, Err)
// need real bodies; the rest are never invoked for an empty result set.
type emptyRows struct{ pgx.Rows }

func (emptyRows) Close()     {}
func (emptyRows) Err() error { return nil }
func (emptyRows) Next() bool { return false }

// newRecordingSource builds a Source over the given catalog that records query
// attempts via a recordingQuerier instead of connecting to a live service. The
// capabilities arg is threaded through the same config seam Factory uses so the
// gate (once implemented) sees the enabled set; on unfixed code it is inert.
func newRecordingSource(cat *Catalog, capabilities []string) (*Source, *recordingQuerier) {
	rec := &recordingQuerier{}
	s := New("postgres://unused", cat)
	s.connect = func(context.Context) (querier, func(), error) { return rec, func() {}, nil }
	s.setEnabledCapabilitiesForTest(capabilities)
	return s, rec
}

// drain runs Collect to completion and returns emitted events and errors.
func drain(t *testing.T, s *Source) ([]rawevent.RawEvent, []error) {
	t.Helper()
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-gate"})
	var events []rawevent.RawEvent
	var errs []error
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			events = append(events, ev)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			errs = append(errs, err)
		}
	}
	return events, errs
}

// gatingCatalog returns a catalog with one core table plus the gated tables
// named by their capability, matching the real aws.yaml capability mapping.
func gatingCatalog() *Catalog {
	return &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_account", IDColumn: "account_id", ScopeColumn: "account_id", Capability: "core"},
			{Table: "aws_eks_cluster", IDColumn: "arn", ScopeColumn: "account_id", Capability: "eks"},
			{Table: "aws_lambda_function", IDColumn: "arn", ScopeColumn: "account_id", Capability: "workload"},
			{Table: "aws_bedrock_custom_model", IDColumn: "arn", ScopeColumn: "account_id", Capability: "ai"},
			// Synthetic DSPM-gated table: no DSPM-only table exists in aws.yaml
			// yet, so construct one to exercise the gate for that capability.
			{Table: "aws_rds_db_instance", IDColumn: "arn", ScopeColumn: "account_id", Capability: "dspm"},
			// Edge case: a gated table whose capability is one the enabled set
			// never names and the catalog otherwise does not use.
			{Table: "aws_made_up_table", IDColumn: "arn", ScopeColumn: "account_id", Capability: "unused-cap"},
			// Ungated table: must always be queried (preservation anchor).
			{Table: "aws_ungated_table", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
}

// TestBugCondition_UngrantedCapabilityTablesAreQueried is the task-1 bug
// exploration test. With enabled set ["core"], every gated table whose
// capability is NOT enabled must be SKIPPED (Property 1). On UNFIXED code the
// collector has no gate, so it queries them anyway and this test FAILS — which
// is the intended outcome that proves the bug. After the fix it PASSES.
func TestBugCondition_UngrantedCapabilityTablesAreQueried(t *testing.T) {
	enabled := []string{"core"}
	s, rec := newRecordingSource(gatingCatalog(), enabled)

	_, _ = drain(t, s)

	// isBugCondition cases: capability set, not "core", not in enabled set.
	gatedUngranted := []struct {
		table, capability string
	}{
		{"aws_eks_cluster", "eks"},
		{"aws_lambda_function", "workload"},
		{"aws_bedrock_custom_model", "ai"},
		{"aws_rds_db_instance", "dspm"},
		{"aws_made_up_table", "unused-cap"},
	}
	for _, g := range gatedUngranted {
		if rec.queried(g.table) {
			t.Errorf("Property 1 violated: table %q (capability %q) was QUERIED with enabled set %v; it must be skipped (no query)", g.table, g.capability, enabled)
		}
	}
}

// --- Task 2: preservation property tests ----------------------------------
//
// These capture behaviors the fix must NOT break. They are written so they
// PASS on the current (unfixed) code and must continue to PASS after the gate
// is added. On unfixed code there is no gate, so every table is queried; each
// assertion below is deliberately scoped to things the gate must leave true:
//   - core tables are always queried (core is never gated)
//   - gated tables whose capability IS enabled are queried
//   - ungated tables (no Capability set) are always queried
//   - per-table error isolation is preserved (one failing table does not stop
//     the rest, and the error surfaces on the error channel)
//   - the "no gating configured" fallback (nil enabled set) collects everything

// erroringQuerier returns an error for one specific table and empty rows for
// all others, so tests can assert error isolation and continuation.
type erroringQuerier struct {
	recordingQuerier
	failTable string
}

func (e *erroringQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	table := tableFromSQL(sql)
	e.mu.Lock()
	e.attempts = append(e.attempts, table)
	e.mu.Unlock()
	if table == e.failTable {
		return nil, context.DeadlineExceeded
	}
	return emptyRows{}, nil
}

// TestPreserve_CoreTableAlwaysQueried: a core table must always be queried,
// even when the enabled set names only "core" (and especially after the fix,
// core must never be gated out).
func TestPreserve_CoreTableAlwaysQueried(t *testing.T) {
	s, rec := newRecordingSource(gatingCatalog(), []string{"core"})
	_, _ = drain(t, s)
	if !rec.queried("aws_account") {
		t.Errorf("core table aws_account was not queried; core must always be collected")
	}
}

// TestPreserve_EnabledGatedTableIsQueried: a gated table whose capability IS in
// the enabled set must be queried. Here "eks" and "workload" are enabled, so
// their tables must be collected.
func TestPreserve_EnabledGatedTableIsQueried(t *testing.T) {
	s, rec := newRecordingSource(gatingCatalog(), []string{"core", "eks", "workload"})
	_, _ = drain(t, s)
	if !rec.queried("aws_eks_cluster") {
		t.Errorf("enabled gated table aws_eks_cluster (eks) was not queried; enabled capabilities must be collected")
	}
	if !rec.queried("aws_lambda_function") {
		t.Errorf("enabled gated table aws_lambda_function (workload) was not queried; enabled capabilities must be collected")
	}
}

// TestPreserve_UngatedTableAlwaysQueried: a table with no Capability set is not
// subject to the gate and must always be queried, regardless of the enabled
// set. This protects tables that predate capability annotation.
func TestPreserve_UngatedTableAlwaysQueried(t *testing.T) {
	s, rec := newRecordingSource(gatingCatalog(), []string{"core"})
	_, _ = drain(t, s)
	if !rec.queried("aws_ungated_table") {
		t.Errorf("ungated table aws_ungated_table was not queried; tables without a capability must always be collected")
	}
}

// TestPreserve_NoGatingConfiguredCollectsEverything: when no enabled set is
// configured (nil/empty), the source must behave exactly as before and collect
// every table including gated ones. This is the backward-compatible fallback.
func TestPreserve_NoGatingConfiguredCollectsEverything(t *testing.T) {
	s, rec := newRecordingSource(gatingCatalog(), nil)
	_, _ = drain(t, s)
	for _, tbl := range []string{
		"aws_account",
		"aws_eks_cluster",
		"aws_lambda_function",
		"aws_bedrock_custom_model",
		"aws_rds_db_instance",
		"aws_made_up_table",
		"aws_ungated_table",
	} {
		if !rec.queried(tbl) {
			t.Errorf("with no gating configured, table %q was not queried; the no-gating fallback must collect every table", tbl)
		}
	}
}

// TestPreserve_ErrorIsolationAndContinuation: one failing table must not stop
// the run; the error must surface on the error channel and later tables must
// still be queried. This behavior is independent of the capability gate and
// must be preserved. The failing table is ungated so it is attempted under any
// enabled set.
func TestPreserve_ErrorIsolationAndContinuation(t *testing.T) {
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_account", IDColumn: "account_id", ScopeColumn: "account_id", Capability: "core"},
			{Table: "aws_ungated_table", IDColumn: "arn", ScopeColumn: "account_id"},
			{Table: "aws_second_ungated", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
	eq := &erroringQuerier{failTable: "aws_ungated_table"}
	s := New("postgres://unused", cat)
	s.connect = func(context.Context) (querier, func(), error) { return eq, func() {}, nil }
	s.setEnabledCapabilitiesForTest(nil)

	_, errs := drain(t, s)

	if len(errs) == 0 {
		t.Fatalf("expected an error from the failing table to surface on the error channel; got none")
	}
	foundErr := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "aws_ungated_table") {
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("expected an error mentioning the failing table aws_ungated_table; got %v", errs)
	}
	// Continuation: a table after the failing one must still be queried.
	if !eq.queried("aws_second_ungated") {
		t.Errorf("table after the failing one (aws_second_ungated) was not queried; error isolation/continuation must be preserved")
	}
}
