package cloudquery

// Capability gating for the CloudQuery source. The semantics mirror the
// Steampipe source one-for-one so the collector swap does not change which
// tables a connection reads: a table whose capability is not in the
// connection's enabled set is excluded from the read set, with "core" and the
// empty (ungated) capability always collected, and a nil/empty enabled set
// meaning "no gating configured" (collect everything, backward compatible).
//
// Keeping these helpers in their own file keeps cloudquery.go focused on the
// staging-row read loop while the gate stays close to its tests.

// capsToSet builds the enabled-capability lookup set, always including core so
// core tables are never gated out even if a caller omits it. A nil/empty input
// (or an input of only empty strings) yields a nil set, which the gate treats
// as "no gating configured" and collects every table.
func capsToSet(caps []string) map[string]bool {
	if len(caps) == 0 {
		return nil
	}
	set := make(map[string]bool, len(caps)+1)
	for _, c := range caps {
		if c != "" {
			set[c] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	set["core"] = true
	return set
}

// capsFromConfig extracts the enabled-capability list from a Factory config
// value. A caller may pass []string (the typed path from the connector adapter)
// or []any (e.g. after a JSON/YAML round-trip). Any other type — including a
// missing key — yields nil, which capsToSet treats as "no gating configured"
// so the collector preserves its collect-everything behavior.
func capsFromConfig(v any) []string {
	switch caps := v.(type) {
	case []string:
		return caps
	case []any:
		out := make([]string, 0, len(caps))
		for _, c := range caps {
			if s, ok := c.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// capabilityEnabled reports whether a table with the given capability should be
// collected for this source. The gate is deliberately permissive so it can
// only ever skip tables that are safe to skip:
//   - no gating configured (nil/empty enabled set): collect everything, exactly
//     as before (backward compatible).
//   - ungated table (empty capability): always collected.
//   - "core": always collected (core permissions are always granted).
//   - otherwise: collected only if the capability is in the enabled set.
func (s *Source) capabilityEnabled(capability string) bool {
	if len(s.enabledCaps) == 0 {
		return true
	}
	if capability == "" || capability == "core" {
		return true
	}
	return s.enabledCaps[capability]
}

// setEnabledCapabilitiesForTest sets the enabled capability set on the Source.
// Exposed for tests that construct a Source via New; production wiring passes
// the enabled set when the Factory is added (a later task).
func (s *Source) setEnabledCapabilitiesForTest(caps []string) {
	s.enabledCaps = capsToSet(caps)
}
