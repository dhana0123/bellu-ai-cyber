//go:build integration

package steampipe

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// The Steampipe service speaks the Postgres wire protocol, so a plain Postgres
// seeded with Steampipe-shaped tables exercises the adapter end to end.
func seedSteampipeShapedDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("steampipe"),
		tcpostgres.WithUsername("steampipe"),
		tcpostgres.WithPassword("bellu_steampipe"),
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
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ddl := []string{
		`CREATE TABLE aws_iam_role (arn text, account_id text, name text, assume_role_policy_std jsonb)`,
		`INSERT INTO aws_iam_role VALUES
		 ('arn:aws:iam::111122223333:role/agent-exec','111122223333','agent-exec','{}'),
		 ('arn:aws:iam::111122223333:role/other','111122223333','other','{}')`,
		`CREATE TABLE aws_s3_bucket (arn text, account_id text, region text, name text)`,
		`INSERT INTO aws_s3_bucket VALUES
		 ('arn:aws:s3:::bellu-training-data','111122223333','us-east-1','bellu-training-data')`,
	}
	for _, s := range ddl {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return dsn
}

func collectAll(t *testing.T, s *Source) ([]rawevent.RawEvent, []error) {
	t.Helper()
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-test"})
	var events []rawevent.RawEvent
	var errs []error
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("invalid event: %v", err)
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

func TestAdapterStreamsRows(t *testing.T) {
	dsn := seedSteampipeShapedDB(t)
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", IDColumn: "arn", ScopeColumn: "account_id"},
			{Table: "aws_s3_bucket", IDColumn: "arn", ScopeColumn: "account_id", RegionColumn: "region"},
		},
	}
	s := New(dsn, cat)
	events, errs := collectAll(t, s)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Verify id/scope/region extraction and collector label.
	var roleFound, bucketFound bool
	for _, e := range events {
		if e.Collector != "steampipe:aws" {
			t.Errorf("collector = %q", e.Collector)
		}
		if e.ResourceType == "aws_iam_role" && e.ResourceID == "arn:aws:iam::111122223333:role/agent-exec" {
			roleFound = true
			if e.Scope != "111122223333" {
				t.Errorf("role scope = %q", e.Scope)
			}
		}
		if e.ResourceType == "aws_s3_bucket" {
			bucketFound = true
			if e.Region != "us-east-1" {
				t.Errorf("bucket region = %q", e.Region)
			}
		}
	}
	if !roleFound || !bucketFound {
		t.Fatalf("missing expected rows: role=%v bucket=%v", roleFound, bucketFound)
	}
}

func TestAdapterErrorIsolation(t *testing.T) {
	dsn := seedSteampipeShapedDB(t)
	// One good table, one that does not exist -> error isolated, good rows
	// still emitted.
	cat := &Catalog{
		Provider: "aws",
		Tables: []TableSpec{
			{Table: "aws_s3_bucket", IDColumn: "arn", ScopeColumn: "account_id", RegionColumn: "region"},
			{Table: "aws_nonexistent_table", IDColumn: "arn"},
		},
	}
	s := New(dsn, cat)
	events, errs := collectAll(t, s)
	if len(events) != 1 {
		t.Fatalf("expected 1 event from good table, got %d", len(events))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 isolated error, got %d: %v", len(errs), errs)
	}
}
