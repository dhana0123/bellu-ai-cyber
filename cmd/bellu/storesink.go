package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/pipeline"
	"github.com/bellu/bellu-ai-cyber/internal/redact"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newStoreSink connects to the Postgres evidence store and returns a sink that
// records a collection run and persists events. If the store is unreachable
// it falls back to stdout so the pipeline is still demonstrable without a DB.
func newStoreSink(ctx context.Context, cfg config.Config, runID, sourceName string) (pipeline.Sink, []pipeline.Stage, func(), error) {
	redactor, err := redact.New(cfg.Redaction.KeyPatterns, cfg.Redaction.JSONPaths)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("redaction: %w", err)
	}
	stages := []pipeline.Stage{redactor}

	st, err := store.Open(ctx, cfg.Postgres.DSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: evidence store unavailable (%v); writing to stdout\n", err)
		sink, _, closeFn, _ := stdoutFallback()
		return sink, stages, closeFn, nil
	}

	if err := st.StartRun(ctx, runID, sourceName); err != nil {
		st.Close()
		return nil, nil, nil, fmt.Errorf("start run: %w", err)
	}

	sink := store.NewSink(st, runID)
	closeFn := func() {
		// Finalize the run status based on whether writing errored. The sink
		// tracks its own error state.
		_ = st.FinishRun(context.Background(), runID, sink.Status(), sink.Written())
		st.Close()
	}
	return sink, stages, closeFn, nil
}
