package temporal

import (
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
)

// fixedEventTime is an arbitrary deterministic event time used across cases so
// classification does not depend on wall-clock.
var fixedEventTime = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

// TestClassifyChangesExactlyOnce drives classifyChanges with table-driven cases
// covering every observed-lane classification the function emits, asserting each
// element yields exactly one Change with the expected ChangeType. The function
// is the pure diff/classify core of the change engine: it takes a currentState
// directly, so the branch logic is exercised without a database.
//
// Note: classifyChanges never emits relation.removed; edge absence / deletion is
// reference-counted and handled by referenceCountedDeletions (task 9.4), outside
// this function. These cases therefore cover what classifyChanges itself produces.
func TestClassifyChangesExactlyOnce(t *testing.T) {
	tests := []struct {
		name string
		// current is the prior projection cache the run is diffed against.
		current currentState
		// res is the normalized run under classification.
		res normalize.Result
		// wantKind/wantType is the single expected classification.
		wantKind string
		wantType string
	}{
		{
			name:    "entity created when prev absent",
			current: currentState{entity: map[string]map[string]any{}},
			res: normalize.Result{
				Entities: []model.Entity{{
					CanonicalID: "entity:aws:bucket-a",
					Attributes:  map[string]any{"region": "us-east-1", "public": false},
				}},
			},
			wantKind: elementEntity,
			wantType: ChangeEntityCreated,
		},
		{
			name: "entity attribute_updated when non-state attr changes",
			current: currentState{entity: map[string]map[string]any{
				"entity:aws:bucket-a": {"region": "us-east-1", "public": false},
			}},
			res: normalize.Result{
				Entities: []model.Entity{{
					CanonicalID: "entity:aws:bucket-a",
					// region changed, public (a state field) unchanged
					Attributes: map[string]any{"region": "us-west-2", "public": false},
				}},
			},
			wantKind: elementEntity,
			wantType: ChangeEntityAttributeUpdated,
		},
		{
			name: "entity state_changed when a state field toggles",
			current: currentState{entity: map[string]map[string]any{
				"entity:aws:bucket-a": {"region": "us-east-1", "public": false},
			}},
			res: normalize.Result{
				Entities: []model.Entity{{
					CanonicalID: "entity:aws:bucket-a",
					// public (a stateFieldKeys attr) toggled
					Attributes: map[string]any{"region": "us-east-1", "public": true},
				}},
			},
			wantKind: elementEntity,
			wantType: ChangeEntityStateChanged,
		},
		{
			name: "entity state_changed wins over attribute_updated when both change",
			current: currentState{entity: map[string]map[string]any{
				"entity:aws:bucket-a": {"region": "us-east-1", "public": false},
			}},
			res: normalize.Result{
				Entities: []model.Entity{{
					CanonicalID: "entity:aws:bucket-a",
					// both a state field (public) and a plain field (region) changed:
					// state_changed must take precedence.
					Attributes: map[string]any{"region": "us-west-2", "public": true},
				}},
			},
			wantKind: elementEntity,
			wantType: ChangeEntityStateChanged,
		},
		{
			name: "entity unchanged is coalesced when identical",
			current: currentState{entity: map[string]map[string]any{
				"entity:aws:bucket-a": {"region": "us-east-1", "public": false},
			}},
			res: normalize.Result{
				Entities: []model.Entity{{
					CanonicalID: "entity:aws:bucket-a",
					Attributes:  map[string]any{"region": "us-east-1", "public": false},
				}},
			},
			wantKind: elementEntity,
			wantType: ChangeEntityUnchanged,
		},
		{
			name:    "relation added when edge absent",
			current: currentState{edge: map[edgeKey]map[string]any{}},
			res: normalize.Result{
				Edges: []model.Edge{{
					Relation:       model.Relation("CONTAINS"),
					SrcCanonicalID: "entity:aws:acct-1",
					DstCanonicalID: "entity:aws:bucket-a",
					Attributes:     map[string]any{"weight": 1},
				}},
			},
			wantKind: elementEdge,
			wantType: ChangeRelationAdded,
		},
		{
			name: "relation attribute_changed when edge attrs differ",
			current: currentState{edge: map[edgeKey]map[string]any{
				{src: "entity:aws:acct-1", rel: "CONTAINS", dst: "entity:aws:bucket-a"}: {"weight": 1},
			}},
			res: normalize.Result{
				Edges: []model.Edge{{
					Relation:       model.Relation("CONTAINS"),
					SrcCanonicalID: "entity:aws:acct-1",
					DstCanonicalID: "entity:aws:bucket-a",
					Attributes:     map[string]any{"weight": 2},
				}},
			},
			wantKind: elementEdge,
			wantType: ChangeRelationAttrChanged,
		},
		{
			name: "edge unchanged is coalesced when identical",
			current: currentState{edge: map[edgeKey]map[string]any{
				{src: "entity:aws:acct-1", rel: "CONTAINS", dst: "entity:aws:bucket-a"}: {"weight": 1},
			}},
			res: normalize.Result{
				Edges: []model.Edge{{
					Relation:       model.Relation("CONTAINS"),
					SrcCanonicalID: "entity:aws:acct-1",
					DstCanonicalID: "entity:aws:bucket-a",
					Attributes:     map[string]any{"weight": 1},
				}},
			},
			wantKind: elementEdge,
			wantType: ChangeEntityUnchanged, // edges reuse the unchanged type and are coalesced
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyChanges("test-source", tt.res, normalizeCurrent(tt.current), nil, fixedEventTime)

			if len(got) != 1 {
				t.Fatalf("expected exactly one classification, got %d: %+v", len(got), got)
			}
			c := got[0]
			if c.ElementKind != tt.wantKind {
				t.Errorf("ElementKind = %q, want %q", c.ElementKind, tt.wantKind)
			}
			if c.ChangeType != tt.wantType {
				t.Errorf("ChangeType = %q, want %q", c.ChangeType, tt.wantType)
			}
			if !c.EventTime.Equal(fixedEventTime) {
				t.Errorf("EventTime = %v, want %v", c.EventTime, fixedEventTime)
			}
		})
	}
}

// TestClassifyChangesUnchangedWritesNoRow asserts the coalescing contract: an
// unchanged element classifies as unchanged and Apply writes no observed_events
// row for it. Apply skips exactly the changes whose ChangeType is
// ChangeEntityUnchanged (the `continue` guard before writeObservedEvent), so we
// assert that invariant directly against the classification output.
func TestClassifyChangesUnchangedWritesNoRow(t *testing.T) {
	current := currentState{
		entity: map[string]map[string]any{
			"entity:aws:bucket-a": {"region": "us-east-1", "public": false},
		},
		edge: map[edgeKey]map[string]any{
			{src: "entity:aws:acct-1", rel: "CONTAINS", dst: "entity:aws:bucket-a"}: {"weight": 1},
		},
	}
	res := normalize.Result{
		Entities: []model.Entity{{
			CanonicalID: "entity:aws:bucket-a",
			Attributes:  map[string]any{"region": "us-east-1", "public": false},
		}},
		Edges: []model.Edge{{
			Relation:       model.Relation("CONTAINS"),
			SrcCanonicalID: "entity:aws:acct-1",
			DstCanonicalID: "entity:aws:bucket-a",
			Attributes:     map[string]any{"weight": 1},
		}},
	}

	changes := classifyChanges("test-source", res, current, nil, fixedEventTime)

	if len(changes) != 2 {
		t.Fatalf("expected 2 classifications (entity + edge), got %d", len(changes))
	}
	for _, c := range changes {
		if c.ChangeType != ChangeEntityUnchanged {
			t.Fatalf("expected all unchanged, got %q for %s", c.ChangeType, c.CanonicalID)
		}
	}

	// Apply writes a row for every change whose type is not unchanged; count the
	// rows that would be written to confirm the coalescing contract.
	rowsWritten := countWritableRows(changes)
	if rowsWritten != 0 {
		t.Errorf("unchanged elements should write no rows, got %d writable rows", rowsWritten)
	}
}

// normalizeCurrent ensures both maps in a currentState are non-nil so a case can
// set only the half it exercises without nil-map lookups mattering (map reads of
// a nil map are safe in Go, but this keeps the fixtures explicit).
func normalizeCurrent(cs currentState) currentState {
	if cs.entity == nil {
		cs.entity = map[string]map[string]any{}
	}
	if cs.edge == nil {
		cs.edge = map[edgeKey]map[string]any{}
	}
	return cs
}

// countWritableRows mirrors Apply's write guard: it counts the changes that
// would produce an observed_events row (every change whose type is not the
// coalesced unchanged type).
func countWritableRows(changes []Change) int {
	n := 0
	for _, c := range changes {
		if c.ChangeType == ChangeEntityUnchanged {
			continue
		}
		n++
	}
	return n
}
