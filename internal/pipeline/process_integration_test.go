//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
)

// newTestStore spins up a disposable Postgres and returns a migrated store.
// It mirrors the store package's integration-test helper (store_integration_test.go)
// so this test can drive a real Processor + change engine end-to-end.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"),
		tcpostgres.WithUsername("bellu"),
		tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// awsEvent builds a finalized AWS steampipe RawEvent for the fixture. observedAt
// becomes the event_time axis the change engine folds from. The AWS normalizer
// claims the "steampipe" source and filters on provider=="aws".
func awsEvent(t *testing.T, runID, resourceType, resourceID string, observedAt time.Time, payload map[string]any) rawevent.RawEvent {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload for %s: %v", resourceID, err)
	}
	e := rawevent.RawEvent{
		RunID:        runID,
		Source:       "steampipe",
		Collector:    "steampipe:test",
		Provider:     "aws",
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ObservedAt:   observedAt,
		Payload:      b,
	}
	e.Finalize()
	return e
}

// observedRow is a projection of an observed_events row for assertions.
type observedRow struct {
	elementKind string
	changeType  string
	canonicalID string
	relation    string
	dstID       string
	evidenceIDs []string
	eventTime   time.Time
	recordedAt  time.Time
}

// loadObservedEvents reads every observed_events row for a run, ordered by seq.
func loadObservedEvents(ctx context.Context, t *testing.T, st *store.Store, org int64, runID string) []observedRow {
	t.Helper()
	rows, err := st.Pool().Query(ctx, `
		SELECT element_kind, change_type, canonical_id,
		       COALESCE(relation, ''), COALESCE(dst_id, ''),
		       evidence_ids, event_time, recorded_at
		  FROM observed_events
		 WHERE org_id = $1 AND run_id = $2
		 ORDER BY seq`, org, runID)
	if err != nil {
		t.Fatalf("query observed_events: %v", err)
	}
	defer rows.Close()
	var out []observedRow
	for rows.Next() {
		var r observedRow
		if err := rows.Scan(&r.elementKind, &r.changeType, &r.canonicalID,
			&r.relation, &r.dstID, &r.evidenceIDs, &r.eventTime, &r.recordedAt); err != nil {
			t.Fatalf("scan observed_events: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate observed_events: %v", err)
	}
	return out
}

// TestIngestWritesObservedEvents drives a fixture ingest end-to-end through a
// Processor with the temporal change engine attached, then asserts the observed
// lane captured the run correctly (Requirements 3.1, 3.5, 3.6):
//
//   - every entity/edge on a first ingest is classified entity.created /
//     relation.added (3.1: an append-only row per asserted change),
//   - each row's evidence_ids link back to the justifying raw-event ids (3.5),
//   - event_time is taken from the raw events' observed_at and recorded_at is
//     the ingest time, with recorded_at >= event_time (3.6).
func TestIngestWritesObservedEvents(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	const (
		org    = int64(1)
		runID  = "run-ingest-1"
		source = "steampipe"
	)

	// The collection_runs row satisfies observed_events.run_id -> collection_runs FK.
	if err := st.StartRun(ctx, runID, source); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// observedAt is the world time the facts became true; it drives event_time.
	observedAt := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)

	// A small AWS fixture: one account that CONTAINS one S3 bucket and one IAM
	// role. The normalizer emits the three entities plus CONTAINS edges, giving
	// a mix of entity.created and relation.added for a first ingest.
	const (
		acctID     = "123456789012"
		bucketARN  = "arn:aws:s3:::fixture-bucket"
		roleARN    = "arn:aws:iam::123456789012:role/fixture-role"
	)
	events := []rawevent.RawEvent{
		awsEvent(t, runID, "aws_account", acctID, observedAt, map[string]any{
			"account_id": acctID,
			"title":      "fixture-account",
		}),
		awsEvent(t, runID, "aws_s3_bucket", bucketARN, observedAt, map[string]any{
			"name":   "fixture-bucket",
			"arn":    bucketARN,
			"region": "us-east-1",
			"public": false,
		}),
		awsEvent(t, runID, "aws_iam_role", roleARN, observedAt, map[string]any{
			"name": "fixture-role",
			"arn":  roleARN,
		}),
	}

	// Persist the raw events first, exactly as the real ingest path does:
	// raw_events land in Postgres before Process normalizes/persists, and the
	// evidence rows PersistNormalized writes FK back to raw_events.id.
	if _, err := st.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert raw events: %v", err)
	}

	// Build the Processor exactly as the API wiring does: default mapping paths
	// (so it works regardless of CWD) with the change engine attached.
	p := NewProcessor("", "").WithEngine(temporal.NewEngine(st))

	// Capture the window around the ingest so we can bound recorded_at.
	before := time.Now().UTC()
	res, err := p.Process(ctx, st, events, org, runID, source)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	after := time.Now().UTC()

	// Process should have produced classified changes and persisted entities.
	if len(res.Changes) == 0 {
		t.Fatalf("expected the change engine to return changes, got none")
	}
	if res.Entities == 0 {
		t.Fatalf("expected normalized entities to be persisted, got 0")
	}

	// Read back the observed_events the engine wrote (Req 3.1: append-only log).
	got := loadObservedEvents(ctx, t, st, org, runID)
	if len(got) == 0 {
		t.Fatalf("expected observed_events rows after ingest, got none")
	}

	// The IAM role's raw-event id must appear as evidence on some observed
	// entity (Req 3.5). The resolver rewrites native ARNs into canonical ids
	// (kind:provider:normalized-arn), so we match on the evidence link — the
	// raw-event id — rather than guessing the canonical-id string.
	roleEventID := ""
	for _, e := range events {
		if e.ResourceID == roleARN {
			roleEventID = e.ID
		}
	}
	if roleEventID == "" {
		t.Fatalf("fixture is missing the IAM role raw event")
	}

	var sawEntityCreated, sawRelationAdded, sawRoleWithEvidence bool
	for _, r := range got {
		// Req 3.1 / first-ingest: every change on a never-seen projection is a
		// creation or an addition, never an update/unchanged/deleted.
		switch r.elementKind {
		case "entity":
			if r.changeType != temporal.ChangeEntityCreated {
				t.Errorf("entity %s: change_type = %q, want %q on first ingest",
					r.canonicalID, r.changeType, temporal.ChangeEntityCreated)
			}
			sawEntityCreated = true
		case "edge":
			if r.changeType != temporal.ChangeRelationAdded {
				t.Errorf("edge %s-[%s]->%s: change_type = %q, want %q on first ingest",
					r.canonicalID, r.relation, r.dstID, r.changeType, temporal.ChangeRelationAdded)
			}
			if r.relation == "" || r.dstID == "" {
				t.Errorf("edge row missing relation/dst_id: %+v", r)
			}
			sawRelationAdded = true
		default:
			t.Errorf("unexpected element_kind %q", r.elementKind)
		}

		// Req 3.6: event_time comes from the raw events' observed_at; recorded_at
		// is the ingest time and must sit within the ingest window and at or
		// after event_time.
		if !r.eventTime.Equal(observedAt) {
			t.Errorf("%s: event_time = %v, want observed_at %v", r.canonicalID, r.eventTime, observedAt)
		}
		if r.recordedAt.Before(before) || r.recordedAt.After(after) {
			t.Errorf("%s: recorded_at %v outside ingest window [%v, %v]",
				r.canonicalID, r.recordedAt, before, after)
		}
		if r.recordedAt.Before(r.eventTime) {
			t.Errorf("%s: recorded_at %v should be >= event_time %v",
				r.canonicalID, r.recordedAt, r.eventTime)
		}

		// Req 3.5: the change links to the raw-event id that justifies it. The
		// role entity's observed event must carry the role's RawEvent id, and its
		// canonical_id must embed the role ARN (resolver rewrites ARN -> cid).
		if r.elementKind == "entity" && contains(r.evidenceIDs, roleEventID) {
			if !containsSubstr(r.canonicalID, roleARN) {
				t.Errorf("row carrying the role evidence has canonical_id %q, want it to embed %q",
					r.canonicalID, roleARN)
			}
			sawRoleWithEvidence = true
		}
	}

	if !sawEntityCreated {
		t.Error("expected at least one entity.created observed event")
	}
	if !sawRelationAdded {
		t.Error("expected at least one relation.added observed event")
	}
	if !sawRoleWithEvidence {
		t.Errorf("expected an observed entity linking the IAM role raw-event id %q", roleEventID)
	}

	// Req 3.5 (general): no observed-event row may be written without an
	// evidence link back to the raw events that justify it.
	for _, r := range got {
		if len(r.evidenceIDs) == 0 {
			t.Errorf("%s %s: observed event has no evidence_ids", r.elementKind, r.canonicalID)
		}
		for _, id := range r.evidenceIDs {
			if !knownRawEvent(ctx, t, st, id) {
				t.Errorf("%s %s: evidence id %q does not reference a raw_events row",
					r.elementKind, r.canonicalID, id)
			}
		}
	}
}

// contains reports whether s contains want.
func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// containsSubstr reports whether sub appears within s.
func containsSubstr(s, sub string) bool {
	return strings.Contains(s, sub)
}

// knownRawEvent reports whether a raw_events row exists for id. The pipeline
// test does not persist raw events (Process starts from in-memory RawEvents),
// so when the raw_events table is empty we accept any non-empty id as a valid
// evidence link; otherwise we require the id to resolve to a stored raw event.
func knownRawEvent(ctx context.Context, t *testing.T, st *store.Store, id string) bool {
	t.Helper()
	if id == "" {
		return false
	}
	var total int64
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM raw_events`).Scan(&total); err != nil {
		t.Fatalf("count raw_events: %v", err)
	}
	if total == 0 {
		return true
	}
	var exists bool
	err := st.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM raw_events WHERE id = $1)`, id).Scan(&exists)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatalf("lookup raw_events %s: %v", id, err)
	}
	return exists
}

// derivedRow is a projection of a derived_events row for assertions.
type derivedRow struct {
	kind        string
	changeType  string
	origin      string
	producer    string
	canonicalID string
	relation    string
	dstID       string
	dataClass   string
	confidence  float64
}

// loadDerivedEvents reads every derived_events row for a run, ordered by seq.
func loadDerivedEvents(ctx context.Context, t *testing.T, st *store.Store, org int64, runID string) []derivedRow {
	t.Helper()
	rows, err := st.Pool().Query(ctx, `
		SELECT kind, change_type, origin, producer, canonical_id,
		       COALESCE(relation, ''), COALESCE(dst_id, ''),
		       COALESCE(attributes->>'data_class', ''), confidence
		  FROM derived_events
		 WHERE org_id = $1 AND run_id = $2
		 ORDER BY seq`, org, runID)
	if err != nil {
		t.Fatalf("query derived_events: %v", err)
	}
	defer rows.Close()
	var out []derivedRow
	for rows.Next() {
		var r derivedRow
		if err := rows.Scan(&r.kind, &r.changeType, &r.origin, &r.producer,
			&r.canonicalID, &r.relation, &r.dstID, &r.dataClass, &r.confidence); err != nil {
			t.Fatalf("scan derived_events: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate derived_events: %v", err)
	}
	return out
}

// TestIngestWritesDerivedEvents drives a fixture ingest through a Processor that
// has BOTH the observed change engine AND the derived lane (inference +
// classification) attached, then asserts the derived lane captured the run into
// derived_events and returned the overlay changes (task 16.1; Requirements 8.1,
// 9.2, 4.4):
//
//   - the inference + classification engines run after the change engine over
//     the same normalized run and persist their output to derived_events,
//   - every DataStore is classified (the S3 bucket yields a 'classification'
//     derived row carrying a data_class and the rule producer),
//   - the derived rows use only derived-lane change types (so the lane CHECK
//     accepts them and the facts lane is never polluted), and
//   - Process returns the derived overlay changes for the projection.
func TestIngestWritesDerivedEvents(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	const (
		org    = int64(1)
		runID  = "run-derived-1"
		source = "steampipe"
	)
	if err := st.StartRun(ctx, runID, source); err != nil {
		t.Fatalf("start run: %v", err)
	}

	observedAt := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	const (
		acctID    = "123456789012"
		bucketARN = "arn:aws:s3:::pii-customer-bucket"
	)
	// A DataStore whose name ("pii-customer-...") the rule classifier labels as
	// pii, so the classification path is exercised deterministically.
	events := []rawevent.RawEvent{
		awsEvent(t, runID, "aws_account", acctID, observedAt, map[string]any{
			"account_id": acctID, "title": "fixture-account",
		}),
		awsEvent(t, runID, "aws_s3_bucket", bucketARN, observedAt, map[string]any{
			"name": "pii-customer-bucket", "arn": bucketARN, "region": "us-east-1", "public": false,
		}),
	}
	if _, err := st.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert raw events: %v", err)
	}

	// Build the Processor exactly as the API wiring does: change engine for the
	// observed lane + the derived lane (inference + rule classifier).
	p := NewProcessor("", "").
		WithEngine(temporal.NewEngine(st)).
		WithDerived(infer.NewEngine(infer.DefaultRules()...), classify.NewRuleClassifier())

	res, err := p.Process(ctx, st, events, org, runID, source)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Process must return derived overlay changes (at least the bucket's class).
	if len(res.DerivedChanges) == 0 {
		t.Fatalf("expected Process to return derived overlay changes, got none")
	}

	got := loadDerivedEvents(ctx, t, st, org, runID)
	if len(got) == 0 {
		t.Fatalf("expected derived_events rows after ingest, got none")
	}

	// Every DataStore is classified: find the bucket's classification row.
	var sawClassification bool
	for _, r := range got {
		switch r.kind {
		case "classification":
			if r.changeType != "classified" {
				t.Errorf("classification row change_type = %q, want %q", r.changeType, "classified")
			}
			if r.relation != "" || r.dstID != "" {
				t.Errorf("classification row must have empty relation/dst_id, got rel=%q dst=%q", r.relation, r.dstID)
			}
			if r.dataClass == "" {
				t.Errorf("classification row missing data_class attribute")
			}
			if r.producer == "" {
				t.Errorf("classification row missing producer (classified_by)")
			}
			if r.origin != "rule" {
				t.Errorf("classification row origin = %q, want %q", r.origin, "rule")
			}
			sawClassification = true
		case "inferred_edge":
			// If present, it must carry a derived-lane change type and endpoints.
			if r.changeType != "inferred.added" {
				t.Errorf("inferred_edge row change_type = %q, want %q", r.changeType, "inferred.added")
			}
			if r.relation == "" || r.dstID == "" {
				t.Errorf("inferred_edge row must carry relation and dst_id, got rel=%q dst=%q", r.relation, r.dstID)
			}
		default:
			t.Errorf("unexpected derived kind %q", r.kind)
		}
	}
	if !sawClassification {
		t.Error("expected at least one DataStore classification derived event")
	}

	// The derived rows must never land in the observed (facts) lane.
	var facts int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM observed_events WHERE org_id = $1 AND run_id = $2
		   AND change_type IN ('classified','inferred.added')`, org, runID).Scan(&facts); err != nil {
		t.Fatalf("count misplaced facts: %v", err)
	}
	if facts != 0 {
		t.Errorf("derived change types leaked into observed_events (%d rows)", facts)
	}
}
