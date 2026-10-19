package cloudquery

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// Integration test for the flipped AWS discovery path (task 6.2).
//
// Task 6.1 wired the discovery orchestrator (and the AWS adapter) to
// cloudquery.Factory in place of the Steampipe source. This test proves the
// seam end-to-end: a discovery run driven through the real
// discovery.Orchestrator, with the plan's config fed to the real
// cloudquery.Factory and a FAKE JobRunner standing in for `cloudquery sync`,
// produces RawEvents that reach the pipeline UNCHANGED — correct
// source="cloudquery", provider, and resource ids drawn from a seeded staging
// fixture (Requirements 1.1, 12.3).
//
// Everything runs in-process: no CloudQuery binary, no live PostgreSQL. The
// fake JobRunner reports a successful sync (so Collect proceeds to the staging
// read) and the staging rows are served by the fixture-backed fakeQuerier,
// injected onto the Factory-built Source via the package-internal connect seam
// (which only this in-package test can reach). Because the test uses the real
// Factory and the real Orchestrator, it exercises the exact wiring task 6.1
// put in place, not a reimplementation of it.

// discIntegFakeProvider is a provider.CloudProvider whose Discover returns a
// DiscoveryPlan whose SteampipeConfig is the cloudquery.Factory config map (the
// field name is retained from the Steampipe era; it is just the generic
// Source-Factory config). It mirrors what the AWS adapter's Discover assembles:
// a staging dsn, a mapping "connection", capability gating, the per-run
// credential env, and — the CloudQuery-specific key — a "runner" the Factory
// wires onto the Source.
type discIntegFakeProvider struct {
	cfg map[string]any
	env map[string]string
}

func (p *discIntegFakeProvider) Name() string { return "aws" }
func (p *discIntegFakeProvider) CreateSetup(context.Context, provider.SetupInput) (*provider.Setup, error) {
	return &provider.Setup{}, nil
}
func (p *discIntegFakeProvider) VerifyConnection(context.Context, provider.Target) (*provider.VerifyResult, error) {
	return &provider.VerifyResult{OK: true}, nil
}
func (p *discIntegFakeProvider) Authenticate(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *discIntegFakeProvider) GetCredentials(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{}, nil
}
func (p *discIntegFakeProvider) CheckPermissions(context.Context, provider.Target, *provider.Credentials, []string) (*provider.VerifyResult, error) {
	return &provider.VerifyResult{OK: true}, nil
}
func (p *discIntegFakeProvider) Discover(context.Context, provider.Target, *provider.Credentials) (*provider.DiscoveryPlan, error) {
	return &provider.DiscoveryPlan{SteampipeConfig: p.cfg, CredentialEnv: p.env}, nil
}

// discIntegCreds is a minimal discovery.Credentialer: it hands back fixed
// session credentials and counts revocations, so the test can assert the
// orchestrator revokes at the end of the run.
type discIntegCreds struct {
	revokes int
}

func (c *discIntegCreds) Get(context.Context, provider.Target) (*provider.Credentials, error) {
	return &provider.Credentials{AccessKeyID: "AK", SessionToken: "TK"}, nil
}
func (c *discIntegCreds) Revoke(int64) { c.revokes++ }

// capturingPipeline is a discovery.PipelineRunner that drives the source's
// Collect to completion and records every RawEvent it emits, exactly as the
// real pipeline would consume them. Capturing here (at the pipeline boundary)
// is what lets the test assert the events reach the pipeline UNCHANGED.
type capturingPipeline struct {
	events []rawevent.RawEvent
	errs   []error
	runID  string
	orgID  int64
	projID int64
}

func (p *capturingPipeline) Run(ctx context.Context, src source.Source, runID string, orgID, projectID int64, reporter source.ProgressReporter) error {
	p.runID = runID
	p.orgID = orgID
	p.projID = projectID

	evc, errc := src.Collect(ctx, source.Run{RunID: runID, Reporter: reporter})
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			p.events = append(p.events, ev)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			p.errs = append(p.errs, err)
		}
	}
	return nil
}

// TestFlippedAWSDiscoveryPath drives a full discovery run through the real
// orchestrator wired to cloudquery.Factory with a fake JobRunner, and asserts
// the RawEvents reach the pipeline unchanged (Requirements 1.1, 12.3).
func TestFlippedAWSDiscoveryPath(t *testing.T) {
	// A fixed staging fixture: the rows a successful `cloudquery sync` would
	// have written into cq_staging, keyed by staging table name. The expected
	// RawEvent resource ids are exactly these arns.
	seedRows := map[string]tableRows{
		"aws_iam_roles": {rows: []map[string]any{
			{
				"arn":             "arn:aws:iam::111122223333:role/app-exec",
				"account_id":      "111122223333",
				"_cq_id":          "11111111-1111-1111-1111-111111111111",
				"_cq_source_name": "aws",
			},
			{
				"arn":             "arn:aws:iam::111122223333:role/ci-deployer",
				"account_id":      "111122223333",
				"_cq_id":          "22222222-2222-2222-2222-222222222222",
				"_cq_source_name": "aws",
			},
		}},
		"aws_s3_buckets": {rows: []map[string]any{
			{
				"arn":        "arn:aws:s3:::prod-assets",
				"account_id": "111122223333",
				"region":     "us-east-1",
			},
			{
				"arn":        "arn:aws:s3:::public-logs",
				"account_id": "111122223333",
				"region":     "us-west-2",
			},
		}},
	}
	wantIDs := []string{
		"arn:aws:iam::111122223333:role/app-exec",
		"arn:aws:iam::111122223333:role/ci-deployer",
		"arn:aws:s3:::prod-assets",
		"arn:aws:s3:::public-logs",
	}

	// A catalog restricted to the two seeded tables. It is passed to the Factory
	// as an explicit "catalog" value below via a tiny in-process round trip:
	// the Factory resolves a catalog from "catalog"/"connection", so to keep the
	// test independent of the embedded aws.yaml we build the Source through the
	// Factory with "connection":"aws" and then narrow the read to the seeded
	// tables by seeding only those staging tables (unseeded tables resolve to an
	// empty result set in the fakeQuerier, emitting nothing).
	q := &fakeQuerier{byTable: seedRows}

	// The fake JobRunner stands in for `cloudquery sync`: it reports success so
	// Collect proceeds to read the (already-seeded) staging rows. It also
	// captures the per-run credential env the Factory forwarded, so the test can
	// assert the credential-env contract reached the runner.
	runner := &fakeRunner{} // success

	// The DiscoveryPlan config map, exactly as the AWS adapter assembles it,
	// plus the CloudQuery-specific "runner" key the Factory wires onto the
	// Source. credential_env carries the session-only STS environment.
	credEnv := map[string]string{
		"AWS_ACCESS_KEY_ID":     "AK",
		"AWS_SECRET_ACCESS_KEY": "SK",
		"AWS_SESSION_TOKEN":     "TK",
	}
	cfg := map[string]any{
		"dsn":            "postgres://unused",
		"connection":     "aws",
		"capabilities":   []string{"core"},
		"credential_env": credEnv,
		"runner":         JobRunner(runner),
	}

	// SourceFactory: build the Source via the REAL cloudquery.Factory (the same
	// construction task 6.1 wired into the orchestrator), then inject the
	// fixture-backed querier onto the connect seam so the staging read is served
	// in-process. Only this in-package test can reach the unexported connect
	// field, which is why the integration test lives in the cloudquery package.
	factory := func(c map[string]any) (source.Source, error) {
		src, err := Factory(c)
		if err != nil {
			return nil, err
		}
		cq, ok := src.(*Source)
		if !ok {
			t.Fatalf("Factory returned %T, want *cloudquery.Source", src)
		}
		cq.connect = func(context.Context) (querier, func(), error) { return q, func() {}, nil }
		return cq, nil
	}

	pipe := &capturingPipeline{}
	creds := &discIntegCreds{}
	orch := discovery.New(discovery.Options{
		Factory: factory,
		Runner:  pipe,
		Creds:   creds,
	})

	prov := &discIntegFakeProvider{cfg: cfg, env: credEnv}
	tgt := provider.Target{
		ConnectionID: 7,
		OrgID:        42,
		ProjectID:    99,
		Capabilities: []string{"core"},
	}

	sess, err := orch.Discover(context.Background(), prov, tgt)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if sess.Status != discovery.SessComplete {
		t.Fatalf("session status = %s, want complete (error=%q)", sess.Status, sess.Error)
	}

	// The fake JobRunner must have been invoked before any staging row was read
	// (the Factory wired it onto the Source as the sync step).
	if !runner.gotCalled {
		t.Error("fake JobRunner was not called; the sync step was skipped")
	}
	// The per-run credential env must have reached the runner (and only the
	// runner): the Factory forwarded credential_env to it for this run.
	if got := runner.gotEnv["AWS_SESSION_TOKEN"]; got != "TK" {
		t.Errorf("runner credential env AWS_SESSION_TOKEN = %q, want %q", got, "TK")
	}

	// The pipeline must have been driven with the session's run id and the
	// target's tenant/project, so discovered inventory is scoped correctly.
	if pipe.runID != sess.RunID {
		t.Errorf("pipeline runID = %q, want session run id %q", pipe.runID, sess.RunID)
	}
	if pipe.orgID != 42 || pipe.projID != 99 {
		t.Errorf("pipeline org/project = %d/%d, want 42/99", pipe.orgID, pipe.projID)
	}

	// No collection errors: the seeded tables read cleanly.
	for _, e := range pipe.errs {
		t.Errorf("unexpected collection error reaching the pipeline: %v", e)
	}

	// The RawEvents reach the pipeline UNCHANGED: one event per seeded staging
	// row, each carrying source="cloudquery", the mapping provider, the run id,
	// finalized, and the resource ids from the staging fixture.
	if len(pipe.events) != len(wantIDs) {
		t.Fatalf("pipeline received %d events, want %d", len(pipe.events), len(wantIDs))
	}

	gotIDs := make([]string, 0, len(pipe.events))
	for _, ev := range pipe.events {
		gotIDs = append(gotIDs, ev.ResourceID)

		if ev.Source != SourceName {
			t.Errorf("event %s Source = %q, want %q", ev.ResourceID, ev.Source, SourceName)
		}
		if ev.Provider != "aws" {
			t.Errorf("event %s Provider = %q, want %q", ev.ResourceID, ev.Provider, "aws")
		}
		if ev.RunID != sess.RunID {
			t.Errorf("event %s RunID = %q, want %q", ev.ResourceID, ev.RunID, sess.RunID)
		}
		if ev.ID == "" || ev.PayloadHash == "" {
			t.Errorf("event %s not finalized: ID=%q PayloadHash=%q", ev.ResourceID, ev.ID, ev.PayloadHash)
		}
		if err := ev.Validate(); err != nil {
			t.Errorf("event %s invalid: %v", ev.ResourceID, err)
		}
		// The CloudQuery-internal _cq_* columns must not survive into the
		// payload the pipeline sees.
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("event %s payload is not valid JSON: %v", ev.ResourceID, err)
		}
		for k := range payload {
			if len(k) >= 4 && k[:4] == "_cq_" {
				t.Errorf("internal column %q leaked into payload for %s", k, ev.ResourceID)
			}
		}
	}

	sort.Strings(gotIDs)
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Errorf("resource id[%d] = %q, want %q (from staging fixture)", i, gotIDs[i], want)
		}
	}

	// The orchestrator must revoke the session credentials when the run ends.
	if creds.revokes != 1 {
		t.Errorf("credential revokes = %d, want 1", creds.revokes)
	}
}
