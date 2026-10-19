package cloudquery

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// --- Fake querier --------------------------------------------------------
//
// The collector reads each table with `SELECT to_jsonb(t) AS row FROM <table>`
// and scans a single JSON column per row. The fakes below satisfy the package
// querier interface and pgx.Rows without a live database, keyed by the staging
// table name parsed out of the SQL, so one fake can serve a multi-table run and
// drive per-table behavior (rows, an undefined table, or a query error).

// tableRows describes what one staging table yields: a sequence of JSON row
// objects, an optional query-time error (returned from Query before any row is
// scanned), and whether the table is "undefined" (SQLSTATE 42P01, i.e. the
// sync never produced it).
type tableRows struct {
	rows      []map[string]any
	queryErr  error
	undefined bool
}

// fakeQuerier serves rows per staging table and records the (bare) table name
// of every query attempted, so tests can assert which tables were read.
type fakeQuerier struct {
	mu       sync.Mutex
	byTable  map[string]tableRows
	attempts []string
}

func (f *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	table := tableFromSQL(sql)
	f.mu.Lock()
	f.attempts = append(f.attempts, table)
	f.mu.Unlock()

	tr, ok := f.byTable[table]
	if !ok {
		// A table the test did not configure behaves as an empty result set.
		return &jsonRows{}, nil
	}
	if tr.undefined {
		return nil, &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}
	}
	if tr.queryErr != nil {
		return nil, tr.queryErr
	}
	encoded := make([][]byte, 0, len(tr.rows))
	for _, r := range tr.rows {
		b, err := json.Marshal(r)
		if err != nil {
			panic(err)
		}
		encoded = append(encoded, b)
	}
	return &jsonRows{data: encoded}, nil
}

func (f *fakeQuerier) queried(table string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.attempts {
		if a == table {
			return true
		}
	}
	return false
}

// tableFromSQL extracts the bare table name from the collector's
// "SELECT to_jsonb(t) AS row FROM <schema>.<table> t ..." query.
func tableFromSQL(sql string) string {
	const marker = "FROM "
	i := strings.Index(sql, marker)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(sql[i+len(marker):])
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

// jsonRows is a pgx.Rows over a slice of pre-encoded JSON row payloads. It
// embeds the interface so only the methods collectTable calls (Next, Scan,
// Close, Err) need real bodies.
type jsonRows struct {
	pgx.Rows
	data [][]byte
	pos  int
}

func (r *jsonRows) Next() bool { return r.pos < len(r.data) }

func (r *jsonRows) Scan(dest ...any) error {
	cur := r.data[r.pos]
	r.pos++
	// collectTable scans a single *[]byte destination.
	p, ok := dest[0].(*[]byte)
	if !ok {
		panic("jsonRows.Scan: expected *[]byte destination")
	}
	buf := make([]byte, len(cur))
	copy(buf, cur)
	*p = buf
	return nil
}

func (r *jsonRows) Close()     {}
func (r *jsonRows) Err() error { return nil }

// --- Fake progress reporter ----------------------------------------------

// recordingReporter captures every progress signal the collector emits so a
// test can assert a skipped table is reported (and counted) as degraded while
// the healthy tables still complete.
type recordingReporter struct {
	mu      sync.Mutex
	planned int
	started []string
	done    map[string]int
	errored map[string]error
}

func newRecordingReporter() *recordingReporter {
	return &recordingReporter{done: map[string]int{}, errored: map[string]error{}}
}

func (r *recordingReporter) TablesPlanned(total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.planned = total
}

func (r *recordingReporter) TableStarted(table string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, table)
}

func (r *recordingReporter) TableDone(table string, rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done[table] = rows
}

func (r *recordingReporter) TableError(table string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errored[table] = err
}

func (r *recordingReporter) tableDone(table string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.done[table]
	return n, ok
}

func (r *recordingReporter) tableErrored(table string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.errored[table]
	return ok
}

// --- Test helpers ---------------------------------------------------------

// newFakeSource builds a Source over the given catalog that reads from the fake
// querier instead of a live PostgreSQL, via the injectable connect seam.
func newFakeSource(cat *Catalog, q *fakeQuerier) *Source {
	s := New("postgres://unused", cat)
	s.connect = func(context.Context) (querier, func(), error) { return q, func() {}, nil }
	return s
}

// collect drains Collect to completion and returns emitted events and errors.
func collect(t *testing.T, s *Source, run source.Run) ([]rawevent.RawEvent, []error) {
	t.Helper()
	evc, errc := s.Collect(context.Background(), run)
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

// --- Tests -----------------------------------------------------------------

// TestMapRowPopulatedRow asserts a populated staging row maps to exactly one
// finalized RawEvent carrying source="cloudquery", the catalog provider, and
// the identity/scope/region columns named by the TableSpec. Table-driven so
// the mapping of each identity column is checked independently
// (Requirements 2.2, 2.3).
func TestMapRowPopulatedRow(t *testing.T) {
	cases := []struct {
		name       string
		spec       TableSpec
		row        map[string]any
		wantID     string
		wantScope  string
		wantRegion string
		wantType   string
	}{
		{
			name: "arn id with scope and region",
			spec: TableSpec{
				Table:        "aws_s3_bucket",
				CQTable:      "aws_s3_buckets",
				IDColumn:     "arn",
				ScopeColumn:  "account_id",
				RegionColumn: "region",
			},
			row: map[string]any{
				"arn":        "arn:aws:s3:::my-bucket",
				"account_id": "111122223333",
				"region":     "us-east-1",
			},
			wantID:     "arn:aws:s3:::my-bucket",
			wantScope:  "111122223333",
			wantRegion: "us-east-1",
			wantType:   "aws_s3_bucket",
		},
		{
			name: "global resource has no region",
			spec: TableSpec{
				Table:       "aws_iam_role",
				CQTable:     "aws_iam_roles",
				IDColumn:    "arn",
				ScopeColumn: "account_id",
			},
			row: map[string]any{
				"arn":        "arn:aws:iam::111122223333:role/admin",
				"account_id": "111122223333",
			},
			wantID:    "arn:aws:iam::111122223333:role/admin",
			wantScope: "111122223333",
			wantType:  "aws_iam_role",
		},
		{
			name: "resource_type override is respected",
			spec: TableSpec{
				Table:        "aws_ec2_instance",
				ResourceType: "ec2_instance",
				IDColumn:     "instance_id",
				ScopeColumn:  "account_id",
				RegionColumn: "region",
			},
			row: map[string]any{
				"instance_id": "i-0abc123",
				"account_id":  "444455556666",
				"region":      "eu-west-1",
			},
			wantID:     "i-0abc123",
			wantScope:  "444455556666",
			wantRegion: "eu-west-1",
			wantType:   "ec2_instance",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := &Catalog{Provider: "aws", Connection: "aws", Tables: []TableSpec{tc.spec}}
			q := &fakeQuerier{byTable: map[string]tableRows{
				tc.spec.cqTable(): {rows: []map[string]any{tc.row}},
			}}
			s := newFakeSource(cat, q)

			events, errs := collect(t, s, source.Run{RunID: "run-map"})
			if len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event, got %d", len(events))
			}
			ev := events[0]

			if ev.Source != SourceName {
				t.Errorf("Source = %q, want %q", ev.Source, SourceName)
			}
			if ev.Provider != "aws" {
				t.Errorf("Provider = %q, want %q", ev.Provider, "aws")
			}
			if ev.ResourceID != tc.wantID {
				t.Errorf("ResourceID = %q, want %q", ev.ResourceID, tc.wantID)
			}
			if ev.Scope != tc.wantScope {
				t.Errorf("Scope = %q, want %q", ev.Scope, tc.wantScope)
			}
			if ev.Region != tc.wantRegion {
				t.Errorf("Region = %q, want %q", ev.Region, tc.wantRegion)
			}
			if ev.ResourceType != tc.wantType {
				t.Errorf("ResourceType = %q, want %q", ev.ResourceType, tc.wantType)
			}
			if ev.RunID != "run-map" {
				t.Errorf("RunID = %q, want %q", ev.RunID, "run-map")
			}
			// The event must be finalized (content-addressed and valid).
			if ev.ID == "" || ev.PayloadHash == "" {
				t.Errorf("event not finalized: ID=%q PayloadHash=%q", ev.ID, ev.PayloadHash)
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestMapRowExcludesInternalAndToleratesUnknownColumns asserts that CloudQuery
// internal `_cq_*` columns are stripped from the payload while unknown columns
// are kept and never fail the row (Requirements 2.5, 18.2).
func TestMapRowExcludesInternalAndToleratesUnknownColumns(t *testing.T) {
	spec := TableSpec{
		Table:       "aws_iam_role",
		CQTable:     "aws_iam_roles",
		IDColumn:    "arn",
		ScopeColumn: "account_id",
	}
	cat := &Catalog{Provider: "aws", Connection: "aws", Tables: []TableSpec{spec}}
	row := map[string]any{
		"arn":                "arn:aws:iam::111122223333:role/admin",
		"account_id":         "111122223333",
		"_cq_id":             "11111111-1111-1111-1111-111111111111",
		"_cq_source_name":    "aws",
		"_cq_sync_time":      "2024-01-01T00:00:00Z",
		"_cq_parent_id":      "parent",
		"_cq_future_column":  "whatever", // unknown _cq_ column must also be dropped
		"some_unknown_field": "kept",     // unknown non-internal column is tolerated and kept
		"tags":               map[string]any{"env": "prod"},
	}
	q := &fakeQuerier{byTable: map[string]tableRows{
		spec.cqTable(): {rows: []map[string]any{row}},
	}}
	s := newFakeSource(cat, q)

	events, errs := collect(t, s, source.Run{RunID: "run-cols"})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}

	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	// No _cq_* column may survive into the payload.
	for k := range payload {
		if strings.HasPrefix(k, "_cq_") {
			t.Errorf("internal column %q leaked into payload", k)
		}
	}
	// Known identity and the unknown field must be retained.
	if _, ok := payload["arn"]; !ok {
		t.Errorf("expected mapped id column arn to remain in payload")
	}
	if got, ok := payload["some_unknown_field"]; !ok || got != "kept" {
		t.Errorf("unknown column some_unknown_field = %v (present=%v), want %q", got, ok, "kept")
	}
	if _, ok := payload["tags"]; !ok {
		t.Errorf("expected unknown column tags to remain in payload")
	}
}

// TestPerTableIsolationAbsentTable asserts a staging table the sync never
// produced (SQLSTATE 42P01) is skipped and counted via the error channel and
// reporter, while other tables still emit their rows (Requirements 2.6, 2.7).
func TestPerTableIsolationAbsentTable(t *testing.T) {
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", CQTable: "aws_iam_roles", IDColumn: "arn", ScopeColumn: "account_id"},
			{Table: "aws_missing_table", CQTable: "aws_missing_tables", IDColumn: "arn", ScopeColumn: "account_id"},
			{Table: "aws_s3_bucket", CQTable: "aws_s3_buckets", IDColumn: "arn", ScopeColumn: "account_id", RegionColumn: "region"},
		},
	}
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
		"aws_missing_tables": {undefined: true},
		"aws_s3_buckets": {rows: []map[string]any{
			{"arn": "arn:aws:s3:::b1", "account_id": "111122223333", "region": "us-east-1"},
			{"arn": "arn:aws:s3:::b2", "account_id": "111122223333", "region": "us-east-1"},
		}},
	}}
	reporter := newRecordingReporter()
	s := newFakeSource(cat, q)

	events, errs := collect(t, s, source.Run{RunID: "run-isolation", Reporter: reporter})

	// Healthy tables still emit: 1 iam role + 2 s3 buckets = 3 events.
	if len(events) != 3 {
		t.Fatalf("expected 3 events from healthy tables, got %d", len(events))
	}
	// The missing table never emits an event.
	for _, ev := range events {
		if ev.ResourceType == "aws_missing_table" {
			t.Errorf("absent table aws_missing_table unexpectedly emitted an event: %+v", ev)
		}
	}

	// The skip surfaces on the error channel: one per-table skip error naming
	// the table, plus a degraded-run summary.
	foundTableSkip, foundSummary := false, false
	for _, err := range errs {
		msg := err.Error()
		if strings.Contains(msg, "aws_missing_table") && strings.Contains(msg, "skipped") {
			foundTableSkip = true
		}
		if strings.Contains(msg, "degraded run") && strings.Contains(msg, "1 of 3") {
			foundSummary = true
		}
	}
	if !foundTableSkip {
		t.Errorf("expected a per-table skip error naming aws_missing_table; got %v", errs)
	}
	if !foundSummary {
		t.Errorf("expected a degraded-run summary (1 of 3 skipped); got %v", errs)
	}

	// The reporter must mark the missing table errored and the healthy tables done.
	if !reporter.tableErrored("aws_missing_table") {
		t.Errorf("reporter did not record aws_missing_table as errored")
	}
	if n, ok := reporter.tableDone("aws_iam_role"); !ok || n != 1 {
		t.Errorf("reporter aws_iam_role done = (%d,%v), want (1,true)", n, ok)
	}
	if n, ok := reporter.tableDone("aws_s3_bucket"); !ok || n != 2 {
		t.Errorf("reporter aws_s3_bucket done = (%d,%v), want (2,true)", n, ok)
	}
	if reporter.planned != 3 {
		t.Errorf("reporter planned = %d, want 3", reporter.planned)
	}
}

// TestPerTableIsolationMissingMappedColumn asserts that a table whose staging
// rows lack a mapping-declared column is skipped-and-counted (the schema does
// not match the mapping), while the other tables still emit
// (Requirements 2.6, 2.7).
func TestPerTableIsolationMissingMappedColumn(t *testing.T) {
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", CQTable: "aws_iam_roles", IDColumn: "arn", ScopeColumn: "account_id"},
			// scope_column "account_id" is declared but absent from the rows below.
			{Table: "aws_broken_table", CQTable: "aws_broken_tables", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
		"aws_broken_tables": {rows: []map[string]any{
			{"arn": "arn:aws:thing/1"}, // missing the declared account_id column
		}},
	}}
	reporter := newRecordingReporter()
	s := newFakeSource(cat, q)

	events, errs := collect(t, s, source.Run{RunID: "run-missing-col", Reporter: reporter})

	// The healthy table still emits.
	if len(events) != 1 {
		t.Fatalf("expected 1 event from the healthy table, got %d", len(events))
	}
	if events[0].ResourceType != "aws_iam_role" {
		t.Errorf("unexpected event from %q", events[0].ResourceType)
	}

	// The broken table is skipped-and-counted with a clear reason.
	foundSkip := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "aws_broken_table") && strings.Contains(err.Error(), "account_id") {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("expected a skip error naming aws_broken_table and the missing account_id column; got %v", errs)
	}
	if !reporter.tableErrored("aws_broken_table") {
		t.Errorf("reporter did not record aws_broken_table as errored")
	}
	// The broken table was still attempted (per-table isolation, not a short-circuit).
	if !q.queried("aws_broken_tables") {
		t.Errorf("broken table was not queried; isolation must still attempt each table")
	}
}
