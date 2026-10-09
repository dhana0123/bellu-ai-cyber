package connector

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

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

// Scope is whether a connection covers a single account or a whole AWS org.
type Scope string

const (
	ScopeAccount      Scope = "account"
	ScopeOrganization Scope = "organization"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { return s == ScopeAccount || s == ScopeOrganization }

// InstallationType selects the connector installation shape. Only "standard"
// is implemented; "outpost" is reserved for future use.
type InstallationType string

const (
	InstallationStandard InstallationType = "standard"
	InstallationOutpost  InstallationType = "outpost"
)

// Valid reports whether it is a known installation type.
func (i InstallationType) Valid() bool {
	return i == InstallationStandard || i == InstallationOutpost
}

// DeploymentMethod selects how the customer deploys the IAM role. It picks the
// setup renderer, not a different connector implementation.
type DeploymentMethod string

const (
	DeploymentCloudFormation DeploymentMethod = "cloudformation"
	DeploymentTerraform      DeploymentMethod = "terraform"
	DeploymentManual         DeploymentMethod = "manual"
)

// Valid reports whether d is a known deployment method.
func (d DeploymentMethod) Valid() bool {
	switch d {
	case DeploymentCloudFormation, DeploymentTerraform, DeploymentManual:
		return true
	}
	return false
}

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
	// Verify can begin from PENDING (first time) or from any settled state
	// (re-verify an already CONNECTED/COMPLETE/ERROR connection).
	{StatusPending, EventBeginVerify}:   StatusVerifying,
	{StatusConnected, EventBeginVerify}: StatusVerifying,
	{StatusComplete, EventBeginVerify}:  StatusVerifying,
	{StatusError, EventBeginVerify}:     StatusVerifying,

	{StatusVerifying, EventVerifySucceeded}: StatusConnected,
	{StatusVerifying, EventVerifyFailed}:    StatusError,

	// Discovery can start from CONNECTED (first run), COMPLETE (re-discover), or
	// ERROR (retry after a failed discovery).
	{StatusConnected, EventDiscover}: StatusDiscoveryRunning,
	{StatusComplete, EventDiscover}:  StatusDiscoveryRunning,
	{StatusError, EventDiscover}:     StatusDiscoveryRunning,

	{StatusDiscoveryRunning, EventDiscoverDone}:   StatusComplete,
	{StatusDiscoveryRunning, EventDiscoverFailed}: StatusError,

	// Retry restarts discovery from ERROR.
	{StatusError, EventRetry}: StatusDiscoveryRunning,
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
// tenant) is never serialized to clients. RoleARN is empty until the customer
// has deployed the role and supplies it at verification time.
type Connection struct {
	ID                   int64            `json:"id"`
	OrgID                int64            `json:"-"`
	ProjectID            int64            `json:"project_id"`
	Provider             string           `json:"provider"`
	Scope                Scope            `json:"scope"`
	InstallationType     InstallationType `json:"installation_type"`
	DeploymentMethod     DeploymentMethod `json:"deployment_method"`
	AccountID            string           `json:"account_id"`
	ManagementAccountID  string           `json:"management_account_id,omitempty"`
	OrganizationalUnitID string           `json:"organizational_unit_id,omitempty"`
	// ParentConnectionID links a per-member-account connection to the
	// organization connection it was expanded from (0 for a standalone/account
	// or the parent organization connection itself).
	ParentConnectionID int64      `json:"parent_connection_id,omitempty"`
	RoleARN            string     `json:"role_arn,omitempty"`
	ExternalID         string     `json:"external_id"`
	Region             string     `json:"region"`
	Capabilities       []string   `json:"capabilities"`
	Status             Status     `json:"status"`
	CreatedAt          time.Time  `json:"created_at"`
	LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
}

// roleARNAccountRe captures the 12-digit account id embedded in an AWS IAM role
// ARN. Kept in the connector package (not the aws adapter) so the manager can
// derive the account id without importing the adapter, which would create an
// import cycle (the adapter imports this package for its sentinel errors).
var (
	// accountIDRe matches a 12-digit AWS account id.
	accountIDRe = regexp.MustCompile(`^\d{12}$`)
	// ouIDRe matches an AWS Organizations OU id (ou-xxxx-xxxxxxxx) or root id
	// (r-xxxx), the valid service-managed StackSet deployment targets.
	ouIDRe = regexp.MustCompile(`^(ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}|r-[0-9a-z]{4,32})$`)
)

var roleARNAccountRe = regexp.MustCompile(`^arn:aws(?:-[a-z-]+)?:iam::(\d{12}):role/.+$`)

// accountIDFromRoleARN extracts the 12-digit AWS account id from a role ARN,
// returning an ErrInvalidValue naming role_arn when the ARN is malformed.
func accountIDFromRoleARN(arn string) (string, error) {
	m := roleARNAccountRe.FindStringSubmatch(strings.TrimSpace(arn))
	if m == nil {
		return "", fmt.Errorf("%w: role_arn", ErrInvalidValue)
	}
	return m[1], nil
}
