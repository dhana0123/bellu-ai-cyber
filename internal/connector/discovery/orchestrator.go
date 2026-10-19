package discovery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// ErrSessionRunning is returned by DiscoverAsync when a discovery session is
// already running for the connection, so a caller can map it to a conflict.
var ErrSessionRunning = errors.New("discovery: a session is already running for this connection")

// SourceFactory builds the collection source from a config map. In production
// this is the real cloudquery.Factory; tests inject a fake.
type SourceFactory func(cfg map[string]any) (source.Source, error)

// PipelineRunner runs the existing Bellu pipeline (ingest -> normalize ->
// resolve -> persist -> sync -> risk) over a source under a run id. In
// production this wraps the `bellu run` wiring; tests inject a fake. It must
// honor context cancellation so Cancel stops collection promptly.
type PipelineRunner interface {
	// Run drives the source through the pipeline under runID, reporting progress
	// via reporter. orgID is the tenant the discovered inventory belongs to (the
	// temporal change engine scopes observed_events by it). projectID tags
	// discovered resources with the connection's project for scoped inventory
	// (0 means untagged / all-projects).
	Run(ctx context.Context, src source.Source, runID string, orgID, projectID int64, reporter source.ProgressReporter) error
}

// CredentialSink receives the session-only credential environment and returns a
// cleanup func. Production wiring exports the AWS_* values to the Steampipe
// process for the session; the cleanup clears them. A nil sink is a no-op.
type CredentialSink func(env map[string]string) (cleanup func(), err error)

// Credentialer obtains credentials for a connection (through the session cache)
// and revokes them. The orchestrator depends on this narrow surface rather than
// the whole session manager.
type Credentialer interface {
	Get(ctx context.Context, t provider.Target) (*provider.Credentials, error)
	Revoke(connID int64)
}

// Orchestrator creates and tracks discovery sessions, driving the existing
// source and pipeline.
type Orchestrator struct {
	mu        sync.Mutex
	latest    map[int64]*Session
	hubs      map[int64]*progressHub
	factory   SourceFactory
	runner    PipelineRunner
	creds     Credentialer
	sink      CredentialSink
	newRunID  func() string
	newSessID func() string
	now       func() time.Time
}

// Options configures an Orchestrator.
type Options struct {
	Factory    SourceFactory
	Runner     PipelineRunner
	Creds      Credentialer
	Sink       CredentialSink
	NewRunID   func() string
	NewSession func() string
	Now        func() time.Time
}

// New builds an Orchestrator. Required: Factory, Runner, Creds. The id/clock
// hooks default to time-based generators.
func New(opts Options) *Orchestrator {
	o := &Orchestrator{
		latest:    map[int64]*Session{},
		hubs:      map[int64]*progressHub{},
		factory:   opts.Factory,
		runner:    opts.Runner,
		creds:     opts.Creds,
		sink:      opts.Sink,
		newRunID:  opts.NewRunID,
		newSessID: opts.NewSession,
		now:       opts.Now,
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.newRunID == nil {
		o.newRunID = func() string { return fmt.Sprintf("run-%d", time.Now().UnixNano()) }
	}
	if o.newSessID == nil {
		o.newSessID = func() string { return fmt.Sprintf("disc-%d", time.Now().UnixNano()) }
	}
	return o
}

// Verify authenticates and checks permissions for a connection without running
// a discovery session.
func (o *Orchestrator) Verify(ctx context.Context, p provider.CloudProvider, t provider.Target) (*provider.VerifyResult, error) {
	return p.VerifyConnection(ctx, t)
}

// Discover starts a new discovery session: it obtains session-only credentials,
// asks the provider for a discovery plan, builds the existing source, and runs
// it through the existing pipeline under a cancellable context. On completion
// (success, failure, or cancellation) the session credentials are revoked. The
// returned Session reflects the terminal state.
func (o *Orchestrator) Discover(ctx context.Context, p provider.CloudProvider, t provider.Target) (*Session, error) {
	sess, hub := o.newSession(t)
	return o.runSession(ctx, sess, hub, p, t)
}

// DiscoverAsync starts a discovery session and returns the running Session
// immediately, driving the run in the background. The run is rooted on a
// detached, cancellable context (so it outlives the HTTP request that started
// it) and honors Cancel. When the run reaches a terminal state, onDone is
// invoked with the terminal session and run error so the caller can record the
// outcome and transition connection status. onDone may be nil.
//
// ErrSessionRunning is returned (and no session is started) if a session is
// already running for the connection.
func (o *Orchestrator) DiscoverAsync(p provider.CloudProvider, t provider.Target, onDone func(*Session, error)) (*Session, error) {
	o.mu.Lock()
	if cur, ok := o.latest[t.ConnectionID]; ok && cur.Status == SessRunning {
		o.mu.Unlock()
		return nil, ErrSessionRunning
	}
	o.mu.Unlock()

	sess, hub := o.newSession(t)
	// Snapshot the running session to return to the caller. The live sess
	// pointer is mutated by the background goroutine (finish/progress), so
	// returning it directly would race the caller reading Status; return a copy.
	o.mu.Lock()
	snap := *sess
	snap.cancel = nil
	snap.Progress = snap.Progress.Clone()
	o.mu.Unlock()

	go func() {
		s, err := o.runSession(context.Background(), sess, hub, p, t)
		if onDone != nil {
			onDone(s, err)
		}
	}()
	return &snap, nil
}

// newSession builds a running Session + progress hub and registers both as the
// latest for the connection. It is the shared setup for the sync and async
// discovery entry points.
func (o *Orchestrator) newSession(t provider.Target) (*Session, *progressHub) {
	hub := newProgressHub()
	sess := &Session{
		ID:           o.newSessID(),
		ConnectionID: t.ConnectionID,
		RunID:        o.newRunID(),
		Status:       SessRunning,
		StartedAt:    o.now(),
		Progress:     hub.prog,
	}
	o.setLatest(sess)
	o.setHub(sess.ConnectionID, hub)
	return sess, hub
}

// runSession executes the discovery pipeline for an already-created session and
// returns its terminal state. It closes the progress hub on exit so subscribed
// SSE streams terminate on every return path.
func (o *Orchestrator) runSession(ctx context.Context, sess *Session, hub *progressHub, p provider.CloudProvider, t provider.Target) (*Session, error) {
	defer func() {
		// Freeze a stable, deep-copied final progress snapshot onto the session
		// before closing the hub, so a completed session still carries its final
		// tallies after the live hub is gone. Guarded by the orchestrator lock
		// since the session is shared with status readers.
		snap := hub.snapshot()
		o.mu.Lock()
		sess.Progress = snap
		o.mu.Unlock()
		o.closeHub(sess.ConnectionID)
	}()

	creds, err := o.creds.Get(ctx, t)
	if err != nil {
		return o.finish(sess, err), err
	}

	plan, err := p.Discover(ctx, t, creds)
	if err != nil {
		return o.finish(sess, err), err
	}

	// Supply the session-only credential environment to the collector.
	var cleanup func()
	if o.sink != nil {
		cleanup, err = o.sink(plan.CredentialEnv)
		if err != nil {
			return o.finish(sess, err), err
		}
	}
	if cleanup != nil {
		defer cleanup()
	}

	src, err := o.factory(plan.SteampipeConfig)
	if err != nil {
		return o.finish(sess, err), err
	}

	runCtx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	sess.cancel = cancel
	o.mu.Unlock()
	defer cancel()

	runErr := o.runner.Run(runCtx, src, sess.RunID, t.OrgID, t.ProjectID, hub)

	if runErr != nil {
		if runCtx.Err() == context.Canceled {
			return o.finishCancelled(sess), nil
		}
		return o.finish(sess, runErr), runErr
	}
	return o.finish(sess, nil), nil
}

// Cancel stops a running session for the connection promptly by cancelling the
// context passed to Source.Collect, and marks the session cancelled.
func (o *Orchestrator) Cancel(connID int64) error {
	o.mu.Lock()
	sess, ok := o.latest[connID]
	if !ok || sess.Status != SessRunning {
		o.mu.Unlock()
		return fmt.Errorf("discovery: no running session for connection %d", connID)
	}
	cancel := sess.cancel
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Latest returns the most-recent session for a connection.
func (o *Orchestrator) Latest(connID int64) (*Session, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.latest[connID]
	return s, ok
}

// LatestView returns a race-free deep snapshot of the latest session for a
// connection: a copy of the session value with a fresh progress snapshot (live
// from the hub if the run is in flight, else the frozen final snapshot stored on
// the session). All reads of the shared session happen under the orchestrator
// lock. ok=false when no session exists. Callers must use this (not Latest) when
// they will serialize or outlive the lock.
func (o *Orchestrator) LatestView(connID int64) (*Session, bool) {
	o.mu.Lock()
	sess, ok := o.latest[connID]
	if !ok {
		o.mu.Unlock()
		return nil, false
	}
	cp := *sess
	hub := o.hubs[connID]
	o.mu.Unlock()

	if hub != nil {
		cp.Progress = hub.snapshot()
	} else {
		cp.Progress = cp.Progress.Clone()
	}
	cp.cancel = nil
	return &cp, true
}

func (o *Orchestrator) setLatest(s *Session) {
	o.mu.Lock()
	o.latest[s.ConnectionID] = s
	o.mu.Unlock()
}

func (o *Orchestrator) setHub(connID int64, h *progressHub) {
	o.mu.Lock()
	o.hubs[connID] = h
	o.mu.Unlock()
}

// closeHub closes and removes the hub for a connection so subscribed streams
// terminate and the map does not leak hubs across runs.
func (o *Orchestrator) closeHub(connID int64) {
	o.mu.Lock()
	h, ok := o.hubs[connID]
	if ok {
		delete(o.hubs, connID)
	}
	o.mu.Unlock()
	if ok {
		h.close()
	}
}

// Subscribe registers a progress subscriber for the latest session of a
// connection. It returns a channel that receives Progress snapshots (the first
// is the current snapshot, delivered immediately), a cancel func to unsubscribe,
// and ok=false when no live session exists for the connection. The channel is
// closed when the session reaches a terminal state. Subscribing never affects
// the running session.
func (o *Orchestrator) Subscribe(connID int64) (<-chan *Progress, func(), bool) {
	o.mu.Lock()
	h, ok := o.hubs[connID]
	o.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	ch, cancel := h.subscribe()
	return ch, cancel, true
}

// ProgressSnapshot returns a deep copy of the current progress for a
// connection's latest session, or nil when none exists. Used by the status
// poll fallback.
func (o *Orchestrator) ProgressSnapshot(connID int64) *Progress {
	o.mu.Lock()
	h, ok := o.hubs[connID]
	o.mu.Unlock()
	if !ok {
		return nil
	}
	return h.snapshot()
}

// finish records a terminal state (complete or failed), revokes the session
// credentials, and returns the session.
func (o *Orchestrator) finish(sess *Session, err error) *Session {
	o.mu.Lock()
	end := o.now()
	sess.EndedAt = &end
	if err != nil {
		sess.Status = SessFailed
		sess.Error = err.Error()
	} else {
		sess.Status = SessComplete
	}
	o.mu.Unlock()
	o.creds.Revoke(sess.ConnectionID)
	return sess
}

// finishCancelled records a cancelled terminal state and revokes credentials.
func (o *Orchestrator) finishCancelled(sess *Session) *Session {
	o.mu.Lock()
	end := o.now()
	sess.EndedAt = &end
	sess.Status = SessCancelled
	o.mu.Unlock()
	o.creds.Revoke(sess.ConnectionID)
	return sess
}
