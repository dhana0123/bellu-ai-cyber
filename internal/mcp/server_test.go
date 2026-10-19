package mcp

import "testing"

// TestClampDepth pins the get_neighbors depth clamp to the [1,5] window the
// tool promises (Req 10.4): a request above 5 is pulled down to 5, a
// non-positive request is lifted to 1, and anything already inside the window
// passes through unchanged. The clamp is what lets GetNeighbors interpolate the
// depth into the Cypher traversal bound safely — it can only ever be a small
// integer in range.
func TestClampDepth(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero lifts to min", 0, 1},
		{"negative lifts to min", -3, 1},
		{"min passes through", 1, 1},
		{"middle passes through", 3, 3},
		{"max passes through", 5, 5},
		{"above max caps at five", 6, 5},
		{"far above max caps at five", 1000, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampDepth(tc.in); got != tc.want {
				t.Fatalf("clampDepth(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestLaneForEdge pins the per-edge lane tag get_neighbors stamps on every edge
// (Req 10.5): an edge whose projection carries inferred=true is surfaced as the
// derived lane, and a base (observed) edge is surfaced as asserted. The two
// labels are the only values the tool emits, so a client can split a mixed
// adjacency into facts versus inference without reading the raw node property.
func TestLaneForEdge(t *testing.T) {
	if got := laneForEdge(true); got != laneInferred {
		t.Fatalf("laneForEdge(true) = %q, want %q", got, laneInferred)
	}
	if got := laneForEdge(false); got != laneAsserted {
		t.Fatalf("laneForEdge(false) = %q, want %q", got, laneAsserted)
	}
	// The two lane labels are distinct, so a tagged edge is unambiguous.
	if laneAsserted == laneInferred {
		t.Fatalf("lane labels must be distinct: both are %q", laneAsserted)
	}
}
