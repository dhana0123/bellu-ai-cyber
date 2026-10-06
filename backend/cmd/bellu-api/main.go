// Command bellu-api is the HTTP control-panel server for the Bellu AI Security
// Graph. It exposes a read-only JSON API over the Postgres evidence store and
// the Memgraph graph for the React frontend.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bellu/bellu-ai-cyber/backend/server"
	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("bellu-api: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath := getenv("BELLU_CONFIG", "bellu.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	addr := getenv("BELLU_API_ADDR", ":8080")
	allowOrigin := getenv("BELLU_API_CORS_ORIGIN", "*")

	// Postgres is required.
	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer st.Close()

	// Memgraph is optional; the API degrades gracefully if it is down.
	var gw *graph.Writer
	if g, err := graph.Open(ctx, cfg.Memgraph.URI, cfg.Memgraph.Username, cfg.Memgraph.Password); err != nil {
		log.Printf("warning: graph unavailable (%v); graph endpoints disabled", err)
	} else {
		gw = g
		defer gw.Close(context.Background())
	}

	// Auth / RBAC: seed the catalog + default admin, then build the service.
	authStore := auth.NewStore(st.Pool())
	jwtSecret := getenv("BELLU_JWT_SECRET", "dev-insecure-change-me")
	adminEmail := getenv("BELLU_ADMIN_EMAIL", "admin@bellu.local")
	adminPassword := getenv("BELLU_ADMIN_PASSWORD", "admin12345")
	created, err := authStore.Seed(ctx, adminEmail, adminPassword, "Bellu")
	if err != nil {
		return fmt.Errorf("seed rbac: %w", err)
	}
	if created {
		log.Printf("seeded default admin user: %s", adminEmail)
	}
	authSvc := auth.NewService(authStore, auth.NewTokenManager(jwtSecret, 12*time.Hour))

	srv := server.New(server.Options{Store: st, Graph: gw, Auth: authSvc, AllowOrigin: allowOrigin})

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("bellu-api listening on %s (cors origin %s)", addr, allowOrigin)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
