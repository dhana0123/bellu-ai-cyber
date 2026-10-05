package zeek

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// offsetPath returns the sidecar file storing a log's consumed byte offset.
func (s *Source) offsetPath(name string) string {
	return filepath.Join(s.offsetDir, "."+name+".offset")
}

// loadOffset reads the saved byte offset for a log, or 0 if none.
func (s *Source) loadOffset(name string) int64 {
	b, err := os.ReadFile(s.offsetPath(name))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// saveOffset persists the consumed byte offset for a log.
func (s *Source) saveOffset(name string, offset int64) error {
	return os.WriteFile(s.offsetPath(name), []byte(strconv.FormatInt(offset, 10)), 0o644)
}

// zeekTime extracts the record timestamp. Zeek JSON "ts" is epoch seconds (a
// float). Defaults to now (UTC) when absent.
func zeekTime(rec map[string]any) time.Time {
	switch v := rec["ts"].(type) {
	case float64:
		sec := int64(v)
		nsec := int64((v - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func recString(rec map[string]any, key string) string {
	if v, ok := rec[key].(string); ok {
		return v
	}
	return ""
}

func recFloat(rec map[string]any, key string) float64 {
	switch v := rec[key].(type) {
	case float64:
		return v
	}
	return 0
}
