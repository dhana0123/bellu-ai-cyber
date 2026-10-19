package export

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/temporal"
)

// recordingSink captures what it was asked to export and can be made to fail or
// panic, so the tests can assert both the mapping and the failure-isolation
// guarantee.
type recordingSink struct {
	calls   int
	events  []EntityEvent
	err     error
	doPanic bool
}

func (s *recordingSink) Export(_ context.Context, events []EntityEvent) error {
	s.calls++
	s.events = append(s.events, events...)
	if s.doPanic {
		panic("sink boom")
	}
	return s.err
}

func sampleChanges() []temporal.Change {
	return []temporal.Change{
		{
			ElementKind: "entity",
			ChangeType:  temporal.ChangeEntityCreated,
			CanonicalID: "ent:1",
			Attributes:  map[string]any{"name": "alpha"},
			Diff:        temporal.Diff{Added: []string{"name"}},
			EvidenceIDs: []string{"raw-1"},
			EventTime:   time.Unix(1700, 0).UTC(),
		},
		{
			ElementKind: "edge",
			ChangeType:  temporal.ChangeRelationAdded,
			CanonicalID: "ent:1",
			Relation:    "DEPENDS_ON",
			DstID:       "ent:2",
			Attributes:  map[string]any{"weight": 1},
			EventTime:   time.Unix(1700, 0).UTC(),
		},
		{
			// Coalesced unchanged: must be dropped from the export feed.
			ElementKind: "entity",
			ChangeType:  temporal.ChangeEntityUnchanged,
			CanonicalID: "ent:3",
		},
	}
}

// Req 15.2: when disabled, Export is a no-op that returns no error and never
// touches the sink — ingest/projection complete without exporting.
func TestExport_DisabledIsNoOp(t *testing.T) {
	sink := &recordingSink{}
	exp := New(false, sink)

	if exp.Enabled() {
		t.Fatal("exporter should report disabled")
	}
	if err := exp.Export(context.Background(), sampleChanges()); err != nil {
		t.Fatalf("disabled Export returned error: %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("disabled exporter called sink %d times, want 0", sink.calls)
	}
}

// A nil sink is also a strict no-op even when "enabled".
func TestExport_NilSinkIsNoOp(t *testing.T) {
	exp := New(true, nil)
	if exp.Enabled() {
		t.Fatal("exporter with nil sink should not report enabled")
	}
	if err := exp.Export(context.Background(), sampleChanges()); err != nil {
		t.Fatalf("nil-sink Export returned error: %v", err)
	}
}

// Req 15.1: a failing sink never surfaces an error to the caller.
func TestExport_FailingSinkIsSwallowed(t *testing.T) {
	sink := &recordingSink{err: errors.New("otlp endpoint unreachable")}
	var logged error
	exp := New(true, sink, WithLogger(func(err error) { logged = err }))

	if err := exp.Export(context.Background(), sampleChanges()); err != nil {
		t.Fatalf("Export propagated sink error to caller: %v", err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink called %d times, want 1", sink.calls)
	}
	if logged == nil {
		t.Fatal("expected the swallowed sink error to be logged")
	}
}

// Req 15.1: a panicking sink also never escapes to the caller.
func TestExport_PanicIsSwallowed(t *testing.T) {
	sink := &recordingSink{doPanic: true}
	var logged error
	exp := New(true, sink, WithLogger(func(err error) { logged = err }))

	if err := exp.Export(context.Background(), sampleChanges()); err != nil {
		t.Fatalf("Export propagated sink panic to caller: %v", err)
	}
	if logged == nil {
		t.Fatal("expected the recovered panic to be logged")
	}
}

// Enabled exporter maps changes to OTLP entity events, dropping unchanged ones.
func TestExport_EnabledMapsChanges(t *testing.T) {
	sink := &recordingSink{}
	exp := New(true, sink)

	if !exp.Enabled() {
		t.Fatal("exporter should report enabled")
	}
	if err := exp.Export(context.Background(), sampleChanges()); err != nil {
		t.Fatalf("Export returned error: %v", err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink called %d times, want 1", sink.calls)
	}
	// Three changes in, one coalesced -> two events out.
	if len(sink.events) != 2 {
		t.Fatalf("exported %d events, want 2 (unchanged dropped)", len(sink.events))
	}

	ent := sink.events[0]
	if ent.EventName != otelEntityEventName {
		t.Errorf("event name = %q, want %q", ent.EventName, otelEntityEventName)
	}
	if ent.EntityType != "entity" || ent.EntityID != "ent:1" {
		t.Errorf("entity event id/type = %q/%q, want ent:1/entity", ent.EntityID, ent.EntityType)
	}
	if ent.ChangeType != temporal.ChangeEntityCreated {
		t.Errorf("entity change_type = %q", ent.ChangeType)
	}

	edge := sink.events[1]
	if edge.EntityType != "edge" {
		t.Errorf("edge entity type = %q, want edge", edge.EntityType)
	}
	if edge.EntityID != "ent:1|DEPENDS_ON|ent:2" {
		t.Errorf("edge entity id = %q, want endpoint triple", edge.EntityID)
	}
	if edge.Attributes["relation"] != "DEPENDS_ON" || edge.Attributes["dst_id"] != "ent:2" {
		t.Errorf("edge attributes missing relation/dst_id: %#v", edge.Attributes)
	}
}

// Empty / no-op inputs do not call the sink.
func TestExport_EmptyChanges(t *testing.T) {
	sink := &recordingSink{}
	exp := New(true, sink)
	if err := exp.Export(context.Background(), nil); err != nil {
		t.Fatalf("Export(nil) returned error: %v", err)
	}
	// Only unchanged -> maps to zero events -> no sink call.
	if err := exp.Export(context.Background(), []temporal.Change{{ChangeType: temporal.ChangeEntityUnchanged}}); err != nil {
		t.Fatalf("Export(unchanged) returned error: %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("sink called %d times for empty/unchanged input, want 0", sink.calls)
	}
}
