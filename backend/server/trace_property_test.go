//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/monitor"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file is the property-based validation for the Phase 2 trace write-back
// and detail/timeline reads (task 15.1). It proves two design properties on the
// real HTTP stack over a real Postgres (testcontainers), gated behind the
// `integration` build tag so it only runs when a container runtime is available
// (`go test -tags integration ./backend/server/...`).
//
// It is a separate file from monitor_loop_integration_test.go and uses its own
// distinctly named harness (traceEnv / newTraceEnv / traceDoJSON / traceDecode
// and friends) so the two integration files coexist in the same package build
// without symbol collisions.
//
//	Property 1 — Tenant isolation (Req 12.1, 13.5, 19.2)
//	  A trace WRITE-BACK (POST /api/agent/runs/:id/trace) or a detail/timeline
//	  READ for a run that does NOT belong to the caller's org returns 404 and
//	  persists NO legs/handoffs. Proven with two principals in two orgs: org A
//	  launches a run; org B (a second seeded org + agent-service-account token)
//	  attempts the trace write-back and the detail/timeline reads against A's
//	  run, all of which must 404 — and afterward A's own detail read shows zero
//	  legs, so B's rejected write persisted nothing. A random nonexistent run id
//	  is covered too (trace POST 404, detail/timeline GET 404).
//
//	Property 8 — Stage-flow leg counts (Req 12.1)
//	  For a generated set of agents (names mapping to various stages, some to
//	  "other"), after a trace write-back the run detail's agents_count equals the
//	  number of distinct (run_id, agent_id) legs emitted, handoffs_count equals
//	  the handoffs emitted, and the sum of the 11 stage-flow leg counts equals
//	  the number of agents that mapped to a pipeline stage (agents mapping to
//	  "other" are persisted but are not among the 11). The agent set is varied
//	  across cases with a fixed-seed math/rand generator.

// --- harness -----------------------------------------------------------------

// traceEnv is the wired server plus two agent-service-account principals in two
// distinct orgs. Both tokens carry the full agent-service-account permission
// set (monitor:ingest + agent:run + findings:read + findings:ingest), so each
// can drive the ingest -> claim -> trace -> detail/timeline seam for its OWN
// org while being a stranger to the other org's runs.
type traceEnv struct {
	srv   http.Handler
	store *store.Store

	orgAID int64
	tokenA string
	orgBID int64
	tokenB string
}

// newTraceEnv spins up a disposable Postgres, migrates, seeds the RBAC catalog,
// and binds TWO agent-service-account principals to TWO distinct orgs. Org A is
// the default seeded org; org B is created explicitly here with its own
// service-account user. It returns a wired Server handler plus both principals'
// freshly minted login tokens. The two-org shape is what makes the cross-org
// tenant-isolation assertions (Property 1) possible.
func newTraceEnv(t *testing.T) *traceEnv {
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
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed the RBAC catalog (permissions + built-in roles, including the agent
	// service account) and the first org the principal binds to.
	authStore := auth.NewStore(st.Pool())
	const adminEmail, adminPassword = "admin@bellu.test", "admin-secret"
	if _, err := authStore.Seed(ctx, adminEmail, adminPassword, "Org A"); err != nil {
		t.Fatalf("seed rbac: %v", err)
	}

	// Org A: the default seeded org + an agent-service-account principal.
	var orgAID int64
	if err := st.Pool().QueryRow(ctx,
		`SELECT id FROM organizations ORDER BY id LIMIT 1`).Scan(&orgAID); err != nil {
		t.Fatalf("read org A: %v", err)
	}
	const saAEmail, saAPassword = "svc-a@bellu.test", "svc-a-secret"
	traceSeedServiceAccount(t, ctx, st.Pool(), orgAID, saAEmail, saAPassword)

	// Org B: a SECOND org with its OWN agent-service-account principal. This is
	// the stranger tenant used to prove cross-org isolation.
	var orgBID int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ('Org B') RETURNING id`).Scan(&orgBID); err != nil {
		t.Fatalf("create org B: %v", err)
	}
	const saBEmail, saBPassword = "svc-b@bellu.test", "svc-b-secret"
	traceSeedServiceAccount(t, ctx, st.Pool(), orgBID, saBEmail, saBPassword)

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	svc := auth.NewService(authStore, tokens)
	srv := New(Options{Store: st, Auth: svc}).Handler()

	return &traceEnv{
		srv:    srv,
		store:  st,
		orgAID: orgAID,
		tokenA: traceLogin(t, srv, saAEmail, saAPassword),
		orgBID: orgBID,
		tokenB: traceLogin(t, srv, saBEmail, saBPassword),
	}
}

// traceSeedServiceAccount inserts an active user bound org-wide (project NULL)
// to the agent-service-account role for the GIVEN org. The principal's org is
// the user's org_id column, so binding the user to orgID is what makes that
// token act as — and only see — that org (tenant isolation). It is named
// distinctly from seedLoopUserWithRole / seedUserWithRole to avoid colliding
// with those helpers in the same package build.
func traceSeedServiceAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID int64, email, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, name, password_hash, active)
		 VALUES ($1,$2,$3,$4,true) RETURNING id`,
		orgID, email, email, string(hash)).Scan(&userID); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	var roleID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM roles WHERE key=$1 AND builtin`, auth.RoleAgentServiceAccount).Scan(&roleID); err != nil {
		t.Fatalf("read role %s: %v", auth.RoleAgentServiceAccount, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, role_id, project_id) VALUES ($1,$2,NULL)`,
		userID, roleID); err != nil {
		t.Fatalf("insert membership %s: %v", email, err)
	}
}

// traceLogin posts credentials to /api/auth/login and returns the issued JWT.
func traceLogin(t *testing.T, srv http.Handler, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: status %d, body %s", email, rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("login %s: decode: %v", email, err)
	}
	if out.Token == "" {
		t.Fatalf("login %s: empty token", email)
	}
	return out.Token
}

// traceDoJSON issues an authenticated request (with the given bearer token) and
// an optional JSON body, returning the recorder. A nil body sends no payload.
func (e *traceEnv) traceDoJSON(t *testing.T, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s body: %v", method, path, err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, r)
	return rec
}

// traceDecode unmarshals a recorder's JSON body into v, failing on a status
// other than wantCode.
func traceDecode(t *testing.T, rec *httptest.ResponseRecorder, wantCode int, v any) {
	t.Helper()
	if rec.Code != wantCode {
		t.Fatalf("status %d, want %d (body %s)", rec.Code, wantCode, rec.Body.String())
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
	}
}

// launchRunForOrg drives ingest -> claim for the given principal's org and
// returns the run id of the read-only run launched for the claimed event. Each
// call uses a unique delivery id so repeated launches in one test never collide
// on the (org, source, delivery_id) idempotency key.
func (e *traceEnv) launchRunForOrg(t *testing.T, token, deliveryID string) string {
	t.Helper()

	// A GitHub check_run payload classifies to event_kind "ci", which the static
	// routing table routes to the red_team so the event becomes claimable.
	payload, err := json.Marshal(map[string]any{
		"event_type":   "check_run",
		"check_run":    map[string]any{"status": "completed", "conclusion": "failure"},
		"canonical_id": "repo:github:octo/" + deliveryID,
	})
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}

	var ingested struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	traceDecode(t, e.traceDoJSON(t, token, "POST", "/api/monitor/events", map[string]any{
		"source":      "github",
		"delivery_id": deliveryID,
		"payload":     json.RawMessage(payload),
	}), http.StatusAccepted, &ingested)
	if ingested.State != "routed" {
		t.Fatalf("ingest state = %q, want routed", ingested.State)
	}

	var claimResp struct {
		Claims []struct {
			ID    int64  `json:"id"`
			RunID string `json:"run_id"`
		} `json:"claims"`
	}
	traceDecode(t, e.traceDoJSON(t, token, "POST", "/api/agent/monitor/claim", map[string]any{"limit": 10}),
		http.StatusOK, &claimResp)
	for _, cl := range claimResp.Claims {
		if cl.ID == ingested.ID {
			if cl.RunID == "" {
				t.Fatalf("claim launched no run for event %d", ingested.ID)
			}
			return cl.RunID
		}
	}
	t.Fatalf("claim did not return the ingested event %d (claims %+v)", ingested.ID, claimResp.Claims)
	return ""
}

// traceDetail is the subset of the run-detail response the property assertions
// read: the folded counts plus the 11-stage stage-flow leg counts.
type traceDetail struct {
	AgentsCount   int `json:"agents_count"`
	HandoffsCount int `json:"handoffs_count"`
	StageFlow     []struct {
		Stage    string `json:"stage"`
		LegCount int    `json:"leg_count"`
	} `json:"stage_flow"`
}

// detailForRun reads GET /api/agent/runs/:id/detail for the given token/run and
// returns the decoded subset.
func (e *traceEnv) detailForRun(t *testing.T, token, runID string) traceDetail {
	t.Helper()
	var d traceDetail
	traceDecode(t, e.traceDoJSON(t, token, "GET", "/api/agent/runs/"+runID+"/detail", nil),
		http.StatusOK, &d)
	return d
}

// --- generated agent sets ----------------------------------------------------

// agentNamePool is a fixed pool of agent-name fragments spanning every pipeline
// stage plus several that map to no stage (so they land in "other"). The
// generator draws from this pool so each case exercises a mix of pipeline and
// "other" legs, which is exactly what Property 8's leg-count accounting checks.
var agentNamePool = []string{
	"recon-agent",      // RECON
	"posture-check",    // POSTURE
	"discovery-worker", // DISCOVERY
	"exploit-agent",    // EXPLOIT
	"sweep-runner",     // SWEEP
	"verify-agent",     // VERIFY
	"stability-probe",  // STABILITY
	"score-engine",     // SCORE
	"format-writer",    // FORMAT
	"coverage-mapper",  // COVERAGE
	"report-builder",   // REPORT
	"orchestrator",     // other (no stage substring)
	"planner",          // other
	"janitor",          // other
}

// generatedCase is one generated trace write-back plus the expected tallies the
// handler should fold for it: the number of distinct agent ids (agents_count),
// the number of handoffs, and how many agents mapped to one of the 11 pipeline
// stages (so NOT "other"), which must equal the sum of the stage-flow leg
// counts.
type generatedCase struct {
	body           map[string]any
	distinctAgents int
	handoffs       int
	pipelineLegs   int
}

// generateCase builds one trace write-back from the fixed-seed rng: a random
// number of agents (each a random name from the pool, keyed by a unique agent
// id) and a random number of handoffs between already-emitted agents. It
// returns the body alongside the expected tallies so the assertions are derived
// from the SAME source of truth the request is built from.
func generateCase(rng *rand.Rand, caseIdx int) generatedCase {
	n := 1 + rng.Intn(8) // 1..8 agents
	start := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	agents := make([]map[string]any, 0, n)
	ids := make([]string, 0, n)
	pipelineLegs := 0
	for i := 0; i < n; i++ {
		name := agentNamePool[rng.Intn(len(agentNamePool))]
		// Unique agent id per (case, index) so every emitted leg is a distinct
		// (run_id, agent_id); distinctAgents == n by construction.
		id := fmt.Sprintf("c%d-a%d", caseIdx, i)
		ids = append(ids, id)
		agents = append(agents, map[string]any{
			"agent_id":   id,
			"agent_name": name,
			"outcome":    "completed",
			"started_at": start.Add(time.Duration(i) * time.Second),
			"ended_at":   start.Add(time.Duration(i+1) * time.Second),
		})
		if monitor.MapStage(name) != monitor.StageOther {
			pipelineLegs++
		}
	}

	// Random handoffs between distinct already-emitted agents. Requires at least
	// two agents to form an edge.
	handoffs := make([]map[string]any, 0)
	if n >= 2 {
		h := rng.Intn(n) // 0..n-1 handoffs
		for j := 0; j < h; j++ {
			from := ids[rng.Intn(len(ids))]
			to := ids[rng.Intn(len(ids))]
			for to == from {
				to = ids[rng.Intn(len(ids))]
			}
			handoffs = append(handoffs, map[string]any{
				"from_agent_id": from,
				"to_agent_id":   to,
				"at":            start.Add(time.Duration(j) * time.Second),
			})
		}
	}

	return generatedCase{
		body:           map[string]any{"agents": agents, "handoffs": handoffs},
		distinctAgents: n,
		handoffs:       len(handoffs),
		pipelineLegs:   pipelineLegs,
	}
}

// --- Property 1: tenant isolation --------------------------------------------

// TestTracePropertyTenantIsolation proves Property 1 for the trace seam: a
// cross-org trace write-back OR a cross-org detail/timeline read returns 404 and
// persists nothing (Req 12.1, 13.5, 19.2).
func TestTracePropertyTenantIsolation(t *testing.T) {
	env := newTraceEnv(t)

	// Org A launches a real read-only run.
	runA := env.launchRunForOrg(t, env.tokenA, "iso-delivery-0001")

	// Org B is a stranger to run A. A trace write-back from B against A's run is
	// a no-disclosure 404 and must persist NO legs/handoffs.
	crossTrace := map[string]any{
		"agents": []map[string]any{
			{"agent_id": "b1", "agent_name": "recon-agent", "outcome": "completed"},
			{"agent_id": "b2", "agent_name": "exploit-agent", "outcome": "completed"},
		},
		"handoffs": []map[string]any{
			{"from_agent_id": "b1", "to_agent_id": "b2"},
		},
	}
	if rec := env.traceDoJSON(t, env.tokenB, "POST", "/api/agent/runs/"+runA+"/trace", crossTrace); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org trace write-back: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	// Org B's detail/timeline reads of A's run are also no-disclosure 404s.
	if rec := env.traceDoJSON(t, env.tokenB, "GET", "/api/agent/runs/"+runA+"/detail", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org detail read: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := env.traceDoJSON(t, env.tokenB, "GET", "/api/agent/runs/"+runA+"/timeline", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org timeline read: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	// The rejected cross-org write persisted nothing: A's own detail read shows
	// zero legs across all 11 stages and zero folded counts.
	detailA := env.detailForRun(t, env.tokenA, runA)
	if detailA.AgentsCount != 0 || detailA.HandoffsCount != 0 {
		t.Fatalf("after cross-org write, run A counts = %d/%d, want 0/0 (nothing persisted)",
			detailA.AgentsCount, detailA.HandoffsCount)
	}
	totalLegs := 0
	for _, sf := range detailA.StageFlow {
		totalLegs += sf.LegCount
	}
	if totalLegs != 0 {
		t.Fatalf("after cross-org write, run A stage-flow has %d legs, want 0", totalLegs)
	}

	// A random nonexistent run id is a 404 on every trace endpoint, with nothing
	// persisted (there is no such run to persist against).
	const ghost = "run_deadbeefdeadbeefdeadbeefdeadbeef"
	if rec := env.traceDoJSON(t, env.tokenA, "POST", "/api/agent/runs/"+ghost+"/trace", crossTrace); rec.Code != http.StatusNotFound {
		t.Fatalf("trace write-back to ghost run: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := env.traceDoJSON(t, env.tokenA, "GET", "/api/agent/runs/"+ghost+"/detail", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("detail read of ghost run: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := env.traceDoJSON(t, env.tokenA, "GET", "/api/agent/runs/"+ghost+"/timeline", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("timeline read of ghost run: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	// Sanity: run A is still writable by its OWN owner (the cross-org rejection
	// did not corrupt the run), and a self write-back now folds real counts.
	var okResp struct {
		AgentsCount   int `json:"agents_count"`
		HandoffsCount int `json:"handoffs_count"`
	}
	traceDecode(t, env.traceDoJSON(t, env.tokenA, "POST", "/api/agent/runs/"+runA+"/trace", crossTrace),
		http.StatusOK, &okResp)
	if okResp.AgentsCount != 2 || okResp.HandoffsCount != 1 {
		t.Fatalf("owner self write-back counts = %d/%d, want 2/1", okResp.AgentsCount, okResp.HandoffsCount)
	}
}

// --- Property 8: stage-flow leg counts ---------------------------------------

// TestTracePropertyStageFlowLegCounts proves Property 8 across a fixed-seed
// sequence of generated agent sets: after each trace write-back the folded
// agents_count equals the distinct emitted legs, handoffs_count equals the
// emitted handoffs, and the sum of the 11 stage-flow leg counts equals the
// number of agents that mapped to a pipeline stage — agents that mapped to
// "other" are persisted but are never among the 11 (Req 12.1).
func TestTracePropertyStageFlowLegCounts(t *testing.T) {
	env := newTraceEnv(t)

	// Fixed seed => deterministic, reproducible agent sets across runs.
	rng := rand.New(rand.NewSource(0x5EED))

	const cases = 12
	for i := 0; i < cases; i++ {
		// Each case launches a fresh run (unique delivery id) so write-backs do
		// not accumulate across cases and each assertion is isolated.
		runID := env.launchRunForOrg(t, env.tokenA, fmt.Sprintf("legs-delivery-%04d", i))
		gc := generateCase(rng, i)

		var resp struct {
			AgentsCount   int `json:"agents_count"`
			HandoffsCount int `json:"handoffs_count"`
		}
		traceDecode(t, env.traceDoJSON(t, env.tokenA, "POST", "/api/agent/runs/"+runID+"/trace", gc.body),
			http.StatusOK, &resp)

		// Folded counts in the write-back response match the generated truth.
		if resp.AgentsCount != gc.distinctAgents {
			t.Fatalf("case %d: write-back agents_count = %d, want %d", i, resp.AgentsCount, gc.distinctAgents)
		}
		if resp.HandoffsCount != gc.handoffs {
			t.Fatalf("case %d: write-back handoffs_count = %d, want %d", i, resp.HandoffsCount, gc.handoffs)
		}

		// The run detail reflects the same folded counts and an 11-stage flow
		// whose leg-count sum equals the agents that mapped to a pipeline stage.
		d := env.detailForRun(t, env.tokenA, runID)
		if d.AgentsCount != gc.distinctAgents {
			t.Fatalf("case %d: detail agents_count = %d, want %d", i, d.AgentsCount, gc.distinctAgents)
		}
		if d.HandoffsCount != gc.handoffs {
			t.Fatalf("case %d: detail handoffs_count = %d, want %d", i, d.HandoffsCount, gc.handoffs)
		}
		if len(d.StageFlow) != len(monitor.Pipeline) {
			t.Fatalf("case %d: stage_flow has %d stages, want %d", i, len(d.StageFlow), len(monitor.Pipeline))
		}
		sumLegs := 0
		for _, sf := range d.StageFlow {
			if sf.Stage == string(monitor.StageOther) {
				t.Fatalf("case %d: stage_flow unexpectedly surfaced the %q bucket", i, monitor.StageOther)
			}
			sumLegs += sf.LegCount
		}
		if sumLegs != gc.pipelineLegs {
			t.Fatalf("case %d: sum of stage-flow leg counts = %d, want %d (agents mapping to a pipeline stage; %d total agents)",
				i, sumLegs, gc.pipelineLegs, gc.distinctAgents)
		}
	}
}
