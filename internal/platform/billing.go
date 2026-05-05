package platform

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan identifies which Stripe-side plan an account
// is on. Distinct from [Tier] (runtime authorisation tier);
// SubscriptionPlan reflects what they're paying for.
type SubscriptionPlan string

const (
	PlanStarter    SubscriptionPlan = "starter"
	PlanPro        SubscriptionPlan = "pro"
	PlanBusiness   SubscriptionPlan = "business"
	PlanEnterprise SubscriptionPlan = "enterprise"
)

// Subscription mirrors Stripe subscription state. One active
// row per account (current_period_end > now()); historical
// rows roll over via UPSERT on stripe_subscription_id.
type Subscription struct {
	ID                   uuid.UUID
	AccountID            uuid.UUID
	StripeSubscriptionID string
	Plan                 SubscriptionPlan
	CurrentPeriodStart   time.Time
	CurrentPeriodEnd     time.Time
	CancelAtPeriodEnd    bool
	CanceledAt           time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// IsActive reports whether the subscription should be granting
// the customer their plan's capabilities right now.
func (s Subscription) IsActive(now time.Time) bool {
	if !s.CanceledAt.IsZero() && !now.Before(s.CanceledAt) {
		return false
	}
	return now.Before(s.CurrentPeriodEnd)
}

// StripeEvent is the deduplication / audit row for inbound
// Stripe webhook events. Idempotency: Stripe retries on
// non-200; we look up by stripe_event_id and skip processing
// if processed_at is non-zero.
type StripeEvent struct {
	StripeEventID string
	Type          string
	ReceivedAt    time.Time
	ProcessedAt   time.Time // zero = received but not yet handled
	Error         string
	Payload       json.RawMessage
}

// BillingStore persists [Subscription] and [StripeEvent].
type BillingStore interface {
	// UpsertSubscription writes the Stripe-derived state. Called
	// from webhook handlers; idempotent on stripe_subscription_id.
	UpsertSubscription(ctx context.Context, s Subscription) error

	// GetActiveSubscriptionForAccount returns the row whose
	// current_period_end is in the future. ErrNotFound when the
	// account has no active subscription (either Free tier or
	// fully cancelled).
	GetActiveSubscriptionForAccount(ctx context.Context, accountID uuid.UUID) (Subscription, error)

	// AppendStripeEvent inserts the dedupe row. Returns
	// ErrAlreadyProcessed when the stripe_event_id is already
	// present — handlers should skip re-processing in that case.
	AppendStripeEvent(ctx context.Context, e StripeEvent) error

	// MarkStripeEventProcessed sets processed_at to now() on a
	// previously-appended event. Called after the handler
	// successfully applied the event's effect.
	MarkStripeEventProcessed(ctx context.Context, stripeEventID string) error

	// MarkStripeEventFailed records the error for diagnosis;
	// processed_at stays zero so the next retry triggers a
	// fresh attempt.
	MarkStripeEventFailed(ctx context.Context, stripeEventID string, err string) error
}
