package zeek

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// copyLogsToTemp copies the fixture logs into a temp dir so offset sidecar
// files don't pollute testdata and each test starts clean.
func copyLogsToTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"conn.log", "dns.log", "ssl.log"} {
		b, err := os.ReadFile(filepath.Join("../../../testdata/zeek", name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func collect(t *testing.T, s *Source) []rawevent.RawEvent {
	t.Helper()
	evc, errc := s.Collect(context.Background(), source.Run{RunID: "run-test"})
	var events []rawevent.RawEvent
	for evc != nil || errc != nil {
		select {
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("invalid event: %v", err)
			}
			events = append(events, ev)
		case err, ok := <-errc:
			if !ok {
				errc = nil
				continue
			}
			t.Errorf("source error: %v", err)
		}
	}
	return events
}

func TestZeekParsesLogs(t *testing.T) {
	dir := copyLogsToTemp(t)
	events := collect(t, New(dir))
	// 3 conn + 2 dns + 1 ssl = 6.
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
	byType := map[string]int{}
	for _, e := range events {
		byType[e.ResourceType]++
	}
	if byType[RTConn] != 3 || byType[RTDNS] != 2 || byType[RTSSL] != 1 {
		t.Fatalf("unexpected type counts: %v", byType)
	}
}

func TestZeekOffsetIdempotent(t *testing.T) {
	dir := copyLogsToTemp(t)
	s := New(dir)
	first := collect(t, s)
	if len(first) == 0 {
		t.Fatal("expected events on first read")
	}
	// Second read from the same source should consume nothing new (offsets
	// persisted at EOF).
	second := collect(t, New(dir))
	if len(second) != 0 {
		t.Fatalf("expected 0 events on rerun (idempotent offsets), got %d", len(second))
	}
}
