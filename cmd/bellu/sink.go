package main

import (
	"context"
	"os"

	"github.com/bellu/bellu-ai-cyber/internal/config"
	"github.com/bellu/bellu-ai-cyber/internal/pipeline"
)

// buildSink selects the pipeline sink and stages. In Task 1 only the stdout
// sink exists; Task 2 adds the Postgres store and the redaction stage.
//
// It returns the sink, the ordered stages, and a close function to release
// resources.
func buildSink(ctx context.Context, cfg config.Config, runID, sourceName string) (pipeline.Sink, []pipeline.Stage, func(), error) {
	return newStoreSink(ctx, cfg, runID, sourceName)
}

// stdoutFallback is used when no evidence store is reachable.
func stdoutFallback() (pipeline.Sink, []pipeline.Stage, func(), error) {
	return pipeline.NewStdoutSink(os.Stdout), nil, func() {}, nil
}
