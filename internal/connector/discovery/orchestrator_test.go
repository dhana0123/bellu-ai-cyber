package discovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// fakeSource is a source.Source whose Collect blocks until the context is
// cancelled (for cancellation tests) or returns immediately.
type fakeSource struct {
	block bool
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if f.block {
			<-ctx.Done()
			errs <- ctx.Err()
		}
	}()
	return events, errs
}

// fakeProvider implements provider.CloudProvider for orchestrator tests.
type fakeProvider struct {
	verifyRes *provider.VerifyResult
	plan      *provider.DiscoveryPlan
	discErr   error
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) CreateSetup(context.Context, provider.SetupInput) (*provider.Setup, error) {
	return &provider.Setup{}, nil
}
func (p *fakeProvider) VerifyConnection(context.Context, provider.Target) (*provider.VerifyResult, error) {
	return p.verifyRes, nil
}
func (p *fakeProvider) Authenticate(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *fakeProvider) GetCredentials(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *fakeProvider) CheckPermissions(context.Context, provider.Target, *provider.Credentials, []string) (*provider.VerifyResult, error) {
	return p.verifyRes, nil
}
func (p *fakeProvider) Discover(context.Context, provider.Target, *provider.Credentials) (*provider.DiscoveryPlan, error) {
	if p.discErr != nil {
		return nil, p.discErr
	}
	if p.plan != nil {
		return p.plan, nil
	}
	return &provider.DiscoveryPlan{SteampipeConfig: map[string]any{}, CredentialEnv: map[string]string{}}, nil
}

// fakeCreds tracks Get/Revoke calls.
type fakeCreds struct {
	mu      sync.Mutex
	gets    int
	revokes int
}

func (f *fakeCreds) Get(context.Context, provider.Target) (*provider.Credentials, error) {
	f.mu.Lock()
	f.gets++
	f.mu.Unlock()
	return &provider.Credentials{AccessKeyID: "AK"}, nil
}
func (f *fakeCreds) Revoke(int64) {
	f.mu.Lock()
	f.revokes++
	f.mu.Unlock()
}

// fakeRunner runs a provided function, honoring context.
type fakeRunner struct {
	run func(ctx context.Context, src source.Source, runID string) error
}

func (r *fakeRunner) Run(ctx context.Context, src source.Source, runID string, _ int64, _ source.ProgressReporter) error {
	return r.run(ctx, src, runID)
}

func newOrch(runner PipelineRunner, creds Credentialer) *Orchestrator {
	return New(Options{
		Factory: func(cfg map[string]any) (source.Source, error) { return &fakeSource{}, nil },
		Runner:  runner,
		Creds:   creds,
	})
}

func target() provider.Target {
	return provider.Target{ConnectionID: 1, Capabilities: []string{"core"}}
}

// TestDiscoverSuccess asserts a session is created running, succeeds, and
// credentials are obtained once and revoked at end.
func TestDiscoverSuccess(t *testing.T) {
	creds := &fakeCreds{}
	runner := &fakeRunner{run: func(ctx context.Context, src source.Source, runID string) error { return nil }}
	o := newOrch(runner, creds)

	sess, err := o.Discover(context.Background(), &fakeProvider{}, target())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if sess.Status != SessComplete {
		t.Errorf("status = %s, want complete", sess.Status)
	}
	if sess.ID == "" || sess.RunID == "" {
		t.Error("session should have a unique id and run id")
	}
	if sess.EndedAt == nil {
		t.Error("completed session should have ended_at")
	}
	if creds.gets != 1 || creds.revokes != 1 {
		t.Errorf("creds gets=%d revokes=%d, want 1/1", creds.gets, creds.revokes)
	}
	if latest, ok := o.Latest(1); !ok || latest.Status != SessComplete {
		t.Error("Latest should return the completed session")
	}
}

// TestDiscoverFailure asserts a pipeline error marks the session failed and
// still revokes credentials.
func TestDiscoverFailure(t *testing.T) {
	creds := &fakeCreds{}
	runner := &fakeRunner{run: func(ctx context.Context, src source.Source, runID string) error {
		return errors.New("pipeline boom")
	}}
	o := newOrch(runner, creds)

	sess, err := o.Discover(context.Background(), &fakeProvider{}, target())
	if err == nil {
		t.Fatal("want error from failed pipeline")
	}
	if sess.Status != SessFailed || sess.Error == "" {
		t.Errorf("status=%s error=%q, want failed with message", sess.Status, sess.Error)
	}
	if creds.revokes != 1 {
		t.Errorf("revokes=%d, want 1", creds.revokes)
	}
}

// TestCancelStopsCollection asserts Cancel cancels the context passed to the
// runner (and thus Source.Collect) and the session is marked cancelled.
func TestCancelStopsCollection(t *testing.T) {
	creds := &fakeCreds{}
	started := make(chan struct{})
	runner := &fakeRunner{run: func(ctx context.Context, src source.Source, runID string) error {
		close(started)
		<-ctx.Done() // block until cancelled
		return ctx.Err()
	}}
	o := New(Options{
		Factory: func(cfg map[string]any) (source.Source, error) { return &fakeSource{block: true}, nil },
		Runner:  runner,
		Creds:   creds,
	})

	done := make(chan *Session, 1)
	go func() {
		s, _ := o.Discover(context.Background(), &fakeProvider{}, target())
		done <- s
	}()

	<-started
	if err := o.Cancel(1); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case s := <-done:
		if s.Status != SessCancelled {
			t.Errorf("status = %s, want cancelled", s.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("discover did not return after cancel")
	}
	if creds.revokes != 1 {
		t.Errorf("revokes=%d, want 1", creds.revokes)
	}
}

// TestCancelNoRunningSession asserts cancel errors when nothing is running.
func TestCancelNoRunningSession(t *testing.T) {
	o := newOrch(&fakeRunner{run: func(context.Context, source.Source, string) error { return nil }}, &fakeCreds{})
	if err := o.Cancel(99); err == nil {
		t.Error("Cancel with no running session should error")
	}
}

// TestVerifyDelegates asserts Verify returns the provider's result.
func TestVerifyDelegates(t *testing.T) {
	o := newOrch(&fakeRunner{run: func(context.Context, source.Source, string) error { return nil }}, &fakeCreds{})
	want := &provider.VerifyResult{OK: true}
	got, err := o.Verify(context.Background(), &fakeProvider{verifyRes: want}, target())
	if err != nil || !got.OK {
		t.Errorf("verify = %+v err=%v, want OK", got, err)
	}
}
