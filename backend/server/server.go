// Package server implements the Bellu control-panel HTTP API. It is a thin
// read layer over the existing Postgres evidence store and the Memgraph graph,
// reusing internal/store and internal/graph. It serves JSON for the React UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// Server holds the dependencies for the control-panel API.
type Server struct {
	store    *store.Store
	graph    *graph.Writer // may be nil if Memgraph is unavailable
	allowOrigin string
}

// Options configures the server.
type Options struct {
	Store       *store.Store
	Graph       *graph.Writer
	AllowOrigin string // CORS allow-origin, e.g. "*" or a specific host
}

// New builds a Server.
func New(opts Options) *Server {
	origin := opts.AllowOrigin
	if origin == "" {
		origin = "*"
	}
	return &Server{store: opts.Store, graph: opts.Graph, allowOrigin: origin}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/entities", s.handleEntities)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/conflicts", s.handleConflicts)
	mux.HandleFunc("/api/edges/find", s.handleFindEdges)
	mux.HandleFunc("/api/edges/explain", s.handleExplainEdge)
	mux.HandleFunc("/api/graph/attack-paths", s.handleAttackPaths)
	mux.HandleFunc("/api/graph/ai-calls", s.handleAICalls)
	return s.withMiddleware(mux)
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]any{"status": "ok", "graph": s.graph != nil}
	s.writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	st, err := s.store.GetStats(ctx)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleEntities(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	kind := r.URL.Query().Get("kind")
	limit := queryInt(r, "limit", 200)
	ents, err := s.store.ListEntities(ctx, kind, limit)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"entities": ents})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runs, err := s.store.ListRuns(ctx, queryInt(r, "limit", 50))
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	c, err := s.store.Conflicts(ctx, queryInt(r, "limit", 100))
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"conflicts": c})
}

func (s *Server) handleFindEdges(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	relation := r.URL.Query().Get("relation")
	like := r.URL.Query().Get("like")
	edges, err := s.store.FindEdges(ctx, relation, like, queryInt(r, "limit", 100))
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

func (s *Server) handleExplainEdge(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, errors.New("invalid or missing edge id"))
		return
	}
	d, err := s.store.ExplainEdge(ctx, id)
	if err != nil {
		s.writeErr(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleAttackPaths(w http.ResponseWriter, r *http.Request) {
	if s.graph == nil {
		s.writeErr(w, http.StatusServiceUnavailable, errors.New("graph not available"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rows, err := s.graph.AttackPaths(ctx)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"paths": rows})
}

func (s *Server) handleAICalls(w http.ResponseWriter, r *http.Request) {
	if s.graph == nil {
		s.writeErr(w, http.StatusServiceUnavailable, errors.New("graph not available"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rows, err := s.graph.ExternalAICalls(ctx)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"calls": rows})
}

// --- middleware + helpers ---

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.allowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, code int, err error) {
	s.writeJSON(w, code, map[string]string{"error": err.Error()})
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
