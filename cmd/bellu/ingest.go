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

	overrides := map[string]any{}
	if *file != "" {
		overrides["file"] = *file
	}
	if *dir != "" {
		overrides["dir"] = *dir
	}
	if *connection != "" {
		overrides["connection"] = *connection
	}

	rid := *runID
	if rid == "" {
		rid = pipeline.NewRunID(time.Now())
	}

	res, err := ingestOne(ctx, cfg, *sourceName, overrides, rid)
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

// ingestOne runs a single source through the pipeline into the evidence store.
// It is shared by the `ingest` and `run` commands.
func ingestOne(ctx context.Context, cfg config.Config, sourceName string, overrides map[string]any, runID string) (pipeline.Result, error) {
	// Per-source config: start from bellu.yaml then overlay overrides.
	scfg := map[string]any{}
	for k, v := range cfg.Sources[sourceName] {
		scfg[k] = v
	}
	for k, v := range overrides {
		scfg[k] = v
	}

	reg := buildRegistry()
	src, err := reg.Build(sourceName, scfg)
	if err != nil {
		return pipeline.Result{}, err
	}

	sink, stages, closeFn, err := buildSink(ctx, cfg, runID, sourceName)
	if err != nil {
		return pipeline.Result{}, err
	}
	defer closeFn()

	p := &pipeline.Pipeline{Source: src, Stages: stages, Sink: sink}
	return p.Run(ctx, runID)
}
