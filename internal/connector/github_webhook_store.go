package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateDelivery is returned when a webhook delivery id has already been
// recorded, so the caller can treat the redelivery as a no-op (idempotency).
var ErrDuplicateDelivery = errors.New("connector: duplicate webhook delivery")

// WebhookDelivery is one recorded GitHub webhook delivery, used for
// idempotency (the delivery id is the primary key) and an audit trail.
type WebhookDelivery struct {
	DeliveryID     string
	InstallationID int64
	EventType      string
	Status         string // received|processed|failed|ignored
	Error          string // sanitized
}

// InsertWebhookDelivery records a delivery, returning ErrDuplicateDelivery when
// the delivery id already exists. The INSERT + unique PK is the concurrency-safe
// dedup mechanism: two concurrent deliveries with the same id race on the PK and
// exactly one wins. The error text is sanitized by the caller before storage.
func (r *Repository) InsertWebhookDelivery(ctx context.Context, d WebhookDelivery) error {
	status := d.Status
	if status == "" {
		status = "received"
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO github_webhook_deliveries
		   (delivery_id, installation_id, event_type, status, error)
		 VALUES ($1,$2,$3,$4,$5)`,
		d.DeliveryID, d.InstallationID, d.EventType, status, d.Error)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return ErrDuplicateDelivery
	}
	if err != nil {
		return fmt.Errorf("connector: insert webhook delivery: %w", err)
	}
	return nil
}

// MarkWebhookDelivery updates a delivery's terminal status (processed/failed/
// ignored) and sanitized error. A missing row is a no-op.
func (r *Repository) MarkWebhookDelivery(ctx context.Context, deliveryID, status, sanitizedErr string) error {
	now := time.Now()
	_, err := r.pool.Exec(ctx,
		`UPDATE github_webhook_deliveries
		    SET status = $2, error = $3, processed_at = $4
		  WHERE delivery_id = $1`,
		deliveryID, status, sanitizedErr, now)
	if err != nil {
		return fmt.Errorf("connector: mark webhook delivery: %w", err)
	}
	return nil
}

// SetGitHubInstallationStatus updates an installation's lifecycle status
// (active|suspended|deleted) by its GitHub installation id. A missing row is a
// no-op (nil error), so a webhook for an unknown installation does not error.
func (r *Repository) SetGitHubInstallationStatus(ctx context.Context, ghInstID int64, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE github_installations SET status = $2, updated_at = now()
		  WHERE github_installation_id = $1`,
		ghInstID, status)
	if err != nil {
		return fmt.Errorf("connector: set installation status: %w", err)
	}
	return nil
}

// DeleteRepositoriesNotIn removes a connection's repositories whose external_id
// is not in the keep set, used when a webhook reports repositories removed from
// the installation. An empty keep set deletes all of the connection's repos.
func (r *Repository) DeleteRepositoriesNotIn(ctx context.Context, connID int64, keepExternalIDs []string) error {
	if len(keepExternalIDs) == 0 {
		_, err := r.pool.Exec(ctx,
			`DELETE FROM connector_repositories WHERE connection_id = $1`, connID)
		if err != nil {
			return fmt.Errorf("connector: delete all repositories: %w", err)
		}
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM connector_repositories
		  WHERE connection_id = $1 AND NOT (external_id = ANY($2))`,
		connID, keepExternalIDs)
	if err != nil {
		return fmt.Errorf("connector: delete removed repositories: %w", err)
	}
	return nil
}

var _ = pgx.ErrNoRows
