// Package github implements a minimal GitHub App API client used by the ci
// GitHub adapter. It mints a short-lived App JWT (RS256) from the App's RSA
// private key, exchanges it for scoped installation access tokens, verifies an
// installation, and enumerates the repositories an installation can access.
//
// Security: every request URL is built from the configured apiBase plus a
// validated numeric path parameter; the client never fetches a client-supplied
// URL (no SSRF). The App JWT and installation tokens are never logged. The
// private key stays in process memory and is never serialized.
package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// GitHubAppClient talks to the GitHub REST API as a GitHub App. It is safe for
// concurrent use.
type GitHubAppClient struct {
	appID      int64
	privateKey *rsa.PrivateKey // loaded from the App private key PEM
	apiBase    string          // e.g. https://api.github.com (no trailing slash)
	http       *http.Client
	now        func() time.Time
}

// New builds a GitHubAppClient. privateKeyPEM is the App's RSA private key in
// PEM (PKCS#1 or PKCS#8). apiBase defaults to https://api.github.com and is
// trimmed of a trailing slash so path joins are unambiguous. httpClient is
// optional; a default client with a timeout is used when nil.
func New(appID int64, privateKeyPEM []byte, apiBase string, httpClient *http.Client) (*GitHubAppClient, error) {
	if appID <= 0 {
		return nil, fmt.Errorf("github: app id must be positive")
	}
	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	hc := httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHubAppClient{
		appID:      appID,
		privateKey: key,
		apiBase:    base,
		http:       hc,
		now:        time.Now,
	}, nil
}

// parseRSAPrivateKey parses an RSA private key from PEM, accepting both the
// PKCS#1 ("RSA PRIVATE KEY") and PKCS#8 ("PRIVATE KEY") encodings GitHub emits.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("github: empty private key")
	}
	// jwt's helper handles both PKCS#1 and PKCS#8 RSA PEM blocks.
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	return key, nil
}

// AppJWT returns a short-lived RS256 JWT authenticating as the App itself.
// Per GitHub's guidance the claims are iss=appID (as a string), iat backdated
// 60s to tolerate clock skew, and exp 9 minutes out (<= the 10-minute max).
func (c *GitHubAppClient) AppJWT(now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(c.appID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("github: sign app jwt: %w", err)
	}
	return signed, nil
}

// Token is an installation access token with its expiry.
type Token struct {
	Token  string
	Expiry time.Time
}

// InstallationToken exchanges the App JWT for an installation access token,
// optionally scoped to specific repository ids. The returned token and expiry
// are from GitHub's response. The token is never logged.
func (c *GitHubAppClient) InstallationToken(ctx context.Context, installationID int64, repoIDs []int64) (string, time.Time, error) {
	if installationID <= 0 {
		return "", time.Time{}, fmt.Errorf("github: installation id must be positive")
	}
	appJWT, err := c.AppJWT(c.now())
	if err != nil {
		return "", time.Time{}, err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBase, installationID)

	var body io.Reader
	if len(repoIDs) > 0 {
		payload, merr := json.Marshal(map[string]any{"repository_ids": repoIDs})
		if merr != nil {
			return "", time.Time{}, fmt.Errorf("github: marshal token request: %w", merr)
		}
		body = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", time.Time{}, err
	}
	c.setAppHeaders(req, appJWT)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(req, http.StatusCreated, &out); err != nil {
		return "", time.Time{}, err
	}
	if out.Token == "" {
		return "", time.Time{}, fmt.Errorf("github: empty installation token in response")
	}
	return out.Token, out.ExpiresAt, nil
}

// Installation is the subset of an installation GitHub returns that the callback
// needs to bind the installation to a tenant.
type Installation struct {
	Login       string
	AccountID   int64
	AccountType string            // User | Organization
	Permissions map[string]string // e.g. {"contents":"read","metadata":"read"}
}

// VerifyInstallation confirms the installation exists and returns its account
// login/id/type and granted permissions. Used by the install callback.
func (c *GitHubAppClient) VerifyInstallation(ctx context.Context, installationID int64) (login string, accountID int64, accountType string, perms map[string]string, err error) {
	if installationID <= 0 {
		return "", 0, "", nil, fmt.Errorf("github: installation id must be positive")
	}
	appJWT, jerr := c.AppJWT(c.now())
	if jerr != nil {
		return "", 0, "", nil, jerr
	}
	url := fmt.Sprintf("%s/app/installations/%d", c.apiBase, installationID)
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if rerr != nil {
		return "", 0, "", nil, rerr
	}
	c.setAppHeaders(req, appJWT)

	var out struct {
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		Permissions map[string]string `json:"permissions"`
	}
	if derr := c.do(req, http.StatusOK, &out); derr != nil {
		return "", 0, "", nil, derr
	}
	return out.Account.Login, out.Account.ID, out.Account.Type, out.Permissions, nil
}

// ListInstallationRepos mints an installation token and pages through the
// repositories the installation can access, mapping each to a provider.Repo.
// Paging follows per_page=100 until a page returns fewer than per_page.
func (c *GitHubAppClient) ListInstallationRepos(ctx context.Context, installationID int64) ([]provider.Repo, error) {
	token, _, err := c.InstallationToken(ctx, installationID, nil)
	if err != nil {
		return nil, err
	}

	const perPage = 100
	var out []provider.Repo
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", c.apiBase, perPage, page)
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if rerr != nil {
			return nil, rerr
		}
		c.setTokenHeaders(req, token)

		var body struct {
			Repositories []struct {
				ID            int64  `json:"id"`
				FullName      string `json:"full_name"`
				HTMLURL       string `json:"html_url"`
				DefaultBranch string `json:"default_branch"`
				Private       bool   `json:"private"`
			} `json:"repositories"`
		}
		if derr := c.do(req, http.StatusOK, &body); derr != nil {
			return nil, derr
		}
		for _, r := range body.Repositories {
			out = append(out, provider.Repo{
				ExternalID:    strconv.FormatInt(r.ID, 10),
				FullName:      r.FullName,
				URL:           r.HTMLURL,
				DefaultBranch: r.DefaultBranch,
				Private:       r.Private,
			})
		}
		if len(body.Repositories) < perPage {
			break
		}
	}
	return out, nil
}

// setAppHeaders sets the App-JWT Authorization and standard GitHub headers.
func (c *GitHubAppClient) setAppHeaders(req *http.Request, appJWT string) {
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// setTokenHeaders sets the installation-token Authorization and standard headers.
func (c *GitHubAppClient) setTokenHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// do executes the request, decodes a successful (wantStatus) body into out, and
// maps common GitHub error statuses to connector-friendly errors. The response
// body is never logged, so no token or secret leaks via error text.
func (c *GitHubAppClient) do(req *http.Request, wantStatus int, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: request %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		// Drain a bounded amount so the connection can be reused, but do not
		// surface the raw body (it may echo request details).
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return statusError(req.URL.Path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: decode %s response: %w", req.URL.Path, err)
	}
	return nil
}

// ErrGitHubAPI is the base error for a non-success GitHub API status.
var ErrGitHubAPI = errors.New("github: api error")

// statusError returns a sanitized error naming the request path and HTTP status
// only (never the response body / any token).
func statusError(path string, status int) error {
	return fmt.Errorf("%w: %s returned %d", ErrGitHubAPI, path, status)
}

// Compile-time assurance the client exposes the repo-listing surface the ci
// adapter depends on.
var _ interface {
	ListInstallationRepos(ctx context.Context, installationID int64) ([]provider.Repo, error)
} = (*GitHubAppClient)(nil)
