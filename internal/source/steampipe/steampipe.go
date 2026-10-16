package steampipe

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// DefaultTableTimeout bounds how long one table may take before it is
// abandoned so one slow/failing table cannot stall the whole run.
const DefaultTableTimeout = 2 * time.Minute

// querier is the minimal query surface the collector needs from a connection
// pool. *pgxpool.Pool satisfies it; tests inject a recorder that captures the
// SQL/table attempted without a live database.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Source collects a provider catalog from a Steampipe service.
type Source struct {
	dsn          string
	catalog      *Catalog
	tableTimeout time.Duration
	// schema optionally qualifies table names so queries target a single
	// Steampipe connection (its Postgres schema). Empty means the default
	// search path (aggregates across all connections of the plugin).
	schema string
	// connect opens the query connection for a run. nil means the default
	// pgxpool path (the production behavior); tests inject a fake querier so
	// the per-table query loop can be exercised without a live service.
	connect func(ctx context.Context) (q querier, closeFn func(), err error)
	// enabledCaps is the set of capabilities granted to the connection. A
	// nil/empty set means "no gating configured" and preserves current
	// behavior (collect every table). core is always treated as enabled.
	enabledCaps map[string]bool
}

// setEnabledCapabilitiesForTest sets the enabled capability set on the Source.
// Exposed for tests that construct a Source via New (production wiring goes
// through Factory's "capabilities" config key).
func (s *Source) setEnabledCapabilitiesForTest(caps []string) {
	s.enabledCaps = capsToSet(caps)
}

// capsToSet builds the enabled-capability lookup set, always including core so
// core tables are never gated out even if a caller omits it. A nil/empty input
// yields a nil set, which the gate treats as "no gating configured".
func capsToSet(caps []string) map[string]bool {
	if len(caps) == 0 {
		return nil
	}
	set := make(map[string]bool, len(caps)+1)
	for _, c := range caps {
		if c != "" {
			set[c] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	set["core"] = true
	return set
}

// capsFromConfig extracts the enabled-capability list from a Factory config
// value. The discovery orchestrator may pass []string (the typed path from the
// AWS adapter) or []any (e.g. after a JSON/YAML round-trip). Any other type —
// including a missing key — yields nil, which capsToSet treats as "no gating
// configured" so the collector preserves its collect-everything behavior.
func capsFromConfig(v any) []string {
	switch caps := v.(type) {
	case []string:
		return caps
	case []any:
		out := make([]string, 0, len(caps))
		for _, c := range caps {
			if s, ok := c.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// capabilityEnabled reports whether a table with the given capability should be
// collected for this source. The gate is deliberately permissive so it can
// only ever skip tables that are safe to skip:
//   - no gating configured (nil/empty enabled set): collect everything, exactly
//     as before this fix (backward compatible).
//   - ungated table (empty capability): always collected.
//   - "core": always collected (core permissions are always granted).
//   - otherwise: collected only if the capability is in the enabled set.
func (s *Source) capabilityEnabled(capability string) bool {
	if len(s.enabledCaps) == 0 {
		return true
	}
	if capability == "" || capability == "core" {
		return true
	}
	return s.enabledCaps[capability]
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
	// Capability gating: tables whose capability is not enabled are skipped so
	// an ungranted capability never produces AccessDenied. A missing/empty
	// "capabilities" key leaves enabledCaps nil, preserving collect-everything.
	src.enabledCaps = capsToSet(capsFromConfig(cfg["capabilities"]))
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

		connect := s.connect
		if connect == nil {
			connect = s.defaultConnect
		}
		q, closeFn, err := connect(ctx)
		if err != nil {
			errs <- err
			return
		}
		if closeFn != nil {
			defer closeFn()
		}

		collector := "steampipe:" + s.catalog.Provider
		if s.catalog.Connection != "" {
			collector = "steampipe:" + s.catalog.Connection
		}

		// Progress reporting is nil-safe: the report* helpers no-op when
		// run.Reporter is nil, so this preserves the collector's behavior
		// exactly when no reporter is wired. Report the planned table count
		// (after capability gating) so a progress UI can render the full
		// checklist up front.
		planned := make([]TableSpec, 0, len(s.catalog.Tables))
		for _, spec := range s.catalog.Tables {
			if s.capabilityEnabled(spec.Capability) {
				planned = append(planned, spec)
			}
		}
		reportPlanned(run.Reporter, len(planned))

		for i, spec := range planned {
			if err := ctx.Err(); err != nil {
				errs <- err
				return
			}
			reportTableStarted(run.Reporter, spec.Table, i)
			tctx, cancel := context.WithTimeout(ctx, s.tableTimeout)
			n, err := s.collectTable(tctx, q, run, collector, spec, events)
			cancel()
			if err != nil {
				if isUnsupportedServiceErr(err) {
					// The service/operation does not exist in a probed region
					// (e.g. Bedrock's 404 UnknownOperationException in regions
					// where Bedrock is not offered). This is benign — not a
					// permission or data problem — so treat it as an empty
					// result instead of an error. Nothing is surfaced to the UI.
					reportTableDone(run.Reporter, spec.Table, n)
					continue
				}
				// Error isolation: record and continue with the next table.
				reportTableError(run.Reporter, spec.Table, err)
				errs <- fmt.Errorf("steampipe: table %s: %w", spec.Table, err)
				continue
			}
			reportTableDone(run.Reporter, spec.Table, n)
		}
	}()

	return events, errs
}

// defaultConnect is the production connection path: open a pgx pool to the
// Steampipe service and ping it. Returned closeFn closes the pool. This
// preserves the original Collect behavior exactly; it is only indirected so
// tests can inject a fake querier.
func (s *Source) defaultConnect(ctx context.Context) (querier, func(), error) {
	pool, err := pgxpool.New(ctx, s.dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("steampipe: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("steampipe: ping service: %w", err)
	}
	return pool, pool.Close, nil
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
func (s *Source) collectTable(ctx context.Context, pool querier, run source.Run, collector string, spec TableSpec, out chan<- rawevent.RawEvent) (int, error) {
	// Identifiers come from the vetted catalog, not user input. We still only
	// ever interpolate the table name here.
	q := fmt.Sprintf(`SELECT to_jsonb(t) AS row FROM %s t`, s.qualifiedTable(spec.Table))
	// whereClause combines any explicit predicate with an optional per-table
	// region restriction (e.g. Bedrock, which is not available in every
	// region). Tables without a region restriction are unaffected.
	if where := spec.whereClause(); where != "" {
		q += " WHERE " + where
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

// report* are nil-safe wrappers around the optional source.ProgressReporter so
// the collection loop stays readable and a nil reporter (the default) costs
// nothing and changes no behavior.
func reportPlanned(r source.ProgressReporter, total int) {
	if r != nil {
		r.TablesPlanned(total)
	}
}

func reportTableStarted(r source.ProgressReporter, table string, index int) {
	if r != nil {
		r.TableStarted(table, index)
	}
}

func reportTableDone(r source.ProgressReporter, table string, rows int) {
	if r != nil {
		r.TableDone(table, rows)
	}
}

func reportTableError(r source.ProgressReporter, table string, err error) {
	if r != nil {
		r.TableError(table, err)
	}
}

// isUnsupportedServiceErr reports whether err indicates the AWS service or
// operation simply does not exist in a region that was probed — a benign
// condition, not a permission or data error. The Steampipe AWS plugin fans
// regional calls across regions; for services not offered everywhere (e.g.
// Amazon Bedrock), unsupported regions answer with a 404
// UnknownOperationException. We treat these as "no data here" rather than
// failing or warning, so clients never see a spurious error. Matching on the
// error text is deliberate: it is region-list independent and never goes stale
// as AWS adds/removes regions.
func isUnsupportedServiceErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unknownoperationexception"):
		return true
	case strings.Contains(msg, "statuscode: 404") && strings.Contains(msg, "operation error"):
		return true
	case strings.Contains(msg, "could not connect to the endpoint url"):
		return true
	case strings.Contains(msg, "no such host"):
		return true
	}
	return false
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
