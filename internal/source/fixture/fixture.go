// Package fixture provides a Source that reads RawEvents from a JSONL file.
// It makes the pipeline runnable before any real collector exists.
package fixture

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// Source reads one JSON object per line and emits it as a RawEvent. Each line
// may set identity fields and payload; RunID is stamped and Finalize is called
// so the event is content-addressed regardless of what the file provided.
type Source struct {
	path string
}

// New returns a fixture Source reading from path.
func New(path string) *Source { return &Source{path: path} }

// Factory builds a fixture Source from cfg["file"].
func Factory(cfg map[string]any) (source.Source, error) {
	path, _ := cfg["file"].(string)
	if path == "" {
		return nil, fmt.Errorf("fixture: config requires a non-empty \"file\"")
	}
	return New(path), nil
}

// Name implements source.Source.
func (s *Source) Name() string { return "fixture" }

// Collect reads the JSONL file and streams events.
func (s *Source) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		f, err := os.Open(s.path)
		if err != nil {
			errs <- fmt.Errorf("fixture: open %s: %w", s.path, err)
			return
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			raw := sc.Bytes()
			if len(raw) == 0 {
				continue
			}
			var ev rawevent.RawEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				errs <- fmt.Errorf("fixture: line %d: %w", line, err)
				continue
			}
			ev.RunID = run.RunID
			ev.Finalize()
			select {
			case events <- ev:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if err := sc.Err(); err != nil {
			errs <- fmt.Errorf("fixture: scan: %w", err)
		}
	}()

	return events, errs
}
