package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// VerifyGitHubWebhook reports whether sig (the raw X-Hub-Signature-256 header,
// e.g. "sha256=abc...") authenticates body under the configured webhook secret,
// using a constant-time comparison. It returns false when no secret is
// configured (fail-safe: unconfigured means reject). The comparison is over the
// EXACT raw bytes the caller received, so the handler must verify before any
// parsing.
func (m *Manager) VerifyGitHubWebhook(body []byte, sig string) bool {
	if len(m.githubWebhookSecret) == 0 {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, m.githubWebhookSecret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// WebhookResult reports how a delivery was handled, for the HTTP response.
type WebhookResult struct {
	Status string // processed | ignored | duplicate
}

// HandleGitHubWebhook records the delivery (deduping on delivery id) and
// dispatches lifecycle events. Signature verification is the caller's
// responsibility and MUST have passed before this is called. A duplicate
// delivery id is a safe no-op (idempotency). Unknown/unsupported events are
// recorded as "ignored". Payload is the already-verified raw body.
func (m *Manager) HandleGitHubWebhook(ctx context.Context, deliveryID, eventType string, payload []byte) (WebhookResult, error) {
	if deliveryID == "" || eventType == "" {
		return WebhookResult{}, fmt.Errorf("%w: delivery id and event type are required", ErrInvalidValue)
	}

	// Parse just enough to find the installation id for the delivery record.
	var head struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	_ = json.Unmarshal(payload, &head)

	// Dedup: the unique delivery-id PK makes this concurrency-safe. A duplicate
	// is a no-op.
	if err := m.repo.InsertWebhookDelivery(ctx, WebhookDelivery{
		DeliveryID: deliveryID, InstallationID: head.Installation.ID, EventType: eventType,
	}); err != nil {
		if err == ErrDuplicateDelivery {
			return WebhookResult{Status: "duplicate"}, nil
		}
		return WebhookResult{}, err
	}

	status := "ignored"
	var procErr error
	switch eventType {
	case "installation":
		procErr = m.handleInstallationEvent(ctx, payload)
		if procErr == nil {
			status = "processed"
		}
	case "installation_repositories":
		procErr = m.handleInstallationReposEvent(ctx, payload)
		if procErr == nil {
			status = "processed"
		}
	case "push", "pull_request":
		// Accepted for downstream analysis; recorded as processed. Heavy
		// analysis is out of scope for this handler and would be enqueued to the
		// background sync, not run synchronously.
		status = "processed"
	default:
		// Unsupported event: safely ignored.
	}

	errText := ""
	if procErr != nil {
		status = "failed"
		errText = procErr.Error() // sanitized: our errors carry no secrets
	}
	_ = m.repo.MarkWebhookDelivery(ctx, deliveryID, status, errText)
	if procErr != nil {
		return WebhookResult{Status: status}, procErr
	}
	return WebhookResult{Status: status}, nil
}

// handleInstallationEvent processes an "installation" event (created, deleted,
// suspend, unsuspend), updating the installation's lifecycle status. On delete/
// suspend, collection stops but historical evidence is retained (no graph
// delete), per the lifecycle policy.
func (m *Manager) handleInstallationEvent(ctx context.Context, payload []byte) error {
	var ev struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("%w: malformed installation event", ErrInvalidValue)
	}
	if ev.Installation.ID == 0 {
		return nil
	}
	switch ev.Action {
	case "deleted":
		return m.repo.SetGitHubInstallationStatus(ctx, ev.Installation.ID, "deleted")
	case "suspend":
		return m.repo.SetGitHubInstallationStatus(ctx, ev.Installation.ID, "suspended")
	case "unsuspend", "created", "new_permissions_accepted":
		return m.repo.SetGitHubInstallationStatus(ctx, ev.Installation.ID, "active")
	}
	return nil
}

// handleInstallationReposEvent processes repositories added/removed from an
// installation, keeping the stored repo set current. Added repos are upserted
// (selected by default); removed repos are deleted from the connection.
func (m *Manager) handleInstallationReposEvent(ctx context.Context, payload []byte) error {
	var ev struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Added []struct {
			ID            int64  `json:"id"`
			FullName      string `json:"full_name"`
			Private       bool   `json:"private"`
			HTMLURL       string `json:"html_url"`
			DefaultBranch string `json:"default_branch"`
		} `json:"repositories_added"`
		Removed []struct {
			ID int64 `json:"id"`
		} `json:"repositories_removed"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("%w: malformed installation_repositories event", ErrInvalidValue)
	}
	inst, ok, err := m.repo.GetGitHubInstallationByInstallationID(ctx, ev.Installation.ID)
	if err != nil || !ok || inst == nil {
		// Unknown installation: nothing to update (safe no-op).
		return nil
	}

	if len(ev.Added) > 0 {
		added := make([]Repo, 0, len(ev.Added))
		for _, r := range ev.Added {
			added = append(added, Repo{
				ExternalID: strconv.FormatInt(r.ID, 10), FullName: r.FullName,
				URL: r.HTMLURL, DefaultBranch: r.DefaultBranch, Private: r.Private,
			})
		}
		if err := m.repo.SaveRepositories(ctx, inst.ConnectionID, added); err != nil {
			return err
		}
	}
	for _, r := range ev.Removed {
		// Delete just this repo by keeping everything except its external id is
		// awkward; instead target it directly via a one-element keep-complement.
		// Reuse DeleteRepositoriesNotIn is for bulk reconcile, so for a removal
		// we fetch current and drop the one id.
		_ = r
	}
	if len(ev.Removed) > 0 {
		if err := m.removeRepos(ctx, inst.ConnectionID, ev.Removed); err != nil {
			return err
		}
	}
	return nil
}

// removeRepos deletes the given repositories (by GitHub id) from a connection,
// computing the keep-set as everything currently stored minus the removed ids.
func (m *Manager) removeRepos(ctx context.Context, connID int64, removed []struct {
	ID int64 `json:"id"`
}) error {
	current, err := m.repo.ListRepositories(ctx, connID)
	if err != nil {
		return err
	}
	drop := make(map[string]bool, len(removed))
	for _, r := range removed {
		drop[strconv.FormatInt(r.ID, 10)] = true
	}
	keep := make([]string, 0, len(current))
	for _, c := range current {
		if !drop[c.ExternalID] {
			keep = append(keep, c.ExternalID)
		}
	}
	return m.repo.DeleteRepositoriesNotIn(ctx, connID, keep)
}
