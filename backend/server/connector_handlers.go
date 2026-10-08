package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/connector"
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
	conns.POST("/:id/discover", s.auth.RequirePermission(auth.PermIntegrations), s.handleDiscover)
	conns.POST("/:id/cancel", s.auth.RequirePermission(auth.PermIntegrations), s.handleCancelDiscovery)
	conns.DELETE("/:id", s.auth.RequirePermission(auth.PermIntegrations), s.handleDeleteConnection)

	// Reads.
	conns.GET("", s.auth.RequirePermission(auth.PermProjectsRead), s.handleListConnections)
	conns.GET("/:id", s.auth.RequirePermission(auth.PermProjectsRead), s.handleGetConnection)
	conns.GET("/:id/status", s.auth.RequirePermission(auth.PermProjectsRead), s.handleConnectionStatus)
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
		ProjectID    int64    `json:"project_id"`
		Provider     string   `json:"provider"`
		AccountID    string   `json:"account_id"`
		RoleARN      string   `json:"role_arn"`
		Region       string   `json:"region"`
		Capabilities []string `json:"capabilities"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.ProjectID == 0 || body.Provider == "" || body.AccountID == "" || body.RoleARN == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id, provider, account_id and role_arn are required"})
		return
	}
	// The principal must be able to access the target project.
	if !p.CanAccessProject(body.ProjectID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	conn, err := s.connector.Create(c.Request.Context(), connector.CreateInput{
		OrgID:        p.User.OrgID,
		ProjectID:    body.ProjectID,
		Provider:     body.Provider,
		AccountID:    body.AccountID,
		RoleARN:      body.RoleARN,
		Region:       body.Region,
		Capabilities: body.Capabilities,
	})
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"connection": conn})
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
	// Filter to projects the principal can access.
	out := make([]connector.Connection, 0, len(all))
	for _, conn := range all {
		if p.CanAccessProject(conn.ProjectID) {
			out = append(out, conn)
		}
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
	res, err := s.connector.Verify(c.Request.Context(), conn.OrgID, conn.ID)
	if err != nil {
		c.JSON(connectorErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verification": res})
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
	case errors.Is(err, connector.ErrUnsupportedProvider), errors.Is(err, connector.ErrInvalidValue), errors.Is(err, connector.ErrInvalidTransition):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
