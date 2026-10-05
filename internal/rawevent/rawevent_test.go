package rawevent

import (
	"encoding/json"
	"testing"
	"time"
)

func sampleEvent() RawEvent {
	return RawEvent{
		RunID:        "run-1",
		Source:       "steampipe",
		Collector:    "steampipe:aws_prod",
		Provider:     "aws",
		Scope:        "123456789012",
		Region:       "us-east-1",
		ResourceType: "aws_iam_role",
		ResourceID:   "arn:aws:iam::123456789012:role/agent",
		ObservedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Payload:      json.RawMessage(`{"name":"agent"}`),
	}
}

func TestFinalizeIsDeterministic(t *testing.T) {
	a := sampleEvent()
	b := sampleEvent()
	a.Finalize()
	b.Finalize()
	if a.ID != b.ID {
		t.Fatalf("expected identical IDs, got %q and %q", a.ID, b.ID)
	}
	if a.PayloadHash != b.PayloadHash {
		t.Fatalf("expected identical payload hashes, got %q and %q", a.PayloadHash, b.PayloadHash)
	}
	if a.ID == "" {
		t.Fatal("ID should not be empty after Finalize")
	}
}

func TestFinalizeIdempotent(t *testing.T) {
	e := sampleEvent()
	e.Finalize()
	id1, hash1 := e.ID, e.PayloadHash
	e.Finalize()
	if e.ID != id1 || e.PayloadHash != hash1 {
		t.Fatalf("Finalize not idempotent: %q/%q then %q/%q", id1, hash1, e.ID, e.PayloadHash)
	}
}

func TestDifferentPayloadDifferentID(t *testing.T) {
	a := sampleEvent()
	b := sampleEvent()
	b.Payload = json.RawMessage(`{"name":"other"}`)
	a.Finalize()
	b.Finalize()
	if a.ID == b.ID {
		t.Fatal("different payloads must produce different IDs")
	}
}

func TestValidateMissingFields(t *testing.T) {
	e := RawEvent{}
	e.Finalize()
	if err := e.Validate(); err == nil {
		t.Fatal("expected validation error for empty event")
	}
}

func TestValidateRejectsNonUTC(t *testing.T) {
	e := sampleEvent()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tz data unavailable: %v", err)
	}
	e.ObservedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, loc)
	// Compute hash/id but keep non-UTC time by not calling Finalize (which
	// forces UTC). Set hash/id manually.
	e.PayloadHash = HashPayload(e.Payload)
	e.ID = ComputeID(e.Source, e.Provider, e.ResourceType, e.ResourceID, e.PayloadHash)
	if err := e.Validate(); err == nil {
		t.Fatal("expected validation error for non-UTC observed_at")
	}
}

func TestValidateOK(t *testing.T) {
	e := sampleEvent()
	e.Finalize()
	if err := e.Validate(); err != nil {
		t.Fatalf("expected valid event, got %v", err)
	}
}

func TestValidateDetectsTampering(t *testing.T) {
	e := sampleEvent()
	e.Finalize()
	e.Payload = json.RawMessage(`{"name":"tampered"}`)
	if err := e.Validate(); err == nil {
		t.Fatal("expected payload_hash mismatch after tampering")
	}
}
