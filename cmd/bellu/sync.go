package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/graph"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// syncCmd loads the resolved graph from Postgres and writes it into Memgraph.
func syncCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	cfgPath := fs.String("config", "bellu.yaml", "path to bellu.yaml")
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

	return syncToGraph(ctx, cfg, st)
}

// syncToGraph is shared by `sync` and `run`.
func syncToGraph(ctx context.Context, cfg config.Config, st *store.Store) error {
	w, err := graph.Open(ctx, cfg.Memgraph.URI, cfg.Memgraph.Username, cfg.Memgraph.Password)
	if err != nil {
		return err
	}
	defer w.Close(ctx)

	if err := w.EnsureSchema(ctx); err != nil {
		return err
	}

	entities, err := st.LoadGraphEntities(ctx)
	if err != nil {
		return err
	}
	edges, err := st.LoadGraphEdges(ctx)
	if err != nil {
		return err
	}

	ne, err := w.SyncEntities(ctx, entities)
	if err != nil {
		return err
	}
	nr, err := w.SyncEdges(ctx, edges)
	if err != nil {
		return err
	}

	nodes, rels, err := w.Counts(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sync: synced entities=%d edges=%d | graph nodes=%d rels=%d\n",
		ne, nr, nodes, rels)
	return nil
}
