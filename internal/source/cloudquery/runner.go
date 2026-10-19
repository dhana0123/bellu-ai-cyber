package cloudquery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// JobRunner launches the CloudQuery CLI for one discovery run. A Spec is
// generated per connection (see SpecFromCatalog); STS session credentials are
// injected via env and are NEVER persisted — not to the staging DB, the
// on-disk spec file, logs, or returned errors (Requirement 16.4). This mirrors
// the DiscoveryPlan.CredentialEnv contract: the secret travels in the
// environment for the run only and is discarded afterward.
//
// A nil JobRunner means "staging is already populated": Source.Collect skips
// the sync and reads the existing cq_staging rows. Parity and golden tests use
// a nil runner against staging seeded from fixtures.
type JobRunner interface {
	// Run executes `cloudquery sync <spec>` to completion. spec selects the
	// source plugin (aws/azure/gcp/k8s) and the postgresql destination into
	// DestSchema. env carries the session credentials (e.g. AWS_* ) for this
	// run only. rep receives per-table progress (nil-safe). Run returns a job
	// failure on non-zero exit and the context error on cancellation; on every
	// failure path it leaks no credential value into the returned error.
	Run(ctx context.Context, spec Spec, env map[string]string, rep source.ProgressReporter) error
}

// cliRunner is the production JobRunner: it writes the generated spec to a
// temporary directory, launches the CloudQuery CLI as a subprocess with the
// per-run credential environment, and reports per-table progress through the
// reporter. The spec file written to disk carries no credentials; the DSN and
// session credentials are passed only through the subprocess environment.
type cliRunner struct {
	// bin is the CloudQuery executable, default "cloudquery".
	bin string
	// extraArgs are appended to the `sync` subcommand (e.g. "--log-level",
	// "error"). They must never carry a credential value.
	extraArgs []string
	// run executes the prepared command and is indirected so tests can observe
	// the spec file, args, and environment without a real CloudQuery binary.
	// nil means the default exec path.
	run func(ctx context.Context, c cliInvocation, rep source.ProgressReporter) error
}

// cliInvocation is the fully prepared, credential-free description of a
// `cloudquery sync` call. The spec file at SpecPath contains no secrets; Env is
// the per-run environment (credentials + destination DSN) that is passed to the
// subprocess and nowhere else.
type cliInvocation struct {
	Bin      string
	Args     []string
	SpecPath string
	// Env is the subprocess environment as KEY=VALUE entries. It is never
	// logged and never folded into an error message.
	Env  []string
	Spec Spec
	Dir  string
}

// NewCLIRunner builds a CLI-backed JobRunner. bin defaults to "cloudquery".
func NewCLIRunner(bin string, extraArgs ...string) JobRunner {
	if bin == "" {
		bin = "cloudquery"
	}
	return &cliRunner{bin: bin, extraArgs: extraArgs}
}

// Run writes the spec to a temp dir, builds the credential-free command plus
// the per-run environment, and executes it, reporting per-table progress.
//
// Credential handling (Requirement 16.4):
//   - env values are placed ONLY into the subprocess environment (c.Env).
//   - the on-disk spec (specYAML) contains no credential and no DSN; the DSN
//     and credentials travel through CQ_DEST_DSN / AWS_* env entries.
//   - no credential value is written to a log line or folded into an error.
func (r *cliRunner) Run(ctx context.Context, spec Spec, env map[string]string, rep source.ProgressReporter) error {
	dir, err := os.MkdirTemp("", "cloudquery-spec-")
	if err != nil {
		return fmt.Errorf("cloudquery: create spec dir: %w", err)
	}
	// Best-effort cleanup; the dir only ever holds the credential-free spec.
	defer os.RemoveAll(dir)

	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML(spec)), 0o600); err != nil {
		return fmt.Errorf("cloudquery: write spec: %w", err)
	}

	args := append([]string{"sync", specPath}, r.extraArgs...)

	inv := cliInvocation{
		Bin:      r.bin,
		Args:     args,
		SpecPath: specPath,
		Env:      buildEnv(env, spec.DestDSN),
		Spec:     spec,
		Dir:      dir,
	}

	// Report the planned table count up front so a run with a wired runner
	// surfaces the same table-level progress shape as the staging read path.
	if rep != nil {
		rep.TablesPlanned(len(spec.Tables))
	}

	exec := r.run
	if exec == nil {
		exec = execCLI
	}
	return exec(ctx, inv, rep)
}

// buildEnv constructs the subprocess environment: the current process env plus
// the per-run credential entries and the destination DSN. The credential map
// is the ONLY place secrets enter the invocation; they are copied into the
// child's environment and nowhere else. Returning a copy (rather than mutating
// os.Environ in place) keeps the parent process environment untouched.
func buildEnv(env map[string]string, destDSN string) []string {
	out := os.Environ()
	// Deterministic ordering for the injected keys so tests can assert the
	// contents without depending on map iteration order.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	// The destination DSN is referenced as ${CQ_DEST_DSN} in the spec file so
	// it, like the credentials, never lands on disk.
	if destDSN != "" {
		out = append(out, "CQ_DEST_DSN="+destDSN)
	}
	return out
}

// execCLI is the default execution path: run the prepared command, piping its
// environment, and wait for completion. On non-zero exit it returns a job
// failure that names the spec and exit condition but never includes any
// environment value. On context cancellation it returns ctx.Err().
func execCLI(ctx context.Context, inv cliInvocation, rep source.ProgressReporter) error {
	cmd := exec.CommandContext(ctx, inv.Bin, inv.Args...)
	cmd.Env = inv.Env
	cmd.Dir = inv.Dir

	// Discard child stdout/stderr rather than surfacing it: CloudQuery echoes
	// its resolved spec (including the expanded ${CQ_DEST_DSN}) to stderr, so
	// forwarding it would leak the DSN. Progress is reported structurally
	// below, not by scraping child output.
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// Prefer the context error on cancellation so callers can distinguish a
		// cancelled run from a genuine failure.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Never wrap err directly: exec.ExitError.String and the underlying
		// *os.PathError can echo argv/env. Report only the exit condition.
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("cloudquery: sync failed (exit code %d)", ee.ExitCode())
		}
		return fmt.Errorf("cloudquery: sync failed to run")
	}

	// A successful sync populated every planned table; report them done so the
	// reporter's tallies are complete. Row counts are not known from the CLI,
	// so each table is reported done with zero rows (the staging read path
	// reports the real per-table counts).
	if rep != nil {
		for i, t := range inv.Spec.Tables {
			rep.TableStarted(t, i)
			rep.TableDone(t, 0)
		}
	}
	return nil
}
