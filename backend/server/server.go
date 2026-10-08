// Package server implements the Bellu control-panel HTTP API using Gin. It is a
// read/management layer over the Postgres evidence store, the Memgraph graph,
// and the auth/RBAC model, serving JSON for the React UI.
package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Server holds the dependencies for the control-panel API.
type Server struct {
	store       *store.Store
	graph       *graph.Writer // may be nil if Memgraph is unavailable
	auth        *auth.Service
	allowOrigin string
}

// Options configures the server.
type Options struct {
	Store       *store.Store
	Graph       *graph.Writer
	Auth        *auth.Service
	AllowOrigin string
}

// New builds a Server.
func New(opts Options) *Server {
	origin := opts.AllowOrigin
	if origin == "" {
		origin = "*"
	}
	return &Server{store: opts.Store, graph: opts.Graph, auth: opts.Auth, allowOrigin: origin}
}

// Handler returns the Gin engine with all routes registered.
func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	corsCfg := cors.DefaultConfig()
	if s.allowOrigin == "*" {
		corsCfg.AllowAllOrigins = true
	} else {
		corsCfg.AllowOrigins = []string{s.allowOrigin}
	}
	corsCfg.AllowHeaders = []string{"Authorization", "Content-Type"}
	corsCfg.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	r.Use(cors.New(corsCfg))

	api := r.Group("/api")
	{
		// Public.
		api.GET("/health", s.handleHealth)
		api.POST("/auth/login", s.handleLogin)

		// Authenticated.
		authed := api.Group("")
		authed.Use(s.auth.Authenticate())
		{
			authed.GET("/auth/me", s.handleMe)

			// Graph / evidence reads require graph:read.
			gr := authed.Group("")
			gr.Use(s.auth.RequirePermission(auth.PermGraphRead))
			{
				gr.GET("/stats", s.handleStats)
				gr.GET("/entities", s.handleEntities)
				gr.GET("/runs", s.handleRuns)
				gr.GET("/conflicts", s.handleConflicts)
				gr.GET("/graph/attack-paths", s.handleAttackPaths)
				gr.GET("/graph/ai-calls", s.handleAICalls)
			}

			// Findings / intelligence reads require findings:read.
			fr := authed.Group("")
			fr.Use(s.auth.RequirePermission(auth.PermFindingsRead))
			{
				fr.GET("/findings", s.handleFindings)
				fr.GET("/findings/:id", s.handleFinding)
				fr.GET("/intel/vulnerabilities", s.handleIntelVulns)
				fr.GET("/intel/vulnerabilities/:id", s.handleIntelVuln)
			}

			// Investigation.
			inv := authed.Group("")
			inv.Use(s.auth.RequirePermission(auth.PermInvestigate))
			{
				inv.GET("/edges/find", s.handleFindEdges)
				inv.GET("/edges/explain", s.handleExplainEdge)
			}

			// Admin: users / roles / projects.
			s.registerAdminRoutes(authed)
		}
	}

	return r
}

// --- data handlers ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "graph": s.graph != nil})
}

func (s *Server) handleStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	st, err := s.store.GetStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) handleEntities(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	kind := c.Query("kind")
	limit := queryInt(c, "limit", 200)
	ents, err := s.store.ListEntities(ctx, kind, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entities": ents})
}

func (s *Server) handleRuns(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	runs, err := s.store.ListRuns(ctx, queryInt(c, "limit", 50))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

func (s *Server) handleConflicts(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	conf, err := s.store.Conflicts(ctx, queryInt(c, "limit", 100))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"conflicts": conf})
}

func (s *Server) handleFindEdges(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	edges, err := s.store.FindEdges(ctx, c.Query("relation"), c.Query("like"), queryInt(c, "limit", 100))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"edges": edges})
}

func (s *Server) handleExplainEdge(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	id, err := strconv.ParseInt(c.Query("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing edge id"})
		return
	}
	d, err := s.store.ExplainEdge(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (s *Server) handleAttackPaths(c *gin.Context) {
	if s.graph == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "graph not available"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := s.graph.AttackPaths(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"paths": rows})
}

func (s *Server) handleAICalls(c *gin.Context) {
	if s.graph == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "graph not available"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := s.graph.ExternalAICalls(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"calls": rows})
}

// --- helpers ---

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
