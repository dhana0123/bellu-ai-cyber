package temporal

import (
	"testing"
	"time"
)

// TestBaselineMarkerRoundTrips is a minimal self-check for the baseline marker
// convention (the DB-backed retention test is task 18.2). It asserts a marked
// snapshot is detectable, the marker does not mutate the source map, and a
// plain attribute map is not mistaken for a baseline.
func TestBaselineMarkerRoundTrips(t *testing.T) {
	src := map[string]any{"region": "us-east-1", "public": false}

	if IsBaselineAttrs(src) {
		t.Fatalf("plain attrs reported as baseline")
	}

	marked := markBaseline(src)
	if !IsBaselineAttrs(marked) {
		t.Fatalf("marked attrs not detected as baseline")
	}
	if _, ok := src[BaselineMarkerKey]; ok {
		t.Fatalf("markBaseline mutated the source map")
	}
	if marked["region"] != "us-east-1" || marked["public"] != false {
		t.Fatalf("markBaseline dropped original attributes: %#v", marked)
	}

	// Non-bool / absent markers are not baselines.
	if IsBaselineAttrs(map[string]any{BaselineMarkerKey: "yes"}) {
		t.Fatalf("string marker value should not count as baseline")
	}
	if IsBaselineAttrs(nil) {
		t.Fatalf("nil attrs reported as baseline")
	}
}

// TestCoalescedUnchangedInvariant documents Req 11.1: an unchanged change is the
// coalesced one and must not be written as a row (Engine.Apply skips it).
func TestCoalescedUnchangedInvariant(t *testing.T) {
	if !IsCoalescedUnchanged(Change{ChangeType: ChangeEntityUnchanged}) {
		t.Fatalf("entity.unchanged should be coalesced")
	}
	if IsCoalescedUnchanged(Change{ChangeType: ChangeEntityAttributeUpdated}) {
		t.Fatalf("attribute_updated must not be coalesced")
	}
}

// TestRetentionCutoff checks the horizon resolution: explicit Horizon wins,
// MaxAge derives now-MaxAge, and a config with neither is rejected.
func TestRetentionCutoff(t *testing.T) {
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	explicit := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, err := (RetentionConfig{Horizon: explicit}).cutoff(now); err != nil || !got.Equal(explicit) {
		t.Fatalf("explicit horizon: got %v err %v", got, err)
	}

	got, err := (RetentionConfig{MaxAge: 24 * time.Hour}).cutoff(now)
	if err != nil {
		t.Fatalf("maxage cutoff err: %v", err)
	}
	if want := now.Add(-24 * time.Hour); !got.Equal(want) {
		t.Fatalf("maxage cutoff: got %v want %v", got, want)
	}

	if _, err := (RetentionConfig{}).cutoff(now); err == nil {
		t.Fatalf("empty config should error")
	}
}
