package release

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// fixedCreatedAt is a stable timestamp used so BuildFrom produces reproducible
// bundles across runs (determinism is only guaranteed for a fixed Meta).
var fixedCreatedAt = time.Date(2024, 3, 2, 1, 0, 0, 0, time.UTC)

// corpus returns a small set of valid CanonicalVuln records deliberately kept
// in UNSORTED order, with unsorted inner slices (aliases, affected packages,
// ranges, fixed-in, sources). ExportJSONL must sort all of this so that
// logically-equal corpora serialize to identical bytes.
func corpus() []canonical.CanonicalVuln {
	vulns := []canonical.CanonicalVuln{
		{
			ID:      "CVE-2024-0002",
			Aliases: []string{"PYSEC-2024-9", "CVE-2024-0002", "GHSA-ZZZZ-YYYY-XXXX"},
			Summary: "second record, listed first",
			Affected: []canonical.AffectedPackage{
				{
					Purl:      "pkg:pypi/requests",
					Ecosystem: "PyPI",
					Ranges: []canonical.SemverRange{
						{Introduced: "2.0.0", Fixed: "2.5.0"},
						{Introduced: "1.0.0", Fixed: "1.9.0"},
					},
					FixedIn: []string{"2.5.0", "1.9.0"},
				},
				{
					Purl:      "pkg:npm/left-pad",
					Ecosystem: "npm",
					Ranges: []canonical.SemverRange{
						{Introduced: "0.0.1", Fixed: "1.0.0"},
					},
				},
			},
			CVSS:           canonical.CVSS{Vector: "CVSS:3.1/AV:N", Score: 7.5, Version: "3.1"},
			Severity:       canonical.SevHigh,
			KnownExploited: true,
			EPSS:           0.42,
			Published:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Modified:       time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Sources:        []string{"osv", "kev", "epss"},
		},
		{
			ID:      "CVE-2024-0001",
			Aliases: []string{"GHSA-AAAA-BBBB-CCCC", "CVE-2024-0001"},
			Summary: "first record, listed second",
			Affected: []canonical.AffectedPackage{
				{
					Purl:      "pkg:golang/github.com/foo/bar",
					Ecosystem: "Go",
					Ranges: []canonical.SemverRange{
						{Introduced: "0", Fixed: "1.2.3"},
					},
				},
			},
			CVSS:     canonical.CVSS{Vector: "CVSS:3.1/AV:L", Score: 4.0, Version: "3.1"},
			Severity: canonical.SevMedium,
			EPSS:     0.01,
			Sources:  []string{"osv"},
		},
	}
	// Sanity: fixtures must be valid canonical records.
	for i, v := range vulns {
		if err := v.Validate(); err != nil {
			panic("invalid fixture at index " + itoa(i) + ": " + err.Error())
		}
	}
	return vulns
}

// itoa is a tiny int->string helper to avoid importing strconv just for a panic
// message in a test fixture.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// shuffledCorpus returns the same logical records as corpus() but with the
// record order and inner-slice order permuted differently. ExportJSONL must
// produce identical bytes for both because it canonicalizes before marshaling.
func shuffledCorpus() []canonical.CanonicalVuln {
	base := corpus()
	// Reverse record order.
	out := []canonical.CanonicalVuln{base[1], base[0]}
	// Permute inner slices of the (now second) record to a different order.
	v := out[1]
	v.Aliases = []string{"CVE-2024-0002", "GHSA-ZZZZ-YYYY-XXXX", "PYSEC-2024-9"}
	v.Sources = []string{"epss", "osv", "kev"}
	affA := v.Affected[0]
	affA.Ranges = []canonical.SemverRange{
		{Introduced: "1.0.0", Fixed: "1.9.0"},
		{Introduced: "2.0.0", Fixed: "2.5.0"},
	}
	affA.FixedIn = []string{"1.9.0", "2.5.0"}
	affB := v.Affected[1]
	// Swap affected package order too.
	v.Affected = []canonical.AffectedPackage{affB, affA}
	out[1] = v
	return out
}

// TestExportJSONLDeterministic asserts byte-identical export for identical data
// and order-independence over a shuffled permutation (Req 16.1, Property 3).
func TestExportJSONLDeterministic(t *testing.T) {
	x := corpus()

	first, err := ExportJSONL(x)
	if err != nil {
		t.Fatalf("ExportJSONL(x) first call: %v", err)
	}
	second, err := ExportJSONL(x)
	if err != nil {
		t.Fatalf("ExportJSONL(x) second call: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("ExportJSONL is not byte-identical across calls on identical data")
	}

	// A shuffled permutation of the same logical records must export identically.
	shuffled, err := ExportJSONL(shuffledCorpus())
	if err != nil {
		t.Fatalf("ExportJSONL(shuffled): %v", err)
	}
	if !bytes.Equal(first, shuffled) {
		t.Fatalf("ExportJSONL is order-dependent: shuffled permutation produced different bytes\nwant:\n%s\ngot:\n%s", first, shuffled)
	}

	// ExportJSONL must not mutate the caller's input (determinism depends on it).
	if !reflect.DeepEqual(x, corpus()) {
		t.Fatalf("ExportJSONL mutated its input slice")
	}
}

// TestBuildFromDeterministic asserts BuildFrom with a FIXED Meta.CreatedAt
// yields byte-identical bundles across two runs (Req 16.1, Property 3).
func TestBuildFromDeterministic(t *testing.T) {
	priv := deterministicKey(t)
	b := NewBuilder()

	var buf1, buf2 bytes.Buffer
	if err := b.BuildFrom(&buf1, corpus(), priv, Meta{CreatedAt: fixedCreatedAt}); err != nil {
		t.Fatalf("BuildFrom first: %v", err)
	}
	if err := b.BuildFrom(&buf2, shuffledCorpus(), priv, Meta{CreatedAt: fixedCreatedAt}); err != nil {
		t.Fatalf("BuildFrom second: %v", err)
	}
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatalf("BuildFrom is not deterministic: identical data + fixed Meta produced different bundles (%d vs %d bytes)", buf1.Len(), buf2.Len())
	}
}

// TestRoundTripVerifyAndImport builds a bundle, verifies it with the matching
// public key, confirms the verified corpus equals the canonicalized input, and
// confirms Import upserts exactly those vulns into a fake sink.
func TestRoundTripVerifyAndImport(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	bundle := mustBuild(t, priv, corpus(), Meta{CreatedAt: fixedCreatedAt})

	vb, err := Verify(bytes.NewReader(bundle), pub)
	if err != nil {
		t.Fatalf("Verify should succeed with the matching public key: %v", err)
	}

	gotVulns, err := vb.Vulns()
	if err != nil {
		t.Fatalf("VerifiedBundle.Vulns: %v", err)
	}

	// The verified corpus must equal the canonicalized/sorted input (the exact
	// bytes ExportJSONL would produce, parsed back).
	wantVulns := canonicalizedCorpus(t, corpus())
	if !reflect.DeepEqual(gotVulns, wantVulns) {
		t.Fatalf("verified corpus does not match canonicalized input\nwant: %+v\ngot:  %+v", wantVulns, gotVulns)
	}

	sink := &recordingSink{}
	if err := ImportBytes(context.Background(), bundle, pub, sink); err != nil {
		t.Fatalf("ImportBytes should succeed on a valid bundle: %v", err)
	}
	if !sink.called {
		t.Fatalf("Import did not upsert any vulns on a valid bundle")
	}
	if !sink.scoredOn.IsZero() {
		t.Fatalf("Import should pass a zero scoredOn for a snapshot bundle, got %v", sink.scoredOn)
	}
	if !reflect.DeepEqual(sink.vulns, wantVulns) {
		t.Fatalf("Import upserted the wrong corpus\nwant: %+v\ngot:  %+v", wantVulns, sink.vulns)
	}
}

// TestVerifyWrongPublicKey asserts a bundle signed by one key is rejected when
// verified against a different key, and that Import performs no writes (Req 16.6).
func TestVerifyWrongPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey (signer): %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey (other): %v", err)
	}

	bundle := mustBuild(t, priv, corpus(), Meta{CreatedAt: fixedCreatedAt})

	if _, err := Verify(bytes.NewReader(bundle), otherPub); !errors.Is(err, ErrVerification) {
		t.Fatalf("Verify with wrong public key: want ErrVerification, got %v", err)
	}

	assertNoWrite(t, bundle, otherPub)
}

// TestVerifyTamperedManifest asserts that mutating the archived manifest after
// signing invalidates the signature and that nothing is upserted (Req 16.6).
func TestVerifyTamperedManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	bundle := mustBuild(t, priv, corpus(), Meta{CreatedAt: fixedCreatedAt})
	members := unpack(t, bundle)

	// Flip a byte in the manifest so the ed25519 signature no longer matches.
	// Pick a byte inside the created_at value region (any content byte works);
	// we mutate a byte that is part of the JSON content, not structural.
	manifest := append([]byte(nil), members[ManifestFileName]...)
	manifest = flipFirstDigit(t, manifest)
	members[ManifestFileName] = manifest

	tampered := repack(t, members, fixedCreatedAt)

	if _, err := Verify(bytes.NewReader(tampered), pub); !errors.Is(err, ErrVerification) {
		t.Fatalf("Verify with tampered manifest: want ErrVerification, got %v", err)
	}

	assertNoWrite(t, tampered, pub)
}

// TestVerifyTamperedDataFile asserts that mutating the data file so its sha256
// no longer matches the manifest is rejected, with no writes (Req 16.6).
func TestVerifyTamperedDataFile(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	bundle := mustBuild(t, priv, corpus(), Meta{CreatedAt: fixedCreatedAt})
	members := unpack(t, bundle)

	data := append([]byte(nil), members[DataFileName]...)
	if len(data) == 0 {
		t.Fatalf("data file is empty; cannot tamper")
	}
	// Flip a byte so the checksum changes but the signed manifest (and thus the
	// signature) stays intact: this exercises the per-file checksum check, not
	// the signature check.
	data[0] ^= 0xFF
	members[DataFileName] = data

	// Confirm we actually broke the recorded checksum (defensive).
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) == recordedSum(t, members[ManifestFileName], DataFileName) {
		t.Fatalf("tampering did not change the data file checksum")
	}

	tampered := repack(t, members, fixedCreatedAt)

	if _, err := Verify(bytes.NewReader(tampered), pub); !errors.Is(err, ErrVerification) {
		t.Fatalf("Verify with tampered data file: want ErrVerification, got %v", err)
	}

	assertNoWrite(t, tampered, pub)
}

// --- helpers -------------------------------------------------------------

// recordingSink is a fake VulnSink that records whether UpsertVulns was called
// and with what arguments. It lets the verification tests assert that a
// rejected bundle produces no intel_* writes.
type recordingSink struct {
	called   bool
	vulns    []canonical.CanonicalVuln
	scoredOn time.Time
}

func (s *recordingSink) UpsertVulns(ctx context.Context, vulns []canonical.CanonicalVuln, scoredOn time.Time) error {
	s.called = true
	s.vulns = vulns
	s.scoredOn = scoredOn
	return nil
}

// deterministicKey derives a fixed ed25519 private key from a constant seed so
// that BuildFrom output is reproducible across runs (the signature is part of
// the archived bytes).
func deterministicKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return ed25519.NewKeyFromSeed(seed)
}

// mustBuild builds a bundle in memory or fails the test.
func mustBuild(t *testing.T, priv ed25519.PrivateKey, vulns []canonical.CanonicalVuln, meta Meta) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := NewBuilder().BuildFrom(&buf, vulns, priv, meta); err != nil {
		t.Fatalf("BuildFrom: %v", err)
	}
	return buf.Bytes()
}

// canonicalizedCorpus returns the corpus exactly as it survives an
// ExportJSONL -> parse round trip, i.e. canonicalized, sorted, and with any
// JSON-marshaling normalization applied. This is the ground truth a verified
// bundle must reproduce.
func canonicalizedCorpus(t *testing.T, vulns []canonical.CanonicalVuln) []canonical.CanonicalVuln {
	t.Helper()
	data, err := ExportJSONL(vulns)
	if err != nil {
		t.Fatalf("ExportJSONL for ground truth: %v", err)
	}
	got, err := parseJSONL(data)
	if err != nil {
		t.Fatalf("parseJSONL for ground truth: %v", err)
	}
	return got
}

// assertNoWrite confirms ImportBytes rejects the given bundle (ErrVerification)
// and that the fake sink's UpsertVulns was never invoked (fail-closed, Req 16.6).
func assertNoWrite(t *testing.T, bundle []byte, pub ed25519.PublicKey) {
	t.Helper()
	sink := &recordingSink{}
	err := ImportBytes(context.Background(), bundle, pub, sink)
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("ImportBytes on a rejected bundle: want ErrVerification, got %v", err)
	}
	if sink.called {
		t.Fatalf("ImportBytes upserted vulns on a rejected bundle (expected no intel_* writes)")
	}
}

// unpack reads every member of a .tar.zst bundle into a name->bytes map.
func unpack(t *testing.T, bundle []byte) map[string][]byte {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	members := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read member %q: %v", hdr.Name, err)
		}
		members[hdr.Name] = body
	}
	return members
}

// repack re-encodes members back into a .tar.zst in a stable order so a
// mutated member can be fed back through Verify. The member order mirrors the
// builder's (data, manifest, signature).
func repack(t *testing.T, members map[string][]byte, modTime time.Time) []byte {
	t.Helper()
	order := []string{DataFileName, ManifestFileName, SignatureFileName}
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	tw := tar.NewWriter(zw)
	write := func(name string, body []byte) {
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    int64(len(body)),
			ModTime: modTime.UTC(),
			Format:  tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %q: %v", name, err)
		}
	}
	written := make(map[string]bool)
	for _, name := range order {
		if body, ok := members[name]; ok {
			write(name, body)
			written[name] = true
		}
	}
	// Write any remaining members not covered by the canonical order.
	for name, body := range members {
		if !written[name] {
			write(name, body)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zstd: %v", err)
	}
	return buf.Bytes()
}

// flipFirstDigit mutates the first ASCII digit found in the manifest JSON so
// the content changes (e.g. a version or timestamp digit) without breaking the
// JSON structure, invalidating the ed25519 signature over the manifest.
func flipFirstDigit(t *testing.T, b []byte) []byte {
	t.Helper()
	for i, c := range b {
		if c >= '0' && c <= '9' {
			if c == '9' {
				b[i] = '0'
			} else {
				b[i] = c + 1
			}
			return b
		}
	}
	t.Fatalf("no digit found in manifest to flip")
	return b
}

// recordedSum extracts the manifest's recorded sha256 for the named file by
// decoding the manifest bytes. It is only used defensively in the data-tamper
// test to confirm the mutation actually changed the checksum.
func recordedSum(t *testing.T, manifestBytes []byte, name string) string {
	t.Helper()
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fe := range m.Files {
		if fe.Name == name {
			return fe.Sha256
		}
	}
	t.Fatalf("manifest has no entry for %q", name)
	return ""
}
