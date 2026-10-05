package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/pipeline"
)

func runIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	cfgPath := fs.String("config", "bellu.yaml", "path to bellu.yaml")
	sourceName := fs.String("source", "", "source to collect from (e.g. fixture, cyclonedx, steampipe, zeek)")
	file := fs.String("file", "", "input file (fixture source)")
	dir := fs.String("dir", "", "input directory (cyclonedx, zeek sources)")
	connection := fs.String("connection", "", "connection name (steampipe source)")
	runID := fs.String("run", "", "run id (default: generated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sourceName == "" {
		return fmt.Errorf("--source is required")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	// Per-source config: start from bellu.yaml then overlay CLI flags.
	scfg := map[string]any{}
	for k, v := range cfg.Sources[*sourceName] {
		scfg[k] = v
	}
	if *file != "" {
		scfg["file"] = *file
	}
	if *dir != "" {
		scfg["dir"] = *dir
	}
	if *connection != "" {
		scfg["connection"] = *connection
	}

	reg := buildRegistry()
	src, err := reg.Build(*sourceName, scfg)
	if err != nil {
		return err
	}

	rid := *runID
	if rid == "" {
		rid = pipeline.NewRunID(time.Now())
	}

	// Task 1 sink: stdout. Task 2 replaces this with the Postgres store when
	// a DSN is reachable.
	sink, stages, closeFn, err := buildSink(ctx, cfg, rid, *sourceName)
	if err != nil {
		return err
	}
	defer closeFn()

	p := &pipeline.Pipeline{Source: src, Stages: stages, Sink: sink}
	res, err := p.Run(ctx, rid)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "run %s: collected=%d accepted=%d persisted=%d errors=%d\n",
		res.RunID, res.Collected, res.Written, res.Persisted, len(res.Errors))
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "  - %v\n", e)
	}
	return nil
}
