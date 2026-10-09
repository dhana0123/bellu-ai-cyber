package connector

import (
	"errors"
	"testing"
)

// TestStatusNext_AllowedTransitions asserts every defined (from, event) pair
// yields the expected status with no error.
func TestStatusNext_AllowedTransitions(t *testing.T) {
	cases := []struct {
		from Status
		ev   Event
		want Status
	}{
		{StatusPending, EventBeginVerify, StatusVerifying},
		{StatusVerifying, EventVerifySucceeded, StatusConnected},
		{StatusVerifying, EventVerifyFailed, StatusError},
		{StatusConnected, EventDiscover, StatusDiscoveryRunning},
		{StatusDiscoveryRunning, EventDiscoverDone, StatusComplete},
		{StatusDiscoveryRunning, EventDiscoverFailed, StatusError},
		{StatusError, EventRetry, StatusDiscoveryRunning},
		// Re-entry transitions: re-verify and re-discover from settled states.
		{StatusConnected, EventBeginVerify, StatusVerifying},
		{StatusComplete, EventBeginVerify, StatusVerifying},
		{StatusError, EventBeginVerify, StatusVerifying},
		{StatusComplete, EventDiscover, StatusDiscoveryRunning},
		{StatusError, EventDiscover, StatusDiscoveryRunning},
	}
	for _, c := range cases {
		got, err := c.from.Next(c.ev)
		if err != nil {
			t.Errorf("%s + %s: unexpected error %v", c.from, c.ev, err)
		}
		if got != c.want {
			t.Errorf("%s + %s: got %s, want %s", c.from, c.ev, got, c.want)
		}
	}
}

// TestStatusNext_InvalidTransitions asserts every (from, event) pair NOT in the
// table returns ErrInvalidTransition and leaves the status unchanged.
func TestStatusNext_InvalidTransitions(t *testing.T) {
	allStatuses := []Status{
		StatusPending, StatusVerifying, StatusConnected,
		StatusDiscoveryRunning, StatusComplete, StatusError,
	}
	allEvents := []Event{
		EventBeginVerify, EventVerifySucceeded, EventVerifyFailed,
		EventDiscover, EventDiscoverDone, EventDiscoverFailed, EventRetry,
	}
	for _, s := range allStatuses {
		for _, e := range allEvents {
			if _, defined := allowedTransitions[transition{s, e}]; defined {
				continue
			}
			got, err := s.Next(e)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s + %s: want ErrInvalidTransition, got %v", s, e, err)
			}
			if got != s {
				t.Errorf("%s + %s: status changed to %s on invalid transition", s, e, got)
			}
		}
	}
}
