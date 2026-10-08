package connector

import "time"

// Status is a Connection lifecycle state.
type Status string

const (
	StatusPending          Status = "PENDING"
	StatusVerifying        Status = "VERIFYING"
	StatusConnected        Status = "CONNECTED"
	StatusDiscoveryRunning Status = "DISCOVERY_RUNNING"
	StatusComplete         Status = "COMPLETE"
	StatusError            Status = "ERROR"
)

// Event drives a Connection status transition.
type Event string

const (
	EventBeginVerify     Event = "begin_verify"
	EventVerifySucceeded Event = "verify_succeeded"
	EventVerifyFailed    Event = "verify_failed"
	EventDiscover        Event = "discover"
	EventDiscoverDone    Event = "discover_done"
	EventDiscoverFailed  Event = "discover_failed"
	EventRetry           Event = "retry"
)

// transition is a (from status, event) key into allowedTransitions.
type transition struct {
	from  Status
	event Event
}

// allowedTransitions is the authoritative connection state machine. Any
// (from, event) pair absent from this map is an invalid transition.
var allowedTransitions = map[transition]Status{
	{StatusPending, EventBeginVerify}:             StatusVerifying,
	{StatusVerifying, EventVerifySucceeded}:       StatusConnected,
	{StatusVerifying, EventVerifyFailed}:          StatusError,
	{StatusConnected, EventDiscover}:              StatusDiscoveryRunning,
	{StatusDiscoveryRunning, EventDiscoverDone}:   StatusComplete,
	{StatusDiscoveryRunning, EventDiscoverFailed}: StatusError,
	{StatusError, EventRetry}:                     StatusDiscoveryRunning,
}

// Next returns the status resulting from applying e to s. If the transition is
// not defined, it returns s unchanged and ErrInvalidTransition.
func (s Status) Next(e Event) (Status, error) {
	if to, ok := allowedTransitions[transition{s, e}]; ok {
		return to, nil
	}
	return s, ErrInvalidTransition
}

// Connection is the single entity owned by the Connector Manager. It carries
// only non-secret metadata: there is no credential field by design. OrgID (the
// tenant) is never serialized to clients.
type Connection struct {
	ID             int64      `json:"id"`
	OrgID          int64      `json:"-"`
	ProjectID      int64      `json:"project_id"`
	Provider       string     `json:"provider"`
	AccountID      string     `json:"account_id"`
	RoleARN        string     `json:"role_arn"`
	ExternalID     string     `json:"external_id"`
	Region         string     `json:"region"`
	Capabilities   []string   `json:"capabilities"`
	Status         Status     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
}
