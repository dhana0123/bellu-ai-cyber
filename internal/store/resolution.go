package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AliasRow is one entity alias to persist.
type AliasRow struct {
	AliasKey    string
	CanonicalID string
	Source      string
}

// ConflictRow is one resolution conflict to persist.
type ConflictRow struct {
	Key    string
	Reason string
	Detail string
}

// SaveAliases upserts entity aliases.
func (s *Store) SaveAliases(ctx context.Context, rows []AliasRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO entity_aliases (alias_key, canonical_id, source)
			 VALUES ($1,$2,$3)
			 ON CONFLICT (alias_key, canonical_id, source) DO NOTHING`,
			r.AliasKey, r.CanonicalID, r.Source)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save alias: %w", err)
		}
	}
	return nil
}

// SaveConflicts appends resolution conflicts.
func (s *Store) SaveConflicts(ctx context.Context, rows []ConflictRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(
			`INSERT INTO resolution_conflicts (key, reason, detail) VALUES ($1,$2,$3)`,
			r.Key, r.Reason, r.Detail)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save conflict: %w", err)
		}
	}
	return nil
}
