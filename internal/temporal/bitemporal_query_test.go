//go:build integration

package temporal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// TestBiTemporalQueryViews proves Property 4 (as-of / asKnownAt correctness,
// Req 6.1-6.5) against Querier.StateAt using a crafted observed history that
// contains a BACKDATED fact: a row whose event_time lies in the past but whose
// recorded_at is LATER than a subsequent event's recorded_at. That out-of-order
// learning is exactly what separates the three views (reality, as-of,
// asKnownAt) and what makes the fold order (event_time, recorded_at, seq)
// observable.
//
// Crafted timeline for a single entity (one event_time axis "day", one
// recorded_at axis "learn"). All times UTC.
//
//	#  event_time (day)   recorded_at (learn)   attrs
//	e1 D1                 L1                     tier=bronze              (created)
//	e2 D3                 L2                     tier=silver              (update)
//	e3 D2  (BACKDATED)    L3  (learned LAST)     tier=gold, backfilled    (update)
//
// where D1 < D2 < D3 and L1 < L2 < L3. e3 is backdated: its event_time D2 is
// BEFORE e2's event_time D3, yet its recorded_at L3 is AFTER e2's L2 — Bellu
// learned at L3 about a fact that became true back at D2. This is the row the
// task asks for: "a backdated event_time learned later via recorded_at".
func TestBiTemporalQueryViews(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	q := NewQuerier(st)

	const (
		org    = int64(99)
		source = "fixture"
		runID  = "run-bitemporal"
		entity = "entity:aws:bucket-bitemporal"
	)

	// collection_runs row to satisfy observed_events.run_id FK.
	if err := st.StartRun(ctx, runID, source); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Event-time axis.
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	// Recorded-at (learn) axis — strictly increasing, independent of event_time.
	l1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	l2 := time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC)
	l3 := time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC) // backdated fact learned LAST

	seedBiTemporal(ctx, t, st, org, runID, source, biRow{
		changeType: ChangeEntityCreated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "tier": "bronze"},
		eventTime:  d1,
		recordedAt: l1,
	})
	seedBiTemporal(ctx, t, st, org, runID, source, biRow{
		changeType: ChangeEntityAttributeUpdated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "tier": "silver"},
		eventTime:  d3,
		recordedAt: l2,
	})
	seedBiTemporal(ctx, t, st, org, runID, source, biRow{
		changeType: ChangeEntityAttributeUpdated,
		canonical:  entity,
		attrs:      map[string]any{"region": "us-east-1", "tier": "gold", "backfilled": true},
		eventTime:  d2, // BACKDATED: before e2's event_time d3
		recordedAt: l3, // learned LAST: after e2's recorded_at l2
	})

	now := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC) // after every event/learn time

	// ---- Reality view (Req 6.1): knownAt = now, event_time = now. Fold ALL
	// events by (event_time, recorded_at, seq); the terminal event_time is d3
	// (e2, tier=silver). The backdated e3 (event_time d2) folds BEFORE e2, so it
	// does NOT win the fold despite being learned last — proving the fold keys on
	// event_time first, not recorded_at. ----
	t.Run("reality view folds to latest event_time", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, now, now)
		if err != nil {
			t.Fatalf("StateAt reality: %v", err)
		}
		if !s.Present {
			t.Fatalf("entity should be present in reality view")
		}
		if got := s.Attributes["tier"]; got != "silver" {
			t.Errorf("reality tier = %v, want silver (e2 at event_time d3 wins)", got)
		}
		if _, ok := s.Attributes["backfilled"]; ok {
			t.Errorf("reality view must not carry e3's backfilled attr; e3's event_time d2 is older than e2's d3")
		}
		if !s.EventTime.Equal(d3) {
			t.Errorf("reality terminal event_time = %v, want d3 %v", s.EventTime, d3)
		}
	})

	// ---- As-of view (Req 6.2, 6.4): event_time cutoff = d2, knownAt = now. Only
	// e1 (d1) and e3 (d2) are in-window; e2 (d3) is excluded. Among the two, the
	// fold order (event_time, recorded_at, seq) puts e3 (d2) last, so tier=gold
	// and backfilled appear. This is the view where the backdated fact DOES win. ----
	t.Run("as-of d2 sees the backdated fact", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, d2, now)
		if err != nil {
			t.Fatalf("StateAt as-of d2: %v", err)
		}
		if !s.Present {
			t.Fatalf("entity should be present as-of d2")
		}
		if got := s.Attributes["tier"]; got != "gold" {
			t.Errorf("as-of d2 tier = %v, want gold (e3 at event_time d2 wins)", got)
		}
		if got := s.Attributes["backfilled"]; got != true {
			t.Errorf("as-of d2 backfilled = %v, want true", got)
		}
		if !s.EventTime.Equal(d2) {
			t.Errorf("as-of d2 terminal event_time = %v, want d2 %v", s.EventTime, d2)
		}
	})

	// As-of d1: only e1 is in-window → created state, tier=bronze.
	t.Run("as-of d1 sees only the creation", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, d1, now)
		if err != nil {
			t.Fatalf("StateAt as-of d1: %v", err)
		}
		if !s.Present {
			t.Fatalf("entity should be present as-of d1")
		}
		if got := s.Attributes["tier"]; got != "bronze" {
			t.Errorf("as-of d1 tier = %v, want bronze", got)
		}
	})

	// ---- asKnownAt view (Req 6.3): BOTH cutoffs. Hold the event_time cutoff at
	// the reality horizon (now) but vary knownAt. ----

	// asKnownAt(event<=now, known<=l1): only e1 recorded (l1). tier=bronze.
	t.Run("asKnownAt l1 knows only the creation", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, now, l1)
		if err != nil {
			t.Fatalf("StateAt asKnownAt l1: %v", err)
		}
		if !s.Present {
			t.Fatalf("entity should be present asKnownAt l1")
		}
		if got := s.Attributes["tier"]; got != "bronze" {
			t.Errorf("asKnownAt l1 tier = %v, want bronze (only e1 known)", got)
		}
	})

	// asKnownAt(event<=now, known<=l2): e1 (l1) and e2 (l2) known; e3 (l3) NOT yet
	// learned. Fold by event_time → e2 (d3) wins. tier=silver, no backfilled.
	t.Run("asKnownAt l2 knows e1,e2 but not the backdated e3", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, now, l2)
		if err != nil {
			t.Fatalf("StateAt asKnownAt l2: %v", err)
		}
		if got := s.Attributes["tier"]; got != "silver" {
			t.Errorf("asKnownAt l2 tier = %v, want silver (e2 wins, e3 not yet known)", got)
		}
		if _, ok := s.Attributes["backfilled"]; ok {
			t.Errorf("asKnownAt l2 must not know e3's backfilled attr (recorded_at l3 > l2)")
		}
	})

	// asKnownAt(event<=now, known<=l3): everything known. Same as reality view:
	// e2 (d3) still wins the event_time fold even though e3 was learned last.
	t.Run("asKnownAt l3 equals the reality view", func(t *testing.T) {
		s, err := q.StateAt(ctx, org, entity, now, l3)
		if err != nil {
			t.Fatalf("StateAt asKnownAt l3: %v", err)
		}
		if got := s.Attributes["tier"]; got != "silver" {
			t.Errorf("asKnownAt l3 tier = %v, want silver (reality view)", got)
		}
	})

	// ---- Monotonicity (Req 6.5): hold event_time cutoff fixed, increase knownAt;
	// a previously-known fact must never disappear and information only grows.
	//
	// Hold event_time cutoff at d2 so the backdated e3 is in the event window.
	// Sweep knownAt across the learn axis. The set of known rows must be
	// monotonically non-decreasing: known(l1) ⊆ known(l2) ⊆ known(l3). We assert
	// this via the fold's row count AND via the specific fact: once backfilled is
	// known (at l3) it stays known for any larger knownAt. ----
	t.Run("monotonicity: increasing knownAt only adds information", func(t *testing.T) {
		fixedEvent := d2 // in-window: e1 (d1) and e3 (d2); e2 (d3) excluded

		// Count the rows the fold would consider at each knownAt. Must be
		// non-decreasing as knownAt grows.
		learnPoints := []time.Time{l1, l2, l3, now}
		prevCount := -1
		var prevTerminal time.Time
		havePrev := false
		for _, k := range learnPoints {
			n := countInWindow(ctx, t, st, org, entity, fixedEvent, k)
			if n < prevCount {
				t.Fatalf("row count decreased as knownAt grew to %v: %d < %d", k, n, prevCount)
			}
			prevCount = n

			s, err := q.StateAt(ctx, org, entity, fixedEvent, k)
			if err != nil {
				t.Fatalf("StateAt monotonicity knownAt=%v: %v", k, err)
			}
			// The folded WINNER can legitimately change (bronze→gold) as a
			// backdated row becomes known, but Req 6.5's guarantee is at the
			// knowledge level: increasing knownAt never removes a fact Bellu
			// already knew. We verify the in-window fold size is monotone (above)
			// and that the terminal event_time never moves earlier — a later
			// knownAt only ever reveals facts, so the winning event_time can only
			// stay or move forward.
			if havePrev && s.EventTime.Before(prevTerminal) {
				t.Errorf("terminal event_time moved earlier as knownAt grew to %v: %v < %v", k, s.EventTime, prevTerminal)
			}
			prevTerminal = s.EventTime
			havePrev = true
		}

		// Concretely: at knownAt=l2 the backdated e3 is NOT known, so as-of d2
		// resolves to e1 (bronze, event_time d1). At knownAt=l3 e3 IS known, so
		// as-of d2 resolves to e3 (gold, event_time d2). Increasing knownAt added
		// the backfilled fact and never removed bronze's existence from history.
		atL2, err := q.StateAt(ctx, org, entity, fixedEvent, l2)
		if err != nil {
			t.Fatalf("StateAt d2@l2: %v", err)
		}
		if got := atL2.Attributes["tier"]; got != "bronze" {
			t.Errorf("as-of d2 asKnownAt l2 tier = %v, want bronze (e3 not yet learned)", got)
		}
		atL3, err := q.StateAt(ctx, org, entity, fixedEvent, l3)
		if err != nil {
			t.Fatalf("StateAt d2@l3: %v", err)
		}
		if got := atL3.Attributes["tier"]; got != "gold" {
			t.Errorf("as-of d2 asKnownAt l3 tier = %v, want gold (e3 now learned)", got)
		}
		if got := atL3.Attributes["backfilled"]; got != true {
			t.Errorf("as-of d2 asKnownAt l3 backfilled = %v, want true", got)
		}
		// Monotonicity: the fact newly known at l3 (backfilled) is still known for
		// any later knownAt.
		atNow, err := q.StateAt(ctx, org, entity, fixedEvent, now)
		if err != nil {
			t.Fatalf("StateAt d2@now: %v", err)
		}
		if got := atNow.Attributes["backfilled"]; got != true {
			t.Errorf("a fact known at l3 must stay known at now: backfilled = %v, want true", got)
		}
	})
}

// biRow is one observed_events row to seed with explicit event_time AND
// recorded_at, so the test controls both bi-temporal axes (seedObserved in the
// retention test fixes recorded_at to now(), which cannot express a backdated
// fact learned later).
type biRow struct {
	changeType string
	canonical  string
	attrs      map[string]any
	eventTime  time.Time
	recordedAt time.Time
}

// seedBiTemporal inserts a single entity observed_events row with explicit
// event_time and recorded_at.
func seedBiTemporal(ctx context.Context, t *testing.T, st *store.Store, org int64, runID, source string, r biRow) {
	t.Helper()
	attrs, err := json.Marshal(r.attrs)
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO observed_events
		   (org_id, run_id, source, element_kind, change_type, canonical_id,
		    attributes, diff, evidence_ids, event_time, recorded_at)
		 VALUES ($1,$2,$3,'entity',$4,$5,$6,'{}'::jsonb,'{}',$7,$8)`,
		org, runID, source, r.changeType, r.canonical, attrs, r.eventTime.UTC(), r.recordedAt.UTC()); err != nil {
		t.Fatalf("seed bi-temporal row: %v", err)
	}
}

// countInWindow returns how many of the entity's observed rows fall inside the
// bi-temporal window (event_time <= eventCutoff AND recorded_at <= knownCutoff).
// It mirrors the predicate StateAt folds over, so the test can assert the window
// grows monotonically as knownAt increases (Req 6.5).
func countInWindow(ctx context.Context, t *testing.T, st *store.Store, org int64, canonical string, eventCutoff, knownCutoff time.Time) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM observed_events
		  WHERE org_id = $1 AND canonical_id = $2 AND element_kind = 'entity'
		    AND event_time <= $3 AND recorded_at <= $4`,
		org, canonical, eventCutoff.UTC(), knownCutoff.UTC()).Scan(&n); err != nil {
		t.Fatalf("count in-window: %v", err)
	}
	return n
}
