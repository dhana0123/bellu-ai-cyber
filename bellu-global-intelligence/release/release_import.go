// Package release — import/verify side.
//
// This file implements the trusted-ingest counterpart to the builder in
// release.go. A disconnected customer receives a `.tar.zst` bundle and must be
// able to prove, before anything touches the database, that the bundle is both
// authentic (signed by Bellu's pinned ed25519 key) and intact (every data file
// hashes to the value recorded in the signed manifest). The import flow mirrors
// the design's "Release / Signing Approach" in reverse:
//
//  1. Unpack (zstd -> tar): read manifest.json, manifest.json.sig, and every
//     data file (intel.jsonl) out of the archive into memory. Nothing is
//     persisted at this stage.
//  2. Authenticate (Req 16.5): verify the detached ed25519 signature over the
//     *exact* manifest.json bytes against the pinned public key.
//  3. Integrity (Req 16.5): re-hash each data file and compare its size and
//     lower-case hex SHA-256 against the manifest's FileEntry. A manifest entry
//     with no matching archived file (or a file with no manifest entry) is a
//     failure.
//  4. Upsert (Req 16.5): ONLY after every check above passes, parse intel.jsonl
//     (one canonical.CanonicalVuln per line) and upsert the corpus through the
//     injected VulnSink.
//
// Fail-closed guarantee (Req 16.6): Verify is a pure, side-effect-free function
// that returns the verified payload or an error — it never writes anywhere. The
// Import entry point calls Verify first and returns immediately on any failure,
// so a bundle that fails signature or checksum verification upserts no intel_*
// rows. This clean split also lets the verification tests (task 22.3) assert
// tamper rejection with no database at all.
package release

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
)

// ErrVerification is the sentinel wrapped by every verification failure
// (bad signature, checksum/size mismatch, missing or extra files). Callers can
// use errors.Is(err, ErrVerification) to distinguish an untrusted bundle from
// an I/O or decode error. When verification fails NOTHING is upserted
// (Req 16.6).
var ErrVerification = errors.New("release: bundle verification failed")

// VulnSink is the write side the importer upserts the verified corpus into.
// *database.IntelStore satisfies it via UpsertVulns. A snapshot import carries
// no per-day EPSS history, so Import always passes a zero scoredOn.
type VulnSink interface {
	UpsertVulns(ctx context.Context, vulns []canonical.CanonicalVuln, scoredOn time.Time) error
}

// VerifiedBundle is the authenticated, integrity-checked payload Verify returns.
// Holding one is proof the signature and every per-file checksum passed; the
// bytes are safe to parse and upsert. DataFiles is keyed by archive member name
// (e.g. DataFileName) so callers can locate the canonical JSONL.
type VerifiedBundle struct {
	Manifest  Manifest
	DataFiles map[string][]byte
}

// JSONL returns the canonical JSONL data file (DataFileName) from a verified
// bundle. It errors if the expected data member is absent.
func (vb VerifiedBundle) JSONL() ([]byte, error) {
	data, ok := vb.DataFiles[DataFileName]
	if !ok {
		return nil, fmt.Errorf("%w: missing data file %q", ErrVerification, DataFileName)
	}
	return data, nil
}

// Vulns parses the verified JSONL data file into canonical vulnerabilities, one
// per line. It is only meaningful on a VerifiedBundle, so parsing untrusted
// bytes is impossible by construction.
func (vb VerifiedBundle) Vulns() ([]canonical.CanonicalVuln, error) {
	data, err := vb.JSONL()
	if err != nil {
		return nil, err
	}
	return parseJSONL(data)
}

// Verify authenticates and integrity-checks a bundle read from r WITHOUT any
// side effects (Req 16.5 verification, Req 16.6 fail-closed). It unpacks the
// `.tar.zst`, verifies the ed25519 signature over the exact manifest.json bytes
// against the pinned public key pub, then re-hashes every data file against the
// manifest. On success it returns the VerifiedBundle; on any verification
// failure it returns an error wrapping ErrVerification and no payload.
//
// Verify never touches a database, so task 22.3 can assert that a tampered
// manifest, signature, or data file is rejected without a store.
func Verify(r io.Reader, pub ed25519.PublicKey) (VerifiedBundle, error) {
	if l := len(pub); l != ed25519.PublicKeySize {
		return VerifiedBundle{}, fmt.Errorf("release: invalid ed25519 public key size %d (want %d)", l, ed25519.PublicKeySize)
	}

	members, err := readTarZst(r)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("release: unpack bundle: %w", err)
	}

	manifestBytes, ok := members[ManifestFileName]
	if !ok {
		return VerifiedBundle{}, fmt.Errorf("%w: missing %q", ErrVerification, ManifestFileName)
	}
	sig, ok := members[SignatureFileName]
	if !ok {
		return VerifiedBundle{}, fmt.Errorf("%w: missing %q", ErrVerification, SignatureFileName)
	}

	// Authenticate (Req 16.5): the signature must cover the exact manifest
	// bytes as archived. Any tampering of the manifest invalidates it.
	if !ed25519.Verify(pub, manifestBytes, sig) {
		return VerifiedBundle{}, fmt.Errorf("%w: ed25519 signature does not match pinned public key", ErrVerification)
	}

	// The manifest is now trusted. Decode it and check every data file against
	// its recorded size + SHA-256 (Req 16.5).
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return VerifiedBundle{}, fmt.Errorf("%w: decode manifest: %v", ErrVerification, err)
	}

	dataFiles, err := verifyFiles(manifest, members)
	if err != nil {
		return VerifiedBundle{}, err
	}

	return VerifiedBundle{Manifest: manifest, DataFiles: dataFiles}, nil
}

// verifyFiles checks that the archive carries exactly the data files the
// (already authenticated) manifest lists, each with a matching size and
// SHA-256. It returns the verified data-file bytes keyed by name. The manifest
// members (manifest.json / manifest.json.sig) are not data files and are
// ignored here. Any mismatch, missing, or extra data file is a verification
// failure (Req 16.6).
func verifyFiles(manifest Manifest, members map[string][]byte) (map[string][]byte, error) {
	dataFiles := make(map[string][]byte, len(manifest.Files))
	for _, fe := range manifest.Files {
		body, ok := members[fe.Name]
		if !ok {
			return nil, fmt.Errorf("%w: manifest lists %q but the bundle has no such file", ErrVerification, fe.Name)
		}
		if int64(len(body)) != fe.Size {
			return nil, fmt.Errorf("%w: file %q size %d does not match manifest size %d", ErrVerification, fe.Name, len(body), fe.Size)
		}
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != fe.Sha256 {
			return nil, fmt.Errorf("%w: file %q sha256 %s does not match manifest %s", ErrVerification, fe.Name, got, fe.Sha256)
		}
		dataFiles[fe.Name] = body
	}

	// Reject stray data members the manifest did not account for. The two
	// control members (manifest + signature) are expected and skipped.
	for name := range members {
		if name == ManifestFileName || name == SignatureFileName {
			continue
		}
		if _, ok := dataFiles[name]; !ok {
			return nil, fmt.Errorf("%w: bundle has unlisted file %q", ErrVerification, name)
		}
	}
	return dataFiles, nil
}

// Import verifies a bundle read from r against the pinned public key and, only
// if every check passes, upserts the canonical corpus through dst (Req 16.5).
// On any verification failure it returns the error from Verify and upserts
// nothing (Req 16.6) — verification is complete before dst is ever touched.
//
// The corpus is upserted with a zero scoredOn: a release bundle is a snapshot,
// not a per-day EPSS reading, so it must not append spurious EPSS history.
func Import(ctx context.Context, r io.Reader, pub ed25519.PublicKey, dst VulnSink) error {
	if dst == nil {
		return fmt.Errorf("release: nil vuln sink")
	}

	bundle, err := Verify(r, pub)
	if err != nil {
		// Verification failed: nothing has been written. Return as-is so the
		// caller can detect ErrVerification.
		return err
	}

	vulns, err := bundle.Vulns()
	if err != nil {
		return fmt.Errorf("release: parse verified corpus: %w", err)
	}

	if err := dst.UpsertVulns(ctx, vulns, time.Time{}); err != nil {
		return fmt.Errorf("release: upsert verified corpus: %w", err)
	}
	return nil
}

// ImportBytes is Import for callers that already hold the bundle in memory.
func ImportBytes(ctx context.Context, bundle []byte, pub ed25519.PublicKey, dst VulnSink) error {
	return Import(ctx, bytes.NewReader(bundle), pub, dst)
}

// parseJSONL decodes canonical JSON Lines (one CanonicalVuln per line, the
// format ExportJSONL produces) into a slice. Blank lines are skipped so a
// trailing newline is tolerated. It is the inverse of ExportJSONL.
func parseJSONL(data []byte) ([]canonical.CanonicalVuln, error) {
	var out []canonical.CanonicalVuln
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var v canonical.CanonicalVuln
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode jsonl record %d: %w", len(out), err)
		}
		out = append(out, v)
	}
	return out, nil
}

// readTarZst reads a zstd-compressed tar stream fully into memory, returning
// each member's bytes keyed by its archive name. It is the inverse of
// writeTarZst. A duplicate member name is rejected so a bundle cannot smuggle a
// second manifest or data file past verification.
func readTarZst(r io.Reader) (map[string][]byte, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("new zstd reader: %w", err)
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
			return nil, fmt.Errorf("read tar header: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			// Skip directories and other non-regular entries; the builder only
			// writes regular files.
			continue
		}
		if _, dup := members[hdr.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate archive member %q", ErrVerification, hdr.Name)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read tar body %q: %w", hdr.Name, err)
		}
		members[hdr.Name] = body
	}
	return members, nil
}
