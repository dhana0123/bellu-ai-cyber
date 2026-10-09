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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/jackc/pgx/v5"

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
// steampipeConfigSink returns a discovery CredentialSink that writes a
// per-session Steampipe AWS connection file into the shared config dir, telling
// Steampipe to assume the customer role with the session's temporary
// credentials. The cleanup restores the default config when the session ends.
// When dir is empty (no shared volume configured), it is a no-op and discovery
// relies on whatever credentials the Steampipe container already has.
func steampipeConfigSink(dir string) discovery.CredentialSink {
	if dir == "" {
		return nil
	}
	return func(env map[string]string) (func(), error) {
		// Each discovery session gets its OWN connection + config file, named
		// after the connection, so concurrent discoveries never overwrite each
		// other's credentials. The session's temporary credentials already grant
		// customer-account access (Bellu performed STS AssumeRole), so Steampipe
		// authenticates as the assumed role directly.
		conn := env["BELLU_STEAMPIPE_CONNECTION"]
		if conn == "" {
			conn = "aws"
		}
		spc := fmt.Sprintf(`connection %q {
  plugin        = "ghcr.io/turbot/steampipe/plugins/turbot/aws@latest"
  regions       = ["*"]
  access_key    = %q
  secret_key    = %q
  session_token = %q
}
`, conn, env["AWS_ACCESS_KEY_ID"], env["AWS_SECRET_ACCESS_KEY"], env["AWS_SESSION_TOKEN"])
		path := filepath.Join(dir, conn+".spc")
		// Share the file with the steampipe process (a different uid) via the
		// common root group (gid 0) that owns the shared config volume, rather
		// than making the credential file world-readable. This is the
		// least-privilege pattern for cross-container volume sharing: group
		// ownership + group-read, no world access, neither container as root.
		if err := os.WriteFile(path, []byte(spc), 0o640); err != nil {
			return nil, fmt.Errorf("write steampipe config: %w", err)
		}
		// Force mode (umask-independent) and set the group to 0 so steampipe,
		// which runs in gid 0, can read it. Chown to gid 0 works because
		// bellu-api is a member of group 0 (group_add in compose).
		_ = os.Chmod(path, 0o640)
		_ = os.Chown(path, -1, 0)
		// Steampipe hot-reloads config, but a new connection becomes queryable
		// only after the plugin attaches its schema. Wait (bounded) until a
		// representative table in the connection resolves, so discovery does not
		// race ahead and hit "relation does not exist".
		waitForSteampipeConnection(conn, 60*time.Second)
		// Cleanup deletes the per-session config so credentials never linger.
		cleanup := func() { _ = os.Remove(path) }
		return cleanup, nil
	}
}

// waitForSteampipeConnection polls the Steampipe service until a representative
// table in the connection's schema is queryable (or the timeout elapses).
// Steampipe reloads config asynchronously and the connection schema exists
// slightly before its tables resolve, so this probes an actual table
// (to_regclass) rather than just information_schema.schemata.
func waitForSteampipeConnection(schema string, timeout time.Duration) {
	host := getenv("BELLU_STEAMPIPE_HOST", "steampipe")
	port := getenv("BELLU_STEAMPIPE_PORT", "9193")
	user := getenv("BELLU_STEAMPIPE_USER", "steampipe")
	pass := os.Getenv("BELLU_STEAMPIPE_PASSWORD")
	db := getenv("BELLU_STEAMPIPE_DB", "steampipe")
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, db)
	probe := schema + ".aws_account"

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			var ready bool
			qerr := conn.QueryRow(context.Background(),
				`SELECT to_regclass($1) IS NOT NULL`, probe).Scan(&ready)
			conn.Close(context.Background())
			if qerr == nil && ready {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func buildConnector(st *store.Store) *connector.Manager {
	reg := provider.NewRegistry()

	awsAdapter := connectoraws.New(connectoraws.Options{
		STS:               newSTSClient(),
		Templates:         newTemplateStore(),
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
		Sink:    steampipeConfigSink(getenv("BELLU_STEAMPIPE_CONFIG_DIR", "")),
	})
	return connector.NewManager(connector.Options{
		Repo:       repo,
		Registry:   reg,
		Discovery:  disc,
		ExternalID: connectoraws.GenerateExternalID,
	})
}

// newTemplateStore builds the S3-backed CloudFormation template store from the
// environment. When BELLU_CONNECTOR_TEMPLATE_BUCKET is unset (local dev), it
// returns nil: setup still works and the customer downloads the template, but
// no one-click launch link is produced. The presigned-link lifetime defaults to
// one hour.
func newTemplateStore() connectoraws.TemplateStore {
	bucket := strings.TrimSpace(os.Getenv("BELLU_CONNECTOR_TEMPLATE_BUCKET"))
	if bucket == "" {
		log.Printf("notice: BELLU_CONNECTOR_TEMPLATE_BUCKET unset; connector launch links disabled (download only)")
		return nil
	}
	region := getenv("BELLU_CONNECTOR_TEMPLATE_REGION", getenv("AWS_REGION", "us-east-1"))
	ttl := time.Duration(atoiOr(os.Getenv("BELLU_CONNECTOR_LINK_TTL_MIN"), 60)) * time.Minute
	store, err := connectoraws.NewS3TemplateStore(context.Background(), bucket, region, ttl)
	if err != nil {
		log.Printf("warning: template store unavailable (%v); connector launch links disabled", err)
		return nil
	}
	log.Printf("connector template store: s3://%s (region %s, links valid %s)", bucket, region, ttl)
	return store
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
