package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/connector"
	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
)

// registerConnectorRoutes wires the RBAC-gated cloud-connector API under the
// authenticated group. Mutations require integrations:manage; reads require
// projects:read. Every connection-targeting handler additionally authorizes the
// request against the connection's project. No handler returns cloud secrets.
func (s *Server) registerConnectorRoutes(g *gin.RouterGroup) {
	conns := g.Group("/connectors")

	// Mutations.
	conns.POST("", s.auth.RequirePermission(auth.PermIntegrations), s.handleCreateConnection)
	conns.POST("/:id/setup", s.auth.RequirePermission(auth.PermIntegrations), s.handleGenerateSetup)
	conns.POST("/:id/verify", s.auth.RequirePermission(auth.PermIntegrations), s.handleVerifyConnection)
	conns.POST("/:id/expand", s.auth.RequirePermission(auth.PermIntegrations), s.handleExpandOrganization)
	conns.POST("/:id/discover", s.auth.RequirePermission(auth.PermIntegrations), s.handleDiscover)
	conns.POST("/:id/cancel", s.auth.RequirePermission(auth.PermIntegrations), s.handleCancelDiscovery)
	conns.PATCH("/:id/capabilities", s.auth.RequirePermission(auth.PermIntegrations), s.handleUpdateCapabilities)
	conns.DELETE("/:id", s.auth.RequirePermission(auth.PermIntegrations), s.handleDeleteConnection)

	// GitHub App install: begin (auth-gated, integrations:manage). The callback
	// is registered separately on the public group (see registerConnectorPublicRoutes)
	// because the browser redirect from GitHub may not carry the JWT; its
	// security is the single-use, tenant-bound state, not auth.
	conns.POST("/:id/github/install", s.auth.RequirePermission(auth.PermIntegrations), s.handleGitHubBeginInstall)

	// Git connector repositories: list, refresh, selection, and mode.
	conns.GET("/:id/repositories", s.auth.RequirePermission(auth.PermProjectsRead), s.handleListRepositories)
	conns.POST("/:id/repositories/refresh", s.auth.RequirePermission(auth.PermIntegrations), s.handleRefreshRepositories)
	conns.PATCH("/:id/repositories", s.auth.RequirePermission(auth.PermIntegrations), s.handleSetRepositorySelection)
	conns.PATCH("/:id/repositories/mode", s.auth.RequirePermission(auth.PermIntegrations), s.handleSetRepoSelectionMode)

	// Reads.
	conns.GET("/capabilities", s.auth.RequirePermission(auth.PermProjectsRead), s.handleConnectorCapabilities)
	conns.GET("", s.auth.RequirePermission(auth.PermProjectsRead), s.handleListConnections)
	conns.GET("/:id", s.auth.RequirePermission(auth.PermProjectsRead), s.handleGetConnection)
	conns.GET("/:id/status", s.auth.RequirePermission(auth.PermProjectsRead), s.handleConnectionStatus)
	// Live discovery progress over Server-Sent Events. Read-only, gated like the
	// other reads. The browser EventSource cannot send an Authorization header,
	// so Authenticate() also accepts the token via the access_token query param.
	conns.GET("/:id/discover/stream", s.auth.RequirePermission(auth.PermProjectsRead), s.handleDiscoverStream)
}

// connectionForRequest loads the path :id connection scoped to the principal's
// org, then enforces project authorization. It writes the appropriate error
// response (503/404/403) and returns ok=false when the caller should stop.
func (s *Server) connectionForRequest(c *gin.Context) (*connector.Connection, bool) {
	if s.connector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "connector not available"})
		return nil, false
	}
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return nil, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connection id"})
		return nil, false
	}
	conn, err := s.connector.Get(c.Request.Context(), p.User.OrgID, id)
	if errors.Is(err, connector.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "connection not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	if !p.CanAccessProject(conn.ProjectID) {
		// Deny with no connection data.
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return nil, false
	}
	return conn, true
}

func (s *Server) handleCreateConnection(c *gin.Context) {
	if s.connector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "connector not available"})
		return
	}
	p, _ := auth.PrincipalFrom(c)
	var body struct {
		ProjectID            int64    `json:"project_id"`
		Provider             string   `json:"provider"`
		Scope                string   `json:"scope"`
		InstallationType     string   `json:"installation_type"`
		DeploymentMethod     string   `json:"deployment_method"`
		AccountID            string   `json:"account_id"`
		ManagementAccountID  string   `json:"management_account_id"`
		OrganizationalUnitID string   `json:"organizational_unit_id"`
		Region               string   `json:"region"`
		Capabilities         []string `json:"capabilities"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// No role ARN, account id, management account id, or OU id is required at
	// create: the customer deploys the role after setup, and the account (the
	// management account, for organization scope) is derived from the role ARN
	// at verification. StackSet deployment targets are chosen in AWS.
	if body.ProjectID == 0 || body.Provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id and provider are required"})
		return
	}
	if !p.CanAccessProject(body.ProjectID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	conn, err := s.connector.Create(c.Request.Context(), connector.CreateInput{
		OrgID:                p.User.OrgID,
		ProjectID:            body.ProjectID,
		Provider:             body.Provider,
		Scope:                connector.Scope(body.Scope),
		InstallationType:     connector.InstallationType(body.InstallationType),
		DeploymentMethod:     connector.DeploymentMethod(body.DeploymentMethod),
		AccountID:            body.AccountID,
		ManagementAccountID:  body.ManagementAccountID,
		OrganizationalUnitID: body.OrganizationalUnitID,
		Region:               body.Region,
		Capabilities:         body.Capabilities,
	})
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"connection": conn})
}

// handleConnectorCapabilities returns the capability catalog as UI cards (no
// raw IAM actions), so the onboarding wizard can render capability toggles.
func (s *Server) handleConnectorCapabilities(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"capabilities": capability.Cards()})
}

func (s *Server) handleListConnections(c *gin.Context) {
	if s.connector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "connector not available"})
		return
	}
	p, _ := auth.PrincipalFrom(c)
	all, err := s.connector.List(c.Request.Context(), p.User.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Optional project scoping: ?project_id=<id> returns only that project's
	// connections (the caller must be authorized for it). Without it, return all
	// connections across the projects the principal can access.
	var projectFilter int64
	if pid := c.Query("project_id"); pid != "" {
		v, perr := strconv.ParseInt(pid, 10, 64)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project_id"})
			return
		}
		if !p.CanAccessProject(v) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		projectFilter = v
	}
	out := make([]connector.Connection, 0, len(all))
	for _, conn := range all {
		if !p.CanAccessProject(conn.ProjectID) {
			continue
		}
		if projectFilter != 0 && conn.ProjectID != projectFilter {
			continue
		}
		out = append(out, conn)
	}
	c.JSON(http.StatusOK, gin.H{"connections": out})
}

func (s *Server) handleGetConnection(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"connection": conn})
}

func (s *Server) handleGenerateSetup(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	setup, err := s.connector.GenerateSetup(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"setup": setup})
}

func (s *Server) handleVerifyConnection(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	// The customer supplies the deployed role ARN at verification time. It may
	// also be omitted if already stored from a prior verify.
	var body struct {
		RoleARN string `json:"role_arn"`
	}
	_ = c.ShouldBindJSON(&body)
	res, err := s.connector.Verify(c.Request.Context(), conn.OrgID, conn.ID, body.RoleARN)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verification": res})
}

func (s *Server) handleExpandOrganization(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	// An empty body means "auto-discover": Bellu lists the organization's member
	// accounts from the management account itself. An explicit list is also
	// accepted (e.g. to add specific accounts).
	var body struct {
		MemberAccountIDs []string `json:"member_account_ids"`
	}
	_ = c.ShouldBindJSON(&body)
	children, err := s.connector.ExpandOrganization(c.Request.Context(), conn.OrgID, conn.ID, body.MemberAccountIDs)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connections": children})
}

func (s *Server) handleDiscover(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	// Discovery on an ERROR connection is a retry.
	var (
		sess any
		err  error
	)
	if conn.Status == connector.StatusError {
		sess, err = s.connector.Retry(c.Request.Context(), conn.OrgID, conn.ID)
	} else {
		sess, err = s.connector.Discover(c.Request.Context(), conn.OrgID, conn.ID)
	}
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": sess})
}

// handleDiscoverStream streams live discovery progress for a connection's
// latest session over Server-Sent Events. It sends the current snapshot
// immediately, a progress event on every change, and a terminal "done" event
// when the session finishes, then closes. Client disconnect ends the stream and
// never affects the running session. No cloud secrets are ever emitted — the
// progress payload carries only table names, counts, states, and error strings.
func (s *Server) handleDiscoverStream(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}

	ch, cancel, ok, err := s.connector.SubscribeProgress(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	if !ok {
		// No live session to stream: respond rather than hanging the connection.
		c.JSON(http.StatusNotFound, gin.H{"error": "no discovery session for this connection"})
		return
	}
	defer cancel()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	c.Writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	writeEvent := func(event string, prog *discovery.Progress) bool {
		data, err := json.Marshal(prog)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	ctxDone := c.Request.Context().Done()
	for {
		select {
		case <-ctxDone:
			// Client disconnected; stop streaming without touching the run.
			return
		case prog, open := <-ch:
			if !open {
				// Hub closed: the session reached a terminal state. Emit a final
				// snapshot (from the status view, which carries the frozen final
				// progress) as the terminal "done" event, then close.
				final := s.finalProgress(c, conn.OrgID, conn.ID)
				writeEvent("done", final)
				return
			}
			if !writeEvent("progress", prog) {
				return
			}
		}
	}
}

// finalProgress fetches the frozen final progress of the latest session via the
// status view, used for the terminal SSE event after the live hub has closed.
func (s *Server) finalProgress(c *gin.Context, orgID, id int64) *discovery.Progress {
	view, err := s.connector.Status(c.Request.Context(), orgID, id)
	if err != nil || view == nil || view.LatestSession == nil {
		return nil
	}
	return view.LatestSession.Progress
}

// handleUpdateCapabilities replaces a connection's enabled capability set. The
// new selection takes effect on the next scan. Blocked while a scan is running.
func (s *Server) handleUpdateCapabilities(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	updated, err := s.connector.UpdateCapabilities(c.Request.Context(), conn.OrgID, conn.ID, body.Capabilities)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connection": updated})
}

func (s *Server) handleCancelDiscovery(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	if err := s.connector.Cancel(c.Request.Context(), conn.OrgID, conn.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelling"})
}

func (s *Server) handleConnectionStatus(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	view, err := s.connector.Status(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": view})
}

func (s *Server) handleDeleteConnection(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	if err := s.connector.Delete(c.Request.Context(), conn.OrgID, conn.ID); err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (s *Server) handleListRepositories(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	view, err := s.connector.Repositories(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (s *Server) handleRefreshRepositories(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	view, err := s.connector.RefreshRepositories(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (s *Server) handleSetRepositorySelection(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	var body struct {
		RepositoryIDs []int64 `json:"repository_ids"`
		Selected      bool    `json:"selected"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := s.connector.SetRepositorySelection(c.Request.Context(), conn.OrgID, conn.ID, body.RepositoryIDs, body.Selected); err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	view, err := s.connector.Repositories(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (s *Server) handleSetRepoSelectionMode(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := s.connector.SetRepoSelectionMode(c.Request.Context(), conn.OrgID, conn.ID, connector.RepoSelectionMode(body.Mode)); err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	view, err := s.connector.Repositories(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

// registerConnectorPublicRoutes wires the connector endpoints that must NOT be
// behind authentication because they are reached by a browser redirect that may
// not carry the JWT. Currently just the GitHub install callback, whose security
// is the single-use, tenant-bound install state (not auth). It is registered on
// the public /api group alongside health and login.
// maxWebhookBody bounds the webhook payload the server will read, so a hostile
// sender cannot exhaust memory. GitHub payloads are well under this.
const maxWebhookBody = 2 << 20 // 2 MiB

// handleGitHubWebhook receives GitHub App webhooks. It is unauthenticated by
// design: the HMAC signature over the raw body is the authentication. It reads
// the RAW body first, verifies X-Hub-Signature-256 in constant time, then
// dedupes + dispatches. Invalid signatures are rejected before any parsing.
func (s *Server) handleGitHubWebhook(c *gin.Context) {
	if s.connector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "connector not available"})
		return
	}
	// Read the exact raw bytes (bounded) before any binding — HMAC is over this.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return
	}
	sig := c.GetHeader("X-Hub-Signature-256")
	if !s.connector.VerifyGitHubWebhook(body, sig) {
		// Reject before parsing. No detail leaked.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}
	deliveryID := c.GetHeader("X-GitHub-Delivery")
	eventType := c.GetHeader("X-GitHub-Event")
	if deliveryID == "" || eventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing delivery id or event type"})
		return
	}
	res, herr := s.connector.HandleGitHubWebhook(c.Request.Context(), deliveryID, eventType, body)
	if herr != nil {
		// Processing failed but the signature was valid; 202 so GitHub does not
		// hammer retries on a persistent processing bug, and the delivery is
		// recorded as failed for inspection.
		c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": res.Status})
}

func (s *Server) registerConnectorPublicRoutes(g *gin.RouterGroup) {
	g.GET("/connectors/github/callback", s.handleGitHubCallback)
	// GitHub App webhook receiver. Unauthenticated by design: the HMAC
	// signature over the raw body (verified in handleGitHubWebhook) is the
	// authentication.
	g.POST("/webhooks/github", s.handleGitHubWebhook)
}

// handleGitHubBeginInstall issues a single-use install state and returns the
// GitHub App install URL for the connection. Tenant + user come from the
// authenticated Principal; the connection is loaded through connectionForRequest
// (IDOR guard). No secret is returned.
func (s *Server) handleGitHubBeginInstall(c *gin.Context) {
	conn, ok := s.connectionForRequest(c)
	if !ok {
		return
	}
	p, _ := auth.PrincipalFrom(c)
	_, installURL, err := s.connector.BeginGitHubInstall(c.Request.Context(), p.User.OrgID, p.User.ID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"install_url": installURL})
}

// handleGitHubCallback is GitHub's post-install redirect target. It reads
// state + installation_id + setup_action from the query, completes the install
// on "install"/"update" (deriving the tenant solely from the consumed state),
// and 302-redirects back to the frontend connectors page. On cancellation /
// missing installation_id, or any error, it redirects with an error reason
// rather than returning JSON. No auth: the single-use, tenant-bound state is
// the authorization.
func (s *Server) handleGitHubCallback(c *gin.Context) {
	if s.connector == nil {
		s.redirectConnectors(c, "error", "connector_unavailable")
		return
	}
	state := c.Query("state")
	instStr := c.Query("installation_id")
	setupAction := c.Query("setup_action")

	// A cancelled install (user denied) or a missing installation id: nothing to
	// complete. Redirect back with a cancelled reason.
	if instStr == "" || (setupAction != "install" && setupAction != "update") {
		s.redirectConnectors(c, "error", "install_cancelled")
		return
	}

	instID, err := strconv.ParseInt(instStr, 10, 64)
	if err != nil || instID <= 0 {
		s.redirectConnectors(c, "error", "invalid_installation_id")
		return
	}

	if _, err := s.connector.CompleteGitHubInstall(c.Request.Context(), state, instID); err != nil {
		// Map the known failure modes to a stable, non-sensitive reason code.
		reason := "install_failed"
		switch {
		case errors.Is(err, connector.ErrInvalidState):
			reason = "invalid_state"
		case errors.Is(err, connector.ErrInvalidValue):
			reason = "invalid_request"
		case errors.Is(err, connector.ErrNotFound):
			reason = "not_found"
		}
		s.redirectConnectors(c, "error", reason)
		return
	}
	s.redirectConnectors(c, "connected", "")
}

// redirectConnectors 302-redirects the browser back to the frontend connectors
// page with a github status (and optional reason). The frontend base path is
// configurable via BELLU_FRONTEND_BASE_URL; it defaults to a relative path so
// the API and UI can share an origin behind a reverse proxy.
func (s *Server) redirectConnectors(c *gin.Context, status, reason string) {
	base := strings.TrimRight(os.Getenv("BELLU_FRONTEND_BASE_URL"), "/")
	target := base + "/connectors?github=" + url.QueryEscape(status)
	if reason != "" {
		target += "&reason=" + url.QueryEscape(reason)
	}
	c.Redirect(http.StatusFound, target)
}

// connectorErrStatus maps connector sentinel errors to HTTP status codes.
func connectorErrStatus(err error) int {
	switch {
	case errors.Is(err, connector.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, connector.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, connector.ErrInvalidTransition):
		return http.StatusConflict
	case errors.Is(err, connector.ErrUnsupportedProvider), errors.Is(err, connector.ErrInvalidValue),
		errors.Is(err, connector.ErrInvalidState):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
