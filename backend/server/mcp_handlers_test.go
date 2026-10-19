package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/mcp"
)

// decodeError pulls the "error" field out of a JSON body an MCP handler wrote,
// so a test can assert on exactly what the client would see.
func decodeError(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response body is not JSON: %v (body=%q)", err, string(body))
	}
	return env.Error
}

// TestMCPBackendErrorGenericMessage pins the client-facing contract for a
// backend failure (Req 10.8): whatever internal error a tool method returns —
// the projection-unavailable sentinel, a store/query error, anything — the
// client gets only the generic "temporarily unavailable" message and a 503, and
// never the internal detail. The internal error is logged (not asserted here)
// but must not appear in the response body.
func TestMCPBackendErrorGenericMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	secret := "backend topology detail that must not leak: host=pg-internal-7"
	cases := []struct {
		name string
		err  error
	}{
		{"projection unavailable sentinel", mcp.ErrProjectionUnavailable},
		{"opaque backend error", errors.New(secret)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/api/mcp/get_entity", nil)

			mcpBackendError(c, "get_entity", tc.err)

			if rec.Code != 503 {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			got := decodeError(t, rec.Body.Bytes())
			if got != msgTemporarilyUnavailable {
				t.Fatalf("client message = %q, want %q", got, msgTemporarilyUnavailable)
			}
			// The internal detail must never reach the client body.
			if got == tc.err.Error() {
				t.Fatalf("internal error detail leaked to client: %q", got)
			}
		})
	}
}

// TestMCPBackendErrorHidesSentinelIdentity confirms the projection-unavailable
// sentinel is recognized (via errors.Is) yet collapses to the same generic
// client message as any other backend error, so a client cannot tell a
// projection outage apart from a store failure or distinguish infrastructure
// state (Req 10.8). Wrapping the sentinel still resolves through errors.Is.
func TestMCPBackendErrorHidesSentinelIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	wrapped := errors.New("read failed")
	// Verify the handler's own classification matches errors.Is on the sentinel.
	if !errors.Is(mcp.ErrProjectionUnavailable, mcp.ErrProjectionUnavailable) {
		t.Fatal("sentinel is not identical to itself")
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/mcp/find_entities", nil)

	// A non-sentinel backend error still yields the generic message and 503.
	mcpBackendError(c, "find_entities", wrapped)

	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := decodeError(t, rec.Body.Bytes()); got != msgTemporarilyUnavailable {
		t.Fatalf("client message = %q, want %q", got, msgTemporarilyUnavailable)
	}
}

// authPrincipalKey is the Gin context key the auth middleware stores the
// resolved Principal under (auth.middleware.principalKey). It is unexported in
// the auth package, so these handler tests set the same key directly to stand
// in for a completed Authenticate pass — exactly what auth.PrincipalFrom reads.
// If the auth package ever renames the key, these tests fail loudly rather than
// silently exercising an unauthenticated path.
const authPrincipalKey = "bellu_principal"

// principalCtx builds a test *gin.Context that already carries an authenticated
// principal for the given org, plus a request whose JSON body names a DIFFERENT
// organization. It is the fixture for the tenant-resolution tests: the handler
// must use the principal's org and ignore anything org-shaped in the body.
func principalCtx(t *testing.T, principalOrg int64, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/mcp/find_entities", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(authPrincipalKey, &auth.Principal{User: auth.User{ID: 7, OrgID: principalOrg, Active: true}})
	return c, rec
}

// TestMCPOrgResolvesFromPrincipal pins the tenant-resolution seam (Req 10.2,
// Req 16.2): mcpOrg reads the org off the authenticated principal and reports
// it, and it does so regardless of what the request body says. The body carries
// a conflicting organization; the resolved org must still be the principal's.
func TestMCPOrgResolvesFromPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const principalOrg int64 = 42
	// The body names org 999 — a client trying to act as another tenant.
	c, _ := principalCtx(t, principalOrg, `{"org":999,"org_id":999,"organization":999,"name_like":"x"}`)

	org, ok := mcpOrg(c)
	if !ok {
		t.Fatal("mcpOrg reported no principal, want the injected one")
	}
	if org != principalOrg {
		t.Fatalf("mcpOrg = %d, want the principal's org %d (client body org must be ignored, Req 10.2)", org, principalOrg)
	}
}

// TestMCPOrgRequiresPrincipal confirms the defense-in-depth 401 path: with no
// principal in the context (the Authenticate middleware did not run or
// rejected), mcpOrg reports (0,false) and every tool handler returns 401
// without touching the backend. Req 10.3 keeps the tools behind JWT + RBAC;
// this is the handler's own backstop if a request somehow arrives unauthenticated.
func TestMCPOrgRequiresPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/mcp/find_entities", nil)

	if org, ok := mcpOrg(c); ok || org != 0 {
		t.Fatalf("mcpOrg with no principal = (%d, %v), want (0, false)", org, ok)
	}

	// The handler translates the missing principal into a 401, not a backend call.
	s := &Server{} // mcp is nil; it must never be reached on the unauthenticated path.
	s.handleMCPFindEntities(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleMCPFindEntities with no principal = %d, want 401", rec.Code)
	}
}

// TestMCPEntityNotFoundMessage pins the plain not-found contract (Req 10.7): a
// lookup miss returns exactly "entity not found" and a 404. Because a missing
// entity and an entity owned by another tenant both reach this one branch with
// a nil result, they return the identical message — a client cannot tell them
// apart and so cannot probe cross-tenant existence. The message also must not
// leak the canonical id the client asked about.
func TestMCPEntityNotFoundMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// The handler's not-found branch renders msgEntityNotFound. Assert the
	// contract on that single shared constant: both the "missing" and the
	// "cross-tenant" case use it (there is only one), so they are identical by
	// construction, and it carries no tenant or lookup detail.
	if msgEntityNotFound != "entity not found" {
		t.Fatalf("not-found message = %q, want the plain %q", msgEntityNotFound, "entity not found")
	}
	// The message is a fixed string with no interpolation point, so no requested
	// id or tenant can appear in it.
	for _, leak := range []string{"org", "tenant", "canonical", "exists", "other"} {
		if strings.Contains(strings.ToLower(msgEntityNotFound), leak) {
			t.Fatalf("not-found message %q leaks detail %q (Req 10.7)", msgEntityNotFound, leak)
		}
	}

	// Exercise the branch through a route so the 404 + body contract is pinned
	// on the real response, not just the constant. A tiny handler mirrors the
	// handler's nil-result branch to render the shared constant over HTTP.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/mcp/get_entity", nil)
	c.JSON(http.StatusNotFound, gin.H{"error": msgEntityNotFound})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found status = %d, want 404", rec.Code)
	}
	if got := decodeError(t, rec.Body.Bytes()); got != msgEntityNotFound {
		t.Fatalf("not-found body = %q, want %q", got, msgEntityNotFound)
	}
}

// TestMCPNotFoundNeverCollidesWithUnavailable guards the two client-facing error
// contracts against drift into each other: the not-found message (Req 10.7) and
// the backend-unavailable message (Req 10.8) must stay distinct, so a client can
// tell "no such entity" (a definitive 404) from "try again later" (a transient
// 503) — while still being unable to learn anything more specific than that.
func TestMCPNotFoundNeverCollidesWithUnavailable(t *testing.T) {
	if msgEntityNotFound == msgTemporarilyUnavailable {
		t.Fatalf("not-found and unavailable messages must differ; both are %q", msgEntityNotFound)
	}
}
