// Command global-intel is the CLI for the Bellu Global Intelligence plane. It is
// a sibling to cmd/bellu and reuses the same bellu.yaml (internal/config),
// sharing Postgres.DSN and Memgraph.* with the main pipeline.
//
// Subcommands:
//
//	fetch      run enabled fetchers -> RawStore, record intel_fetch_runs
//	parse      parse stored raw payloads into source records
//	normalize  canonicalize source records into CanonicalVuln
//	correlate  dedup + upsert into intel_* (Phase 2 also runs the Matcher)
//	score      run the Risk Engine and upsert findings (Phase 2)
//	release    build/sign/import a release bundle (Phase 3)
//	run        fetch -> parse -> normalize -> correlate (--mode daily|incremental)
//
// This file (task 10.1) establishes the dispatch, config loading, and a working
// skeleton for every subcommand. The full fetch->parse->normalize->correlate
// sequencing for `run` is task 10.2, and the correlate/score Matcher + Risk
// Engine wiring is task 16.1; those stages are intentionally left as clearly
// labelled "not yet wired" skeletons here.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/global-intel/correlation"
	"github.com/bellu/bellu-ai-cyber/global-intel/database"
	"github.com/bellu/bellu-ai-cyber/global-intel/fetch"
	epssfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/epss"
	kevfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/kev"
	osvfetch "github.com/bellu/bellu-ai-cyber/global-intel/fetch/osv"
	"github.com/bellu/bellu-ai-cyber/global-intel/parser"
	epssparse "github.com/bellu/bellu-ai-cyber/global-intel/parser/epss"
	kevparse "github.com/bellu/bellu-ai-cyber/global-intel/parser/kev"
	osvparse "github.com/bellu/bellu-ai-cyber/global-intel/parser/osv"
	"github.com/bellu/bellu-ai-cyber/global-intel/raw"
	"github.com/bellu/bellu-ai-cyber/global-intel/release"
	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/finding"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/intelcorrelate"
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/risk"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "fetch":
		err = runFetch(ctx, args)
	case "parse":
		err = runParse(ctx, args)
	case "normalize":
		err = runNormalize(ctx, args)
	case "correlate":
		err = runCorrelate(ctx, args)
	case "score":
		err = runScore(ctx, args)
	case "release":
		err = runRelease(ctx, args)
	case "run":
		err = runPipeline(ctx, args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "global-intel: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "global-intel %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `global-intel - Global Intelligence ingestion

usage: global-intel <command> [flags]

commands:
  fetch       run enabled fetchers into the content-addressed raw store
  parse       parse stored raw payloads into source records
  normalize   canonicalize source records into canonical vulnerabilities
  correlate   dedup + upsert into the intel_* tables
  score       run the risk engine and upsert findings
  release     build, sign, or import a release bundle
  run         fetch -> parse -> normalize -> correlate

run "global-intel <command> -h" for command flags
`)
}

// defaultConfigPath is the config file every subcommand reads by default, the
// same bellu.yaml used by the main bellu CLI (Requirement 7.5).
const defaultConfigPath = "bellu.yaml"

// phaseOneSources is the ordered set of sources Phase 1 implements end to end:
// OSV (purl ecosystems, PyPI first), CISA KEV, and FIRST EPSS.
var phaseOneSources = []string{"osv", "kev", "epss"}

// newFetchers constructs the Phase-1 fetchers keyed by source. The returned
// fetchers use their default (live) upstream endpoints; tests inject their own.
func newFetchers() map[string]fetch.Fetcher {
	return map[string]fetch.Fetcher{
		"osv":  osvfetch.New(),
		"kev":  kevfetch.New(),
		"epss": epssfetch.New(),
	}
}

// newParsers constructs the Phase-1 parsers keyed by source.
func newParsers() map[string]parser.Parser {
	return map[string]parser.Parser{
		"osv":  osvparse.New(),
		"kev":  kevparse.New(),
		"epss": epssparse.New(),
	}
}

// newCanonicalizers constructs the Phase-1 canonicalizers keyed by source.
func newCanonicalizers() map[string]canonical.Canonicalizer {
	return map[string]canonical.Canonicalizer{
		"osv":  canonical.NewOSVCanonicalizer(),
		"kev":  canonical.NewKEVCanonicalizer(),
		"epss": canonical.NewEPSSCanonicalizer(),
	}
}

// openRawStore builds the content-addressed raw store. When BELLU_RAW_BUCKET is
// set (as it is for the Fargate task), payloads are stored in S3 under the raw
// bucket using the task role's credentials; the optional BELLU_RAW_PREFIX
// namespaces the keys. Otherwise (local dev) it falls back to the
// filesystem-backed store rooted at dir. Both satisfy raw.RawStore, so the rest
// of the pipeline is unchanged.
func openRawStore(ctx context.Context, dir string) (raw.RawStore, error) {
	if bucket := strings.TrimSpace(os.Getenv("BELLU_RAW_BUCKET")); bucket != "" {
		var opts []raw.S3Option
		if prefix := strings.TrimSpace(os.Getenv("BELLU_RAW_PREFIX")); prefix != "" {
			opts = append(opts, raw.WithPrefix(prefix))
		}
		fmt.Fprintf(os.Stderr, "raw store: s3://%s\n", bucket)
		return raw.NewS3Store(ctx, bucket, opts...)
	}
	fmt.Fprintf(os.Stderr, "raw store: fs://%s\n", dir)
	return raw.NewFSStore(dir)
}

// runFetch runs the enabled Phase-1 fetchers into the raw store. The end-to-end
// fetch->parse->normalize->correlate sequencing with intel_fetch_runs
// accounting is task 10.2; here we load config, construct the components, and
// report what will run.
func runFetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	rawDir := fs.String("raw-dir", "intel-raw", "content-addressed raw store directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	rs, err := openRawStore(ctx, *rawDir)
	if err != nil {
		return err
	}
	_ = rs // the fetch loop (fetcher -> raw store) is wired in task 10.2

	fetchers := newFetchers()
	fmt.Fprintf(os.Stderr, "fetch: raw-dir=%s postgres=%s\n", *rawDir, redactDSN(cfg.Postgres.DSN))
	for _, src := range phaseOneSources {
		if f, ok := fetchers[src]; ok {
			fmt.Fprintf(os.Stderr, "  source %-5s fetcher=%s (not yet wired; see task 10.2)\n", src, f.Source())
		}
	}
	return nil
}

// runParse parses stored raw payloads into source records. Reading refs back
// out of the raw store and feeding them through the parsers is part of the
// task 10.2 pipeline; here we construct the parsers and report readiness.
func runParse(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	rawDir := fs.String("raw-dir", "intel-raw", "content-addressed raw store directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := config.Load(*cfgPath); err != nil {
		return err
	}

	rs, err := openRawStore(ctx, *rawDir)
	if err != nil {
		return err
	}
	_ = rs // raw-ref enumeration + parsing is wired in task 10.2

	parsers := newParsers()
	fmt.Fprintf(os.Stderr, "parse: raw-dir=%s\n", *rawDir)
	for _, src := range phaseOneSources {
		if p, ok := parsers[src]; ok {
			fmt.Fprintf(os.Stderr, "  source %-5s parser=%s (not yet wired; see task 10.2)\n", src, p.Source())
		}
	}
	return nil
}

// runNormalize canonicalizes source records into canonical vulnerabilities.
// The parse->canonicalize hand-off runs in task 10.2; here we construct the
// canonicalizers and report readiness.
func runNormalize(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := config.Load(*cfgPath); err != nil {
		return err
	}

	canons := newCanonicalizers()
	fmt.Fprintln(os.Stderr, "normalize:")
	for _, src := range phaseOneSources {
		if c, ok := canons[src]; ok {
			fmt.Fprintf(os.Stderr, "  source %-5s canonicalizer=%s (not yet wired; see task 10.2)\n", src, c.Source())
		}
	}
	return nil
}

// runCorrelate runs the Intelligence Matcher into the customer graph (task
// 16.1, Requirement 7.1). The dedup + intel_* upsert of the canonical corpus is
// already performed by the `run`/ingestion stages; correlate's Phase-2 job is
// to correlate the persisted intelligence against the graph and enrich it with
// Vulnerability nodes + HAS_VULN edges through the allow-listed write path.
//
// It opens the shared Postgres store and the Memgraph writer, builds a Matcher
// over (graph-reader, intel-store), collects the candidate (asset, vuln)
// pairings, hydrates the canonical vulns the candidates cite, and enriches the
// graph via intelcorrelate.Enrich. The enrichment writes no evidence rows and
// cites only intel record ids (never raw_event ids).
func runCorrelate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("correlate", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	g, err := graph.Open(ctx, cfg.Memgraph.URI, cfg.Memgraph.Username, cfg.Memgraph.Password)
	if err != nil {
		return err
	}
	defer g.Close(ctx)

	fmt.Fprintf(os.Stderr, "correlate: postgres=%s memgraph=%s\n",
		redactDSN(cfg.Postgres.DSN), cfg.Memgraph.URI)

	intel := database.New(st)
	cands, vulns, err := correlateCandidates(ctx, g, intel)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  matcher: %d candidate(s) across %d vulnerabilit(ies)\n",
		len(cands), len(vulns))

	res, err := intelcorrelate.Enrich(ctx, g, cands, vulns)
	if err != nil {
		return fmt.Errorf("correlate: enrich graph: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  enrich: %d Vulnerability node(s), %d HAS_VULN edge(s)\n",
		res.Nodes, res.Edges)
	return nil
}

// correlateCandidates runs the Matcher over the graph + intel store and returns
// the candidate pairings alongside a map of each cited vuln id to its canonical
// record (hydrated from the intel_* tables). The map lets both Enrich (graph
// attributes) and Score (risk inputs) reuse the same hydrated corpus without a
// second lookup. Shared by correlate and score so the two stages agree on the
// matched set.
func correlateCandidates(ctx context.Context, g intelcorrelate.GraphReader, intel *database.IntelStore) ([]intelcorrelate.Candidate, map[string]canonical.CanonicalVuln, error) {
	m := intelcorrelate.New(g, intel)
	cands, err := m.Correlate(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("correlate: matcher: %w", err)
	}

	// Hydrate each distinct cited vuln once so attributes/risk inputs are
	// available without re-querying per candidate.
	vulns := make(map[string]canonical.CanonicalVuln)
	for _, c := range cands {
		if c.VulnID == "" {
			continue
		}
		if _, ok := vulns[c.VulnID]; ok {
			continue
		}
		v, err := intel.VulnByID(ctx, c.VulnID)
		if err != nil {
			return nil, nil, fmt.Errorf("correlate: load vuln %q: %w", c.VulnID, err)
		}
		vulns[c.VulnID] = v
	}
	return cands, vulns, nil
}

// runScore runs the Risk Engine over the current matches and upserts findings
// (task 16.1, Requirements 10.3, 18.4). Score follows correlate: it re-runs the
// Matcher to recover the current (asset, vuln) candidates, then for each one
// builds a risk.RiskContext from the canonical vuln's CVSS/EPSS/KEV flag, sets
// Reachable from the graph's read-only ReachesAsset (Requirement 10.3), and an
// AI-criticality derived from the asset kind. It computes the risk, upserts an
// open Finding per match, and finally resolves any open findings that no longer
// match so re-runs are idempotent except for last_seen (Requirement 18.4).
func runScore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	g, err := graph.Open(ctx, cfg.Memgraph.URI, cfg.Memgraph.Username, cfg.Memgraph.Password)
	if err != nil {
		return err
	}
	defer g.Close(ctx)

	fmt.Fprintf(os.Stderr, "score: postgres=%s memgraph=%s\n",
		redactDSN(cfg.Postgres.DSN), cfg.Memgraph.URI)

	intel := database.New(st)
	cands, vulns, err := correlateCandidates(ctx, g, intel)
	if err != nil {
		return err
	}

	findings := finding.NewStore(st)
	scorer := risk.NewScorer()
	matched, err := scoreCandidates(ctx, g, findings, scorer, cands, vulns)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  risk: upserted %d finding(s)\n", len(matched))

	// Transition open findings that no longer match to resolved so the finding
	// lifecycle stays consistent and re-runs are idempotent (Requirement 18.4).
	if err := findings.ResolveMissing(ctx, matched); err != nil {
		return fmt.Errorf("score: resolve missing findings: %w", err)
	}
	fmt.Fprintln(os.Stderr, "  risk: resolved findings no longer matched")
	return nil
}

// scoreCandidates scores every candidate and upserts it as an open finding,
// returning the set of matched (asset, vuln) keys for ResolveMissing. Each
// candidate's reachability is read once from the graph; its risk context is
// built from the hydrated canonical vuln. Extracted so runScore stays small.
func scoreCandidates(
	ctx context.Context,
	reach reachChecker,
	findings *finding.Store,
	scorer risk.Scorer,
	cands []intelcorrelate.Candidate,
	vulns map[string]canonical.CanonicalVuln,
) ([]finding.Key, error) {
	matched := make([]finding.Key, 0, len(cands))
	for _, c := range cands {
		if c.AssetID == "" || c.VulnID == "" {
			continue
		}

		reachable, err := reach.ReachesAsset(ctx, c.AssetID)
		if err != nil {
			return nil, fmt.Errorf("score: reachability for %q: %w", c.AssetID, err)
		}

		v := vulns[c.VulnID] // zero value is a valid (empty) context
		rctx := risk.RiskContext{
			CVSS:           v.CVSS.Score,
			EPSS:           v.EPSS,
			KnownExploited: v.KnownExploited,
			Reachable:      reachable,
			AICriticality:  aiCriticality(c.AssetKind),
		}
		result, err := scorer.Score(ctx, rctx)
		if err != nil {
			return nil, fmt.Errorf("score: risk for %q/%q: %w", c.AssetID, c.VulnID, err)
		}

		intelRef := c.IntelRef
		if intelRef == "" {
			intelRef = c.VulnID
		}
		f := finding.Finding{
			AssetID:   c.AssetID,
			AssetKind: c.AssetKind,
			VulnID:    c.VulnID,
			IntelRef:  intelRef,
			RiskScore: result.Score,
			Severity:  result.Severity,
		}
		if err := findings.Upsert(ctx, f); err != nil {
			return nil, fmt.Errorf("score: upsert finding %q/%q: %w", c.AssetID, c.VulnID, err)
		}
		matched = append(matched, finding.Key{AssetID: c.AssetID, VulnID: c.VulnID})
	}
	return matched, nil
}

// reachChecker is the read-only reachability slice of graph.Writer the scorer
// needs. *graph.Writer satisfies it via ReachesAsset, and tests can supply a
// fake without a live Memgraph.
type reachChecker interface {
	ReachesAsset(ctx context.Context, canonicalID string) (bool, error)
}

// aiCriticality maps an asset kind to the risk engine's AI-criticality signal
// in [0,1]. Non-AI assets contribute no AI boost (0); AI assets with broad
// blast radius (agents acting autonomously) weigh highest, then models, then
// MCP servers brokering tool access. The mapping is a pure function so it is
// unit-testable without a graph or DB.
func aiCriticality(kind model.Kind) float64 {
	switch kind {
	case model.KindAIAgent:
		return 1.0
	case model.KindAIModel:
		return 0.75
	case model.KindMCPServer:
		return 0.5
	default:
		return 0
	}
}

// runRelease builds, signs, or imports a release bundle. It dispatches on a
// release sub-command:
//
//	import   verify a signed .tar.zst against a pinned ed25519 public key and,
//	         only on success, upsert its canonical corpus into intel_* (task
//	         22.2, Requirements 16.5, 16.6)
//
// The build/sign side (task 22.1) is a library in package release; wiring a
// `release build` sub-command remains future work, so with no sub-command (or
// an unknown one) runRelease prints usage and leaves the skeleton behaviour.
func runRelease(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "release: signed release bundles — subcommands: import")
		fmt.Fprintln(os.Stderr, "  global-intel release import --bundle <path> --pubkey <path>")
		return nil
	}

	sub := args[0]
	rest := args[1:]
	switch sub {
	case "import":
		return runReleaseImport(ctx, rest)
	default:
		return fmt.Errorf("unknown release subcommand %q (want: import)", sub)
	}
}

// runReleaseImport verifies a signed release bundle and upserts its intel on
// success (task 22.2, Requirements 16.5, 16.6). It reads the pinned ed25519
// public key and the bundle file, opens the shared Postgres store, and runs the
// verifying importer with *database.IntelStore as the VulnSink. On any
// verification failure (bad signature or checksum) the importer returns before
// touching the store, so a rejected bundle upserts nothing; the error is
// surfaced to main which exits non-zero.
func runReleaseImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("release import", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	bundlePath := fs.String("bundle", "", "path to the signed .tar.zst release bundle (required)")
	pubkeyPath := fs.String("pubkey", "", "path to the pinned ed25519 public key file (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundlePath == "" {
		return fmt.Errorf("import: --bundle is required")
	}
	if *pubkeyPath == "" {
		return fmt.Errorf("import: --pubkey is required")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	pub, err := loadEd25519PublicKey(*pubkeyPath)
	if err != nil {
		return fmt.Errorf("import: load pinned public key: %w", err)
	}

	bundle, err := os.Open(*bundlePath)
	if err != nil {
		return fmt.Errorf("import: open bundle: %w", err)
	}
	defer bundle.Close()

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Fprintf(os.Stderr, "release import: bundle=%s pubkey=%s postgres=%s\n",
		*bundlePath, *pubkeyPath, redactDSN(cfg.Postgres.DSN))

	sink := database.New(st)
	if err := release.Import(ctx, bundle, pub, sink); err != nil {
		if errors.Is(err, release.ErrVerification) {
			// Fail closed (Requirement 16.6): nothing was upserted.
			return fmt.Errorf("rejected bundle, no intel_* rows written: %w", err)
		}
		return err
	}

	fmt.Fprintln(os.Stderr, "  verified signature + checksums; upserted intel corpus")
	return nil
}

// loadEd25519PublicKey reads a pinned ed25519 public key from path. The file is
// the raw 32-byte key, optionally hex-encoded (a text-friendly form that is
// easy to pin in config or ship alongside a bundle). It accepts whichever form
// the bytes represent and rejects anything that is not a valid key length.
func loadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Raw 32-byte key.
	if len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}

	// Hex-encoded key (trim trailing whitespace/newline a text editor may add).
	trimmed := strings.TrimSpace(string(raw))
	decoded, derr := hex.DecodeString(trimmed)
	if derr == nil && len(decoded) == ed25519.PublicKeySize {
		return ed25519.PublicKey(decoded), nil
	}

	return nil, fmt.Errorf("key is %d bytes; want a %d-byte raw or hex-encoded ed25519 public key",
		len(raw), ed25519.PublicKeySize)
}

// Pipeline run modes accepted by `run --mode`. daily is a full-schedule run
// (empty cursor, full fetch per Requirement 7.3); incremental resumes each
// source from its stored cursor (Requirement 7.4).
const (
	modeDaily       = "daily"
	modeIncremental = "incremental"
)

// Terminal per-run states recorded in intel_fetch_runs.status (see design,
// "Source-aware scheduling, sync modes, and failure handling"). Every run ends
// in exactly one of these; the failing-stage detail is kept in the error field.
const (
	statusSuccess         = "SUCCESS"
	statusFetchFailed     = "FETCH_FAILED"
	statusParseFailed     = "PARSE_FAILED"
	statusNormalizeFailed = "NORMALIZE_FAILED"
	statusIndexFailed     = "INDEX_FAILED"
)

// runPipeline runs the full fetch -> parse -> normalize -> correlate pipeline
// for every Phase-1 source in order (Requirement 7.2). Per source it fetches
// raw payloads into the content-addressed raw store, parses them back into
// source records, canonicalizes them, and records the run in intel_fetch_runs.
// After every source has been canonicalized the combined set is deduped and
// upserted into the intel_* tables (idempotent, Requirement 6.2).
//
// --mode daily performs a full fetch (empty cursor) for every source; --mode
// incremental resumes each source from its stored cursor (Requirements 7.3,
// 7.4). The stored cursor is read from the most recent SUCCESS row in
// intel_fetch_runs (cursor_after) for the source; see loadStoredCursor for why
// that table — rather than intel_sources — is the cursor of record here.
//
// Failure policy (design, "Per-source isolation"): a failure in one source is
// recorded with the appropriate *_FAILED status and does not abort the other
// sources. The failed source simply contributes no records to the dedup/upsert
// set, so a partial run never wipes valid existing intelligence. If any source
// failed, runPipeline returns a non-nil error after attempting all sources.
func runPipeline(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to bellu.yaml")
	rawDir := fs.String("raw-dir", "intel-raw", "content-addressed raw store directory")
	mode := fs.String("mode", modeDaily, "run mode: daily (full) or incremental (cursor-based)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *mode {
	case modeDaily, modeIncremental:
	default:
		return fmt.Errorf("run: invalid --mode %q (want daily or incremental)", *mode)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	rs, err := openRawStore(ctx, *rawDir)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	intel := database.New(st)
	pl := &pipeline{
		mode:     *mode,
		rawDir:   *rawDir,
		raw:      rs,
		intel:    intel,
		fetchers: newFetchers(),
		parsers:  newParsers(),
		canons:   newCanonicalizers(),
		deduper:  correlation.NewDeduper(),
	}

	fmt.Fprintf(os.Stderr, "run: mode=%s raw-dir=%s sources=%v postgres=%s\n",
		*mode, *rawDir, phaseOneSources, redactDSN(cfg.Postgres.DSN))
	return pl.run(ctx)
}

// pipeline carries the components the fetch -> parse -> normalize -> correlate
// sequence needs so the per-source and per-stage helpers stay small.
type pipeline struct {
	mode   string
	rawDir string

	raw      raw.RawStore
	intel    *database.IntelStore
	fetchers map[string]fetch.Fetcher
	parsers  map[string]parser.Parser
	canons   map[string]canonical.Canonicalizer
	deduper  correlation.Deduper
}

// run drives every Phase-1 source through fetch -> parse -> normalize, records
// each source run, then dedups and upserts the combined canonical set. EPSS
// history is recorded separately with its scored date; all other records are
// upserted with a zero scoredOn so no spurious EPSS history is written.
func (p *pipeline) run(ctx context.Context) error {
	var combined []canonical.CanonicalVuln
	var epssVulns []canonical.CanonicalVuln
	var epssScoredOn time.Time
	var sourceErrs []error

	for _, src := range phaseOneSources {
		vulns, scoredOn, err := p.runSource(ctx, src)
		if err != nil {
			// Failure already recorded in intel_fetch_runs; isolate it so the
			// remaining sources still run (design: per-source isolation).
			fmt.Fprintf(os.Stderr, "  source %-5s FAILED: %v\n", src, err)
			sourceErrs = append(sourceErrs, fmt.Errorf("%s: %w", src, err))
			continue
		}
		fmt.Fprintf(os.Stderr, "  source %-5s ok: %d canonical record(s)\n", src, len(vulns))
		combined = append(combined, vulns...)
		if src == "epss" {
			epssVulns = append(epssVulns, vulns...)
			if !scoredOn.IsZero() {
				epssScoredOn = scoredOn
			}
		}
	}

	// Correlate: dedup the combined set, then upsert idempotently. Non-EPSS
	// history is suppressed by passing a zero scoredOn.
	deduped, err := p.deduper.Dedup(combined)
	if err != nil {
		return fmt.Errorf("run: dedup: %w", err)
	}
	if err := p.intel.UpsertVulns(ctx, deduped, time.Time{}); err != nil {
		return fmt.Errorf("run: upsert canonical set: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  correlate: upserted %d deduped vulnerabilit(ies)\n", len(deduped))

	// Record EPSS history keyed by the scored date so the daily series is
	// captured. RecordEPSSHistory writes the per-day history rows and refreshes
	// only the current epss + last_seen on each vuln; it deliberately does NOT
	// re-upsert the full canonical row, so the merged KEV flag / CVSS / summary
	// from the dedup pass above are preserved for CVEs that also have an EPSS row.
	if len(epssVulns) > 0 && !epssScoredOn.IsZero() {
		if err := p.intel.RecordEPSSHistory(ctx, epssVulns, epssScoredOn); err != nil {
			return fmt.Errorf("run: record epss history: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  correlate: recorded EPSS history for %s\n", epssScoredOn.Format("2006-01-02"))
	}

	if len(sourceErrs) > 0 {
		return fmt.Errorf("run: %d source(s) failed: %w", len(sourceErrs), errors.Join(sourceErrs...))
	}
	return nil
}

// runSource fetches, parses, and canonicalizes a single source, recording the
// run in intel_fetch_runs with its cursor, counts, and terminal status. On any
// stage failure it records the matching *_FAILED status and returns the error
// so the caller can isolate the source. On success it returns the canonical
// records and, for EPSS, the scored date so the caller can record EPSS history.
func (p *pipeline) runSource(ctx context.Context, src string) ([]canonical.CanonicalVuln, time.Time, error) {
	fetcher, ok := p.fetchers[src]
	if !ok {
		return nil, time.Time{}, fmt.Errorf("no fetcher registered")
	}
	psr, ok := p.parsers[src]
	if !ok {
		return nil, time.Time{}, fmt.Errorf("no parser registered")
	}
	canon, ok := p.canons[src]
	if !ok {
		return nil, time.Time{}, fmt.Errorf("no canonicalizer registered")
	}

	run := database.FetchRun{Source: src, StartedAt: time.Now()}

	// 1. Determine the cursor for this run from the mode.
	cursor, err := p.cursorFor(ctx, src)
	if err != nil {
		run.Status = statusFetchFailed
		run.Error = err.Error()
		p.recordRun(ctx, &run)
		return nil, time.Time{}, err
	}
	run.CursorBefore = string(cursor)

	// 2. Fetch raw payloads into the content-addressed store.
	res, err := fetcher.Fetch(ctx, p.raw, cursor)
	if err != nil {
		run.Status = statusFetchFailed
		run.Error = err.Error()
		p.recordRun(ctx, &run)
		return nil, time.Time{}, fmt.Errorf("fetch: %w", err)
	}
	run.RawCount = len(res.RawRefs)
	run.Cursor = string(res.Next)
	run.CursorAfter = string(res.Next)

	// 3. Parse each stored payload back into source records.
	records, err := p.parseRefs(ctx, psr, res.RawRefs)
	if err != nil {
		run.Status = statusParseFailed
		run.Error = err.Error()
		p.recordRun(ctx, &run)
		return nil, time.Time{}, fmt.Errorf("parse: %w", err)
	}

	// 4. Normalize source records into canonical vulnerabilities.
	vulns, err := canon.Canonicalize(records)
	if err != nil {
		run.Status = statusNormalizeFailed
		run.Error = err.Error()
		p.recordRun(ctx, &run)
		return nil, time.Time{}, fmt.Errorf("normalize: %w", err)
	}

	run.RecordsAdded = len(vulns)
	run.Status = statusSuccess
	run.FinishedAt = time.Now()
	p.recordRun(ctx, &run)

	return vulns, epssScoredDate(records), nil
}

// parseRefs reads every stored RawRef back out of the content-addressed store
// and runs the source's parser over the exact bytes, accumulating the source
// records in fetch order.
func (p *pipeline) parseRefs(ctx context.Context, psr parser.Parser, refs []raw.RawRef) ([]parser.SourceRecord, error) {
	var records []parser.SourceRecord
	for _, ref := range refs {
		payload, err := p.raw.Get(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("read raw %s: %w", ref.Hash, err)
		}
		recs, err := psr.Parse(ref, payload)
		if err != nil {
			return nil, fmt.Errorf("parse raw %s: %w", ref.Hash, err)
		}
		records = append(records, recs...)
	}
	return records, nil
}

// cursorFor selects the fetch cursor for a source based on the run mode. A
// daily run always uses an empty cursor (a full fetch, Requirement 7.3); an
// incremental run resumes from the source's stored cursor (Requirement 7.4),
// falling back to an empty cursor when no prior successful run exists.
func (p *pipeline) cursorFor(ctx context.Context, src string) (fetch.Cursor, error) {
	if isFullFetch(p.mode) {
		return "", nil
	}
	return p.loadStoredCursor(ctx, src)
}

// isFullFetch reports whether a run mode performs a full fetch (empty cursor)
// rather than resuming from a stored cursor. daily is a full-schedule run
// (Requirement 7.3); incremental is cursor-based (Requirement 7.4). Any other
// value is treated as a full fetch so an unexpected mode never silently reuses
// a stale cursor. This is a pure function so the mode decision is unit-testable
// without a database.
func isFullFetch(mode string) bool {
	return mode != modeIncremental
}

// loadStoredCursor returns the most recent successful cursor_after recorded for
// a source in intel_fetch_runs, or an empty cursor when the source has never
// completed a run.
//
// We read the cursor from intel_fetch_runs rather than intel_sources.last_cursor
// because intel_fetch_runs is the table this pipeline already writes on every
// run (RecordFetchRun), so the resume marker and the audit trail stay in lock
// step with no extra write path. intel_sources.last_cursor remains the
// scheduler's authoritative marker; wiring the scheduler to it is future work.
func (p *pipeline) loadStoredCursor(ctx context.Context, src string) (fetch.Cursor, error) {
	var cursor string
	err := p.intel.LastCursor(ctx, src, &cursor)
	if err != nil {
		return "", fmt.Errorf("load stored cursor: %w", err)
	}
	return fetch.Cursor(cursor), nil
}

// recordRun writes the run accounting into intel_fetch_runs, stamping a finish
// time when one was not already set. A failure to record the audit row is
// logged but never masks the real pipeline outcome.
func (p *pipeline) recordRun(ctx context.Context, run *database.FetchRun) {
	if run.FinishedAt.IsZero() {
		run.FinishedAt = time.Now()
	}
	if _, err := p.intel.RecordFetchRun(ctx, *run); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: record fetch run (%s): %v\n", run.Source, err)
	}
}

// epssScoredDate extracts the EPSS scored date from a set of parsed source
// records. EPSS rows carry a per-day score date; all rows in one daily file
// share it, so the first non-empty value is used. Non-EPSS records carry no
// scored date and yield the zero time, which suppresses EPSS history.
func epssScoredDate(records []parser.SourceRecord) time.Time {
	for _, r := range records {
		row, ok := r.(epssparse.Row)
		if !ok {
			continue
		}
		if row.ScoredOn == "" {
			continue
		}
		// EPSS score_date can be a bare date or an RFC3339-ish timestamp; try
		// the date layout first, then fall back to parsing the leading date.
		if t, err := time.Parse("2006-01-02", row.ScoredOn); err == nil {
			return t
		}
		if len(row.ScoredOn) >= 10 {
			if t, err := time.Parse("2006-01-02", row.ScoredOn[:10]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// redactDSN hides the password in a Postgres DSN before logging it, so a run
// log never leaks a credential. A DSN without credentials is returned as-is.
func redactDSN(dsn string) string {
	const sep = "@"
	at := indexOf(dsn, sep)
	if at < 0 {
		return dsn
	}
	scheme := indexOf(dsn, "://")
	if scheme < 0 || scheme+3 >= at {
		return dsn
	}
	return dsn[:scheme+3] + "***" + dsn[at:]
}

// indexOf is a tiny strings.Index helper kept local so main.go pulls in no
// extra imports solely for DSN redaction.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
