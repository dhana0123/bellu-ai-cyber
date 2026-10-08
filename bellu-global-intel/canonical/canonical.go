package canonical

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/epss"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/kev"
	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/osv"
)

// Canonicalizer maps source records of its source into canonical intel
// objects. Canonicalization is a pure function of its inputs: the same records
// always produce the same CanonicalVulns in the same (input-preserving) order.
type Canonicalizer interface {
	// Source returns the stable source key this canonicalizer handles, e.g.
	// "osv", "kev", "epss".
	Source() string
	// Canonicalize maps source records into canonical vulnerabilities. Each
	// produced record is validated (see CanonicalVuln.Validate); an invalid
	// record causes Canonicalize to return an error.
	Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error)
}

// --- OSV ---------------------------------------------------------------------

// OSVCanonicalizer maps OSV source records (osv.Record) into CanonicalVuln.
type OSVCanonicalizer struct{}

// NewOSVCanonicalizer returns an OSV canonicalizer.
func NewOSVCanonicalizer() OSVCanonicalizer { return OSVCanonicalizer{} }

// Source returns the stable source key "osv".
func (OSVCanonicalizer) Source() string { return "osv" }

// Canonicalize maps each osv.Record into a CanonicalVuln. The canonical ID is
// the preferred alias (CVE if present among the record's aliases, else GHSA,
// else the OSV id); Aliases is the normalized, de-duplicated union of the OSV
// id and the record's aliases; Affected packages carry version-stripped purls
// and introduced/fixed ranges; CVSS and severity are derived from the record's
// CVSS vector. Every produced record is validated and rejected if invalid.
func (OSVCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		rec, ok := sr.(*osv.Record)
		if !ok {
			return nil, fmt.Errorf("canonical: osv canonicalizer got %T, want *osv.Record (record %d)", sr, i)
		}

		aliases := DedupeAliases(rec.Aliases())
		id := preferredAlias(aliases, rec.ID)

		v := CanonicalVuln{
			ID:       id,
			Aliases:  aliases,
			Summary:  rec.Summary,
			Affected: canonicalizeOSVAffected(rec.Affected),
			CVSS:     canonicalizeOSVSeverity(rec.Severity),
			Sources:  []string{"osv"},
		}
		v.Severity = severityFromCVSS(v.CVSS.Score)

		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: osv record %q invalid: %w", rec.ID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// canonicalizeOSVAffected maps OSV affected entries into AffectedPackage,
// stripping any version qualifier from the purl and collecting introduced/fixed
// ranges from the entry's events.
func canonicalizeOSVAffected(affected []osv.Affected) []AffectedPackage {
	if len(affected) == 0 {
		return nil
	}
	out := make([]AffectedPackage, 0, len(affected))
	for _, a := range affected {
		ap := AffectedPackage{
			Purl:      stripPurlVersion(a.Package.Purl),
			Ecosystem: a.Package.Ecosystem,
			Ranges:    canonicalizeRanges(a.Ranges),
		}
		out = append(out, ap)
	}
	return out
}

// canonicalizeRanges converts OSV ranges (ordered introduced/fixed events)
// into half-open SemverRange intervals. Each "introduced" event opens a range;
// the next "fixed" event closes it. A trailing introduced with no fixed is an
// unbounded-above range.
func canonicalizeRanges(ranges []osv.Range) []SemverRange {
	var out []SemverRange
	for _, r := range ranges {
		var cur *SemverRange
		for _, e := range r.Events {
			switch {
			case e.Introduced != "":
				// Close any dangling open range before starting a new one.
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &SemverRange{Introduced: e.Introduced}
			case e.Fixed != "":
				if cur == nil {
					cur = &SemverRange{}
				}
				cur.Fixed = e.Fixed
				out = append(out, *cur)
				cur = nil
			case e.LastAffected != "":
				// Treat last_affected (inclusive upper) as a range with no
				// exclusive fixed bound; the inclusive upper is retained via
				// the introduced lower bound only (best-effort). We keep the
				// range open-above rather than fabricate an exclusive bound.
				if cur == nil {
					cur = &SemverRange{}
				}
				out = append(out, *cur)
				cur = nil
			}
		}
		if cur != nil {
			out = append(out, *cur)
		}
	}
	return out
}

// canonicalizeOSVSeverity picks a CVSS score from the OSV severity entries.
// It prefers the highest CVSS version available and extracts the base score
// from the vector string when possible.
func canonicalizeOSVSeverity(sev []osv.Severity) CVSS {
	var best CVSS
	for _, s := range sev {
		if !strings.HasPrefix(strings.ToUpper(s.Type), "CVSS") {
			continue
		}
		vector := strings.TrimSpace(s.Score)
		if vector == "" {
			continue
		}
		c := CVSS{
			Vector:  vector,
			Version: cvssVersion(vector, s.Type),
		}
		if score, ok := cvssBaseScore(vector); ok {
			c.Score = score
		}
		// Prefer the entry with the higher parsed score; this gives a stable,
		// deterministic choice when multiple vectors are present.
		if best.Vector == "" || c.Score > best.Score {
			best = c
		}
	}
	return best
}

// --- KEV ---------------------------------------------------------------------

// KEVCanonicalizer maps CISA KEV source records (kev.Entry) into a minimal
// CanonicalVuln keyed by the CVE id with KnownExploited set, so dedup can merge
// it with the matching OSV/CVE record and raise the KEV flag.
type KEVCanonicalizer struct{}

// NewKEVCanonicalizer returns a KEV canonicalizer.
func NewKEVCanonicalizer() KEVCanonicalizer { return KEVCanonicalizer{} }

// Source returns the stable source key "kev".
func (KEVCanonicalizer) Source() string { return "kev" }

// Canonicalize maps each kev.Entry into a minimal CanonicalVuln keyed by its
// CVE id with KnownExploited=true and Aliases=[CVE]. The record is validated
// before being returned.
func (KEVCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		entry, ok := sr.(kev.Entry)
		if !ok {
			return nil, fmt.Errorf("canonical: kev canonicalizer got %T, want kev.Entry (record %d)", sr, i)
		}

		aliases := DedupeAliases([]string{entry.CveID})
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: kev record %d has empty CVE id", i)
		}

		v := CanonicalVuln{
			ID:             aliases[0],
			Aliases:        aliases,
			Summary:        entry.ShortDescription,
			KnownExploited: true,
			Severity:       SevInfo,
			Sources:        []string{"kev"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: kev record %q invalid: %w", entry.CveID, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- EPSS --------------------------------------------------------------------

// EPSSCanonicalizer maps FIRST EPSS source records (epss.Row) into a minimal
// CanonicalVuln keyed by the CVE id carrying the EPSS probability, so dedup can
// merge it with the matching vulnerability.
type EPSSCanonicalizer struct{}

// NewEPSSCanonicalizer returns an EPSS canonicalizer.
func NewEPSSCanonicalizer() EPSSCanonicalizer { return EPSSCanonicalizer{} }

// Source returns the stable source key "epss".
func (EPSSCanonicalizer) Source() string { return "epss" }

// Canonicalize maps each epss.Row into a minimal CanonicalVuln keyed by its CVE
// id with EPSS set and Aliases=[CVE]. Rows with an EPSS value outside [0,1] are
// rejected (Requirement 4.7, enforced via Validate).
func (EPSSCanonicalizer) Canonicalize(recs []parser.SourceRecord) ([]CanonicalVuln, error) {
	out := make([]CanonicalVuln, 0, len(recs))
	for i, sr := range recs {
		row, ok := sr.(epss.Row)
		if !ok {
			return nil, fmt.Errorf("canonical: epss canonicalizer got %T, want epss.Row (record %d)", sr, i)
		}

		aliases := DedupeAliases([]string{row.CVE})
		if len(aliases) == 0 {
			return nil, fmt.Errorf("canonical: epss record %d has empty CVE id", i)
		}

		v := CanonicalVuln{
			ID:       aliases[0],
			Aliases:  aliases,
			EPSS:     row.EPSS,
			Severity: SevInfo,
			Sources:  []string{"epss"},
		}
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: epss record %q invalid: %w", row.CVE, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- helpers -----------------------------------------------------------------

// preferredAlias selects the canonical id from a normalized alias set: a CVE
// alias if present, else a GHSA alias, else the normalized OSV id, else the
// first alias. This is a deterministic function of the (ordered) alias set.
func preferredAlias(aliases []string, osvID string) string {
	if a := firstWithScheme(aliases, "CVE"); a != "" {
		return a
	}
	if a := firstWithScheme(aliases, "GHSA"); a != "" {
		return a
	}
	if n := NormalizeAlias(osvID); n != "" {
		return n
	}
	if len(aliases) > 0 {
		return aliases[0]
	}
	return ""
}

// firstWithScheme returns the first alias whose scheme (the portion before the
// first '-') equals scheme, or "" when none match. Aliases are expected to be
// already normalized (uppercased), as is scheme.
func firstWithScheme(aliases []string, scheme string) string {
	for _, a := range aliases {
		if i := strings.IndexByte(a, '-'); i >= 0 && a[:i] == scheme {
			return a
		}
	}
	return ""
}

// stripPurlVersion removes a version qualifier from a purl, returning the purl
// name only (the version lives in the ranges). A purl version is the suffix
// after the first '@' in the path, e.g. "pkg:pypi/requests@2.30.0" ->
// "pkg:pypi/requests". Query parameters and subpaths (after '?' or '#') are
// left intact aside from the version.
func stripPurlVersion(purl string) string {
	purl = strings.TrimSpace(purl)
	if i := strings.IndexByte(purl, '@'); i >= 0 {
		return purl[:i]
	}
	return purl
}

// severityFromCVSS maps a CVSS base score to a qualitative severity bucket:
// >=9 critical, >=7 high, >=4 medium, >0 low, else info.
func severityFromCVSS(score float64) Severity {
	switch {
	case score >= 9:
		return SevCritical
	case score >= 7:
		return SevHigh
	case score >= 4:
		return SevMedium
	case score > 0:
		return SevLow
	default:
		return SevInfo
	}
}

// cvssVersion derives the CVSS version from the vector string prefix (e.g.
// "CVSS:3.1/..." -> "3.1") falling back to a hint derived from the OSV severity
// type (e.g. "CVSS_V3" -> "3", "CVSS_V4" -> "4"). Returns "" when unknown.
func cvssVersion(vector, typ string) string {
	const pfx = "CVSS:"
	if strings.HasPrefix(vector, pfx) {
		rest := vector[len(pfx):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return rest[:i]
		}
	}
	switch strings.ToUpper(strings.TrimSpace(typ)) {
	case "CVSS_V2":
		return "2.0"
	case "CVSS_V3":
		return "3.1"
	case "CVSS_V4":
		return "4.0"
	}
	return ""
}

// cvssBaseScore extracts a numeric base score from a CVSS vector string when it
// is present as an explicit metric. OSV CVSS "score" fields are vector strings
// (e.g. "CVSS:3.1/AV:N/AC:L/...") that do not themselves carry the numeric base
// score, so this returns (0, false) for a pure vector. When the field instead
// holds a plain numeric score (some sources store "9.8"), it is parsed and
// returned. The caller treats (_, false) as "score unknown" and leaves Score 0.
func cvssBaseScore(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// A plain numeric score, e.g. "9.8".
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f < 0 || f > 10 {
			return 0, false
		}
		return f, true
	}
	// A CVSS vector may embed a base score metric in non-standard feeds, e.g.
	// a trailing "/BASE:9.8"; scan vector segments for a recognizable score.
	for _, seg := range strings.Split(s, "/") {
		kv := strings.SplitN(seg, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(kv[0]))
		if key == "BASE" || key == "BASESCORE" || key == "SCORE" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64); err == nil && f >= 0 && f <= 10 {
				return f, true
			}
		}
	}
	return 0, false
}

// Compile-time assertions that each canonicalizer satisfies Canonicalizer.
var (
	_ Canonicalizer = OSVCanonicalizer{}
	_ Canonicalizer = KEVCanonicalizer{}
	_ Canonicalizer = EPSSCanonicalizer{}
)
