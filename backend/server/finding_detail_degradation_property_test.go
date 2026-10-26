//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
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

// This file is the Property 7 (graceful degradation) slice of the API, gated
// behind the `integration` build tag so it only runs when a container runtime
// is available (`go test -tags integration ./backend/server/...`). It exercises
// the same Server.Handler() HTTP stack over a real Postgres (testcontainers) as
// monitor_loop_integration_test.go and findings_handlers_integration_test.go,
// but reuses NONE of their fixtures — every symbol here is prefixed `degr` so it
// never collides with those files' helpers in the same package build.
//
// Property 7 — Graceful degradation (Validates Requirements 17.2, 18.2 — API
// slice):
//
//   - For a finding ingested WITHOUT any finding_reports row, GET
//     /api/findings/:id ALWAYS returns HTTP 200 with `reports` as a NON-NULL
//     empty JSON array ([], never null) and still includes the `finding`,
//     `intel`, and `evidence` keys (Req 17.2 / 18.2).
//   - Degradation happens ONLY when there is genuinely no report: when a report
//     IS written for a finding, the SAME endpoint returns the report(s)
//     (reports length > 0), so a present report is never dropped (Req 17.1
//     boundary that bounds 17.2).
//
// The property is checked across many generated cases: each case ingests a set
// of findings, randomly partitioning them into report-less and reported
// findings, and asserts the invariant on every one. The split varies case to
// case under a fixed math/rand seed so the run is deterministic and reproducible
// yet covers a spread of report-less vs reported mixes.

// --- harness (distinct `degr*` symbols) --------------------------------------

// degrEnv is the wired server plus one service-account token holding the
// permission set this property needs: findings:ingest (to ingest findings and
// write reports) and findings:read (to read the extended finding detail). The
// agent service account carries both (see auth.RoleAgentServiceAccount), so a
// single token drives every seam here.
type degrEnv struct {
	srv   http.Handler
	store *store.Store
	token string
	orgID int64
}

// newDegrEnv spins up a disposable Postgres, migrates, seeds the RBAC catalog,
// and binds one agent-service-account principal to the seeded org. It mirrors
// newLoopEnv but is named distinctly so the two integration files coexist in
// the same package build.
func newDegrEnv(t *testing.T) *degrEnv {
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

	// The agent service account holds findings:read + findings:ingest, which is
	// everything this property touches (ingest findings, write reports, read the
	// extended finding detail).
	const saEmail, saPassword = "svc@bellu.test", "svc-secret"
	degrSeedUserWithRole(t, ctx, st.Pool(), saEmail, saPassword, auth.RoleAgentServiceAccount)

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	svc := auth.NewService(authStore, tokens)
	srv := New(Options{Store: st, Auth: svc}).Handler()

	return &degrEnv{
		srv:   srv,
		store: st,
		token: degrLogin(t, srv, saEmail, saPassword),
		orgID: orgID,
	}
}

// degrSeedUserWithRole inserts an active user bound org-wide to the given
// built-in role so LoadPrincipal resolves exactly that role's permission set.
// Named distinctly from seedUserWithRole / seedLoopUserWithRole to avoid
// colliding with the other integration files in the same package build.
func degrSeedUserWithRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, password, roleKey string) {
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

// degrLogin posts credentials to /api/auth/login and returns the issued JWT.
func degrLogin(t *testing.T, srv http.Handler, email, password string) string {
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

// degrDoJSON issues an authenticated request with an optional JSON body and
// returns the recorder. A nil body sends no payload (for GETs).
func (e *degrEnv) degrDoJSON(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
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

// degrFindingID resolves a finding's surrogate id from its (asset_id, vuln_id)
// dedup key via finding.NewStore(store).GetIDByKey — the test needs the numeric
// id to hit GET /api/findings/:id, which the ingest response does not return.
func (e *degrEnv) degrFindingID(t *testing.T, assetID, vulnID string) int64 {
	t.Helper()
	id, err := finding.NewStore(e.store).GetIDByKey(context.Background(), assetID, vulnID)
	if err != nil {
		t.Fatalf("resolve finding id (%s/%s): %v", assetID, vulnID, err)
	}
	return id
}

// --- the property ------------------------------------------------------------

// TestFindingDetailGracefulDegradationProperty asserts Property 7 on the API
// slice (Validates Requirements 17.2, 18.2) across a spread of generated cases
// under a fixed math/rand seed:
//
//   - A finding WITHOUT a report always reads back 200 with reports == [] (a
//     non-null empty array) and the finding/intel/evidence keys present.
//   - A finding WITH a report reads back 200 with reports length > 0, so a
//     present report is never dropped by the degradation path.
//
// The container is expensive, so one env is shared across all cases; each case
// uses case-scoped, collision-free asset/vuln ids so findings never alias
// between cases.
func TestFindingDetailGracefulDegradationProperty(t *testing.T) {
	env := newDegrEnv(t)

	// Fixed seed -> deterministic, reproducible spread of report-less vs
	// reported mixes across cases.
	rng := rand.New(rand.NewSource(0x7e57_5eed))

	const cases = 12
	for c := 0; c < cases; c++ {
		// Each case ingests between 1 and 6 findings; the mix of report-less vs
		// reported findings therefore varies case to case (Req 17.2 across a
		// range of empty/non-empty report sets).
		n := 1 + rng.Intn(6)

		type seeded struct {
			findingID  int64
			hasReport  bool
			reportTeam string // "" when no report
		}
		got := make([]seeded, 0, n)

		for i := 0; i < n; i++ {
			// Case-scoped, collision-free identity so findings never alias across
			// cases or within a case.
			assetID := "asset:degr:" + degrItoa(int64(c)) + ":" + degrItoa(int64(i))
			vulnID := "CVE-9000-" + degrItoa(int64(c)) + degrItoa(int64(i))

			// Ingest the thin finding. No report is attached yet, so at this point
			// every finding is genuinely report-less.
			var ingestResp struct {
				Created  int `json:"created"`
				Rejected int `json:"rejected"`
			}
			degrDecode(t, env.degrDoJSON(t, "POST", "/api/agent/findings:ingest", map[string]any{
				"findings": []map[string]any{
					{
						"asset_id":   assetID,
						"asset_kind": "Package",
						"vuln_id":    vulnID,
						"risk_score": 10 + rng.Intn(90),
						"severity":   "high",
					},
				},
			}), http.StatusOK, &ingestResp)
			if ingestResp.Created != 1 || ingestResp.Rejected != 0 {
				t.Fatalf("case %d finding %d: ingest created/rejected = %d/%d, want 1/0",
					c, i, ingestResp.Created, ingestResp.Rejected)
			}

			fid := env.degrFindingID(t, assetID, vulnID)

			// Randomly decide whether THIS finding gets a report. The coin flip
			// varies the report-less vs reported split per case, so some cases are
			// all report-less, some all reported, and most a mix.
			withReport := rng.Intn(2) == 0
			team := ""
			if withReport {
				team = "red"
				if rng.Intn(2) == 0 {
					team = "blue"
				}
				degrDecode(t, env.degrDoJSON(t, "POST", "/api/agent/findings/reports", map[string]any{
					"finding_id":    fid,
					"team":          team,
					"title":         team + " report",
					"severity":      "high",
					"poc_available": true,
					"sections": degrMustJSON(t, map[string]any{
						"summary": team + " team analysis",
					}),
					"rendered_markdown": "# " + team + " report",
				}), http.StatusOK, nil)
			}

			got = append(got, seeded{findingID: fid, hasReport: withReport, reportTeam: team})
		}

		// Assert the property on every seeded finding in this case.
		for i, s := range got {
			rec := env.degrDoJSON(t, "GET", "/api/findings/"+degrItoa(s.findingID), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("case %d finding %d (id %d): GET status %d, want 200 (body %s)",
					c, i, s.findingID, rec.Code, rec.Body.String())
			}

			// Decode into a shape that distinguishes a missing/null `reports` key
			// from an empty array: json.RawMessage preserves the literal so we can
			// assert it is exactly `[]` (non-null) in the degraded case.
			var resp struct {
				Finding     json.RawMessage `json:"finding"`
				Intel       json.RawMessage `json:"intel"`
				Evidence    json.RawMessage `json:"evidence"`
				Reports     json.RawMessage `json:"reports"`
				TriggeredBy string          `json:"triggered_by"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("case %d finding %d: decode body %q: %v", c, i, rec.Body.String(), err)
			}

			// The header/intel/evidence keys are ALWAYS present (Req 17.2 / 18.2).
			// `finding` and `evidence` are always non-null; `intel` may be JSON
			// null when the cited vuln is absent (the handler tolerates a missing
			// intel row), so we require only that the key is present.
			if len(resp.Finding) == 0 || degrIsJSONNull(resp.Finding) {
				t.Fatalf("case %d finding %d: response missing non-null `finding` (body %s)",
					c, i, rec.Body.String())
			}
			if len(resp.Evidence) == 0 || degrIsJSONNull(resp.Evidence) {
				t.Fatalf("case %d finding %d: response missing non-null `evidence` (body %s)",
					c, i, rec.Body.String())
			}
			if len(resp.Intel) == 0 { // key present (value may be JSON null)
				t.Fatalf("case %d finding %d: response missing `intel` key (body %s)",
					c, i, rec.Body.String())
			}

			// `reports` is NEVER null. It is always a JSON array literal.
			if len(resp.Reports) == 0 || degrIsJSONNull(resp.Reports) {
				t.Fatalf("case %d finding %d: `reports` is null/absent, want a JSON array (body %s)",
					c, i, rec.Body.String())
			}

			var reports []store.FindingReport
			if err := json.Unmarshal(resp.Reports, &reports); err != nil {
				t.Fatalf("case %d finding %d: `reports` is not a JSON array: %v (raw %s)",
					c, i, err, string(resp.Reports))
			}

			if s.hasReport {
				// Degradation must NOT drop a present report: a reported finding
				// reads back at least one report, and the team we wrote is present.
				if len(reports) == 0 {
					t.Fatalf("case %d finding %d (id %d): reported finding returned 0 reports, want > 0 (team %q)",
						c, i, s.findingID, s.reportTeam)
				}
				foundTeam := false
				for _, r := range reports {
					if r.Team == s.reportTeam {
						foundTeam = true
					}
				}
				if !foundTeam {
					t.Fatalf("case %d finding %d (id %d): reports %+v missing written team %q",
						c, i, s.findingID, reports, s.reportTeam)
				}
			} else {
				// Genuine degradation: a report-less finding reads back the
				// NON-NULL empty array `[]` (Req 17.2 / 18.2). Assert both the
				// decoded length and the literal shape.
				if len(reports) != 0 {
					t.Fatalf("case %d finding %d (id %d): report-less finding returned %d reports, want 0",
						c, i, s.findingID, len(reports))
				}
				if degrCompactJSON(t, resp.Reports) != "[]" {
					t.Fatalf("case %d finding %d (id %d): report-less `reports` literal = %s, want []",
						c, i, s.findingID, degrCompactJSON(t, resp.Reports))
				}
			}
		}
	}
}

// --- small helpers (distinct `degr*` symbols) --------------------------------

// degrDecode unmarshals a recorder's JSON body into v, failing on an unexpected
// status.
func degrDecode(t *testing.T, rec *httptest.ResponseRecorder, wantCode int, v any) {
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

// degrMustJSON marshals v to json.RawMessage, failing the test on error.
func degrMustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

// degrItoa renders an int64 as a decimal string for path/id building.
func degrItoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// degrIsJSONNull reports whether a raw JSON value is the literal `null` (modulo
// surrounding whitespace).
func degrIsJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// degrCompactJSON returns the whitespace-normalized form of a raw JSON value so
// the degraded `reports` literal can be compared to the canonical "[]".
func degrCompactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact json %q: %v", string(raw), err)
	}
	return buf.String()
}
