// Package export implements the optional OTLP change exporter for the observed
// lane (Req 15.1, 15.2). When enabled, it maps the observed-lane changes a run
// produced ([]temporal.Change) into OTLP-shaped entity events and hands them to
// an injectable sink. When disabled it is a strict no-op, and a failing sink
// NEVER surfaces an error to the caller: export is best-effort relative to
// ingest and projection, so an exporter problem can never fail a discovery run.
//
// # Dependency choice
//
// The OpenTelemetry OTLP log/event exporter SDK
// (go.opentelemetry.io/otel/exporters/otlp/...) is NOT a direct dependency of
// this module — only go.opentelemetry.io/otel and its trace/metric API arrive
// transitively (indirect) through other libraries, and none of the OTLP
// exporter transport packages are present. Pulling the full OTLP logs exporter
// + gRPC/HTTP transport in for an OPTIONAL feature would add a heavy dependency
// tree (gRPC, the logs SDK, protobuf log models) for a code path that is off by
// default.
//
// Instead this package keeps the OTLP shape as a plain struct (EntityEvent)
// following the OpenTelemetry "entity events" convention — a log-record-shaped
// event with a well-known event name and a resource/attribute bag — and writes
// it through an injectable Sink. The testable core is therefore (a) the
// Change -> EntityEvent mapping and (b) the failure-isolation guarantee, which
// is exactly what Req 15.1/15.2 constrain. A real OTLP transport can be added
// later as a Sink implementation (e.g. one that marshals EntityEvent to an OTLP
// LogRecord and ships it) without touching callers or the pipeline wiring.
package export

import (
	"context"
	"log"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/temporal"
)

// otelEntityEventName is the OpenTelemetry event name carried on every exported
// record, following the entity-events convention (otel.entity.*). Downstream
// collectors route on this name.
const otelEntityEventName = "otel.entity.state"

// EntityEvent is the OTLP-shaped representation of one observed change. It
// mirrors the fields an OpenTelemetry log-record/event carries for an "entity
// event": an event name, an observed timestamp, a stable entity type + id, and
// an attribute bag. Keeping it a plain struct lets the mapping be unit-tested
// without an OTLP transport; a Sink turns it into whatever wire form it needs.
type EntityEvent struct {
	// EventName is the OTLP event name (otel.entity.state).
	EventName string
	// Timestamp is the event_time of the underlying change (when the fact
	// became true), carried as the OTLP observed/event timestamp.
	Timestamp time.Time
	// EntityType is the OTLP entity type — "entity" or "edge" — from the
	// change's element kind.
	EntityType string
	// EntityID identifies the subject: an entity's canonical id, or an edge's
	// "src|relation|dst" key.
	EntityID string
	// ChangeType is the observed_events change_type (e.g. entity.created).
	ChangeType string
	// Attributes is the OTLP attribute bag: the change's identity, relation
	// endpoints (edges), diff summary, and evidence ids. Values are kept as the
	// native types a Sink can translate to OTLP AnyValue.
	Attributes map[string]any
}

// Sink receives mapped OTLP entity events. Implementations ship them to an OTLP
// endpoint (or a collector). A Sink MAY return an error; the Exporter isolates
// that error so it never reaches ingest/projection. Export is best-effort, so a
// Sink should itself be non-blocking / bounded where it matters.
type Sink interface {
	// Export ships a batch of entity events. An error is logged and swallowed by
	// the Exporter, never propagated to the discovery run.
	Export(ctx context.Context, events []EntityEvent) error
}

// Exporter optionally emits observed-lane changes as OTLP entity events. A nil
// Exporter, a disabled Exporter, or an Exporter with a nil Sink is a strict
// no-op. A Sink error (or panic) is logged and swallowed so export failure
// never fails ingest or projection (Req 15.1).
type Exporter struct {
	enabled bool
	sink    Sink
	// logErr records a swallowed export failure. Injectable for tests; defaults
	// to the standard logger.
	logErr func(error)
}

// Option configures an Exporter.
type Option func(*Exporter)

// WithLogger overrides where swallowed export errors are reported. Passing nil
// is ignored (the default logger is kept).
func WithLogger(fn func(error)) Option {
	return func(e *Exporter) {
		if fn != nil {
			e.logErr = fn
		}
	}
}

// New builds an Exporter. When enabled is false (or sink is nil) the Exporter is
// a no-op regardless of the changes passed to Export, satisfying Req 15.2: with
// the exporter disabled, ingest and projection complete without exporting.
func New(enabled bool, sink Sink, opts ...Option) *Exporter {
	e := &Exporter{
		enabled: enabled,
		sink:    sink,
		logErr: func(err error) {
			log.Printf("temporal/export: OTLP change export failed (ignored): %v", err)
		},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Enabled reports whether the exporter will emit (enabled and wired to a sink).
// Callers may use it to skip mapping work entirely, though Export is itself a
// no-op when disabled.
func (e *Exporter) Enabled() bool {
	return e != nil && e.enabled && e.sink != nil
}

// Export maps the run's observed changes to OTLP entity events and hands them to
// the Sink. It ALWAYS returns nil: this is the failure-isolation boundary. When
// the exporter is disabled, nil, or sink-less, Export does nothing. Unchanged
// changes (coalesced, no observed-event row) are skipped so the export feed
// mirrors what the facts log actually recorded. A Sink error — or a Sink panic —
// is logged and swallowed, never returned (Req 15.1).
//
// The returned error is always nil by design; the signature keeps an error slot
// so the contract stays explicit at call sites and a future variant could opt
// into surfacing, but the current guarantee is total isolation.
func (e *Exporter) Export(ctx context.Context, changes []temporal.Change) (err error) {
	if !e.Enabled() {
		return nil
	}
	if len(changes) == 0 {
		return nil
	}

	// A panicking Sink must not escape into ingest/projection either.
	defer func() {
		if r := recover(); r != nil {
			e.logErr(recoveredError(r))
		}
		err = nil
	}()

	events := MapChanges(changes)
	if len(events) == 0 {
		return nil
	}

	if sinkErr := e.sink.Export(ctx, events); sinkErr != nil {
		e.logErr(sinkErr)
	}
	return nil
}

// MapChanges converts observed-lane changes to OTLP entity events, dropping
// coalesced "unchanged" changes (which never produced an observed-event row).
// It is exported so the mapping — the testable core — can be exercised directly.
func MapChanges(changes []temporal.Change) []EntityEvent {
	events := make([]EntityEvent, 0, len(changes))
	for _, c := range changes {
		if c.ChangeType == temporal.ChangeEntityUnchanged {
			continue
		}
		events = append(events, mapChange(c))
	}
	return events
}

// mapChange maps one change to its OTLP entity-event shape.
func mapChange(c temporal.Change) EntityEvent {
	attrs := map[string]any{
		"change_type":  c.ChangeType,
		"element_kind": c.ElementKind,
		"canonical_id": c.CanonicalID,
	}
	entityID := c.CanonicalID
	if c.ElementKind == "edge" {
		// An edge's subject is its endpoint triple so collectors can key on the
		// full relationship, matching the observed lane's edge evidence key.
		entityID = c.CanonicalID + "|" + c.Relation + "|" + c.DstID
		attrs["relation"] = c.Relation
		attrs["dst_id"] = c.DstID
	}
	if len(c.EvidenceIDs) > 0 {
		attrs["evidence_ids"] = append([]string(nil), c.EvidenceIDs...)
	}
	if d := diffAttrs(c.Diff); len(d) > 0 {
		attrs["diff"] = d
	}
	// Carry the full attribute snapshot (empty for deletions) so a consumer can
	// reconstruct the element without a second lookup.
	if len(c.Attributes) > 0 {
		attrs["attributes"] = c.Attributes
	}

	return EntityEvent{
		EventName:  otelEntityEventName,
		Timestamp:  c.EventTime,
		EntityType: c.ElementKind,
		EntityID:   entityID,
		ChangeType: c.ChangeType,
		Attributes: attrs,
	}
}

// diffAttrs flattens the change diff into an attribute-friendly map, omitting
// empty sides so the OTLP attribute bag stays lean.
func diffAttrs(d temporal.Diff) map[string]any {
	out := map[string]any{}
	if len(d.Added) > 0 {
		out["added"] = d.Added
	}
	if len(d.Removed) > 0 {
		out["removed"] = d.Removed
	}
	if len(d.Changed) > 0 {
		out["changed"] = d.Changed
	}
	return out
}

// recoveredError normalizes a recovered panic value into an error for logging.
func recoveredError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return &panicError{value: r}
}

type panicError struct{ value any }

func (p *panicError) Error() string {
	return "temporal/export: sink panicked"
}
