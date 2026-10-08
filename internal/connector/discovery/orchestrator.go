package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// SourceFactory builds the existing collection source from a config map. In
// production this is the real steampipe.Factory; tests inject a fake.
type SourceFactory func(cfg map[string]any) (source.Source, error)

// PipelineRunner runs the existing Bellu pipeline (ingest -> normalize ->
// resolve -> persist -> sync -> risk) over a source under a run id. In
// production this wraps the `bellu run` wiring; tests inject a fake. It must
// honor context cancellation so Cancel stops collection promptly.
type PipelineRunner interface {
	Run(ctx context.Context, src source.Source, runID string) error
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
	sess := &Session{
		ID:           o.newSessID(),
		ConnectionID: t.ConnectionID,
		RunID:        o.newRunID(),
		Status:       SessRunning,
		StartedAt:    o.now(),
	}
	o.setLatest(sess)

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

	runErr := o.runner.Run(runCtx, src, sess.RunID)

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

func (o *Orchestrator) setLatest(s *Session) {
	o.mu.Lock()
	o.latest[s.ConnectionID] = s
	o.mu.Unlock()
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
