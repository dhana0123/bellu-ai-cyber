package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/pipeline"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// runAllCmd runs the full pipeline: ingest (one or more sources) -> normalize
// -> resolve -> sync. It uses a single run id across all stages.
func runAllCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "bellu.yaml", "path to bellu.yaml")
	sourcesCSV := fs.String("sources", "", "comma-separated sources to ingest (default: all configured)")
	skipSync := fs.Bool("skip-sync", false, "skip the Memgraph sync step")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	sources := selectSources(cfg, *sourcesCSV)
	if len(sources) == 0 {
		return fmt.Errorf("run: no sources configured; set sources in bellu.yaml or pass --sources")
	}

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	runID := pipeline.NewRunID(time.Now())
	fmt.Fprintf(os.Stderr, "run %s: sources=%v\n", runID, sources)

	// 1) Ingest each source under the shared run id.
	for _, s := range sources {
		res, err := ingestOne(ctx, cfg, s, nil, runID)
		if err != nil {
			return fmt.Errorf("ingest %s: %w", s, err)
		}
		fmt.Fprintf(os.Stderr, "  ingest %-10s collected=%d persisted=%d errors=%d\n",
			s, res.Collected, res.Persisted, len(res.Errors))
	}

	// 2) Normalize + 3) resolve + persist (over all raw events).
	events, err := st.LoadRawEvents(ctx, "")
	if err != nil {
		return err
	}
	norm := buildNormalizers()
	nres, err := norm.Normalize(events)
	if err != nil {
		return err
	}
	resolver := buildResolver()
	rres, err := resolver.Resolve(nres)
	if err != nil {
		return err
	}
	agentEnts, agentEdges := deriveAgents(rres.Entities, rres.Edges)
	rres.Entities = append(rres.Entities, agentEnts...)
	rres.Edges = append(rres.Edges, agentEdges...)
	if err := st.PersistNormalized(ctx, rres.Entities, rres.Edges); err != nil {
		return err
	}
	if err := persistResolution(ctx, st, resolver); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  normalize entities=%d edges=%d aliases=%d conflicts=%d agents=%d\n",
		len(rres.Entities), len(rres.Edges), len(resolver.Aliases), len(resolver.Conflicts), len(agentEnts))

	// 4) Sync.
	if *skipSync {
		fmt.Fprintln(os.Stderr, "  sync skipped")
		return nil
	}
	return syncToGraph(ctx, cfg, st)
}

// selectSources returns the sources to ingest. An explicit CSV wins; otherwise
// every source configured in bellu.yaml that has a registered factory.
func selectSources(cfg config.Config, csv string) []string {
	if csv != "" {
		var out []string
		for _, s := range strings.Split(csv, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	reg := buildRegistry()
	known := map[string]bool{}
	for _, n := range reg.Names() {
		known[n] = true
	}
	var out []string
	for name := range cfg.Sources {
		if known[name] {
			out = append(out, name)
		}
	}
	return out
}
