// Package rawevent defines the stable boundary of the ingestion pipeline:
// the RawEvent type, deterministic identity/hashing, and validation.
//
// Every collector in Bellu emits RawEvents. Everything downstream
// (normalizer, resolver, graph writer) consumes them. This boundary does
// not change when a message bus is introduced later.
package rawevent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RawEvent is a single piece of collected evidence from a source, before
// normalization. It is append-only and content-addressed: the same observed
// resource with the same payload always produces the same ID, which gives
// free deduplication on rerun.
type RawEvent struct {
	// ID is sha256(source|provider|resource_type|resource_id|payload_hash).
	ID string `json:"id"`
	// RunID ties this event to a single collection run.
	RunID string `json:"run_id"`
	// Source is the producing collector family: steampipe, syft, trivy,
	// cdxgen, zeek.
	Source string `json:"source"`
	// Collector is the specific collector instance, e.g. "steampipe:aws_prod".
	Collector string `json:"collector"`
	// Provider is the evidence domain: aws, azure, gcp, k8s, repo, image,
	// network.
	Provider string `json:"provider"`
	// Scope is the account / subscription / project / cluster the event
	// belongs to.
	Scope string `json:"scope,omitempty"`
	// Region is the cloud region, when applicable.
	Region string `json:"region,omitempty"`
	// ResourceType is the native type: aws_iam_role, cyclonedx.component,
	// zeek.conn, ...
	ResourceType string `json:"resource_type"`
	// ResourceID is the native identifier: ARN, Azure ID, GCP name, purl, uid.
	ResourceID string `json:"resource_id"`
	// ObservedAt is when the evidence was observed, in UTC.
	ObservedAt time.Time `json:"observed_at"`
	// Payload is the redacted raw payload.
	Payload json.RawMessage `json:"payload"`
	// PayloadHash is sha256 of the payload bytes.
	PayloadHash string `json:"payload_hash"`
}

// HashPayload returns the hex sha256 of the given payload bytes.
func HashPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ComputeID derives the deterministic content-addressed ID from the event's
// identity fields and payload hash. It does not read the ID field.
func ComputeID(source, provider, resourceType, resourceID, payloadHash string) string {
	parts := strings.Join([]string{source, provider, resourceType, resourceID, payloadHash}, "|")
	sum := sha256.Sum256([]byte(parts))
	return hex.EncodeToString(sum[:])
}

// Finalize fills PayloadHash and ID from the event's current contents, and
// normalizes ObservedAt to UTC. Call this after setting the identity fields
// and payload. It is idempotent.
func (e *RawEvent) Finalize() {
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("null")
	}
	e.PayloadHash = HashPayload(e.Payload)
	e.ObservedAt = e.ObservedAt.UTC()
	e.ID = ComputeID(e.Source, e.Provider, e.ResourceType, e.ResourceID, e.PayloadHash)
}

// Validate reports whether the event has all required fields and a consistent,
// deterministic ID. It must be called after Finalize (or on an event that was
// loaded with ID/PayloadHash already set).
func (e *RawEvent) Validate() error {
	var missing []string
	if e.RunID == "" {
		missing = append(missing, "run_id")
	}
	if e.Source == "" {
		missing = append(missing, "source")
	}
	if e.Collector == "" {
		missing = append(missing, "collector")
	}
	if e.Provider == "" {
		missing = append(missing, "provider")
	}
	if e.ResourceType == "" {
		missing = append(missing, "resource_type")
	}
	if e.ResourceID == "" {
		missing = append(missing, "resource_id")
	}
	if e.ObservedAt.IsZero() {
		missing = append(missing, "observed_at")
	}
	if len(missing) > 0 {
		return fmt.Errorf("rawevent: missing required fields: %s", strings.Join(missing, ", "))
	}
	if e.ObservedAt.Location() != time.UTC {
		return fmt.Errorf("rawevent: observed_at must be UTC, got %s", e.ObservedAt.Location())
	}
	if !json.Valid(e.Payload) {
		return fmt.Errorf("rawevent: payload is not valid JSON")
	}
	wantHash := HashPayload(e.Payload)
	if e.PayloadHash != wantHash {
		return fmt.Errorf("rawevent: payload_hash mismatch: have %s want %s", e.PayloadHash, wantHash)
	}
	wantID := ComputeID(e.Source, e.Provider, e.ResourceType, e.ResourceID, e.PayloadHash)
	if e.ID != wantID {
		return fmt.Errorf("rawevent: id mismatch: have %s want %s", e.ID, wantID)
	}
	return nil
}
