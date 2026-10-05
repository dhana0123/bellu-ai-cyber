// Package pipeline orchestrates a collection run: it drives a Source, applies
// stages (redaction), and writes to a Sink. V1 runs in batch. The Source ->
// Sink boundary is stable; a message bus can be slotted in later.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// Sink consumes validated, redacted RawEvents. Flush is called once at the end
// of a successful run so buffered sinks can commit.
type Sink interface {
	Write(ctx context.Context, ev rawevent.RawEvent) error
	Flush(ctx context.Context) error
}

// Persister is an optional Sink capability that reports how many rows were
// actually persisted (as opposed to accepted/deduplicated). Sinks that back a
// store implement it so the run summary can show the true insert count.
type Persister interface {
	Persisted() int
}

// Stage transforms an event before it reaches the sink (e.g. redaction).
type Stage interface {
	Apply(ev rawevent.RawEvent) (rawevent.RawEvent, error)
}

// Result summarizes a completed run.
type Result struct {
	RunID     string
	Collected int // events read from the source and validated
	Written   int // events accepted by the sink (before dedupe)
	Persisted int // rows actually persisted (nil-safe; 0 if sink can't report)
	Errors    []error
}

// NewRunID returns a time-sortable run identifier.
func NewRunID(now time.Time) string {
	return fmt.Sprintf("run-%s", now.UTC().Format("20060102T150405.000000000Z"))
}

// Pipeline wires a source through stages to a sink.
type Pipeline struct {
	Source source.Source
	Stages []Stage
	Sink   Sink
}

// Run collects from the source, applies stages, validates, and writes to the
// sink. Non-fatal per-event errors are accumulated in Result; a fatal source
// error (returned on the error channel) does not stop already-collected
// events from being written.
func (p *Pipeline) Run(ctx context.Context, runID string) (Result, error) {
	res := Result{RunID: runID}

	events, errs := p.Source.Collect(ctx, source.Run{RunID: runID})

	// Drain both channels until closed.
	for events != nil || errs != nil {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			res.Collected++
			out := ev
			var stageErr error
			for _, st := range p.Stages {
				out, stageErr = st.Apply(out)
				if stageErr != nil {
					res.Errors = append(res.Errors, fmt.Errorf("stage: %w", stageErr))
					break
				}
			}
			if stageErr != nil {
				continue
			}
			// Re-finalize in case a stage mutated the payload, then validate.
			out.Finalize()
			if err := out.Validate(); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("validate %s: %w", out.ResourceID, err))
				continue
			}
			if err := p.Sink.Write(ctx, out); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("write %s: %w", out.ResourceID, err))
				continue
			}
			res.Written++
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("source: %w", err))
			}
		case <-ctx.Done():
			return res, ctx.Err()
		}
	}

	if err := p.Sink.Flush(ctx); err != nil {
		return res, fmt.Errorf("flush: %w", err)
	}
	if p, ok := p.Sink.(Persister); ok {
		res.Persisted = p.Persisted()
	}
	return res, nil
}
