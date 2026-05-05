package platform

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Tier is the billing-plan / capability bucket an account sits in.
// Matches the `accounts.tier` CHECK constraint in migration 0027.
type Tier string

const (
	TierFree       Tier = "free"
	TierStarter    Tier = "starter"
	TierPro        Tier = "pro"
	TierBusiness   Tier = "business"
	TierEnterprise Tier = "enterprise"
)

// AccountStatus is the lifecycle state of an account.
type AccountStatus string

const (
	AccountActive    AccountStatus = "active"
	AccountSuspended AccountStatus = "suspended"
	AccountClosed    AccountStatus = "closed"
)

// Account is the top-level org primitive — every API key,
// subscription, webhook, audit-log entry hangs off Account.ID.
//
// v1 is one user per account; v2 multi-org migrates by moving
// (account_id, role) from User to a new memberships join table
// without changing this type.
type Account struct {
	ID                          uuid.UUID
	Name                        string
	Slug                        string
	BillingEmail                string
	StripeCustomerID            string // empty when no Stripe customer minted yet
	Tier                        Tier
	Status                      AccountStatus
	CreatedAt                   time.Time
	SuspendedAt                 time.Time // zero when not suspended
	SuspendedReason             string
	RateLimitPerMinOverride     int   // 0 = inherit tier default
	MonthlyRequestQuotaOverride int64 // 0 = inherit tier default
}

// AccountStore is the persistence boundary for [Account].
//
// Implementations: PostgresAccountStore (Week 1, this PR ships
// the interface; concrete impl lands when the auth flow needs
// it in Week 2).
type AccountStore interface {
	// Create inserts a new account. Returns the created row with
	// server-generated ID + CreatedAt populated.
	Create(ctx context.Context, a Account) (Account, error)

	// Get returns the account by ID.
	Get(ctx context.Context, id uuid.UUID) (Account, error)

	// GetBySlug returns the account by URL-safe handle.
	GetBySlug(ctx context.Context, slug string) (Account, error)

	// GetByStripeCustomerID maps Stripe customer back to our
	// account — used by webhook handlers.
	GetByStripeCustomerID(ctx context.Context, stripeCustomerID string) (Account, error)

	// Update writes mutable fields (name, billing_email, tier,
	// status, overrides). Immutable fields (id, slug, created_at)
	// are ignored.
	Update(ctx context.Context, a Account) error

	// Suspend sets status=suspended with a reason. Idempotent.
	Suspend(ctx context.Context, id uuid.UUID, reason string) error

	// Unsuspend clears the suspension. Idempotent.
	Unsuspend(ctx context.Context, id uuid.UUID) error
}
