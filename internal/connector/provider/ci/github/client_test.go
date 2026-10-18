package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testKeyPEM generates a throwaway RSA key in PKCS#1 PEM so no real credential
// is needed.
func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(k),
	})
}

func newTestClient(t *testing.T, apiBase string) *GitHubAppClient {
	t.Helper()
	c, err := New(42, testKeyPEM(t), apiBase, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// decodeJWTClaims returns the decoded claims segment of a JWT without verifying
// the signature (the test only checks claim shape).
func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt should have 3 segments, got %d", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return m
}

func TestAppJWTClaims(t *testing.T) {
	c := newTestClient(t, "https://api.github.com")
	now := time.Unix(1_700_000_000, 0)
	tok, err := c.AppJWT(now)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeJWTClaims(t, tok)
	if claims["iss"] != "42" {
		t.Errorf("iss = %v, want \"42\"", claims["iss"])
	}
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if iat > now.Unix() {
		t.Errorf("iat %d should be backdated at/under now %d", iat, now.Unix())
	}
	if d := exp - now.Unix(); d <= 0 || d > 600 {
		t.Errorf("exp-now = %ds, want in (0,600]", d)
	}
}

func TestInstallationTokenRequest(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/99/access_tokens" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_installation_token",
			"expires_at": time.Unix(1_700_000_600, 0).UTC(),
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, exp, err := c.InstallationToken(context.Background(), 99, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_installation_token" {
		t.Errorf("token = %q", tok)
	}
	if exp.IsZero() {
		t.Error("expiry should be parsed")
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("auth header = %q, want Bearer <jwt>", gotAuth)
	}
	if !strings.Contains(gotBody, "repository_ids") || !strings.Contains(gotBody, "1") {
		t.Errorf("body = %q, want repository_ids", gotBody)
	}
}

func TestVerifyInstallation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/7" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account":     map[string]any{"id": 555, "login": "bellu-ai", "type": "Organization"},
			"permissions": map[string]string{"contents": "read", "metadata": "read"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	login, id, typ, perms, err := c.VerifyInstallation(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if login != "bellu-ai" || id != 555 || typ != "Organization" {
		t.Errorf("account = %q/%d/%q", login, id, typ)
	}
	if perms["contents"] != "read" {
		t.Errorf("perms = %v", perms)
	}
}

func TestListInstallationReposPaginates(t *testing.T) {
	// Page 1 returns a full page (100), page 2 returns 1 → stop after page 2.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "t", "expires_at": time.Now().Add(time.Hour)})
		case r.URL.Path == "/installation/repositories":
			page := r.URL.Query().Get("page")
			repos := []map[string]any{}
			if page == "1" {
				for i := 0; i < 100; i++ {
					repos = append(repos, map[string]any{
						"id": i + 1, "full_name": fmt.Sprintf("bellu-ai/repo-%d", i+1),
						"html_url": "https://github.com/x", "default_branch": "main", "private": true,
					})
				}
			} else {
				repos = append(repos, map[string]any{
					"id": 101, "full_name": "bellu-ai/last", "html_url": "https://github.com/x",
					"default_branch": "main", "private": false,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	repos, err := c.ListInstallationRepos(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 101 {
		t.Fatalf("got %d repos, want 101 (two pages)", len(repos))
	}
	if repos[0].ExternalID != "1" || repos[0].FullName != "bellu-ai/repo-1" {
		t.Errorf("first repo mapped wrong: %+v", repos[0])
	}
	if repos[100].ExternalID != "101" || repos[100].Private {
		t.Errorf("last repo mapped wrong: %+v", repos[100])
	}
}

func TestAPIErrorIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-leaking-body ghs_xxx", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, _, _, _, err := c.VerifyInstallation(context.Background(), 7)
	if err == nil {
		t.Fatal("want error on 404")
	}
	if strings.Contains(err.Error(), "ghs_") || strings.Contains(err.Error(), "secret-leaking-body") {
		t.Errorf("error leaked response body: %v", err)
	}
}
