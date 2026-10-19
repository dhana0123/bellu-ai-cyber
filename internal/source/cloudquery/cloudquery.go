package cloudquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// SourceName is the stable collector name every emitted RawEvent carries. It
// is also the key the source registry resolves this collector under.
const SourceName = "cloudquery"

// DefaultStagingSchema is the PostgreSQL schema a CloudQuery sync writes its
// destination tables into. Keeping the default here, inside the cloudquery
// package, is deliberate: every assumption about the staging layout (schema
// name, internal columns, table naming) is isolated to this package so a
// surprise in CloudQuery's destination behavior is contained and no CloudQuery
// internals leak past this boundary (Requirement 18.1).
const DefaultStagingSchema = "cq_staging"

// DefaultTableTimeout bounds how long one table may take before it is
// abandoned so one slow/failing table cannot stall the whole run.
const DefaultTableTimeout = 2 * time.Minute

// cqInternalColumns is the set of CloudQuery-internal bookkeeping columns the
// PostgreSQL destination adds to every staging table. They are not resource
// data and must never leak into a RawEvent payload. The exact set was pinned
// by the destination-behavior spike (task 1): see the "Risks / Unverified
// Items" register in design.md. Keeping the set here, rather than in the
// mapping or downstream, keeps all staging-schema assumptions inside this
// package (Requirement 18.1).
var cqInternalColumns = map[string]bool{
	"_cq_id":          true,
	"_cq_source_name": true,
	"_cq_sync_time":   true,
	"_cq_parent_id":   true,
}

// isCQInternalColumn reports whether a staging column is a CloudQuery-internal
// column that must be dropped before a row becomes a RawEvent payload. It
// matches the pinned set exactly and also treats any other `_cq_`-prefixed
// column as internal, so a future CloudQuery version that adds a bookkeeping
// column does not leak it into the payload (Requirement 2.5).
func isCQInternalColumn(col string) bool {
	if cqInternalColumns[col] {
		return true
	}
	return strings.HasPrefix(col, "_cq_")
}

// querier is the minimal query surface the collector needs from a connection
// pool. *pgxpool.Pool satisfies it; tests inject a recorder that captures the
// SQL/table attempted without a live database.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Source reads cloud inventory a CloudQuery sync wrote into the staging schema
// and emits one RawEvent per staging row. It implements source.Source at the
// same pipeline boundary the Steampipe source occupied, so every downstream
// normalizer is unchanged.
type Source struct {
	dsn          string
	catalog      *Catalog
	schema       string
	tableTimeout time.Duration
	// connect opens the query connection for a run. nil means the default
	// pgxpool path (the production behavior); tests inject a fake querier so
	// the per-table query loop can be exercised without a live database.
	connect func(ctx context.Context) (q querier, closeFn func(), err error)
	// enabledCaps is the set of capabilities granted to the connection. A
	// nil/empty set means "no gating configured" and preserves the
	// collect-everything behavior. core is always treated as enabled. See
	// gating.go for the gate semantics (mirrors the Steampipe source).
	enabledCaps map[string]bool
	// runner launches the CloudQuery CLI for this run before any staging rows
	// are read. A nil runner means "staging is already populated" — Collect
	// skips the sync and reads the existing cq_staging rows, the mode parity
	// and golden tests use. When a runner is wired, Collect runs the sync to
	// completion first; only if it succeeds does Collect read and emit rows, so
	// a failed or cancelled sync emits zero RawEvents (Requirements 1.6, 1.7).
	runner JobRunner
	// spec is the generated per-connection CloudQuery Spec handed to the
	// runner. It is credential-free (STS credentials travel in credentialEnv);
	// it is only meaningful when runner is non-nil.
	spec Spec
	// credentialEnv carries the run's STS session credentials for the runner.
	// It is handed to runner.Run only and is discarded (dropped from the
	// Source) once the run returns, on every path, so no credential value is
	// retained after the sync (Requirement 16.4).
	credentialEnv map[string]string
}

// New builds a Source from an explicit DSN and catalog, reading the default
// staging schema. The DSN and schema are the only CloudQuery-destination
// configuration this package needs; both stay inside the package.
func New(dsn string, catalog *Catalog) *Source {
	return &Source{
		dsn:          dsn,
		catalog:      catalog,
		schema:       DefaultStagingSchema,
		tableTimeout: DefaultTableTimeout,
	}
}

// Name implements source.Source and returns the stable collector name
// (Requirement 2.1).
func (s *Source) Name() string { return SourceName }

// setRunnerForTest wires the JobRunner, the generated Spec, and the per-run
// credential env onto the Source. Collect runs the sync (via runner.Run) before
// reading any staging rows, and discards the env after the run on every path.
// Exposed for tests that construct a Source via New; production wiring passes
// the runner, spec, and credential env when the Factory is added (a later
// task), mirroring setEnabledCapabilitiesForTest.
func (s *Source) setRunnerForTest(r JobRunner, spec Spec, env map[string]string) {
	s.runner = r
	s.spec = spec
	s.credentialEnv = env
}

// Collect connects to the customer PostgreSQL and streams one RawEvent per
// staging row across every mapped table. Rows are read from the staging schema
// a CloudQuery sync populated; each row becomes exactly one finalized RawEvent
// via mapRow (Requirement 2.2). Errors are isolated per table so one bad table
// does not abort the run, and both channels close when collection finishes.
func (s *Source) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, len(s.catalog.Tables)+1)

	go func() {
		defer close(events)
		defer close(errs)

		// Run the CloudQuery sync to completion before reading any staging
		// rows. A nil runner means staging is already populated (parity/golden
		// tests): skip the sync and read the existing cq_staging rows. When a
		// runner is wired and the sync fails or is cancelled, Collect returns
		// here having emitted zero RawEvents, so no partial inventory from an
		// aborted sync reaches the change engine (Requirements 1.6, 1.7).
		if err := s.runSync(ctx, run); err != nil {
			errs <- err
			return
		}

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

		collector := SourceName + ":" + s.catalog.Provider
		if s.catalog.Connection != "" {
			collector = SourceName + ":" + s.catalog.Connection
		}

		// Capability gating: exclude tables whose capability is not enabled for
		// this connection from the read set, so an ungranted capability never
		// produces an AccessDenied-style failure. The gate mirrors the
		// Steampipe source (see gating.go). A nil/empty enabled set collects
		// everything. The planned count reported below is the post-gating count
		// so a progress UI renders exactly the tables that will be read.
		planned := make([]TableSpec, 0, len(s.catalog.Tables))
		for _, spec := range s.catalog.Tables {
			if s.capabilityEnabled(spec.Capability) {
				planned = append(planned, spec)
			}
		}

		// Progress reporting is nil-safe: the report* helpers no-op when
		// run.Reporter is nil, so a run with no reporter wired behaves exactly
		// as before.
		reportPlanned(run.Reporter, len(planned))

		// skipped counts tables isolated out of this run (absent staging table,
		// or a mapped column absent/malformed for the table). Counting it makes
		// a degraded run visibly degraded rather than silently partial
		// (Requirements 2.6, 2.7): the count is surfaced on the error channel
		// as a final summary even when no reporter is wired.
		skipped := 0
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
				// Per-table isolation: a failure reading or mapping one table
				// (absent staging table, missing/malformed mapped column, bad
				// JSON) skips that table and continues with the rest. The skip
				// is reported through the reporter and counted; it never aborts
				// the whole Collect. (A sync-level failure that fails the whole
				// run is the JobRunner's concern, not this read loop.)
				skipped++
				reportTableError(run.Reporter, spec.Table, err)
				errs <- fmt.Errorf("cloudquery: table %s skipped: %w", spec.Table, err)
				continue
			}
			reportTableDone(run.Reporter, spec.Table, n)
		}

		// Surface the degraded-run summary so the run is visibly degraded even
		// if nothing consumed the per-table skip errors above.
		if skipped > 0 {
			errs <- fmt.Errorf("cloudquery: %s: degraded run, %d of %d table(s) skipped",
				s.catalog.Provider, skipped, len(planned))
		}
	}()

	return events, errs
}

// runSync launches the CloudQuery sync for this run and blocks until it
// completes, fails, or is cancelled. It is a no-op (returns nil) when no runner
// is wired, which is the "staging already populated" mode parity and golden
// tests use.
//
// The sync must finish before any staging row is read: this is what makes the
// no-partial-emit guarantee hold. If the runner returns an error — a non-zero
// CLI exit (job failure) or ctx.Err() on cancellation — runSync propagates it
// and Collect returns before connecting, so zero RawEvents are emitted from an
// aborted sync and the change engine never diffs partial inventory
// (Requirements 1.6, 1.7).
//
// Credential handling (Requirement 16.4): the per-run credential env is handed
// to the runner for this call only and is then dropped from the Source on every
// path (success, job failure, cancellation) so no credential value is retained
// after the run. The runner itself never persists the env to the staging DB,
// the on-disk spec, logs, or the returned error.
func (s *Source) runSync(ctx context.Context, run source.Run) error {
	if s.runner == nil {
		return nil
	}

	// Discard the credential env on every exit path. Capturing it in a local
	// and clearing the field before Run returns guarantees nothing retains the
	// secret after this method, regardless of whether the run succeeds, fails,
	// or is cancelled.
	env := s.credentialEnv
	s.credentialEnv = nil
	defer func() {
		for k := range env {
			delete(env, k)
		}
		env = nil
	}()

	if err := s.runner.Run(ctx, s.spec, env, run.Reporter); err != nil {
		return fmt.Errorf("cloudquery: sync job failed: %w", err)
	}
	return nil
}

// defaultConnect is the production connection path: open a pgx pool to the
// customer PostgreSQL and ping it. Returned closeFn closes the pool. It is only
// indirected so tests can inject a fake querier.
func (s *Source) defaultConnect(ctx context.Context) (querier, func(), error) {
	pool, err := pgxpool.New(ctx, s.dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("cloudquery: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("cloudquery: ping staging db: %w", err)
	}
	return pool, pool.Close, nil
}

// stagingTable returns the staging table name qualified by the staging schema,
// using the mapping's cq_table (which defaults to the Bellu table name). The
// schema and table name both come from the vetted mapping and package default,
// never from user input.
func (s *Source) stagingTable(spec TableSpec) string {
	if s.schema == "" {
		return spec.cqTable()
	}
	return s.schema + "." + spec.cqTable()
}

// collectTable reads one staging table and emits a RawEvent per row via mapRow.
func (s *Source) collectTable(ctx context.Context, pool querier, run source.Run, collector string, spec TableSpec, out chan<- rawevent.RawEvent) (int, error) {
	// Identifiers come from the vetted mapping, not user input. We select the
	// whole row as JSON so mapRow can pull the mapped columns and drop the
	// CloudQuery-internal ones.
	q := fmt.Sprintf(`SELECT to_jsonb(t) AS row FROM %s t`, s.stagingTable(spec))
	if where := spec.whereClause(); where != "" {
		q += " WHERE " + where
	}

	rows, err := pool.Query(ctx, q)
	if err != nil {
		// An absent staging table (the sync never produced it, or a mapping
		// names a table CloudQuery did not create) surfaces as the Postgres
		// undefined_table code. Translate it to a clear skip reason so the
		// caller isolates and counts the table rather than treating it as a
		// generic query failure (Requirement 2.6).
		if isUndefinedTable(err) {
			return 0, fmt.Errorf("staging table %s absent: %w", s.stagingTable(spec), err)
		}
		return 0, err
	}
	defer rows.Close()

	count := 0
	checkedColumns := false
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return count, err
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return count, fmt.Errorf("decode row: %w", err)
		}

		// Validate the mapping's required columns against the first row's
		// actual shape. A mapped column that is absent for this table means the
		// staging schema does not match the mapping, so the whole table is
		// skipped-and-counted rather than silently emitting events with empty
		// identity (Requirement 2.6). We check once, on the first row, since
		// every row of a table shares the same columns.
		if !checkedColumns {
			if missing := missingMappedColumn(spec, obj); missing != "" {
				return count, fmt.Errorf("mapped column %q absent", missing)
			}
			checkedColumns = true
		}

		ev, err := s.mapRow(run, collector, spec, obj, count)
		if err != nil {
			return count, err
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return count, ctx.Err()
		}
		count++
	}
	return count, rows.Err()
}

// missingMappedColumn returns the name of the first mapping-declared column
// that is absent from a decoded staging row, or "" when every declared column
// is present. Only columns the mapping actually names (id/scope/region) are
// required; a table may declare only some of these, and an empty column name
// is not required. A present-but-null column is NOT treated as missing: null is
// a valid value a resource may legitimately have, and colString already handles
// it. Absence of the key itself is the malformed-schema signal.
func missingMappedColumn(spec TableSpec, obj map[string]any) string {
	for _, col := range []string{spec.IDColumn, spec.ScopeColumn, spec.RegionColumn} {
		if col == "" {
			continue
		}
		if _, ok := obj[col]; !ok {
			return col
		}
	}
	return ""
}

// isUndefinedTable reports whether err is the Postgres undefined_table error
// (SQLSTATE 42P01), i.e. the staging table named by the mapping does not exist.
func isUndefinedTable(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42P01"
}

// mapRow turns one staging row into a single finalized RawEvent. It is the only
// place that knows the staging-schema shape: it selects the id/scope/region
// columns named by the TableSpec, stamps source="cloudquery" and the mapping's
// provider, strips every CloudQuery-internal `_cq_*` column from the payload,
// and tolerates unknown columns without failing (Requirements 2.2, 2.3, 2.5,
// 18.2). index is the row's position in the table, used only to synthesize a
// stable id when the mapped id column is absent so the event stays valid.
func (s *Source) mapRow(run source.Run, collector string, spec TableSpec, obj map[string]any, index int) (rawevent.RawEvent, error) {
	// Pull the mapped identity/scope/region before stripping internal columns.
	// Unknown or absent mapped columns yield empty strings rather than an
	// error, so an unexpected staging shape degrades gracefully.
	resourceID := colString(obj, spec.IDColumn)
	scope := colString(obj, spec.ScopeColumn)
	region := colString(obj, spec.RegionColumn)

	// Drop CloudQuery-internal bookkeeping columns from the payload so they
	// never leak downstream. Any other (unknown) column is kept as-is: the
	// normalizers read what they recognize and ignore the rest, so tolerating
	// unknown columns is the correct, non-failing behavior.
	for col := range obj {
		if isCQInternalColumn(col) {
			delete(obj, col)
		}
	}

	// Re-marshal the cleaned payload so PayloadHash/ID are computed over the
	// resource data only (internal columns excluded).
	cleaned, err := json.Marshal(obj)
	if err != nil {
		return rawevent.RawEvent{}, fmt.Errorf("encode payload: %w", err)
	}

	ev := rawevent.RawEvent{
		RunID:        run.RunID,
		Source:       SourceName,
		Collector:    collector,
		Provider:     s.catalog.Provider,
		Scope:        scope,
		Region:       region,
		ResourceType: spec.resourceType(),
		ResourceID:   resourceID,
		ObservedAt:   time.Now().UTC(),
		Payload:      json.RawMessage(cleaned),
	}
	if ev.ResourceID == "" {
		// Fall back to a stable synthetic id so the event is still valid when
		// the mapped id column is missing or empty for this row.
		ev.ResourceID = fmt.Sprintf("%s/%d", spec.Table, index)
	}
	ev.Finalize()
	return ev, nil
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

// colString reads a column from a decoded staging row as a string. A missing
// (unknown) or null column yields "" rather than an error, so a mapped column
// that is absent for one table degrades gracefully instead of failing the row.
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

var _ = pgx.ErrNoRows
