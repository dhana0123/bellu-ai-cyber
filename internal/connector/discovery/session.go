// Package discovery implements the Connector's Discovery Orchestrator. It
// drives the EXISTING Steampipe source and Bellu pipeline for a connection; it
// contains no provider-specific resource queries and introduces no new model
// Kinds, Relations, or normalizers. Credentials are supplied to the collection
// for the duration of a session only and discarded when it ends.
package discovery

import (
	"context"
	"time"
)

// SessionStatus is the lifecycle state of a discovery session.
type SessionStatus string

const (
	SessRunning   SessionStatus = "running"
	SessComplete  SessionStatus = "complete"
	SessFailed    SessionStatus = "failed"
	SessCancelled SessionStatus = "cancelled"
)

// Session tracks a single discovery execution for a connection.
type Session struct {
	ID           string        `json:"id"`
	ConnectionID int64         `json:"connection_id"`
	RunID        string        `json:"run_id"`
	Status       SessionStatus `json:"status"`
	StartedAt    time.Time     `json:"started_at"`
	EndedAt      *time.Time    `json:"ended_at,omitempty"`
	Error        string        `json:"error,omitempty"`

	cancel context.CancelFunc // not serialized
}
