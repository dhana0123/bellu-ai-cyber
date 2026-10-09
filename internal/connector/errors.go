// Package connector implements the Bellu Cloud Connector: a runtime subsystem
// that onboards and accesses customer cloud accounts (AWS first) and hands the
// resulting access to Bellu's existing collection pipeline. It owns connection
// lifecycle, provider abstraction, credential sessions, modular capabilities,
// permission verification, and discovery orchestration, and stops at the point
// where it configures and invokes the existing Steampipe source.
package connector

import "errors"

// Sentinel errors returned across the connector. Handlers map these to HTTP
// status codes; the manager and provider layers return them directly.
var (
	// ErrUnsupportedProvider is returned when a Connection names a provider for
	// which no CloudProvider implementation is registered.
	ErrUnsupportedProvider = errors.New("connector: unsupported provider")

	// ErrInvalidTransition is returned when a status transition is requested
	// that the connection state machine does not define; the status is left
	// unchanged.
	ErrInvalidTransition = errors.New("connector: invalid status transition")

	// ErrNotFound is returned when a Connection cannot be found for the given
	// tenant and id.
	ErrNotFound = errors.New("connector: connection not found")

	// ErrInvalidValue is returned when a provided value fails validation (for
	// example a malformed role ARN or account id); the wrapped message names
	// the offending field.
	ErrInvalidValue = errors.New("connector: invalid value")

	// ErrConflict is returned when a connection for the same tenant, provider,
	// and account already exists.
	ErrConflict = errors.New("connector: a connection for this account already exists")
)
