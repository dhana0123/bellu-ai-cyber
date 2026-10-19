package cloudquery

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// --- Fake JobRunner --------------------------------------------------------
//
// A fakeRunner stands in for the real cliRunner so Source.Collect's sync step
// can be driven to each failure/cancel outcome without a CloudQuery binary. It
// records the Spec and env it was handed so a test can assert the credential
// contract (the secret value reaches the runner's env and nowhere else).

type fakeRunner struct {
	// err is returned from Run; nil means the sync "succeeded".
	err error
	// gotSpec / gotEnv capture what Collect handed the runner.
	gotSpec   Spec
	gotEnv    map[string]string
	gotCalled bool
}

func (f *fakeRunner) Run(_ context.Context, spec Spec, env map[string]string, rep source.ProgressReporter) error {
	f.gotCalled = true
	f.gotSpec = spec
	// Copy the env: runSync clears the caller's map after Run returns, so a
	// snapshot is needed to assert on it afterward.
	f.gotEnv = map[string]string{}
	for k, v := range env {
		f.gotEnv[k] = v
	}
	return f.err
}

// --- Non-zero exit: job failure, zero RawEvents ----------------------------

// TestCollectSyncNonZeroExitEmitsNoEvents asserts that when the wired runner
// returns a non-zero-exit job failure, Source.Collect surfaces the failure on
// the error channel and emits zero RawEvents — the aborted sync never streams
// partial inventory downstream (Requirements 1.6, 1.7). The staging read is
// never reached, so the fake querier records no query.
func TestCollectSyncNonZeroExitEmitsNoEvents(t *testing.T) {
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", CQTable: "aws_iam_roles", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
	// Seed the querier with rows that MUST NOT be read: the sync fails first.
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
	}}
	s := newFakeSource(cat, q)

	runner := &fakeRunner{err: errors.New("cloudquery: sync failed (exit code 1)")}
	spec, err := SpecFromCatalog(cat, "postgres://unused", nil)
	if err != nil {
		t.Fatalf("SpecFromCatalog: %v", err)
	}
	s.setRunnerForTest(runner, spec, map[string]string{})

	events, errs := collect(t, s, source.Run{RunID: "run-fail"})

	if len(events) != 0 {
		t.Fatalf("expected zero events from an aborted sync, got %d", len(events))
	}
	if len(errs) == 0 {
		t.Fatalf("expected a job-failure error on the error channel, got none")
	}
	// The failure must be surfaced and name the sync-job failure.
	foundFailure := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "sync job failed") {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Errorf("expected a sync-job-failure error; got %v", errs)
	}
	if !runner.gotCalled {
		t.Errorf("runner.Run was not called")
	}
	// The staging read must never have been reached.
	if q.queried("aws_iam_roles") {
		t.Errorf("staging table was queried despite the sync failing; no rows should be read")
	}
}

// --- Cancellation: ctx.Err() surfaced --------------------------------------

// TestCollectSyncCancellationSurfacesCtxErr asserts that when the runner
// returns the context error on a cancelled run, Collect surfaces a
// cancellation error (wrapping ctx.Err()) and emits zero RawEvents
// (Requirement 1.7). The runner's Run itself returns ctx.Err(); here we drive
// that outcome through a fake runner so the Collect wiring is exercised without
// a real subprocess.
func TestCollectSyncCancellationSurfacesCtxErr(t *testing.T) {
	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", CQTable: "aws_iam_roles", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
	}}
	s := newFakeSource(cat, q)

	runner := &fakeRunner{err: context.Canceled}
	spec, err := SpecFromCatalog(cat, "postgres://unused", nil)
	if err != nil {
		t.Fatalf("SpecFromCatalog: %v", err)
	}
	s.setRunnerForTest(runner, spec, map[string]string{})

	events, errs := collect(t, s, source.Run{RunID: "run-cancel"})

	if len(events) != 0 {
		t.Fatalf("expected zero events from a cancelled sync, got %d", len(events))
	}
	if len(errs) == 0 {
		t.Fatalf("expected a cancellation error on the error channel, got none")
	}
	// The surfaced error must carry the context cancellation.
	foundCancel := false
	for _, e := range errs {
		if errors.Is(e, context.Canceled) {
			foundCancel = true
		}
	}
	if !foundCancel {
		t.Errorf("expected an error wrapping context.Canceled; got %v", errs)
	}
	if q.queried("aws_iam_roles") {
		t.Errorf("staging table was queried despite cancellation; no rows should be read")
	}
}

// TestExecCLICancellationReturnsCtxErr asserts the default execution path
// returns ctx.Err() (not a generic job failure) when the context is cancelled,
// so a cancelled run is distinguishable from a genuine non-zero exit
// (Requirement 1.7).
func TestExecCLICancellationReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the command runs

	// `sleep` would block; with an already-cancelled context the command is
	// killed and execCLI must prefer ctx.Err() over the exit-code path.
	inv := cliInvocation{
		Bin:  "sleep",
		Args: []string{"sleep", "30"},
		Spec: Spec{Tables: []string{"aws_iam_roles"}},
	}

	err := execCLI(ctx, inv, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("execCLI on a cancelled context = %v, want context.Canceled", err)
	}
}

// --- Credential non-persistence --------------------------------------------

// TestCredentialNeverLeaksOutsideSubprocessEnv asserts the credential contract
// on every observable surface of a run (Requirements 1.3, 16.4): the secret
// STS value is injected via the env map and must appear ONLY in the subprocess
// environment the cliRunner builds — never in the on-disk spec file content,
// the command args, or any returned error string, on either the success or the
// failure path.
func TestCredentialNeverLeaksOutsideSubprocessEnv(t *testing.T) {
	const secret = "AQoDYXdzEExampleSECRETsessiontoken1234567890"

	spec := Spec{
		Provider:   "aws",
		Tables:     []string{"aws_iam_roles", "aws_s3_buckets"},
		Regions:    []string{"us-east-1"},
		DestSchema: DefaultStagingSchema,
		DestDSN:    "postgres://user:pw@db/cq",
		WriteMode:  DefaultWriteMode,
	}
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "wJalrEXAMPLEKEY",
		"AWS_SESSION_TOKEN":     secret,
	}

	cases := []struct {
		name   string
		runErr error // what the injected run seam returns
	}{
		{name: "success path", runErr: nil},
		{name: "failure path", runErr: errors.New("cloudquery: sync failed (exit code 1)")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured cliInvocation
			var specContent string
			r := &cliRunner{
				bin: "cloudquery",
				run: func(_ context.Context, inv cliInvocation, _ source.ProgressReporter) error {
					captured = inv
					// Read the on-disk spec the runner wrote; it exists only
					// for the duration of Run (the dir is cleaned up on return).
					b, readErr := readSpecFile(inv.SpecPath)
					if readErr != nil {
						t.Fatalf("read spec file: %v", readErr)
					}
					specContent = b
					return tc.runErr
				},
			}

			err := r.Run(context.Background(), spec, env, nil)

			if tc.runErr == nil && err != nil {
				t.Fatalf("unexpected error on success path: %v", err)
			}
			if tc.runErr != nil && err == nil {
				t.Fatalf("expected an error on the failure path, got nil")
			}

			// 1) The on-disk spec content must not contain the secret or DSN.
			if strings.Contains(specContent, secret) {
				t.Errorf("secret leaked into the on-disk spec file")
			}
			if strings.Contains(specContent, spec.DestDSN) {
				t.Errorf("destination DSN leaked into the on-disk spec file")
			}

			// 2) The command args must not contain the secret.
			for _, a := range captured.Args {
				if strings.Contains(a, secret) {
					t.Errorf("secret leaked into command arg %q", a)
				}
			}

			// 3) A returned error (failure path) must not contain the secret.
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Errorf("secret leaked into the returned error %q", err)
			}

			// 4) The secret MUST be present in the subprocess env — that is the
			// one and only place it is allowed to travel.
			foundInEnv := false
			for _, e := range captured.Env {
				if e == "AWS_SESSION_TOKEN="+secret {
					foundInEnv = true
				}
			}
			if !foundInEnv {
				t.Errorf("secret missing from the subprocess env; it must be injected there")
			}
			// The DSN travels through CQ_DEST_DSN in the env, not the spec.
			foundDSN := false
			for _, e := range captured.Env {
				if e == "CQ_DEST_DSN="+spec.DestDSN {
					foundDSN = true
				}
			}
			if !foundDSN {
				t.Errorf("destination DSN missing from CQ_DEST_DSN env entry")
			}
		})
	}
}

// TestCredentialFromFakeRunnerStaysInEnv asserts the Source-level contract: the
// per-run credential env handed to a wired runner carries the secret VALUE,
// while the credential-free Spec the runner receives never does. This exercises
// the setRunnerForTest seam with the fake querier for the staging read on the
// success path (Requirements 1.3, 16.4).
func TestCredentialFromFakeRunnerStaysInEnv(t *testing.T) {
	const secret = "AQoDYXdzEExampleSECRETsessiontokenABCDEF"

	cat := &Catalog{
		Provider:   "aws",
		Connection: "aws",
		Tables: []TableSpec{
			{Table: "aws_iam_role", CQTable: "aws_iam_roles", IDColumn: "arn", ScopeColumn: "account_id"},
		},
	}
	q := &fakeQuerier{byTable: map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{"arn": "arn:aws:iam::111122223333:role/admin", "account_id": "111122223333"},
		}},
	}}
	s := newFakeSource(cat, q)

	spec, err := SpecFromCatalog(cat, "postgres://unused", nil)
	if err != nil {
		t.Fatalf("SpecFromCatalog: %v", err)
	}
	runner := &fakeRunner{} // success
	s.setRunnerForTest(runner, spec, map[string]string{"AWS_SESSION_TOKEN": secret})

	events, errs := collect(t, s, source.Run{RunID: "run-cred"})

	// Success path: the staging read runs and emits the single row.
	if len(events) != 1 {
		t.Fatalf("expected 1 event after a successful sync, got %d (errs=%v)", len(events), errs)
	}
	for _, e := range errs {
		t.Errorf("unexpected error: %v", e)
	}

	// The runner received the secret only through the env map.
	if got := runner.gotEnv["AWS_SESSION_TOKEN"]; got != secret {
		t.Errorf("runner env AWS_SESSION_TOKEN = %q, want the secret value", got)
	}
	// The Spec the runner received must be credential-free: the secret value
	// must not appear anywhere in its rendered YAML.
	if strings.Contains(specYAML(runner.gotSpec), secret) {
		t.Errorf("secret leaked into the Spec handed to the runner")
	}

	// The Source must retain no credential after the run (discarded on every
	// path), so nothing holds the secret once Collect returns.
	if s.credentialEnv != nil {
		t.Errorf("Source retained credentialEnv after the run; it must be discarded")
	}
}

// readSpecFile reads the on-disk spec file the cliRunner wrote, so the
// credential test can inspect its content from inside the injected run seam
// (where the temp dir still exists; it is cleaned up when Run returns).
func readSpecFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
