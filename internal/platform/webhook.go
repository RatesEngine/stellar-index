package platform

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CustomerWebhook is an outbound HTTPS endpoint a customer
// registers to receive event notifications. Stripe-shape:
// signed deliveries (HMAC-SHA-256 of payload), exponential
// retry over 72h.
type CustomerWebhook struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	Name       string
	URL        string
	SecretHash []byte // sha256 of the signing secret; secret itself shown to customer once at creation
	Events     []string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WebhookDelivery is one attempt to deliver an event to a
// customer webhook. We track every attempt (delivered or
// failed) so the dashboard can render the delivery log.
type WebhookDelivery struct {
	ID                 uuid.UUID
	WebhookID          uuid.UUID
	EventType          string
	Payload            json.RawMessage
	AttemptCount       int
	NextAttemptAt      time.Time // zero = no further retry scheduled (delivered or exhausted)
	DeliveredAt        time.Time
	LastError          string
	LastResponseStatus int
	CreatedAt          time.Time
}

// IsTerminal reports whether the delivery has stopped retrying:
// either it was delivered, or the retry budget is exhausted
// (signalled by NextAttemptAt being zero with DeliveredAt also
// zero — caller distinguishes by checking DeliveredAt).
func (d WebhookDelivery) IsTerminal() bool {
	return !d.DeliveredAt.IsZero() || d.NextAttemptAt.IsZero()
}

// WebhookStore persists [CustomerWebhook] and [WebhookDelivery].
type WebhookStore interface {
	// CreateWebhook registers a new outbound endpoint.
	CreateWebhook(ctx context.Context, w CustomerWebhook) (CustomerWebhook, error)

	// GetWebhook by ID.
	GetWebhook(ctx context.Context, id uuid.UUID) (CustomerWebhook, error)

	// ListWebhooksForAccount returns every webhook (enabled +
	// disabled) for the account.
	ListWebhooksForAccount(ctx context.Context, accountID uuid.UUID) ([]CustomerWebhook, error)

	// UpdateWebhook writes mutable fields (name, url, events,
	// enabled). Secret rotation is a separate explicit method.
	UpdateWebhook(ctx context.Context, w CustomerWebhook) error

	// RotateWebhookSecret replaces the signing secret. Returns
	// the new plaintext (shown once, not stored).
	RotateWebhookSecret(ctx context.Context, id uuid.UUID) (newSecret string, err error)

	// DeleteWebhook hard-deletes (cascades to deliveries).
	DeleteWebhook(ctx context.Context, id uuid.UUID) error

	// AppendDelivery records one attempt. Called by the
	// delivery worker after each send.
	AppendDelivery(ctx context.Context, d WebhookDelivery) (WebhookDelivery, error)

	// UpdateDelivery rewrites the attempt-state fields after a
	// retry. Idempotent.
	UpdateDelivery(ctx context.Context, d WebhookDelivery) error

	// ListDeliveries returns recent attempts for the webhook,
	// most-recent first. Used by the dashboard delivery log.
	ListDeliveries(ctx context.Context, webhookID uuid.UUID, limit int) ([]WebhookDelivery, error)
}
