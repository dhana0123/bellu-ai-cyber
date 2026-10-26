package monitor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/intelcorrelate"
)

// The sweep re-runs correlation per org on a timer and upserts the resulting
// finding matches. It deliberately does NOT emit any monitoring events itself:
// the vulnerability delta tap lives in the finding-upsert path and fires only
// on a nothing->open transition (UpsertResult.Created == true), so a sweep over
// still-open findings re-upserts with Created == false and emits nothing — no
// re-trigger storm (Requirements 5.1, 5.2, 5.3). The sweep's only job is to
// re-correlate + upsert and rely on that delta.

// Correlator is the read-only slice of the intel Matcher the sweep needs: it
// re-runs correlation for one org and returns the current (asset, vuln)
// candidate matches. The concrete intelcorrelate Matcher satisfies it via its
// Correlate method; tests can supply a fake without a live graph or intel store.
//
// A Correlator is bound to a single org (its graph + intel source are the org's)
// — mirroring how intelcorrelate.New(GraphReader, IntelSource) yields a matcher
// over one tenant's data. The CorrelatorFactory builds the per-org instance.
type Correlator interface {
	Correlate(ctx context.Context) ([]intelcorrelate.Candidate, error)
}

// CorrelatorFactory yields the Correlator for a given org. It exists so the
// sweep stays bound to one tenant per run (org is never read from a payload)
// and so the heavy intelcorrelate.New(graph, intel) wiring can be assembled by
// the caller that owns the graph handle and the intel store, keeping this
// package free of those import dependencies.
type CorrelatorFactory interface {
	Correlator(org int64) (Correlator, error)
}

// CorrelatorFactoryFunc adapts a plain function to CorrelatorFactory.
type CorrelatorFactoryFunc func(org int64) (Correlator, error)

// Correlator implements CorrelatorFactory.
func (f CorrelatorFactoryFunc) Correlator(org int64) (Correlator, error) { return f(org) }

// FindingUpserter is the slice of the finding store the sweep needs: the
// idempotent per-(asset,vuln) upsert whose UpsertResult.Created drives the
// vulnerability delta tap. The concrete *finding.Store satisfies it via its
// UpsertFinding method; tests can supply a fake without a live Postgres.
type FindingUpserter interface {
	UpsertFinding(ctx context.Context, org int64, f finding.Finding) (finding.UpsertResult, error)
}

// Sweeper periodically re-runs correlation per org and upserts the resulting
// finding matches, relying on the finding delta tap so unchanged still-open
// findings emit no events (Requirement 5). It holds only small interfaces so it
// stays testable and does not create import cycles.
type Sweeper struct {
	correlators CorrelatorFactory
	findings    FindingUpserter
	logf        func(format string, args ...any)
}

// NewSweeper builds a Sweeper from a per-org correlator factory and the finding
// upserter. Both are required.
func NewSweeper(correlators CorrelatorFactory, findings FindingUpserter) *Sweeper {
	return &Sweeper{
		correlators: correlators,
		findings:    findings,
		logf:        log.Printf,
	}
}

// RunSweep re-runs correlation for one org and upserts every resulting match.
//
// It builds the org's Correlator, runs Correlate, and UpsertFinding's each
// candidate. UpsertResult.Created drives the vulnerability delta tap in the
// upsert path: only a nothing->open transition emits a monitor event, so a
// sweep over an unchanged, still-open corpus re-upserts (Created == false) and
// emits nothing (Requirements 5.1, 5.2, 5.3). This method does NOT duplicate
// the delta/emit logic; it only re-correlates and upserts, and remains correct
// whether or not the delta tap is wired yet.
func (s *Sweeper) RunSweep(ctx context.Context, org int64) error {
	c, err := s.correlators.Correlator(org)
	if err != nil {
		return fmt.Errorf("monitor: sweep build correlator (org %d): %w", org, err)
	}
	matches, err := c.Correlate(ctx)
	if err != nil {
		return fmt.Errorf("monitor: sweep correlate (org %d): %w", org, err)
	}
	for _, m := range matches {
		if err := ctx.Err(); err != nil {
			return err
		}
		// UpsertResult.Created is what the delta tap reads; the sweep itself only
		// needs the write to land. A single bad row must not abort the whole
		// sweep, so per-candidate failures are logged and the sweep continues.
		if _, err := s.findings.UpsertFinding(ctx, org, candidateToFinding(m)); err != nil {
			s.logf("monitor: sweep upsert (org %d, %s/%s): %v", org, m.AssetID, m.VulnID, err)
		}
	}
	return nil
}

// candidateToFinding maps a correlation Candidate onto the finding row the
// sweep upserts. The sweep carries the match through unscored: risk score and
// severity are refreshed by the scoring stage, and the upsert preserves
// first_seen while advancing last_seen, so a re-swept still-open finding is
// idempotent apart from last_seen (and therefore Created == false).
func candidateToFinding(c intelcorrelate.Candidate) finding.Finding {
	intelRef := c.IntelRef
	if intelRef == "" {
		intelRef = c.VulnID
	}
	return finding.Finding{
		AssetID:   c.AssetID,
		AssetKind: c.AssetKind,
		VulnID:    c.VulnID,
		IntelRef:  intelRef,
		Status:    finding.StatusOpen,
	}
}

// RunSweepAllOrgs runs RunSweep for every org the factory can supply, returning
// the number of orgs swept. A per-org failure is logged and does not abort the
// others, so one broken tenant never starves the rest. orgs is the set of org
// ids to sweep this tick (resolved by the caller from the tenant catalog).
func (s *Sweeper) RunSweepAllOrgs(ctx context.Context, orgs []int64) int {
	swept := 0
	for _, org := range orgs {
		if err := ctx.Err(); err != nil {
			return swept
		}
		if err := s.RunSweep(ctx, org); err != nil {
			s.logf("monitor: sweep org %d failed: %v", org, err)
			continue
		}
		swept++
	}
	return swept
}

// SweepScheduler drives RunSweepAllOrgs on an interval until ctx is cancelled.
// It mirrors the connector discovery scheduler convention: an interval-driven
// goroutine with ctx cancellation, off by default and configurable via env.
type SweepScheduler struct {
	sweeper  *Sweeper
	interval time.Duration
	orgs     func(ctx context.Context) ([]int64, error)
	logf     func(format string, args ...any)
}

// MinSweepInterval is the shortest re-correlation interval the scheduler
// accepts. Correlation re-reads the whole graph per org; sweeping more often
// than this costs graph/intel load for no gain because findings change slowly.
const MinSweepInterval = 5 * time.Minute

// SweepSchedulerOptions configures a SweepScheduler.
type SweepSchedulerOptions struct {
	// Sweeper performs the per-org re-correlation + upsert.
	Sweeper *Sweeper
	// Interval is how often the full sweep runs. Clamped up to MinSweepInterval.
	Interval time.Duration
	// Orgs resolves the set of org ids to sweep each tick.
	Orgs func(ctx context.Context) ([]int64, error)
	// Logf is injectable for tests; defaults to log.Printf.
	Logf func(format string, args ...any)
}

// NewSweepScheduler validates options and applies defaults.
func NewSweepScheduler(o SweepSchedulerOptions) (*SweepScheduler, error) {
	if o.Sweeper == nil || o.Orgs == nil {
		return nil, fmt.Errorf("monitor: sweep scheduler requires Sweeper and Orgs")
	}
	s := &SweepScheduler{
		sweeper:  o.Sweeper,
		interval: o.Interval,
		orgs:     o.Orgs,
		logf:     o.Logf,
	}
	if s.interval < MinSweepInterval {
		s.interval = MinSweepInterval
	}
	if s.logf == nil {
		s.logf = log.Printf
	}
	return s, nil
}

// Interval reports the effective (clamped) sweep interval.
func (s *SweepScheduler) Interval() time.Duration { return s.interval }

// Run blocks, sweeping every org on each tick, until ctx is cancelled.
func (s *SweepScheduler) Run(ctx context.Context) {
	s.logf("monitor sweep scheduler: re-correlating every org every %s", s.interval)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce resolves the org set and sweeps each one. A panic in one pass is
// recovered so the scheduler keeps running for the life of the process.
func (s *SweepScheduler) runOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("monitor sweep scheduler: recovered from panic: %v", r)
		}
	}()
	orgs, err := s.orgs(ctx)
	if err != nil {
		s.logf("monitor sweep scheduler: list orgs: %v", err)
		return
	}
	swept := s.sweeper.RunSweepAllOrgs(ctx, orgs)
	s.logf("monitor sweep scheduler: swept %d/%d org(s)", swept, len(orgs))
}
