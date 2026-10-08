// Package release builds air-gapped, verifiable Global Intelligence bundles.
//
// A bundle is a `.tar.zst` archive that lets a disconnected customer receive
// signed intelligence without direct internet access. The builder performs
// four steps, mirroring the design's "Release / Signing Approach":
//
//  1. Export (Req 16.1): the canonical intel corpus is serialized to a
//     canonical JSON Lines file with STABLE ordering — records sorted by vuln
//     id, every inner slice (aliases, affected packages/ranges, sources)
//     sorted deterministically, and Go's declaration-order struct marshaling —
//     so identical data yields byte-identical output.
//  2. Manifest (Req 16.2): a SHA-256 is computed per exported file and recorded
//     in a manifest (version, created-at, and a sorted list of
//     {name, size, sha256}). The manifest is marshaled deterministically.
//  3. Sign (Req 16.3): the exact manifest bytes are signed with an ed25519
//     private key, producing a detached signature.
//  4. Bundle (Req 16.4): the data file(s), the manifest, and the signature are
//     packaged into a `.tar.zst` archive.
//
// The import/verify side (task 22.2) can re-derive the manifest bytes from the
// archived manifest.json, verify the ed25519 signature against a pinned public
// key, and re-hash each data file to check the per-file SHA-256. The export is
// pure and deterministic (given a fixed Meta.CreatedAt) so task 22.3 can assert
// byte-identical output across runs.
package release

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
)

// Bundle entry names. These are the archive member names and must stay stable
// so the verify side (task 22.2) can locate each member by name.
const (
	// DataFileName is the canonical JSON Lines export of the intel corpus.
	DataFileName = "intel.jsonl"
	// ManifestFileName is the signed manifest listing every data file.
	ManifestFileName = "manifest.json"
	// SignatureFileName is the detached ed25519 signature over the manifest
	// bytes exactly as they appear in ManifestFileName.
	SignatureFileName = "manifest.json.sig"
	// ManifestVersion is the current manifest schema version.
	ManifestVersion = 1
)

// VulnSource supplies the canonical corpus to export. *database.IntelStore
// satisfies it via its AllVulns method, so the builder can snapshot the live
// intel_* tables; tests and callers may also provide an in-memory source.
type VulnSource interface {
	AllVulns(ctx context.Context) ([]canonical.CanonicalVuln, error)
}

// Meta carries caller-provided bundle metadata. CreatedAt is injected (rather
// than read from the clock) so the whole export path is deterministic: the same
// corpus and the same Meta always produce a byte-identical bundle.
type Meta struct {
	// CreatedAt is stamped into the manifest. Callers that need reproducible
	// bundles must pass a fixed timestamp. A zero value is treated as the Unix
	// epoch so determinism is preserved even when a caller forgets to set it.
	CreatedAt time.Time
}

// FileEntry records one archived data file in the manifest: its archive member
// name, byte size, and lower-case hex SHA-256.
type FileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

// Manifest is the signed index of a bundle. It is marshaled with
// MarshalManifest (indented, declaration-order fields, name-sorted Files) so
// the bytes that get signed and archived are deterministic and reproducible by
// the verify side.
type Manifest struct {
	Version   int         `json:"version"`
	CreatedAt time.Time   `json:"created_at"`
	Files     []FileEntry `json:"files"`
}

// Builder assembles release bundles. It is stateless; the zero value is ready
// to use.
type Builder struct{}

// NewBuilder returns a ready-to-use Builder.
func NewBuilder() *Builder { return &Builder{} }

// Build snapshots the corpus from src, writes the canonical export, builds and
// signs the manifest, and streams the resulting `.tar.zst` bundle to w. It is
// the streaming entry point; BuildBytes wraps it for callers that want the
// bundle in memory.
func (b *Builder) Build(ctx context.Context, w io.Writer, src VulnSource, priv ed25519.PrivateKey, meta Meta) error {
	if src == nil {
		return fmt.Errorf("release: nil vuln source")
	}
	vulns, err := src.AllVulns(ctx)
	if err != nil {
		return fmt.Errorf("release: load corpus: %w", err)
	}
	return b.BuildFrom(w, vulns, priv, meta)
}

// BuildFrom is Build with the corpus already in hand. Splitting the data load
// from the deterministic packaging keeps the export path pure, which is what
// the determinism tests (task 22.3) exercise.
func (b *Builder) BuildFrom(w io.Writer, vulns []canonical.CanonicalVuln, priv ed25519.PrivateKey, meta Meta) error {
	if l := len(priv); l != ed25519.PrivateKeySize {
		return fmt.Errorf("release: invalid ed25519 private key size %d (want %d)", l, ed25519.PrivateKeySize)
	}

	// 1. Export (Req 16.1): deterministic canonical JSON Lines.
	data, err := ExportJSONL(vulns)
	if err != nil {
		return fmt.Errorf("release: export: %w", err)
	}

	// 2. Manifest + checksums (Req 16.2).
	manifest := buildManifest(meta, []FileEntry{fileEntry(DataFileName, data)})
	manifestBytes, err := MarshalManifest(manifest)
	if err != nil {
		return fmt.Errorf("release: marshal manifest: %w", err)
	}

	// 3. Sign (Req 16.3): detached ed25519 signature over the manifest bytes.
	sig := ed25519.Sign(priv, manifestBytes)

	// 4. Bundle (Req 16.4): data + manifest + signature into a .tar.zst.
	//
	// Members are written in a fixed order (data, manifest, signature) with a
	// fixed modtime so the archive bytes are themselves deterministic, not just
	// the export inside it.
	members := []tarMember{
		{name: DataFileName, body: data},
		{name: ManifestFileName, body: manifestBytes},
		{name: SignatureFileName, body: sig},
	}
	if err := writeTarZst(w, members, meta.CreatedAt); err != nil {
		return fmt.Errorf("release: package bundle: %w", err)
	}
	return nil
}

// BuildBytes is Build for callers that want the bundle as a byte slice.
func (b *Builder) BuildBytes(ctx context.Context, src VulnSource, priv ed25519.PrivateKey, meta Meta) ([]byte, error) {
	var buf bytesBuffer
	if err := b.Build(ctx, &buf, src, priv, meta); err != nil {
		return nil, err
	}
	return buf.b, nil
}

// ExportJSONL serializes the corpus to canonical JSON Lines: one JSON object
// per line, records sorted by canonical id, and every inner slice sorted
// deterministically. Given identical input it returns byte-identical output
// (Req 16.1).
func ExportJSONL(vulns []canonical.CanonicalVuln) ([]byte, error) {
	normalized := make([]canonical.CanonicalVuln, len(vulns))
	copy(normalized, vulns)
	for i := range normalized {
		normalized[i] = canonicalize(normalized[i])
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].ID < normalized[j].ID
	})

	var out []byte
	for _, v := range normalized {
		line, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal vuln %q: %w", v.ID, err)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out, nil
}

// canonicalize returns a copy of v with every slice placed in a stable order so
// that logically equal records serialize to identical bytes. It sorts aliases,
// sources, affected packages (by purl then ecosystem), and each package's
// ranges (by introduced then fixed). FixedIn lists are also sorted. It does not
// otherwise alter the record's data.
func canonicalize(v canonical.CanonicalVuln) canonical.CanonicalVuln {
	v.Aliases = sortedCopy(v.Aliases)
	v.Sources = sortedCopy(v.Sources)

	affected := make([]canonical.AffectedPackage, len(v.Affected))
	copy(affected, v.Affected)
	for i := range affected {
		affected[i].FixedIn = sortedCopy(affected[i].FixedIn)
		ranges := make([]canonical.SemverRange, len(affected[i].Ranges))
		copy(ranges, affected[i].Ranges)
		sort.Slice(ranges, func(a, b int) bool {
			if ranges[a].Introduced != ranges[b].Introduced {
				return ranges[a].Introduced < ranges[b].Introduced
			}
			return ranges[a].Fixed < ranges[b].Fixed
		})
		affected[i].Ranges = ranges
	}
	sort.Slice(affected, func(a, b int) bool {
		if affected[a].Purl != affected[b].Purl {
			return affected[a].Purl < affected[b].Purl
		}
		return affected[a].Ecosystem < affected[b].Ecosystem
	})
	v.Affected = affected
	return v
}

// sortedCopy returns a sorted copy of in without mutating the caller's slice. A
// nil input stays nil so marshaling matches the "absent vs empty" distinction.
func sortedCopy(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

// buildManifest assembles a Manifest for the given files and metadata. A zero
// CreatedAt is normalized to the Unix epoch (UTC) so the manifest bytes stay
// deterministic even when the caller leaves CreatedAt unset.
func buildManifest(meta Meta, files []FileEntry) Manifest {
	created := meta.CreatedAt
	if created.IsZero() {
		created = time.Unix(0, 0)
	}
	entries := make([]FileEntry, len(files))
	copy(entries, files)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return Manifest{
		Version:   ManifestVersion,
		CreatedAt: created.UTC(),
		Files:     entries,
	}
}

// fileEntry computes the manifest entry (size + SHA-256) for one archived file.
func fileEntry(name string, body []byte) FileEntry {
	sum := sha256.Sum256(body)
	return FileEntry{
		Name:   name,
		Size:   int64(len(body)),
		Sha256: hex.EncodeToString(sum[:]),
	}
}

// MarshalManifest serializes the manifest to deterministic JSON. Go marshals
// struct fields in declaration order, and buildManifest already name-sorts
// Files, so the output is reproducible. The verify side (task 22.2) re-reads
// manifest.json verbatim to check the signature, so this is the single source
// of the signed bytes.
func MarshalManifest(m Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// tarMember is one file to place in the archive.
type tarMember struct {
	name string
	body []byte
}

// writeTarZst writes the members into a zstd-compressed tar stream. Every
// header uses a fixed mode and the given modtime (zero -> Unix epoch) so the
// archive is reproducible. Members are written in the order given.
func writeTarZst(w io.Writer, members []tarMember, modTime time.Time) error {
	if modTime.IsZero() {
		modTime = time.Unix(0, 0)
	}
	modTime = modTime.UTC()

	zw, err := zstd.NewWriter(w)
	if err != nil {
		return fmt.Errorf("new zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	for _, m := range members {
		hdr := &tar.Header{
			Name:    m.name,
			Mode:    0o644,
			Size:    int64(len(m.body)),
			ModTime: modTime,
			Format:  tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			tw.Close()
			zw.Close()
			return fmt.Errorf("write header %q: %w", m.name, err)
		}
		if _, err := tw.Write(m.body); err != nil {
			tw.Close()
			zw.Close()
			return fmt.Errorf("write body %q: %w", m.name, err)
		}
	}

	if err := tw.Close(); err != nil {
		zw.Close()
		return fmt.Errorf("close tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd: %w", err)
	}
	return nil
}

// bytesBuffer is a tiny io.Writer collector, used by BuildBytes to avoid
// importing bytes just for a growable sink.
type bytesBuffer struct{ b []byte }

func (bb *bytesBuffer) Write(p []byte) (int, error) {
	bb.b = append(bb.b, p...)
	return len(p), nil
}
