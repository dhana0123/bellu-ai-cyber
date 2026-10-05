package pipeline

import (
	"context"
	"encoding/json"
	"io"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// StdoutSink writes each event as a JSON line to the given writer. It is the
// default sink used before the Postgres evidence store is configured.
type StdoutSink struct {
	w   io.Writer
	enc *json.Encoder
}

// NewStdoutSink returns a sink that writes JSONL to w.
func NewStdoutSink(w io.Writer) *StdoutSink {
	return &StdoutSink{w: w, enc: json.NewEncoder(w)}
}

// Write implements Sink.
func (s *StdoutSink) Write(_ context.Context, ev rawevent.RawEvent) error {
	return s.enc.Encode(ev)
}

// Flush implements Sink. Nothing is buffered.
func (s *StdoutSink) Flush(_ context.Context) error { return nil }
