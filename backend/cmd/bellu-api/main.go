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
	"strconv"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/bellu/bellu-ai-cyber/backend/server"
	"github.com/bellu/bellu-ai-cyber/internal/auth"
	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/connector"
	"github.com/bellu/bellu-ai-cyber/internal/connector/discovery"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	connectoraws "github.com/bellu/bellu-ai-cyber/internal/connector/provider/aws"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/source"
	"github.com/bellu/bellu-ai-cyber/internal/source/steampipe"
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

	// Cloud connector: register the AWS adapter (only phase-one provider) and
	// build the provider-agnostic manager. The AWS adapter authenticates via STS
	// using the default AWS credential chain for Bellu's own principal; the
	// per-connection assumed-role credentials it mints are never persisted.
	connMgr := buildConnector(st)

	srv := server.New(server.Options{Store: st, Graph: gw, Auth: authSvc, Connector: connMgr, AllowOrigin: allowOrigin})

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

// buildConnector wires the cloud-connector manager with the AWS adapter
// registered. The adapter uses the default AWS credential chain for Bellu's own
// principal to call STS AssumeRole; per-connection temporary credentials are
// held in memory only and never persisted. Steampipe connection parameters come
// from the environment with local-dev defaults.
func buildConnector(st *store.Store) *connector.Manager {
	reg := provider.NewRegistry()

	awsAdapter := connectoraws.New(connectoraws.Options{
		STS:               newSTSClient(),
		BelluPrincipalARN: os.Getenv("BELLU_CONNECTOR_PRINCIPAL_ARN"),
		SteampipeHost:     getenv("BELLU_STEAMPIPE_HOST", "steampipe"),
		SteampipePort:     atoiOr(os.Getenv("BELLU_STEAMPIPE_PORT"), 9193),
		SteampipeUser:     getenv("BELLU_STEAMPIPE_USER", "steampipe"),
		SteampipePassword: os.Getenv("BELLU_STEAMPIPE_PASSWORD"),
		SteampipeDB:       getenv("BELLU_STEAMPIPE_DB", "steampipe"),
		SteampipeConn:     getenv("BELLU_STEAMPIPE_CONNECTION", "aws"),
	})
	reg.Register(awsAdapter)

	repo := connector.NewRepository(st)
	disc := discovery.New(discovery.Options{
		Factory: steampipe.Factory,
		Runner:  connectorPipelineRunner{},
		Creds:   adapterCredentialer{adapter: awsAdapter},
	})
	return connector.NewManager(connector.Options{
		Repo:       repo,
		Registry:   reg,
		Discovery:  disc,
		ExternalID: connectoraws.GenerateExternalID,
	})
}

// newSTSClient builds an STS client from the default AWS config. On failure
// (e.g. no credentials locally) it returns nil; the AWS adapter still serves
// setup generation and validation, and STS-backed verify/discover report an
// authentication error at call time rather than panicking at startup.
func newSTSClient() connectoraws.STSAPI {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Printf("warning: AWS config unavailable (%v); connector STS calls will fail until configured", err)
		return nil
	}
	return sts.NewFromConfig(cfg)
}

// adapterCredentialer adapts the AWS adapter's GetCredentials/session into the
// discovery.Credentialer surface.
type adapterCredentialer struct{ adapter *connectoraws.Adapter }

func (a adapterCredentialer) Get(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	return a.adapter.GetCredentials(ctx, t)
}
func (a adapterCredentialer) Revoke(connID int64) { a.adapter.Revoke(connID) }

// connectorPipelineRunner runs the existing Bellu pipeline over a discovered
// source. The full ingest->normalize->resolve->sync wiring lives in the bellu
// CLI; here the runner drains the source so discovery completes. Wiring the
// complete pipeline is deferred to the pipeline-integration follow-up.
type connectorPipelineRunner struct{}

func (connectorPipelineRunner) Run(ctx context.Context, src source.Source, runID string) error {
	events, errs := src.Collect(ctx, source.Run{RunID: runID})
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
