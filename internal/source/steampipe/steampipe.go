package steampipe

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// DefaultTableTimeout bounds how long one table may take before it is
// abandoned so one slow/failing table cannot stall the whole run.
const DefaultTableTimeout = 2 * time.Minute

// Source collects a provider catalog from a Steampipe service.
type Source struct {
	dsn          string
	catalog      *Catalog
	tableTimeout time.Duration
	// schema optionally qualifies table names so queries target a single
	// Steampipe connection (its Postgres schema). Empty means the default
	// search path (aggregates across all connections of the plugin).
	schema string
}

// Config holds the resolved connection and catalog settings.
type Config struct {
	Host        string
	Port        int
	User        string
	Password    string
	Database    string
	CatalogPath string
	Connection  string
}

// New builds a Source from an explicit DSN and catalog.
func New(dsn string, catalog *Catalog) *Source {
	return &Source{dsn: dsn, catalog: catalog, tableTimeout: DefaultTableTimeout}
}

// Factory builds a Source from a config map. Required keys: host, port, user,
// database, and either catalog (path) or a connection resolved to a catalog by
// the caller. password is optional.
func Factory(cfg map[string]any) (source.Source, error) {
	host := strOr(cfg, "host", "localhost")
	port := intOr(cfg, "port", 9193)
	user := strOr(cfg, "user", "steampipe")
	password := strOr(cfg, "password", "")
	database := strOr(cfg, "database", "steampipe")

	// Resolve the catalog: an explicit "catalog" path wins; otherwise map
	// "connection" to "<catalog_dir>/<connection>.yaml".
	catalogPath := strOr(cfg, "catalog", "")
	if catalogPath == "" {
		conn := strOr(cfg, "connection", "")
		if conn == "" {
			return nil, fmt.Errorf("steampipe: config requires \"catalog\" or \"connection\"")
		}
		dir := strOr(cfg, "catalog_dir", "internal/source/steampipe/catalog")
		catalogPath = filepath.Join(dir, conn+".yaml")
	}
	cat, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	if conn := strOr(cfg, "connection", ""); conn != "" && cat.Connection == "" {
		cat.Connection = conn
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		user, password, host, port, database)
	src := New(dsn, cat)
	src.schema = strOr(cfg, "connection_schema", "")
	return src, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return "steampipe" }

// Collect connects to the Steampipe service and streams one RawEvent per row
// across all catalog tables. Each table runs under its own timeout and errors
// are isolated per table.
func (s *Source) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, len(s.catalog.Tables)+1)

	go func() {
		defer close(events)
		defer close(errs)

		pool, err := pgxpool.New(ctx, s.dsn)
		if err != nil {
			errs <- fmt.Errorf("steampipe: connect: %w", err)
			return
		}
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			errs <- fmt.Errorf("steampipe: ping service: %w", err)
			return
		}

		collector := "steampipe:" + s.catalog.Provider
		if s.catalog.Connection != "" {
			collector = "steampipe:" + s.catalog.Connection
		}

		for _, spec := range s.catalog.Tables {
			if err := ctx.Err(); err != nil {
				errs <- err
				return
			}
			tctx, cancel := context.WithTimeout(ctx, s.tableTimeout)
			n, err := s.collectTable(tctx, pool, run, collector, spec, events)
			cancel()
			if err != nil {
				// Error isolation: record and continue with the next table.
				errs <- fmt.Errorf("steampipe: table %s: %w", spec.Table, err)
				continue
			}
			_ = n
		}
	}()

	return events, errs
}

// qualifiedTable returns the table name scoped to the source's connection
// schema when set, so a query targets exactly one Steampipe connection and
// never aggregates across concurrent connections. An empty schema returns the
// bare table (default search path).
func (s *Source) qualifiedTable(table string) string {
	if s.schema == "" {
		return table
	}
	return s.schema + "." + table
}

// collectTable queries one table and emits a RawEvent per row.
func (s *Source) collectTable(ctx context.Context, pool *pgxpool.Pool, run source.Run, collector string, spec TableSpec, out chan<- rawevent.RawEvent) (int, error) {
	// Identifiers come from the vetted catalog, not user input. We still only
	// ever interpolate the table name here.
	q := fmt.Sprintf(`SELECT to_jsonb(t) AS row FROM %s t`, s.qualifiedTable(spec.Table))
	if spec.Where != "" {
		q += " WHERE " + spec.Where
	}

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return count, err
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return count, fmt.Errorf("decode row: %w", err)
		}

		ev := rawevent.RawEvent{
			RunID:        run.RunID,
			Source:       "steampipe",
			Collector:    collector,
			Provider:     s.catalog.Provider,
			Scope:        colString(obj, spec.ScopeColumn),
			Region:       colString(obj, spec.RegionColumn),
			ResourceType: spec.resourceType(),
			ResourceID:   colString(obj, spec.IDColumn),
			ObservedAt:   time.Now().UTC(),
			Payload:      json.RawMessage(raw),
		}
		if ev.ResourceID == "" {
			// Fall back to a stable synthetic id so the event is still valid.
			ev.ResourceID = fmt.Sprintf("%s/%d", spec.Table, count)
		}
		ev.Finalize()
		select {
		case out <- ev:
		case <-ctx.Done():
			return count, ctx.Err()
		}
		count++
	}
	return count, rows.Err()
}

func colString(obj map[string]any, col string) string {
	if col == "" {
		return ""
	}
	switch v := obj[col].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func strOr(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return def
}

func intOr(cfg map[string]any, key string, def int) int {
	switch v := cfg[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

var _ = pgx.ErrNoRows
