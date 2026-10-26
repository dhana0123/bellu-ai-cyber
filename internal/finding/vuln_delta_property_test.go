//go:build integration

package finding

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file is gated behind the `integration` build tag because UpsertFinding
// performs a real INSERT ... ON CONFLICT ... RETURNING (xmax = 0) against
// Postgres — the Created flag that drives the vulnerability delta tap is only
// produced by the database, so there is nothing meaningful to exercise without
// a live connection. It therefore only runs when a container runtime is
// available: `go test -tags integration ./internal/finding/... -run
// TestVulnerabilityDeltaProperty`.
//
// Property 3: Vulnerability delta (Validates Requirements 4.5, 4.6, 5.2, 5.3).
//
// For a generated set of distinct (asset_id, vuln_id) findings, the FIRST
// UpsertFinding of a key (Created == true) emits EXACTLY ONE monitor event with
// source "correlate", kind "vulnerability", and delivery_id = asset_id + ":" +
// vuln_id (Req 4.5). Every subsequent UpsertFinding of the SAME key
// (Created == false) emits ZERO additional events (Req 4.6, 5.2, 5.3), so a
// re-correlation sweep over unchanged still-open findings produces no events —
// no re-trigger storm. The captured emit count per delivery_id is therefore
// exactly 1 regardless of how many times the key is re-upserted.

// newFindingTestStore spins up a disposable Postgres and returns a migrated
// *store.Store. The findings table needs no org seed — findings are not
// org-partitioned (the tenant boundary is carried in the synthetic asset_id the
// ingest seam builds), so Migrate alone is enough to exercise UpsertFinding.
func newFindingTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"),
		tcpostgres.WithUsername("bellu"),
		tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// emittedEvent captures a single InsertMonitorEvent call for later assertion.
type emittedEvent struct {
	org    int64
	source string
	kind   string
	// deliveryID is the per-source idempotency key the tap stamps; for the
	// vulnerability delta it is asset_id + ":" + vuln_id.
	deliveryID string
}

// recordingEmitter is a fake MonitorEmitter that records every
// InsertMonitorEvent(ctx, org, in) call under a mutex. It always succeeds,
// returning a monotonically increasing id, so the tap's best-effort emit path
// never short-circuits — we want to observe exactly how many times the tap
// fires. Capturing under the lock makes it safe even though the production tap
// is best-effort and swallows errors.
type recordingEmitter struct {
	mu     sync.Mutex
	nextID int64
	calls  []emittedEvent
}

func (r *recordingEmitter) InsertMonitorEvent(_ context.Context, org int64, in store.MonitorEventInput) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.calls = append(r.calls, emittedEvent{
		org:        org,
		source:     in.Source,
		kind:       in.EventKind,
		deliveryID: in.DeliveryID,
	})
	return r.nextID, nil
}

// snapshot returns a copy of the recorded calls under the lock.
func (r *recordingEmitter) snapshot() []emittedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]emittedEvent, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestVulnerabilityDeltaProperty is Property 3. It uses a fixed-seed math/rand
// loop (no new dependency — rapid is not in go.mod) to vary the finding set and
// the number of re-upserts per key, asserting that the delta tap fires exactly
// once per key on the nothing->open transition and never again.
//
// **Property 3: Vulnerability delta**
// **Validates: Requirements 4.5, 4.6, 5.2, 5.3**
func TestVulnerabilityDeltaProperty(t *testing.T) {
	ctx := context.Background()
	st := newFindingTestStore(t)

	// Fixed seed => deterministic, reproducible trials.
	rng := rand.New(rand.NewSource(0xDE17A))

	const (
		org    = int64(1)
		trials = 25
	)
	severities := []canonical.Severity{
		canonical.SevCritical, canonical.SevHigh, canonical.SevMedium,
		canonical.SevLow, canonical.SevInfo,
	}

	for trial := 0; trial < trials; trial++ {
		// A fresh emitter per trial keeps the captured call set scoped to this
		// trial's findings; a fresh findings Store wires the fake tap in via the
		// fluent setter exactly as production code does.
		fake := &recordingEmitter{}
		fstore := NewStore(st).WithMonitorEmitter(fake)

		// Clean slate so each trial's keys are brand-new (Created == true) on
		// their first upsert — otherwise a key surviving from a prior trial
		// would upsert as Created == false and emit nothing, confounding the
		// per-trial count.
		// CASCADE because finding_reports (migration 00022) carries an FK to
		// findings; both are empty here, so the cascade only clears findings.
		if _, err := st.Pool().Exec(ctx, `TRUNCATE findings RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("trial %d: truncate findings: %v", trial, err)
		}

		// Generate a set of distinct (asset_id, vuln_id) keys for this trial.
		keyCount := 1 + rng.Intn(6) // 1..6 distinct findings
		type keyPlan struct {
			f         Finding
			delivery  string
			reUpserts int // N >= 1 re-upserts of the same key
		}
		plans := make([]keyPlan, 0, keyCount)
		seen := map[string]bool{}
		for i := 0; len(plans) < keyCount; i++ {
			assetID := fmt.Sprintf("asset-t%d-%d", trial, rng.Intn(1000))
			vulnID := fmt.Sprintf("CVE-%d-%04d", 2024, rng.Intn(10000))
			delivery := assetID + ":" + vulnID
			if seen[delivery] {
				continue // enforce distinctness of keys within the trial
			}
			seen[delivery] = true
			plans = append(plans, keyPlan{
				f: Finding{
					AssetID:   assetID,
					AssetKind: model.Kind("Package"),
					VulnID:    vulnID,
					IntelRef:  "intel-" + vulnID,
					RiskScore: float64(rng.Intn(101)),
					Severity:  severities[rng.Intn(len(severities))],
				},
				delivery:  delivery,
				reUpserts: 1 + rng.Intn(4), // 1..4 re-upserts after the first
			})
		}

		// First upsert of each key: a nothing->open transition. Created must be
		// true and the tap must fire exactly one event for that key.
		for _, p := range plans {
			res, err := fstore.UpsertFinding(ctx, org, p.f)
			if err != nil {
				t.Fatalf("trial %d: first upsert %s: %v", trial, p.delivery, err)
			}
			if !res.Created {
				t.Fatalf("trial %d: first upsert %s returned Created=false, want true (nothing->open)",
					trial, p.delivery)
			}
		}

		// Re-upsert each key N>=1 more times. Every re-upsert is a still-open
		// finding re-seen (Created == false) and must emit ZERO additional
		// events — this is the re-correlation sweep's no-re-trigger guarantee
		// (Req 4.6, 5.2, 5.3). Interleaving does not matter; vary the order.
		rng.Shuffle(len(plans), func(i, j int) { plans[i], plans[j] = plans[j], plans[i] })
		for _, p := range plans {
			for n := 0; n < p.reUpserts; n++ {
				res, err := fstore.UpsertFinding(ctx, org, p.f)
				if err != nil {
					t.Fatalf("trial %d: re-upsert %s (#%d): %v", trial, p.delivery, n+1, err)
				}
				if res.Created {
					t.Fatalf("trial %d: re-upsert %s (#%d) returned Created=true, want false",
						trial, p.delivery, n+1)
				}
			}
		}

		// Assertions on the captured emits.
		calls := fake.snapshot()

		// Total emits must equal the number of distinct keys: exactly one per
		// nothing->open transition, independent of the re-upsert counts.
		if len(calls) != len(plans) {
			t.Fatalf("trial %d: captured %d emits, want %d (exactly one per distinct key, re-upserts emit nothing)",
				trial, len(calls), len(plans))
		}

		// Per-delivery_id emit count must be exactly 1, and every emit must
		// carry source "correlate", kind "vulnerability", the caller's org, and
		// the delivery_id = asset_id + ":" + vuln_id the tap stamps (Req 4.5).
		countByDelivery := map[string]int{}
		for _, c := range calls {
			if c.source != "correlate" {
				t.Fatalf("trial %d: emit %q source = %q, want \"correlate\"", trial, c.deliveryID, c.source)
			}
			if c.kind != "vulnerability" {
				t.Fatalf("trial %d: emit %q kind = %q, want \"vulnerability\"", trial, c.deliveryID, c.kind)
			}
			if c.org != org {
				t.Fatalf("trial %d: emit %q org = %d, want %d (tenant from caller)", trial, c.deliveryID, c.org, org)
			}
			countByDelivery[c.deliveryID]++
		}
		for _, p := range plans {
			got := countByDelivery[p.delivery]
			if got != 1 {
				t.Fatalf("trial %d: delivery_id %q emitted %d times, want exactly 1 (re-upserts=%d)",
					trial, p.delivery, got, p.reUpserts)
			}
		}
		// No stray delivery_ids beyond the generated key set.
		if len(countByDelivery) != len(plans) {
			t.Fatalf("trial %d: emitted %d distinct delivery_ids, want %d",
				trial, len(countByDelivery), len(plans))
		}
	}
}
