//go:build integration

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// newTestStore spins up a disposable Postgres and returns a migrated store.
func newTestStore(t *testing.T) *Store {
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
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func mkEvent(runID, rid, payload string) rawevent.RawEvent {
	e := rawevent.RawEvent{
		RunID:        runID,
		Source:       "fixture",
		Collector:    "fixture:test",
		Provider:     "aws",
		ResourceType: "aws_iam_role",
		ResourceID:   rid,
		ObservedAt:   time.Now().UTC(),
		Payload:      json.RawMessage(payload),
	}
	e.Finalize()
	return e
}

func TestInsertAndDedupe(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.StartRun(ctx, "run-1", "fixture"); err != nil {
		t.Fatal(err)
	}
	events := []rawevent.RawEvent{
		mkEvent("run-1", "arn:a", `{"k":1}`),
		mkEvent("run-1", "arn:b", `{"k":2}`),
	}
	n, err := st.InsertEvents(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 inserted, got %d", n)
	}

	// Re-insert the same events under a new run: dedupe => 0 new rows.
	if err := st.StartRun(ctx, "run-2", "fixture"); err != nil {
		t.Fatal(err)
	}
	dup := []rawevent.RawEvent{
		mkEvent("run-2", "arn:a", `{"k":1}`),
		mkEvent("run-2", "arn:b", `{"k":2}`),
	}
	n2, err := st.InsertEvents(ctx, dup)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 inserted on rerun, got %d", n2)
	}

	var count int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM raw_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows total, got %d", count)
	}
}

func TestRunStatusTransitions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.StartRun(ctx, "run-x", "fixture"); err != nil {
		t.Fatal(err)
	}
	status, err := st.RunStatus(ctx, "run-x")
	if err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("expected running, got %q", status)
	}

	if err := st.FinishRun(ctx, "run-x", "completed", 5); err != nil {
		t.Fatal(err)
	}
	status, _ = st.RunStatus(ctx, "run-x")
	if status != "completed" {
		t.Fatalf("expected completed, got %q", status)
	}

	var count int
	if err := st.Pool().QueryRow(ctx, `SELECT event_count FROM collection_runs WHERE run_id='run-x'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("expected event_count 5, got %d", count)
	}
}
