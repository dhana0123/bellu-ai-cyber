// Command lab is the Bellu Cloud Lab Test_Runner. It provisions a scenario with
// Terraform, runs the unmodified `bellu run` pipeline, compares the resulting
// graph against the scenario's scenario.yaml contract, and tears everything
// down — always. It is a consumer of Bellu and modifies nothing in it.
//
// Usage:
//
//	lab --scenario <provider>/<name> --mode <up|test|down> [flags]
//
// Flags (with env fallbacks so no secrets live in files):
//
//	--lab-root     path to bellu-cloud-lab/ (default ".")
//	--terraform    terraform binary (default "terraform")
//	--bellu        path to the built bellu binary (default "bin/bellu")
//	--config       bellu.yaml path for `bellu run` (default "bellu.yaml")
//	--api          control-panel API base URL ($BELLU_API_URL or http://localhost:8080)
//	--token        API bearer token ($BELLU_API_TOKEN); or --email/--password to log in
//	--postgres-dsn customer Postgres DSN fallback ($BELLU_POSTGRES_DSN)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/contract"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/graphquery"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/report"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/runner"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet("lab", flag.ContinueOnError)
	scenario := fs.String("scenario", "", "scenario id <provider>/<name> (e.g. aws/public-ec2)")
	modeStr := fs.String("mode", "test", "up | test | down")
	labRoot := fs.String("lab-root", ".", "path to the bellu-cloud-lab/ directory")
	tfBin := fs.String("terraform", "terraform", "terraform binary")
	belluBin := fs.String("bellu", "bin/bellu", "path to the built bellu binary")
	cfg := fs.String("config", "bellu.yaml", "bellu.yaml path for bellu run")
	apiURL := fs.String("api", envOr("BELLU_API_URL", "http://localhost:8080"), "control-panel API base URL")
	token := fs.String("token", os.Getenv("BELLU_API_TOKEN"), "API bearer token")
	email := fs.String("email", os.Getenv("BELLU_ADMIN_EMAIL"), "API login email (if no token)")
	password := fs.String("password", os.Getenv("BELLU_ADMIN_PASSWORD"), "API login password (if no token)")
	pgDSN := fs.String("postgres-dsn", os.Getenv("BELLU_POSTGRES_DSN"), "customer Postgres DSN (fallback)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *scenario == "" {
		fmt.Fprintln(os.Stderr, "lab: --scenario <provider>/<name> is required")
		os.Exit(2)
	}
	mode, err := parseMode(*modeStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lab:", err)
		os.Exit(2)
	}

	provider, name, ok := strings.Cut(*scenario, "/")
	if !ok || provider == "" || name == "" {
		fmt.Fprintf(os.Stderr, "lab: invalid --scenario %q (want <provider>/<name>)\n", *scenario)
		os.Exit(2)
	}
	scenarioDir := filepath.Join(*labRoot, provider, "scenarios", name)
	contractPath := filepath.Join(scenarioDir, "scenario.yaml")

	// Load + validate the contract BEFORE provisioning (Requirement 3.7).
	sc, err := contract.Load(contractPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lab:", err)
		os.Exit(1)
	}

	// Build the graph client (API-first, Postgres fallback). down/up modes
	// don't need it, so only resolve a client for test mode.
	var graph graphquery.Client
	if mode == runner.ModeTest {
		graph = resolveGraphClient(ctx, *apiURL, *token, *email, *password, *pgDSN)
	}

	r := &runner.Runner{
		TerraformBin:     *tfBin,
		BelluBin:         *belluBin,
		BelluConfig:      *cfg,
		Sources:          "steampipe",
		Graph:            graph,
		ReadinessTimeout: runner.DefaultReadinessTimeout,
		PipelineTimeout:  runner.DefaultPipelineTimeout,
		Out:              os.Stderr,
	}

	res := r.Run(ctx, mode, scenarioDir, sc)
	printResult(res)
	os.Exit(res.ExitCode())
}

func parseMode(s string) (runner.Mode, error) {
	switch s {
	case "test":
		return runner.ModeTest, nil
	case "up":
		return runner.ModeUp, nil
	case "down":
		return runner.ModeDown, nil
	default:
		return 0, fmt.Errorf("invalid --mode %q (want up|test|down)", s)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveGraphClient prefers the control-panel API (logging in if no token was
// supplied) and falls back to a direct Postgres reader. Returns nil only when
// neither is reachable; the runner reports that as a compare failure.
func resolveGraphClient(ctx context.Context, apiURL, token, email, password, pgDSN string) graphquery.Client {
	if token == "" && email != "" && password != "" {
		if t, err := login(ctx, apiURL, email, password); err == nil {
			token = t
		} else {
			fmt.Fprintf(os.Stderr, "lab: API login failed (%v); will try Postgres fallback\n", err)
		}
	}
	var clients []graphquery.Client
	if token != "" {
		clients = append(clients, graphquery.NewAPIClient(apiURL, token))
	}
	if pgDSN != "" {
		clients = append(clients, graphquery.NewPostgresReader(pgDSN))
	}
	return graphquery.Selecting(ctx, clients...)
}

// login posts to /api/auth/login and returns the bearer token.
func login(ctx context.Context, apiURL, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login status %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("login response had no token")
	}
	return out.Token, nil
}

// printResult emits a human-readable summary of the scenario outcome.
func printResult(res *report.Result) {
	fmt.Fprintf(os.Stderr, "\n=== %s: %s ===\n", res.Scenario, res.Status)
	if res.Err != "" {
		fmt.Fprintf(os.Stderr, "  phase=%s reason=%s: %s\n", res.Phase, res.FailureReason, res.Err)
	}
	for _, n := range res.MissingNodes {
		fmt.Fprintf(os.Stderr, "  missing node:  kind=%s name=%s\n", n.Kind, n.Name)
	}
	for _, e := range res.MissingEdges {
		fmt.Fprintf(os.Stderr, "  missing edge:  %s %s->%s (min_conf %.2f)\n", e.Relation, e.From, e.To, e.MinConfidence)
	}
	for _, p := range res.MissingPaths {
		fmt.Fprintf(os.Stderr, "  missing path:  %s\n", p.Detail)
	}
	if res.ResourcesMayRemain {
		fmt.Fprintln(os.Stderr, "  WARNING: terraform destroy failed — cloud resources may remain provisioned")
	}
	if len(res.Timings) > 0 {
		fmt.Fprint(os.Stderr, "  timings:")
		for _, ph := range []string{report.PhaseApply, report.PhaseReady, report.PhasePipeline, report.PhaseCompare, report.PhaseDestroy} {
			if d, ok := res.Timings[ph]; ok {
				fmt.Fprintf(os.Stderr, " %s=%s", ph, d.Round(time.Millisecond))
			}
		}
		fmt.Fprintln(os.Stderr)
	}
}
