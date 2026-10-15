package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SaveEntityProjects tags the given canonical entity ids with a project, so
// inventory can be scoped per project. It upserts (canonical_id, project_id)
// rows into entity_projects, ignoring duplicates. A zero projectID or empty id
// list is a no-op.
func (s *Store) SaveEntityProjects(ctx context.Context, projectID int64, canonicalIDs []string) error {
	if projectID == 0 || len(canonicalIDs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, id := range canonicalIDs {
		if id == "" {
			continue
		}
		batch.Queue(
			`INSERT INTO entity_projects (canonical_id, project_id)
			 VALUES ($1,$2)
			 ON CONFLICT (canonical_id, project_id) DO NOTHING`,
			id, projectID)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range canonicalIDs {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store: save entity project: %w", err)
		}
	}
	return nil
}

// ListEntitiesForProjects returns entities visible to the given set of project
// ids, optionally filtered by kind, with a limit. An entity is included if it
// is tagged (in entity_projects) with any of the project ids. When projectIDs
// is empty, no project filter is applied (all entities) — callers enforce
// authorization before calling.
func (s *Store) ListEntitiesForProjects(ctx context.Context, projectIDs []int64, kind string, limit int) ([]EntityRow, error) {
	// No project scoping requested: fall back to the unscoped listing.
	if len(projectIDs) == 0 {
		return s.ListEntities(ctx, kind, limit)
	}

	q := `SELECT e.canonical_id, e.kind, e.provider, e.name, e.attributes, e.stale
	      FROM normalized_entities e
	      JOIN entity_projects ep ON ep.canonical_id = e.canonical_id
	      WHERE ep.project_id = ANY($1)`
	args := []any{projectIDs}
	if kind != "" {
		args = append(args, kind)
		q += fmt.Sprintf(" AND e.kind = $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY e.kind, e.canonical_id LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntityRow
	for rows.Next() {
		var e EntityRow
		var attrs []byte
		if err := rows.Scan(&e.CanonicalID, &e.Kind, &e.Provider, &e.Name, &attrs, &e.Stale); err != nil {
			return nil, err
		}
		_ = unmarshalInto(attrs, &e.Attributes)
		out = append(out, e)
	}
	return out, rows.Err()
}
