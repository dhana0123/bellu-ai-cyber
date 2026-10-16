// Package runner orchestrates a scenario: terraform apply -> readiness poll ->
// bellu run -> graph query -> compare to contract -> terraform destroy. Teardown
// is unconditional (Requirement 10.6): destroy runs on apply failure, readiness
// timeout, pipeline failure, PASS, and FAIL. The only non-zero-with-resources-
// remaining exit is a destroy failure (Requirement 10.8).
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/contract"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/graphquery"
	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/report"
)

// Default timeouts from the design (Requirements 10.1, 10.4).
const (
	DefaultReadinessTimeout = 600 * time.Second
	DefaultPipelineTimeout  = 900 * time.Second
)

// Runner carries the external tools and the graph client the orchestration
// needs. It never modifies Bellu; it shells out to terraform and the bellu
// binary and reads the graph through graphquery.Client.
type Runner struct {
	// TerraformBin is the terraform executable (default "terraform").
	TerraformBin string
	// BelluBin is the path to the built bellu binary (default "bellu").
	BelluBin string
	// BelluConfig is the --config path passed to bellu run.
	BelluConfig string
	// Sources is the --sources value for bellu run (default "steampipe").
	Sources string
	// Graph is the API-first / Postgres-fallback graph reader for comparison.
	Graph graphquery.Client

	ReadinessTimeout time.Duration
	PipelineTimeout  time.Duration

	// Out receives human-readable progress; defaults to os.Stderr.
	Out *os.File
}

// Mode selects which phases Run executes.
type Mode int

const (
	// ModeTest runs the full cycle: apply -> run -> compare -> destroy.
	ModeTest Mode = iota
	// ModeUp provisions only (no assertions, no destroy).
	ModeUp
	// ModeDown destroys only.
	ModeDown
)

func (r *Runner) terraformBin() string {
	if r.TerraformBin != "" {
		return r.TerraformBin
	}
	return "terraform"
}

func (r *Runner) belluBin() string {
	if r.BelluBin != "" {
		return r.BelluBin
	}
	return "bellu"
}

func (r *Runner) out() *os.File {
	if r.Out != nil {
		return r.Out
	}
	return os.Stderr
}

func (r *Runner) logf(format string, a ...any) {
	fmt.Fprintf(r.out(), format+"\n", a...)
}

// Run executes the scenario in dir (the scenario's root Terraform module) with
// the already-loaded contract sc. The chosen mode controls which phases run.
func (r *Runner) Run(ctx context.Context, mode Mode, dir string, sc contract.Scenario) *report.Result {
	res := report.New(sc.Name)

	switch mode {
	case ModeDown:
		r.destroy(ctx, dir, res)
		if !res.ResourcesMayRemain {
			res.Status = report.StatusPass
		}
		return res
	case ModeUp:
		if !r.apply(ctx, dir, res) {
			return res
		}
		if !r.waitReady(ctx, dir, res) {
			r.destroy(ctx, dir, res)
			return res
		}
		res.Status = report.StatusPass // provisioned; no assertions in up mode
		return res
	}

	// ModeTest: full cycle with unconditional teardown.
	defer r.destroy(ctx, dir, res)

	if !r.apply(ctx, dir, res) {
		return res
	}
	if !r.waitReady(ctx, dir, res) {
		return res
	}
	if !r.runPipeline(ctx, res) {
		return res
	}
	r.compareGraph(ctx, sc, res)
	return res
}

// apply runs `terraform init` + `terraform apply -auto-approve`.
func (r *Runner) apply(ctx context.Context, dir string, res *report.Result) bool {
	res.Phase = report.PhaseApply
	start := time.Now()
	r.logf("==> terraform init/apply (%s)", dir)
	if err := r.tf(ctx, dir, "init", "-input=false"); err != nil {
		res.FailureReason = report.ReasonApply
		res.Err = fmt.Sprintf("terraform init: %v", err)
		return false
	}
	if err := r.tf(ctx, dir, "apply", "-auto-approve", "-input=false"); err != nil {
		res.FailureReason = report.ReasonApply
		res.Err = fmt.Sprintf("terraform apply: %v", err)
		res.Timings[report.PhaseApply] = time.Since(start)
		return false
	}
	res.Timings[report.PhaseApply] = time.Since(start)
	return true
}

// waitReady polls provisioned-resource readiness. In this implementation,
// `terraform apply` already blocks until resources are created, so readiness is
// confirmed by a successful `terraform output` within the readiness budget.
func (r *Runner) waitReady(ctx context.Context, dir string, res *report.Result) bool {
	res.Phase = report.PhaseReady
	timeout := r.ReadinessTimeout
	if timeout == 0 {
		timeout = DefaultReadinessTimeout
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := r.tf(ctx, dir, "output", "-json"); err != nil {
		if ctx.Err() != nil {
			res.FailureReason = report.ReasonTimeout
			res.Err = fmt.Sprintf("readiness timeout after %s", timeout)
		} else {
			res.FailureReason = report.ReasonApply
			res.Err = fmt.Sprintf("readiness check: %v", err)
		}
		res.Timings[report.PhaseReady] = time.Since(start)
		return false
	}
	res.Timings[report.PhaseReady] = time.Since(start)
	return true
}

// runPipeline invokes `bellu run` with a bounded budget (Requirement 10.4).
func (r *Runner) runPipeline(ctx context.Context, res *report.Result) bool {
	res.Phase = report.PhasePipeline
	timeout := r.PipelineTimeout
	if timeout == 0 {
		timeout = DefaultPipelineTimeout
	}
	sources := r.Sources
	if sources == "" {
		sources = "steampipe"
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"run", "--sources", sources}
	if r.BelluConfig != "" {
		args = append(args, "--config", r.BelluConfig)
	}
	r.logf("==> bellu %v", args)
	cmd := exec.CommandContext(ctx, r.belluBin(), args...)
	cmd.Stdout = r.out()
	cmd.Stderr = r.out()
	err := cmd.Run()
	res.Timings[report.PhasePipeline] = time.Since(start)
	if err != nil {
		res.FailureReason = report.ReasonPipeline
		if ctx.Err() != nil {
			res.Err = fmt.Sprintf("bellu run timeout after %s", timeout)
		} else {
			res.Err = fmt.Sprintf("bellu run: %v", err)
		}
		return false
	}
	return true
}

// compareGraph queries the graph and compares it against the contract.
func (r *Runner) compareGraph(ctx context.Context, sc contract.Scenario, res *report.Result) {
	res.Phase = report.PhaseCompare
	start := time.Now()
	if r.Graph == nil {
		res.FailureReason = report.ReasonCompare
		res.Err = "no graph client available (API and Postgres both unreachable)"
		return
	}
	nodes, err := r.Graph.Nodes(ctx, "")
	if err != nil {
		res.FailureReason = report.ReasonCompare
		res.Err = fmt.Sprintf("query nodes: %v", err)
		return
	}
	edges, err := r.Graph.Edges(ctx, "")
	if err != nil {
		res.FailureReason = report.ReasonCompare
		res.Err = fmt.Sprintf("query edges: %v", err)
		return
	}

	res.MissingNodes, res.MissingEdges, res.MissingPaths = compare(sc, nodes, edges)
	res.Timings[report.PhaseCompare] = time.Since(start)

	if len(res.MissingNodes) == 0 && len(res.MissingEdges) == 0 && len(res.MissingPaths) == 0 {
		res.Status = report.StatusPass
	} else {
		res.FailureReason = report.ReasonCompare
		res.Err = "graph did not match contract"
	}
}

// destroy runs `terraform destroy -auto-approve`. On failure it sets
// ResourcesMayRemain (Requirement 10.8). Idempotent: destroying already-empty
// state is a no-op.
func (r *Runner) destroy(ctx context.Context, dir string, res *report.Result) {
	start := time.Now()
	r.logf("==> terraform destroy (%s)", dir)
	// Use a fresh context so teardown still runs even if the parent is done.
	dctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := r.tf(dctx, dir, "destroy", "-auto-approve", "-input=false"); err != nil {
		res.ResourcesMayRemain = true
		if res.Err == "" {
			res.FailureReason = report.ReasonDestroy
			res.Err = fmt.Sprintf("terraform destroy: %v", err)
		}
	}
	res.Timings[report.PhaseDestroy] = time.Since(start)
}

// tf runs a terraform subcommand in dir, streaming output.
func (r *Runner) tf(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, r.terraformBin(), args...)
	cmd.Dir = dir
	cmd.Stdout = r.out()
	cmd.Stderr = r.out()
	return cmd.Run()
}
