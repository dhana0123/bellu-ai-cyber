package graph

import (
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/store"
)

func isAlreadyExists(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") ||
		strings.Contains(s, "already") && strings.Contains(s, "constraint") ||
		strings.Contains(s, "index already")
}

func chunkEntities(in []store.GraphEntity, n int) [][]store.GraphEntity {
	var out [][]store.GraphEntity
	for i := 0; i < len(in); i += n {
		end := i + n
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}

func chunkEdges(in []store.GraphEdge, n int) [][]store.GraphEdge {
	var out [][]store.GraphEdge
	for i := 0; i < len(in); i += n {
		end := i + n
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// scalarProps flattens an attributes map into Bolt-safe scalar properties.
// Nested maps/slices are dropped (the full detail lives in Postgres evidence);
// only string/number/bool scalars are promoted to graph properties, prefixed
// with "attr_" to avoid clobbering reserved node fields.
func scalarProps(attrs map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			out["attr_"+k] = val
		case bool:
			out["attr_"+k] = val
		case float64, int, int64:
			out["attr_"+k] = val
		}
	}
	return out
}
