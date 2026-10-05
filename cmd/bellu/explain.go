package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// explainCmd prints the evidence chain for an edge: the edge, its endpoints,
// and every raw event that justified it (which collector saw it and when).
//
// Usage:
//
//	bellu explain <edge-id>
//	bellu explain --find --relation CAN_READ --like s3   (locate edge ids)
func explainCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	cfgPath := fs.String("config", "bellu.yaml", "path to bellu.yaml")
	find := fs.Bool("find", false, "list matching edge ids instead of explaining one")
	relation := fs.String("relation", "", "filter by relation (with --find)")
	like := fs.String("like", "", "filter by endpoint substring (with --find)")
	limit := fs.Int("limit", 20, "max rows for --find")
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

	if *find {
		edges, err := st.FindEdges(ctx, *relation, *like, *limit)
		if err != nil {
			return err
		}
		for _, e := range edges {
			fmt.Printf("#%d  %s -[%s]-> %s  (confidence %.2f)\n", e.ID, e.SrcID, e.Relation, e.DstID, e.Confidence)
		}
		return nil
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: bellu explain <edge-id>  (or --find)")
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid edge id %q", rest[0])
	}

	d, err := st.ExplainEdge(ctx, id)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "edge #%d: %s -[%s]-> %s  (confidence %.2f)\n",
		d.ID, d.SrcID, d.Relation, d.DstID, d.Confidence)
	if len(d.Evidence) == 0 {
		fmt.Fprintln(os.Stdout, "  (no evidence recorded)")
		return nil
	}
	fmt.Fprintln(os.Stdout, "  evidence:")
	for _, e := range d.Evidence {
		fmt.Fprintf(os.Stdout, "    - %s observed %s %s (run %s, raw %s)\n",
			e.Collector, e.ResourceType, e.ResourceID, e.RunID, e.RawEventID)
	}
	return nil
}
