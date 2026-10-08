//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/global-intel/database"
	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file exercises the Findings/Intelligence API end to end against a real
// Postgres (testcontainers) through the Server.Handler() HTTP stack. It covers
// two slices of Requirement 13:
//
//   - Permission gating (13.5, 13.6): every findings/intel endpoint sits behind
//     RequirePermission(findings:read). A token for a user holding the
//     permission gets 200; a token for a user lacking it gets 403; a request
//     with no bearer token gets 401. Both the findings and intel endpoints are
//     checked so the whole gated group is covered.
//   - Filter behavior (13.1, 13.3): the handlers translate severity/status/
//     asset_kind/limit (findings) and severity/kev/purl (intel) query params
//     into narrowed result sets over seeded rows.
//
// The auth path requires a DB-backed principal load (Authenticate ->
// LoadPrincipal), so these assertions are integration-only; the pure
// query-param parsing is unit-tested separately in findings_handlers_unit_test.go.

// --- harness ---

type testEnv struct {
	srv         http.Handler
	readerToken string // user WITH findings:read (Global Reader)
	noPermToken string // user WITHOUT findings:read (Viewer)
}

// newTestEnv spins up a disposable Postgres, migrates, seeds the RBAC catalog
// plus two users (one with findings:read, one without), seeds intel + findings
// fixtures, and returns a wired Server handler and freshly minted login tokens.
func newTestEnv(t *testing.T) *testEnv {
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

	// Seed the RBAC catalog (permissions + built-in roles) and a Global Admin.
	authStore := auth.NewStore(st.Pool())
	const adminEmail, adminPassword = "admin@bellu.test", "admin-secret"
	if _, err := authStore.Seed(ctx, adminEmail, adminPassword, "Test Org"); err != nil {
		t.Fatalf("seed rbac: %v", err)
	}

	// A Global Reader holds findings:read; a Viewer does not (see BuiltinRoles).
	const readerEmail, readerPassword = "reader@bellu.test", "reader-secret"
	const viewerEmail, viewerPassword = "viewer@bellu.test", "viewer-secret"
	seedUserWithRole(t, ctx, st.Pool(), readerEmail, readerPassword, auth.RoleGlobalReader)
	seedUserWithRole(t, ctx, st.Pool(), viewerEmail, viewerPassword, auth.RoleViewer)

	seedIntel(t, ctx, st)
	seedFindings(t, ctx, st)

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	svc := auth.NewService(authStore, tokens)
	srv := New(Options{Store: st, Auth: svc}).Handler()

	return &testEnv{
		srv:         srv,
		readerToken: login(t, srv, readerEmail, readerPassword),
		noPermToken: login(t, srv, viewerEmail, viewerPassword),
	}
}

// seedUserWithRole inserts an active user bound org-wide to the given built-in
// role, so LoadPrincipal resolves exactly that role's permission set.
func seedUserWithRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, password, roleKey string) {
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

// seedIntel writes a small canonical corpus through the real intel persistence
// path so the intel read API (AllVulns / VulnsByPurl + Go-side severity/kev
// filtering) has something to narrow.
func seedIntel(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	intel := database.New(st)
	// Published/Modified are set on every record: the intel_* schema stores a
	// zero time as NULL, and the hydrate read scans those columns into
	// *time.Time, so a NULL would surface a scan error unrelated to this task.
	// Real intelligence always carries a published date, so non-zero timestamps
	// keep the fixture realistic.
	published := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	modified := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	vulns := []canonical.CanonicalVuln{
		{
			ID:             "CVE-2024-0001",
			Aliases:        []string{"CVE-2024-0001"},
			Summary:        "critical kev vuln in requests",
			Severity:       canonical.SevCritical,
			KnownExploited: true,
			Sources:        []string{"osv", "kev"},
			Published:      published,
			Modified:       modified,
			Affected: []canonical.AffectedPackage{
				{Purl: "pkg:pypi/requests", Ecosystem: "PyPI",
					Ranges: []canonical.SemverRange{{Introduced: "2.0.0", Fixed: "2.31.0"}}},
			},
		},
		{
			ID:             "CVE-2024-0002",
			Aliases:        []string{"CVE-2024-0002"},
			Summary:        "high non-kev vuln in flask",
			Severity:       canonical.SevHigh,
			KnownExploited: false,
			Sources:        []string{"osv"},
			Published:      published,
			Modified:       modified,
			Affected: []canonical.AffectedPackage{
				{Purl: "pkg:pypi/flask", Ecosystem: "PyPI",
					Ranges: []canonical.SemverRange{{Introduced: "1.0.0", Fixed: "1.1.4"}}},
			},
		},
		{
			ID:             "CVE-2024-0003",
			Aliases:        []string{"CVE-2024-0003"},
			Summary:        "low vuln in requests",
			Severity:       canonical.SevLow,
			KnownExploited: false,
			Sources:        []string{"osv"},
			Published:      published,
			Modified:       modified,
			Affected: []canonical.AffectedPackage{
				{Purl: "pkg:pypi/requests", Ecosystem: "PyPI",
					Ranges: []canonical.SemverRange{{Introduced: "1.0.0", Fixed: "2.0.0"}}},
			},
		},
	}
	for _, v := range vulns {
		if err := intel.UpsertVuln(ctx, v, time.Time{}, 0); err != nil {
			t.Fatalf("seed intel %s: %v", v.ID, err)
		}
	}
}

// seedFindings writes findings covering distinct severity/status/asset_kind so
// each findings filter has a discriminating fixture.
func seedFindings(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	fs := finding.NewStore(st)
	seed := []finding.Finding{
		{AssetID: "asset-a", AssetKind: model.Kind("Package"), VulnID: "CVE-2024-0001",
			IntelRef: "CVE-2024-0001", RiskScore: 95, Severity: canonical.SevCritical},
		{AssetID: "asset-b", AssetKind: model.Kind("Package"), VulnID: "CVE-2024-0002",
			IntelRef: "CVE-2024-0002", RiskScore: 72, Severity: canonical.SevHigh},
		{AssetID: "asset-c", AssetKind: model.Kind("ContainerImage"), VulnID: "CVE-2024-0003",
			IntelRef: "CVE-2024-0003", RiskScore: 20, Severity: canonical.SevLow},
	}
	for _, f := range seed {
		if err := fs.Upsert(ctx, f); err != nil {
			t.Fatalf("seed finding %s/%s: %v", f.AssetID, f.VulnID, err)
		}
	}
	// Resolve one finding so the status filter can distinguish open vs resolved.
	if err := fs.ResolveMissing(ctx, []finding.Key{
		{AssetID: "asset-a", VulnID: "CVE-2024-0001"},
		{AssetID: "asset-b", VulnID: "CVE-2024-0002"},
	}); err != nil {
		t.Fatalf("resolve finding: %v", err)
	}
}

// login posts credentials to /api/auth/login and returns the issued JWT.
func login(t *testing.T, srv http.Handler, email, password string) string {
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

// get issues an authenticated GET and returns the recorder.
func get(srv http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// --- permission gating (Requirements 13.5, 13.6) ---

func TestFindingsEndpointsRequireFindingsRead(t *testing.T) {
	env := newTestEnv(t)

	// Both the findings and intel endpoints share the findings:read group, so
	// gating is asserted on each.
	for _, path := range []string{"/api/findings", "/api/intel/vulnerabilities"} {
		t.Run(path, func(t *testing.T) {
			// With findings:read -> 200.
			if rec := get(env.srv, path, env.readerToken); rec.Code != http.StatusOK {
				t.Fatalf("with findings:read: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			// Without findings:read -> 403 (denied by RequirePermission).
			if rec := get(env.srv, path, env.noPermToken); rec.Code != http.StatusForbidden {
				t.Fatalf("without findings:read: got %d, want 403 (body %s)", rec.Code, rec.Body.String())
			}
			// No token -> 401 (denied by Authenticate before the store is touched).
			if rec := get(env.srv, path, ""); rec.Code != http.StatusUnauthorized {
				t.Fatalf("no token: got %d, want 401 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- findings filter behavior (Requirement 13.1) ---

func TestFindingsFilters(t *testing.T) {
	env := newTestEnv(t)

	decodeFindings := func(t *testing.T, rec *httptest.ResponseRecorder) []finding.Finding {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Findings []finding.Finding `json:"findings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.Findings
	}

	t.Run("no filter returns all seeded findings", func(t *testing.T) {
		got := decodeFindings(t, get(env.srv, "/api/findings", env.readerToken))
		if len(got) != 3 {
			t.Fatalf("got %d findings, want 3", len(got))
		}
	})

	t.Run("severity narrows to critical", func(t *testing.T) {
		got := decodeFindings(t, get(env.srv, "/api/findings?severity=critical", env.readerToken))
		if len(got) != 1 || got[0].VulnID != "CVE-2024-0001" {
			t.Fatalf("severity=critical: got %+v, want only CVE-2024-0001", got)
		}
	})

	t.Run("status narrows to resolved", func(t *testing.T) {
		got := decodeFindings(t, get(env.srv, "/api/findings?status=resolved", env.readerToken))
		if len(got) != 1 || got[0].VulnID != "CVE-2024-0003" {
			t.Fatalf("status=resolved: got %+v, want only CVE-2024-0003", got)
		}
	})

	t.Run("asset_kind narrows to ContainerImage", func(t *testing.T) {
		got := decodeFindings(t, get(env.srv, "/api/findings?asset_kind=ContainerImage", env.readerToken))
		if len(got) != 1 || got[0].AssetID != "asset-c" {
			t.Fatalf("asset_kind=ContainerImage: got %+v, want only asset-c", got)
		}
	})

	t.Run("limit caps the result count", func(t *testing.T) {
		got := decodeFindings(t, get(env.srv, "/api/findings?limit=1", env.readerToken))
		if len(got) != 1 {
			t.Fatalf("limit=1: got %d findings, want 1", len(got))
		}
	})
}

// --- intel filter behavior (Requirement 13.3) ---

func TestIntelVulnFilters(t *testing.T) {
	env := newTestEnv(t)

	decodeVulns := func(t *testing.T, rec *httptest.ResponseRecorder) []canonical.CanonicalVuln {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Vulnerabilities []canonical.CanonicalVuln `json:"vulnerabilities"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.Vulnerabilities
	}

	ids := func(vs []canonical.CanonicalVuln) map[string]bool {
		m := make(map[string]bool, len(vs))
		for _, v := range vs {
			m[v.ID] = true
		}
		return m
	}

	t.Run("no filter returns the full corpus", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities", env.readerToken))
		if len(got) != 3 {
			t.Fatalf("got %d vulns, want 3", len(got))
		}
	})

	t.Run("severity narrows to high", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities?severity=high", env.readerToken))
		if len(got) != 1 || got[0].ID != "CVE-2024-0002" {
			t.Fatalf("severity=high: got %+v, want only CVE-2024-0002", got)
		}
	})

	t.Run("kev=true narrows to known-exploited", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities?kev=true", env.readerToken))
		if len(got) != 1 || got[0].ID != "CVE-2024-0001" {
			t.Fatalf("kev=true: got %+v, want only CVE-2024-0001", got)
		}
	})

	t.Run("kev=false excludes known-exploited", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities?kev=false", env.readerToken))
		set := ids(got)
		if len(got) != 2 || !set["CVE-2024-0002"] || !set["CVE-2024-0003"] {
			t.Fatalf("kev=false: got %+v, want CVE-2024-0002 and CVE-2024-0003", got)
		}
	})

	t.Run("purl keys the affected-package fast path", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities?purl=pkg:pypi/requests", env.readerToken))
		set := ids(got)
		if len(got) != 2 || !set["CVE-2024-0001"] || !set["CVE-2024-0003"] {
			t.Fatalf("purl=requests: got %+v, want CVE-2024-0001 and CVE-2024-0003", got)
		}
	})

	t.Run("purl combined with severity narrows further", func(t *testing.T) {
		got := decodeVulns(t, get(env.srv, "/api/intel/vulnerabilities?purl=pkg:pypi/requests&severity=low", env.readerToken))
		if len(got) != 1 || got[0].ID != "CVE-2024-0003" {
			t.Fatalf("purl=requests&severity=low: got %+v, want only CVE-2024-0003", got)
		}
	})
}
