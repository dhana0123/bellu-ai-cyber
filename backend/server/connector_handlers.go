package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

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

// connectorErrStatus maps connector sentinel errors to HTTP status codes.
func connectorErrStatus(err error) int {
	switch {
	case errors.Is(err, connector.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, connector.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, connector.ErrInvalidTransition):
		return http.StatusConflict
	case errors.Is(err, connector.ErrUnsupportedProvider), errors.Is(err, connector.ErrInvalidValue):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
