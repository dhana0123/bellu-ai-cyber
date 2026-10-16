package graphquery

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresReader is the fallback graph reader. It reads the normalized
// entities/edges tables directly from the customer Postgres DSN when the
// control-panel API is unreachable (Requirement 10.3). It is read-only and
// touches only the customer plane, never the intel plane.
type PostgresReader struct {
	DSN  string
	pool *pgxpool.Pool
}

// NewPostgresReader builds a reader for the given customer Postgres DSN. The
// pool is opened lazily on first use so constructing the reader never fails.
func NewPostgresReader(dsn string) *PostgresReader {
	return &PostgresReader{DSN: dsn}
}

func (p *PostgresReader) connect(ctx context.Context) error {
	if p.pool != nil {
		return nil
	}
	pool, err := pgxpool.New(ctx, p.DSN)
	if err != nil {
		return err
	}
	p.pool = pool
	return nil
}

// Close releases the pool.
func (p *PostgresReader) Close() {
	if p.pool != nil {
		p.pool.Close()
		p.pool = nil
	}
}

// Healthy reports whether the Postgres pool can be reached with a ping.
func (p *PostgresReader) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.connect(ctx); err != nil {
		return false
	}
	return p.pool.Ping(ctx) == nil
}

// Nodes reads normalized_entities (optionally filtered by kind).
func (p *PostgresReader) Nodes(ctx context.Context, kind string) ([]Node, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}
	q := `SELECT canonical_id, kind, name FROM normalized_entities WHERE 1=1`
	var args []any
	if kind != "" {
		args = append(args, kind)
		q += ` AND kind = $1`
	}
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.CanonicalID, &n.Kind, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Edges reads normalized_edges (optionally filtered by relation).
func (p *PostgresReader) Edges(ctx context.Context, relation string) ([]Edge, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}
	q := `SELECT src_id, relation, dst_id, confidence FROM normalized_edges WHERE 1=1`
	var args []any
	if relation != "" {
		args = append(args, relation)
		q += ` AND relation = $1`
	}
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.SrcID, &e.Relation, &e.DstID, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
