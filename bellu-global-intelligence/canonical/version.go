package canonical

import (
	"strconv"
	"strings"
)

// Contains reports whether version v falls inside the half-open range
// [Introduced, Fixed): Introduced <= v < Fixed.
//
// Empty or "0" Introduced means "from the start" (lower bound satisfied).
// Empty Fixed means "unbounded above" (upper bound satisfied).
func (r SemverRange) Contains(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	// Lower bound: Introduced <= v.
	intro := strings.TrimSpace(r.Introduced)
	if intro != "" && intro != "0" {
		if compareVersions(v, intro) < 0 {
			return false
		}
	}
	// Upper bound: v < Fixed.
	fixed := strings.TrimSpace(r.Fixed)
	if fixed != "" {
		if compareVersions(v, fixed) >= 0 {
			return false
		}
	}
	return true
}

// compareVersions compares two dotted version strings component-by-component.
// Numeric components compare numerically; non-numeric compare lexically, and a
// numeric component is considered greater than a non-numeric one at the same
// position (so "1.0.0" > "1.0.0rc1" is handled approximately). Missing trailing
// components are treated as 0. Returns -1, 0, or 1.
//
// This is intentionally a pragmatic comparator covering semver and common
// PyPI/npm version forms; exact PEP 440 / semver pre-release ordering is a
// future refinement.
func compareVersions(a, b string) int {
	as := splitVersion(a)
	bs := splitVersion(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		aPresent := i < len(as)
		bPresent := i < len(bs)
		var ac, bc string
		if aPresent {
			ac = as[i]
		}
		if bPresent {
			bc = bs[i]
		}
		// A missing trailing component means "release" relative to a
		// pre-release token on the other side: "1.0.0" > "1.0.0rc1".
		// If the present component is numeric, the missing side is the
		// smaller version ("1.0" < "1.0.1").
		if aPresent != bPresent {
			present := bc
			if aPresent {
				present = ac
			}
			if isNumericComponent(present) {
				// extra numeric component -> the longer version is greater
				if aPresent {
					return 1
				}
				return -1
			}
			// extra non-numeric (pre-release) component -> shorter is greater
			if aPresent {
				return -1
			}
			return 1
		}
		if c := compareComponent(ac, bc); c != 0 {
			return c
		}
	}
	return 0
}

func splitVersion(v string) []string {
	// Normalize separators: treat '.', '-', '+', '_' as component separators,
	// and split a trailing alpha suffix (e.g. "1.0rc1" -> "1","0","rc1").
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '+' || r == '_'
	})
	// Further split mixed alphanumeric like "0rc1" into "0","rc1".
	var out []string
	for _, f := range fields {
		out = append(out, splitAlphaNum(f)...)
	}
	if len(out) == 0 {
		return []string{v}
	}
	return out
}

// splitAlphaNum splits a token at numeric/alpha boundaries: "0rc1" -> ["0","rc","1"].
func splitAlphaNum(s string) []string {
	var out []string
	var cur strings.Builder
	var curDigit bool
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i, r := range s {
		d := r >= '0' && r <= '9'
		if i > 0 && d != curDigit {
			flush()
		}
		cur.WriteRune(r)
		curDigit = d
	}
	flush()
	return out
}

func isNumericComponent(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

func compareComponent(a, b string) int {
	if a == b {
		return 0
	}
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
		return 0
	case aerr == nil && berr != nil:
		// numeric > non-numeric (e.g. release > pre-release token)
		return 1
	case aerr != nil && berr == nil:
		return -1
	default:
		if a < b {
			return -1
		}
		return 1
	}
}
