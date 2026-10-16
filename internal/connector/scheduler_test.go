package connector

import (
	"context"
	"errors"
	"testing"
	"time"
)

type schedRig struct {
	now     time.Time
	due     []ScheduleTarget
	dueErr  error
	cutoffs []time.Time
	runs    []int64
	runErr  map[int64]error
	logs    int
}

func (r *schedRig) scheduler(t *testing.T, o SchedulerOptions) *Scheduler {
	t.Helper()
	o.Due = func(_ context.Context, before time.Time) ([]ScheduleTarget, error) {
		r.cutoffs = append(r.cutoffs, before)
		return r.due, r.dueErr
	}
	o.Run = func(_ context.Context, _ int64, id int64) error {
		r.runs = append(r.runs, id)
		return r.runErr[id]
	}
	o.Now = func() time.Time { return r.now }
	o.Logf = func(string, ...any) { r.logs++ }
	s, err := NewScheduler(o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newRig() *schedRig {
	return &schedRig{now: time.Unix(1_700_000_000, 0).UTC(), runErr: map[int64]error{}}
}

func TestNewSchedulerRequiresCallbacks(t *testing.T) {
	if _, err := NewScheduler(SchedulerOptions{}); err == nil {
		t.Fatal("expected error without Due/Run")
	}
}

func TestSchedulerClampsIntervalAndDefaults(t *testing.T) {
	r := newRig()
	s := r.scheduler(t, SchedulerOptions{Interval: time.Minute})
	if s.Interval() != MinScheduleInterval {
		t.Fatalf("interval = %s, want clamp to %s", s.Interval(), MinScheduleInterval)
	}
	if s.maxPerTick != 2 {
		t.Fatalf("maxPerTick = %d, want default 2", s.maxPerTick)
	}
	if s.tick > s.interval {
		t.Fatalf("tick %s must not exceed interval %s", s.tick, s.interval)
	}
}

// The cutoff handed to Due is now minus the interval, so only connections not
// scanned within the interval are returned.
func TestSchedulerUsesIntervalCutoff(t *testing.T) {
	r := newRig()
	s := r.scheduler(t, SchedulerOptions{Interval: 6 * time.Hour})
	s.RunOnce(context.Background())
	want := r.now.Add(-6 * time.Hour)
	if len(r.cutoffs) != 1 || !r.cutoffs[0].Equal(want) {
		t.Fatalf("cutoff = %v, want %v", r.cutoffs, want)
	}
}

// A backlog is staggered: at most MaxPerTick scans start per pass.
func TestSchedulerCapsStartsPerTick(t *testing.T) {
	r := newRig()
	r.due = []ScheduleTarget{{1, 1}, {1, 2}, {1, 3}, {1, 4}}
	s := r.scheduler(t, SchedulerOptions{Interval: time.Hour, MaxPerTick: 2})
	if n := s.RunOnce(context.Background()); n != 2 {
		t.Fatalf("started %d, want 2", n)
	}
	if len(r.runs) != 2 || r.runs[0] != 1 || r.runs[1] != 2 {
		t.Fatalf("ran %v, want [1 2] (oldest-first order preserved)", r.runs)
	}
}

// A connection that is already running (ErrConflict) is skipped without being
// counted as started or penalised.
func TestSchedulerConflictIsNotAFailure(t *testing.T) {
	r := newRig()
	r.due = []ScheduleTarget{{1, 1}, {1, 2}}
	r.runErr[1] = ErrConflict
	s := r.scheduler(t, SchedulerOptions{Interval: time.Hour, MaxPerTick: 5})
	if n := s.RunOnce(context.Background()); n != 1 {
		t.Fatalf("started %d, want 1 (conn 2 only)", n)
	}
	if _, penalised := s.failedAt[1]; penalised {
		t.Fatal("conflict must not trigger failure backoff")
	}
}

// A connection whose scan cannot start is backed off for a full interval so it
// neither spams the log nor starves the others; it is retried afterwards.
func TestSchedulerBacksOffFailedStarts(t *testing.T) {
	r := newRig()
	r.due = []ScheduleTarget{{1, 1}, {1, 2}}
	r.runErr[1] = errors.New("invalid state")
	s := r.scheduler(t, SchedulerOptions{Interval: time.Hour, MaxPerTick: 5})

	if n := s.RunOnce(context.Background()); n != 1 {
		t.Fatalf("first pass started %d, want 1", n)
	}
	r.runs = nil
	s.RunOnce(context.Background())
	for _, id := range r.runs {
		if id == 1 {
			t.Fatal("failed connection retried before backoff elapsed")
		}
	}

	r.now = r.now.Add(time.Hour + time.Second)
	r.runs = nil
	s.RunOnce(context.Background())
	retried := false
	for _, id := range r.runs {
		retried = retried || id == 1
	}
	if !retried {
		t.Fatal("failed connection should be retried after a full interval")
	}
}

func TestSchedulerSurvivesDueError(t *testing.T) {
	r := newRig()
	r.dueErr = errors.New("db down")
	s := r.scheduler(t, SchedulerOptions{Interval: time.Hour})
	if n := s.RunOnce(context.Background()); n != 0 {
		t.Fatalf("started %d on error, want 0", n)
	}
}

func TestSchedulerRecoversFromPanic(t *testing.T) {
	r := newRig()
	r.due = []ScheduleTarget{{1, 1}}
	s, err := NewScheduler(SchedulerOptions{
		Interval: time.Hour,
		Due: func(context.Context, time.Time) ([]ScheduleTarget, error) {
			return r.due, nil
		},
		Run:  func(context.Context, int64, int64) error { panic("boom") },
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.RunOnce(context.Background()) // must not panic
}

func TestSchedulerRunStopsOnCancel(t *testing.T) {
	r := newRig()
	s := r.scheduler(t, SchedulerOptions{Interval: time.Hour, Tick: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
