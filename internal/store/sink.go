package store

import (
	"context"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Sink buffers events and flushes them to the store in batches. It implements
// pipeline.Sink.
type Sink struct {
	st        *Store
	runID     string
	buf       []rawevent.RawEvent
	batchSize int
	written   int
	failed    bool
}

// NewSink returns a store-backed sink for the given run.
func NewSink(st *Store, runID string) *Sink {
	return &Sink{st: st, runID: runID, batchSize: 500}
}

// Write buffers an event, flushing when the batch is full.
func (s *Sink) Write(ctx context.Context, ev rawevent.RawEvent) error {
	s.buf = append(s.buf, ev)
	if len(s.buf) >= s.batchSize {
		return s.flushBuf(ctx)
	}
	return nil
}

// Flush writes any remaining buffered events.
func (s *Sink) Flush(ctx context.Context) error {
	return s.flushBuf(ctx)
}

func (s *Sink) flushBuf(ctx context.Context) error {
	if len(s.buf) == 0 {
		return nil
	}
	n, err := s.st.InsertEvents(ctx, s.buf)
	s.buf = s.buf[:0]
	if err != nil {
		s.failed = true
		return err
	}
	s.written += n
	return nil
}

// Written returns the number of newly inserted rows so far.
func (s *Sink) Written() int { return s.written }

// Persisted implements pipeline.Persister: the count of rows actually inserted
// (duplicates excluded).
func (s *Sink) Persisted() int { return s.written }

// Status returns the run status to record: "failed" if any write errored,
// else "completed".
func (s *Sink) Status() string {
	if s.failed {
		return "failed"
	}
	return "completed"
}
