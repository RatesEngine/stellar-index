package platform

import "errors"

// Sentinel errors for the platform stores. Wrap with %w so
// callers can errors.Is() — never compare error strings.

var (
	// ErrNotFound is returned by every Get*/Consume*/Accept*
	// method when the requested record doesn't exist (or, for
	// magic-link tokens, has already been consumed).
	ErrNotFound = errors.New("platform: not found")

	// ErrTokenExpired is returned by ConsumeMagicLinkToken /
	// AcceptInvite when the row exists but its expires_at is
	// in the past. Distinct from ErrNotFound so the handler
	// can render "this link expired, request a fresh one"
	// rather than the generic "invalid link" message.
	ErrTokenExpired = errors.New("platform: token expired")

	// ErrConflict signals a uniqueness violation (duplicate
	// email, duplicate stripe_customer_id, key_hash collision).
	// Callers handle by surfacing the appropriate user-facing
	// message — typically "an account with that email already
	// exists" for users, or retry-with-new-id for keys.
	ErrConflict = errors.New("platform: conflict")

	// ErrAlreadyProcessed is returned by AppendStripeEvent when
	// the stripe_event_id is already in the dedupe table.
	// Handlers skip processing (Stripe retried; we already
	// applied the effect).
	ErrAlreadyProcessed = errors.New("platform: stripe event already processed")

	// ErrLastOwner is returned when an operation would leave an
	// account with zero owner-role users — demotion + deletion
	// both refuse rather than orphan the account.
	ErrLastOwner = errors.New("platform: cannot remove the last owner")
)
