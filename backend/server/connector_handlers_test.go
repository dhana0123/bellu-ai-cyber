package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/connector"
)

// TestConnectorErrStatus pins the sentinel-error to HTTP status mapping.
func TestConnectorErrStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{connector.ErrNotFound, http.StatusNotFound},
		{connector.ErrConflict, http.StatusConflict},
		{connector.ErrUnsupportedProvider, http.StatusBadRequest},
		{connector.ErrInvalidValue, http.StatusBadRequest},
		{connector.ErrInvalidTransition, http.StatusConflict},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := connectorErrStatus(c.err); got != c.want {
			t.Errorf("connectorErrStatus(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// TestNilConnectorReturns503 asserts handlers degrade to 503 when the connector
// dependency is nil, mirroring the nil-graph precedent.
func TestNilConnectorReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{} // connector is nil

	// List: direct handler call (no auth needed to reach the nil check).
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/connectors", nil)
	s.handleListConnections(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("list with nil connector = %d, want 503", w.Code)
	}

	// Create: also 503.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("POST", "/api/connectors", strings.NewReader("{}"))
	s.handleCreateConnection(c2)
	if w2.Code != http.StatusServiceUnavailable {
		t.Errorf("create with nil connector = %d, want 503", w2.Code)
	}
}

// TestConnectionJSONHasNoSecrets asserts the serialized Connection carries no
// credential-like fields and never leaks the tenant org id.
func TestConnectionJSONHasNoSecrets(t *testing.T) {
	conn := connector.Connection{
		ID:         1,
		OrgID:      42,
		ProjectID:  7,
		Provider:   "aws",
		AccountID:  "123456789012",
		RoleARN:    "arn:aws:iam::123456789012:role/BelluConnectorRole",
		ExternalID: "BELLU-X",
		Region:     "us-east-1",
		Status:     connector.StatusPending,
	}
	b, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := strings.ToLower(string(b))
	for _, banned := range []string{"secret", "access_key", "accesskey", "session_token", "sessiontoken", "password", "\"org_id\""} {
		if strings.Contains(s, banned) {
			t.Errorf("serialized connection contains %q: %s", banned, string(b))
		}
	}
	// Sanity: non-secret fields are present.
	if !strings.Contains(s, "role_arn") || !strings.Contains(s, "external_id") {
		t.Errorf("serialized connection missing expected non-secret fields: %s", string(b))
	}
}
