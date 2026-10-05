// Package store is the PostgreSQL evidence store. It persists collection runs
// and append-only raw events, and (from Task 4) normalized entities and edges.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Store wraps a pgx connection pool.
type Store struct {
	pool *pgxpool.Pool
	dsn  string
}

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, dsn: dsn}, nil
}

// Pool exposes the underlying pool for packages that need direct queries
// (normalizer, resolver, graph sync).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close releases the pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Migrate applies all embedded goose migrations.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()

	goose.SetBaseFS(MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// StartRun inserts a collection_runs row in the running state.
func (s *Store) StartRun(ctx context.Context, runID, source string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO collection_runs (run_id, source, status) VALUES ($1, $2, 'running')
		 ON CONFLICT (run_id) DO NOTHING`,
		runID, source)
	if err != nil {
		return fmt.Errorf("store: start run: %w", err)
	}
	return nil
}

// FinishRun sets the final status and event count for a run.
func (s *Store) FinishRun(ctx context.Context, runID, status string, eventCount int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE collection_runs
		 SET status = $2, finished_at = now(), event_count = $3
		 WHERE run_id = $1`,
		runID, status, eventCount)
	if err != nil {
		return fmt.Errorf("store: finish run: %w", err)
	}
	return nil
}

// InsertEvents writes a batch of raw events, skipping duplicates by the dedupe
// constraint. It returns the number of newly inserted rows.
func (s *Store) InsertEvents(ctx context.Context, events []rawevent.RawEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(
			`INSERT INTO raw_events
			   (id, run_id, source, collector, provider, scope, region,
			    resource_type, resource_id, observed_at, payload, payload_hash)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			 ON CONFLICT (source, resource_type, resource_id, payload_hash) DO NOTHING`,
			e.ID, e.RunID, e.Source, e.Collector, e.Provider, e.Scope, e.Region,
			e.ResourceType, e.ResourceID, e.ObservedAt, []byte(e.Payload), e.PayloadHash,
		)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	inserted := 0
	for range events {
		tag, err := br.Exec()
		if err != nil {
			return inserted, fmt.Errorf("store: insert event: %w", err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// RunStatus returns the status of a run.
func (s *Store) RunStatus(ctx context.Context, runID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM collection_runs WHERE run_id = $1`, runID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("store: run %s not found", runID)
	}
	if err != nil {
		return "", err
	}
	return status, nil
}
