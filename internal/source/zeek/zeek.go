// Package zeek reads Zeek JSON logs (conn.log, dns.log, ssl.log, http.log) and
// emits a RawEvent per record. Reruns are idempotent via per-file byte offsets
// persisted next to the logs.
package zeek

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
	"github.com/bellu/bellu-ai-cyber/internal/source"
)

// Resource types for emitted events.
const (
	RTConn = "zeek.conn"
	RTDNS  = "zeek.dns"
	RTSSL  = "zeek.ssl"
	RTHTTP = "zeek.http"
)

// logFiles maps a Zeek log filename to its resource type.
var logFiles = map[string]string{
	"conn.log": RTConn,
	"dns.log":  RTDNS,
	"ssl.log":  RTSSL,
	"http.log": RTHTTP,
}

// Source reads Zeek logs from a directory.
type Source struct {
	dir string
	// offsetDir is where per-file byte offsets are stored; defaults to dir.
	offsetDir string
}

// New returns a Zeek source reading from dir.
func New(dir string) *Source { return &Source{dir: dir, offsetDir: dir} }

// Factory builds a Source from cfg["dir"].
func Factory(cfg map[string]any) (source.Source, error) {
	dir, _ := cfg["dir"].(string)
	if dir == "" {
		return nil, fmt.Errorf("zeek: config requires \"dir\"")
	}
	s := New(dir)
	if od, ok := cfg["offset_dir"].(string); ok && od != "" {
		s.offsetDir = od
	}
	return s, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return "zeek" }

// Collect reads each known log file from the saved offset to EOF, emits one
// event per JSON record, and persists the new offset so reruns don't reprocess
// already-ingested lines.
func (s *Source) Collect(ctx context.Context, run source.Run) (<-chan rawevent.RawEvent, <-chan error) {
	events := make(chan rawevent.RawEvent)
	errs := make(chan error, len(logFiles)+1)

	go func() {
		defer close(events)
		defer close(errs)

		for name, rt := range logFiles {
			if err := ctx.Err(); err != nil {
				errs <- err
				return
			}
			path := filepath.Join(s.dir, name)
			if _, err := os.Stat(path); err != nil {
				continue // log not present; skip
			}
			if err := s.readLog(ctx, run, path, name, rt, events); err != nil {
				errs <- fmt.Errorf("zeek: %s: %w", name, err)
			}
		}
	}()

	return events, errs
}

func (s *Source) readLog(ctx context.Context, run source.Run, path, name, rt string, out chan<- rawevent.RawEvent) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	start := s.loadOffset(name)
	if start > 0 {
		if _, err := f.Seek(start, 0); err != nil {
			return err
		}
	}

	br := bufio.NewReader(f)
	consumed := start
	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	seq := 0
	for sc.Scan() {
		line := sc.Bytes()
		consumed += int64(len(line)) + 1 // + newline
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip malformed line
		}
		ev := s.event(run, rt, rec, name, seq)
		seq++
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return s.saveOffset(name, consumed)
}

// event builds a RawEvent from a Zeek record. The resource id is the Zeek "uid"
// when present (conn/ssl/http share uids with conn), else a synthetic id.
func (s *Source) event(run source.Run, rt string, rec map[string]any, name string, seq int) rawevent.RawEvent {
	rid := recString(rec, "uid")
	if rid == "" {
		// dns.log uses uid too, but fall back to a per-file sequence id.
		rid = fmt.Sprintf("%s/%d", name, seq)
	}
	// Scope by uid+type so the same uid across conn/dns/ssl/http dedupe per log.
	resourceID := rt + ":" + rid
	payload, _ := json.Marshal(rec)
	ev := rawevent.RawEvent{
		RunID:        run.RunID,
		Source:       "zeek",
		Collector:    "zeek:sensor",
		Provider:     "network",
		ResourceType: rt,
		ResourceID:   resourceID,
		ObservedAt:   zeekTime(rec),
		Payload:      payload,
	}
	ev.Finalize()
	return ev
}
