package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// normalizeCmd loads raw events (for a run, or all), normalizes them into
// canonical entities and edges, resolves canonical IDs, and persists the
// result to the normalized tables.
func normalizeCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	cfgPath := fs.String("config", "bellu.yaml", "path to bellu.yaml")
	runID := fs.String("run", "", "run id to normalize (default: all raw events)")
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

	events, err := st.LoadRawEvents(ctx, *runID)
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

	if err := st.PersistNormalized(ctx, rres.Entities, rres.Edges); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "normalize: raw=%d entities=%d edges=%d\n",
		len(events), len(rres.Entities), len(rres.Edges))
	return nil
}
