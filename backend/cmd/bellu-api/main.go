// Command bellu-api is the HTTP control-panel server for the Bellu AI Security
// Graph. It exposes a read-only JSON API over the Postgres evidence store and
// the Memgraph graph for the React frontend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	connectorazure "github.com/bellu/bellu-ai-cyber/internal/connector/provider/azure"
	connectorci "github.com/bellu/bellu-ai-cyber/internal/connector/provider/ci"
	connectorcigithub "github.com/bellu/bellu-ai-cyber/internal/connector/provider/ci/github"
	connectorgcp "github.com/bellu/bellu-ai-cyber/internal/connector/provider/gcp"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/mcp"
	"github.com/bellu/bellu-ai-cyber/internal/pipeline"
	"github.com/bellu/bellu-ai-cyber/internal/redact"
	"github.com/bellu/bellu-ai-cyber/internal/source"
	"github.com/bellu/bellu-ai-cyber/internal/source/cloudquery"
	"github.com/bellu/bellu-ai-cyber/internal/store"
	"github.com/bellu/bellu-ai-cyber/internal/temporal"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/classify"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/infer"
	"github.com/bellu/bellu-ai-cyber/internal/temporal/replay"
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
	connMgr := buildConnector(st, gw, cfg)

	// Durability: any discovery session left 'running' by a prior crash/restart
	// cannot resume mid-flight, so mark it interrupted on startup. The UI shows
	// it as interrupted (not stuck) and offers a re-run.
	if n, err := connMgr.ReconcileInterrupted(ctx); err != nil {
		log.Printf("warning: reconcile interrupted sessions: %v", err)
	} else if n > 0 {
		log.Printf("reconciled %d interrupted discovery session(s) from a prior restart", n)
	}

	// Scheduled re-discovery (opt-in): keeps the inventory, attack-path graph and
	// Inspector vulnerability findings fresh without a manual "discover".
	startDiscoveryScheduler(ctx, st, connMgr)

	// MCP read surface: six tenant-scoped tools over the temporal graph. The
	// Querier reads the two append-only logs (history + recent changes) and the
	// graph writer backs the projection reads (inventory, detail, adjacency).
	// The MCP server resolves its org from the authenticated principal at the
	// mount, never from a client-supplied value, and is served only behind the
	// existing JWT + RBAC router (graph:read). gw may be nil (Memgraph down):
	// the log-backed tools still work and the projection-backed tools report the
	// projection is unavailable.
	mcpSrv := mcp.NewServer(temporal.NewQuerier(st), gw)

	srv := server.New(server.Options{Store: st, Graph: gw, Auth: authSvc, Connector: connMgr, MCP: mcpSrv, AllowOrigin: allowOrigin})

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

// startDiscoveryScheduler launches the background re-scan scheduler when
// BELLU_DISCOVERY_INTERVAL is set to a Go duration (e.g. "6h", "24h"). It is
// off by default: scanning customer accounts on a timer is a deliberate choice,
// and each scan costs AWS API calls. Values below the scheduler minimum are
// clamped up; "", "0" and "off" disable it. It stops with ctx on shutdown.
func startDiscoveryScheduler(ctx context.Context, st *store.Store, mgr *connector.Manager) {
	raw := strings.TrimSpace(os.Getenv("BELLU_DISCOVERY_INTERVAL"))
	switch strings.ToLower(raw) {
	case "", "0", "off", "false", "disabled":
		log.Printf("discovery scheduler disabled (set BELLU_DISCOVERY_INTERVAL, e.g. 6h, to enable)")
		return
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		log.Printf("warning: invalid BELLU_DISCOVERY_INTERVAL %q (%v); discovery scheduler disabled", raw, err)
		return
	}

	repo := connector.NewRepository(st)
	sched, err := connector.NewScheduler(connector.SchedulerOptions{
		Interval:   interval,
		MaxPerTick: atoiOr(os.Getenv("BELLU_DISCOVERY_MAX_CONCURRENT"), 2),
		Due:        repo.ListDueForDiscovery,
		Run: func(ctx context.Context, orgID, id int64) error {
			_, err := mgr.Discover(ctx, orgID, id)
			return err
		},
	})
	if err != nil {
		log.Printf("warning: discovery scheduler not started: %v", err)
		return
	}
	go sched.Run(ctx)
}

// buildConnector wires the cloud-connector manager with the AWS adapter
// registered. The adapter uses the default AWS credential chain for Bellu's own
// principal to call STS AssumeRole; per-connection temporary credentials are
// held in memory only and never persisted. Discovery collects through a
// per-run CloudQuery job: the AWS adapter's Discover emits the cloudquery.Factory
// config and the session-only credential env, and the Factory wires the CLI
// JobRunner.
func buildConnector(st *store.Store, gw *graph.Writer, cfg config.Config) *connector.Manager {
	reg := provider.NewRegistry()

	// CloudQuery staging DSN: the CloudQuery sync writes its cq_staging
	// destination into the customer PostgreSQL (Requirement 16.3), the same
	// database the rest of Bellu uses, and the cloudquery.Source reads the
	// staging rows back from it. BELLU_CQ_STAGING_DSN overrides it for a split
	// deployment; it defaults to the primary Postgres DSN.
	cqStagingDSN := getenv("BELLU_CQ_STAGING_DSN", cfg.Postgres.DSN)

	awsAdapter := connectoraws.New(connectoraws.Options{
		STS:               newSTSClient(),
		Templates:         newTemplateStore(),
		BelluPrincipalARN: os.Getenv("BELLU_CONNECTOR_PRINCIPAL_ARN"),
		CQStagingDSN:      cqStagingDSN,
		CQConnection:      getenv("BELLU_CQ_CONNECTION", "aws"),
	})
	reg.Register(awsAdapter)

	// Additional cloud providers (Azure, GCP) share the CloudProvider interface
	// and the CloudQuery discovery path (azure.yaml / gcp.yaml mappings). They
	// register in the same registry with zero Manager changes and reuse the same
	// cq_staging DSN as AWS; the per-connection credentials ride on the discovery
	// plan's credential env, never persisted.
	reg.Register(connectorazure.New(connectorazure.Options{
		CQStagingDSN: cqStagingDSN, CQConnection: "azure",
	}))
	reg.Register(connectorgcp.New(connectorgcp.Options{
		CQStagingDSN: cqStagingDSN, CQConnection: "gcp",
	}))

	// CI/VCS providers (GitHub, GitLab, Atlassian). GitHub uses a GitHub App
	// (installation flow + short-lived installation tokens); GitLab/Atlassian
	// use read-only tokens. They register in the same registry and surface in
	// the same Settings page.
	ghAPIBase := getenv("BELLU_GITHUB_API_BASE", "https://api.github.com")
	ghClient, ghSlug := newGitHubAppClient(ghAPIBase)
	if ghClient != nil {
		// App config present: the adapter can list the installation's repos via
		// the GitHub REST API, and the Manager can verify installations.
		reg.Register(connectorci.NewGitHubApp(ghAPIBase, ghClient))
	} else {
		reg.Register(connectorci.NewGitHub(ghAPIBase))
	}
	reg.Register(connectorci.NewGitLab(os.Getenv("BELLU_GITLAB_API_BASE")))
	reg.Register(connectorci.NewAtlassian(os.Getenv("BELLU_ATLASSIAN_API_BASE")))

	repo := connector.NewRepository(st)

	// Build the shared normalize->resolve->persist processor and a redactor so
	// discovery output lands in normalized_entities (what the inventory reads).
	// Optional rule/mapping files are loaded by path with defaults when absent.
	proc := pipeline.NewProcessor(
		"internal/resolve/mappings.yaml",
		"internal/normalize/aiagent/rules.yaml",
	)
	// Temporal change engine (observed lane): runs inside the pipeline after
	// normalize+resolve and before the Memgraph projection, appending
	// observed_events for every ingest so the inventory becomes an event-sourced
	// temporal graph. It diffs each run against the current projection cache
	// (normalized_entities/normalized_edges). The engine shares the same store.
	proc.WithEngine(temporal.NewEngine(st))

	// Derived lane (inference + classification): after the change engine records
	// the observed facts, the inference engine emits cross-source edges and the
	// rule-based classifier labels datastores, both from observed/normalized data
	// only. Their output is written to the derived_events log and overlaid onto
	// the Memgraph projection by the replay Rebuilder (inferred edges carry
	// inferred=true + confidence + evidence; classified nodes get the three
	// namespaced data_class/data_class_confidence/classified_by properties).
	proc.WithDerived(
		infer.NewEngine(infer.DefaultRules()...),
		classify.NewRuleClassifier(),
	)
	// The replay Rebuilder is the single write path from the two logs to the
	// Memgraph projection. When the graph is unavailable it is left nil and the
	// overlay is skipped (the facts remain in the logs, recoverable later).
	if gw != nil {
		proc.WithRebuilder(replay.NewRebuilder(st, gw))
	}
	redactor, rerr := redact.New(cfg.Redaction.KeyPatterns, cfg.Redaction.JSONPaths)
	if rerr != nil {
		log.Printf("warning: redaction disabled (%v)", rerr)
		redactor = nil
	}
	runner := connectorPipelineRunner{store: st, graph: gw, proc: proc, redactor: redactor}

	// Discovery runs on CloudQuery now that the Steampipe/CloudQuery parity
	// tests are green (task 5, Property 5). The orchestrator builds the source
	// via cloudquery.Factory: the AWS adapter's Discover emits the Factory
	// config (staging DSN + mapping selector + capabilities + per-run credential
	// env), and the Factory wires the production CLI JobRunner that launches
	// `cloudquery sync` before any staging row is read. No credential sink is
	// needed: the STS session credentials ride on the discovery plan's
	// credential_env to the JobRunner's subprocess environment and are discarded
	// after the run on every path, so nothing is written to disk (contrast the
	// Steampipe `.spc` sink this replaces).
	disc := discovery.New(discovery.Options{
		Factory: cloudquery.Factory,
		Runner:  runner,
		Creds:   adapterCredentialer{adapter: awsAdapter},
	})
	// The Manager's GitHubClient interface needs only VerifyInstallation, which
	// the concrete client satisfies. Pass nil when App config is absent so
	// BeginGitHubInstall still works in dev but CompleteGitHubInstall returns a
	// clear "not configured" error.
	var mgrGitHub connector.GitHubAppClient
	if ghClient != nil {
		mgrGitHub = ghClient
	}
	return connector.NewManager(connector.Options{
		Repo:                repo,
		Registry:            reg,
		Discovery:           disc,
		ExternalID:          connectoraws.GenerateExternalID,
		GitHubClient:        mgrGitHub,
		GitHubAppSlug:       ghSlug,
		GitHubWebhookSecret: os.Getenv("BELLU_GITHUB_WEBHOOK_SECRET"),
	})
}

// newGitHubAppClient builds the GitHub App client from the environment, and
// returns it with the configured App slug. It returns (nil, slug) when App
// config is incomplete so the rest of the connector stays dev-safe. The private
// key is read from BELLU_GITHUB_APP_PRIVATE_KEY_PATH (a file) or, as a fallback,
// inline PEM in BELLU_GITHUB_APP_PRIVATE_KEY_PEM. No secret is ever logged.
func newGitHubAppClient(apiBase string) (*connectorcigithub.GitHubAppClient, string) {
	slug := os.Getenv("BELLU_GITHUB_APP_SLUG")
	appID := atoiOr(os.Getenv("BELLU_GITHUB_APP_ID"), 0)
	if appID <= 0 {
		log.Printf("notice: BELLU_GITHUB_APP_ID unset; GitHub App integration disabled")
		return nil, slug
	}
	var pem []byte
	if path := strings.TrimSpace(os.Getenv("BELLU_GITHUB_APP_PRIVATE_KEY_PATH")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Printf("warning: GitHub App private key unreadable (%v); GitHub App disabled", err)
			return nil, slug
		}
		pem = b
	} else if inline := os.Getenv("BELLU_GITHUB_APP_PRIVATE_KEY_PEM"); inline != "" {
		pem = []byte(inline)
	} else {
		log.Printf("notice: GitHub App private key unset; GitHub App integration disabled")
		return nil, slug
	}
	client, err := connectorcigithub.New(int64(appID), pem, apiBase, nil)
	if err != nil {
		log.Printf("warning: GitHub App client init failed (%v); GitHub App disabled", err)
		return nil, slug
	}
	log.Printf("GitHub App integration enabled (app id %d, slug %q)", appID, slug)
	return client, slug
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
// connectorPipelineRunner drives a discovery source through the real pipeline:
// it ingests collected RawEvents into Postgres (raw_events), then normalizes,
// resolves, and persists them into normalized_entities/edges, and finally syncs
// the resolved graph into Memgraph when available. This is what makes
// discovered cloud resources appear in the inventory (GET /api/entities reads
// normalized_entities). Previously this runner discarded events, so discovery
// completed but produced nothing queryable.
type connectorPipelineRunner struct {
	store    *store.Store
	graph    *graph.Writer // may be nil; graph sync is skipped when nil
	proc     *pipeline.Processor
	redactor *redact.Redactor
}

func (r connectorPipelineRunner) Run(ctx context.Context, src source.Source, runID string, orgID, projectID int64, reporter source.ProgressReporter) error {
	// 1) Ingest: record the run and stream collected events into Postgres,
	// redacting payloads first. We consume Collect directly (rather than via
	// pipeline.Pipeline) so the progress reporter stays wired to the hub.
	if err := r.store.StartRun(ctx, runID, "cloudquery"); err != nil {
		return fmt.Errorf("start run: %w", err)
	}
	sink := store.NewSink(r.store, runID)

	collected := 0
	tableWarnings := 0
	events, errs := src.Collect(ctx, source.Run{RunID: runID, Reporter: reporter})
	var runErr error
drain:
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break drain
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			collected++
			out := ev
			if r.redactor != nil {
				red, rerr := r.redactor.Apply(out)
				if rerr == nil {
					out = red
					out.Finalize() // recompute hash/id after payload rewrite
				}
			}
			if err := sink.Write(ctx, out); err != nil {
				// Non-fatal: record and keep going so one bad row doesn't abort.
				log.Printf("discovery %s: write event: %v", runID, err)
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err == nil {
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// A cancellation/timeout of the whole run is fatal.
				runErr = err
				continue
			}
			// Per-table errors (e.g. a service unavailable in a region, like
			// Bedrock's 404, or an AccessDenied on one table) are NON-FATAL
			// warnings: the collector already isolated them and moved to the next
			// table, so we just count + log and keep processing everything else.
			// One bad table must never fail the whole discovery.
			tableWarnings++
			log.Printf("discovery %s: table error (continuing): %v", runID, err)
		}
	}
	_ = sink.Flush(ctx)
	_ = r.store.FinishRun(context.Background(), runID, sink.Status(), sink.Written())

	// Only a hard run-level failure (cancel/timeout) aborts. Per-table warnings
	// do NOT fail the run — we still normalize and persist what WAS collected.
	if runErr != nil {
		return runErr
	}
	if tableWarnings > 0 {
		log.Printf("discovery %s: completed with %d table warning(s) — persisting collected data anyway",
			runID, tableWarnings)
	}

	// 2) Normalize -> resolve -> derive-agents -> persist.
	// We re-normalize ALL raw events, not just this run's. Raw events are
	// content-addressed and de-duplicated on insert (ON CONFLICT DO NOTHING),
	// so a re-scan of an unchanged account inserts few/no new rows — loading
	// only this run's events would then re-normalize almost nothing and never
	// refresh entities/edges. Loading everything rebuilds the full graph each
	// run; PersistNormalized upserts, so this is idempotent.
	raw, err := r.store.LoadRawEvents(ctx, "")
	if err != nil {
		return fmt.Errorf("load raw events: %w", err)
	}
	// "cloudquery" is the collection source label (same as StartRun above); it is
	// what the temporal change engine keys entity_assertions / reference-counted
	// deletion on, and what the normalize registry dispatches on.
	pres, err := r.proc.Process(ctx, r.store, raw, orgID, runID, "cloudquery")
	if err != nil {
		return err
	}
	log.Printf("discovery %s: collected=%d entities=%d edges=%d agents=%d changes=%d",
		runID, collected, pres.Entities, pres.Edges, pres.Agents, len(pres.Changes))

	// Tag the discovered entities with the connection's project so inventory can
	// be scoped per project. Best-effort: a tagging failure must not fail the
	// run (the entities are already persisted and visible org-wide).
	if projectID > 0 {
		if err := r.store.SaveEntityProjects(ctx, projectID, pres.CanonicalIDs); err != nil {
			log.Printf("discovery %s: tag project %d failed (non-fatal): %v", runID, projectID, err)
		}
	}

	// 3) Project to Memgraph (optional; inventory reads from Postgres so this is
	// best-effort and never fails the discovery run). The change engine's output
	// (pres.Changes) is the authoritative input for the temporal replay/overlay
	// projection; wiring it into the incremental ApplyRun path is task 11.1/16.1.
	// For now the projection step receives the changes and the base graph sync
	// keeps running so the inventory stays current.
	if r.graph != nil {
		if ne, nr, serr := r.proc.Sync(ctx, r.store, r.graph, orgID, pres.Changes, pres.DerivedChanges); serr != nil {
			log.Printf("discovery %s: graph sync failed (non-fatal): %v", runID, serr)
		} else {
			log.Printf("discovery %s: graph synced entities=%d edges=%d derived=%d",
				runID, ne, nr, len(pres.DerivedChanges))
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
