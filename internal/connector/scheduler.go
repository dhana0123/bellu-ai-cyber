package connector

import (
	"context"
	"errors"
	"log"
	"time"
)

// MinScheduleInterval is the shortest re-scan interval the scheduler accepts.
// Discovery calls many AWS APIs per account; scanning more often than this
// costs API quota and Steampipe load for no gain, because Inspector findings
// and inventory change slowly.
const MinScheduleInterval = 15 * time.Minute

// ScheduleTarget identifies a tenant-scoped connection due for a re-scan.
type ScheduleTarget struct {
	OrgID int64
	ID    int64
}

// SchedulerOptions configures a Scheduler.
type SchedulerOptions struct {
	// Interval is how long a connection may go without a scan before it is due
	// again. Clamped up to MinScheduleInterval.
	Interval time.Duration
	// Tick is how often the scheduler looks for due connections. Defaults to one
	// minute (or the interval, if shorter).
	Tick time.Duration
	// MaxPerTick caps how many scans are started per tick, which staggers a
	// large backlog instead of launching every account at once. Defaults to 2.
	MaxPerTick int
	// Due returns connections with no scan started after startedBefore.
	Due func(ctx context.Context, startedBefore time.Time) ([]ScheduleTarget, error)
	// Run starts an (asynchronous) discovery for one connection.
	Run func(ctx context.Context, orgID, id int64) error
	// Now and Logf are injectable for tests.
	Now  func() time.Time
	Logf func(format string, args ...any)
}

// Scheduler periodically re-runs discovery for every idle connection, so the
// inventory, network/IAM graph and Inspector vulnerability findings stay
// current without a person pressing "discover". Each run goes through the same
// Manager.Discover path as a manual run (status machine, credential minting,
// progress, the normalize/resolve/sync pipeline), so scheduled and manual scans
// behave identically.
//
// It is designed for a single bellu-api replica: Manager.Discover guards
// against a second concurrent run of the same connection via its status, but
// that check is not an atomic compare-and-set across processes.
type Scheduler struct {
	interval   time.Duration
	tick       time.Duration
	maxPerTick int
	due        func(context.Context, time.Time) ([]ScheduleTarget, error)
	run        func(context.Context, int64, int64) error
	now        func() time.Time
	logf       func(string, ...any)

	// failedAt backs off connections whose scan could not even be started
	// (e.g. invalid state), so one broken connection neither spams the log every
	// tick nor starves the others.
	failedAt map[int64]time.Time
}

// NewScheduler validates options and applies defaults.
func NewScheduler(o SchedulerOptions) (*Scheduler, error) {
	if o.Due == nil || o.Run == nil {
		return nil, errors.New("connector: scheduler requires Due and Run")
	}
	s := &Scheduler{
		interval:   o.Interval,
		tick:       o.Tick,
		maxPerTick: o.MaxPerTick,
		due:        o.Due,
		run:        o.Run,
		now:        o.Now,
		logf:       o.Logf,
		failedAt:   map[int64]time.Time{},
	}
	if s.interval < MinScheduleInterval {
		s.interval = MinScheduleInterval
	}
	if s.tick <= 0 {
		s.tick = time.Minute
	}
	if s.tick > s.interval {
		s.tick = s.interval
	}
	if s.maxPerTick <= 0 {
		s.maxPerTick = 2
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logf == nil {
		s.logf = log.Printf
	}
	return s, nil
}

// Interval reports the effective (clamped) re-scan interval.
func (s *Scheduler) Interval() time.Duration { return s.interval }

// Run blocks, scanning due connections every tick, until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.logf("discovery scheduler: re-scanning idle connections every %s (checked every %s, max %d per check)",
		s.interval, s.tick, s.maxPerTick)
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce starts scans for due connections (up to MaxPerTick) and returns how
// many it started. A panic in one pass is recovered so the scheduler keeps
// running for the life of the process.
func (s *Scheduler) RunOnce(ctx context.Context) (started int) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("discovery scheduler: recovered from panic: %v", r)
		}
	}()

	now := s.now()
	targets, err := s.due(ctx, now.Add(-s.interval))
	if err != nil {
		s.logf("discovery scheduler: list due connections: %v", err)
		return 0
	}
	for _, t := range targets {
		if started >= s.maxPerTick || ctx.Err() != nil {
			break
		}
		if at, bad := s.failedAt[t.ID]; bad && now.Sub(at) < s.interval {
			continue // recently failed to start; retry after a full interval
		}
		err := s.run(ctx, t.OrgID, t.ID)
		switch {
		case err == nil:
			delete(s.failedAt, t.ID)
			started++
			s.logf("discovery scheduler: started scan for connection %d", t.ID)
		case errors.Is(err, ErrConflict):
			// Already running (e.g. started manually a moment ago): fine.
		default:
			s.failedAt[t.ID] = now
			s.logf("discovery scheduler: could not start scan for connection %d: %v", t.ID, err)
		}
	}
	return started
}
