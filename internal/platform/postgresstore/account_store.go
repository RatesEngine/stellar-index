package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// AccountStore implements [platform.AccountStore] against
// Postgres. Constructor takes a [*Store] so multiple platform
// stores share the connection pool.
type AccountStore struct{ s *Store }

// NewAccountStore returns the Postgres-backed implementation.
func NewAccountStore(s *Store) *AccountStore { return &AccountStore{s: s} }

const (
	pgErrUniqueViolation = "23505"
)

// accountColumns lists the SELECT projection used by every
// reader; centralised so it stays consistent with the
// scanAccount helper below.
const accountColumns = `
	id, name, slug, billing_email,
	COALESCE(stripe_customer_id, ''),
	tier, status, created_at,
	suspended_at, COALESCE(suspended_reason, ''),
	COALESCE(rate_limit_per_min_override, 0),
	COALESCE(monthly_request_quota_override, 0)
`

func scanAccount(row interface {
	Scan(...any) error
},
) (platform.Account, error) {
	var a platform.Account
	var suspendedAt sql.NullTime
	if err := row.Scan(
		&a.ID,
		&a.Name,
		&a.Slug,
		&a.BillingEmail,
		&a.StripeCustomerID,
		&a.Tier,
		&a.Status,
		&a.CreatedAt,
		&suspendedAt,
		&a.SuspendedReason,
		&a.RateLimitPerMinOverride,
		&a.MonthlyRequestQuotaOverride,
	); err != nil {
		return platform.Account{}, err
	}
	if suspendedAt.Valid {
		a.SuspendedAt = suspendedAt.Time
	}
	return a, nil
}

// Create inserts a new account. The schema's CHECK constraints
// catch malformed slugs / tiers / statuses; we map the unique-
// violation case (slug or stripe_customer_id collision) to
// platform.ErrConflict.
func (r *AccountStore) Create(ctx context.Context, a platform.Account) (platform.Account, error) {
	const q = `
		INSERT INTO accounts (
			name, slug, billing_email, stripe_customer_id,
			tier, status,
			rate_limit_per_min_override, monthly_request_quota_override
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6,
		        NULLIF($7, 0), NULLIF($8, 0))
		RETURNING ` + accountColumns

	row := r.s.db.QueryRowContext(ctx, q,
		a.Name, a.Slug, a.BillingEmail, a.StripeCustomerID,
		string(a.Tier), string(a.Status),
		a.RateLimitPerMinOverride, a.MonthlyRequestQuotaOverride,
	)
	out, err := scanAccount(row)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgErrUniqueViolation {
			return platform.Account{}, fmt.Errorf("create account: %w", platform.ErrConflict)
		}
		return platform.Account{}, fmt.Errorf("create account: %w", err)
	}
	return out, nil
}

// Get returns the account by ID; ErrNotFound if absent.
func (r *AccountStore) Get(ctx context.Context, id uuid.UUID) (platform.Account, error) {
	const q = `SELECT ` + accountColumns + ` FROM accounts WHERE id = $1`
	row := r.s.db.QueryRowContext(ctx, q, id)
	out, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Account{}, platform.ErrNotFound
		}
		return platform.Account{}, fmt.Errorf("get account: %w", err)
	}
	return out, nil
}

// GetBySlug — same shape as Get; the slug column is UNIQUE.
func (r *AccountStore) GetBySlug(ctx context.Context, slug string) (platform.Account, error) {
	const q = `SELECT ` + accountColumns + ` FROM accounts WHERE slug = $1`
	row := r.s.db.QueryRowContext(ctx, q, slug)
	out, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Account{}, platform.ErrNotFound
		}
		return platform.Account{}, fmt.Errorf("get account by slug: %w", err)
	}
	return out, nil
}

// GetByStripeCustomerID maps the Stripe customer back to our
// account row. Stripe webhook handlers use this to find the
// account a `customer.*` event applies to.
func (r *AccountStore) GetByStripeCustomerID(ctx context.Context, stripeCustomerID string) (platform.Account, error) {
	if stripeCustomerID == "" {
		return platform.Account{}, platform.ErrNotFound
	}
	const q = `SELECT ` + accountColumns + ` FROM accounts WHERE stripe_customer_id = $1`
	row := r.s.db.QueryRowContext(ctx, q, stripeCustomerID)
	out, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Account{}, platform.ErrNotFound
		}
		return platform.Account{}, fmt.Errorf("get account by stripe customer: %w", err)
	}
	return out, nil
}

// Update writes the mutable fields. Immutable (id, slug,
// created_at) are ignored; passing different values is a no-op
// rather than an error so callers can round-trip a Get →
// mutate → Update pattern.
func (r *AccountStore) Update(ctx context.Context, a platform.Account) error {
	const q = `
		UPDATE accounts SET
			name = $2,
			billing_email = $3,
			stripe_customer_id = NULLIF($4, ''),
			tier = $5,
			status = $6,
			rate_limit_per_min_override = NULLIF($7, 0),
			monthly_request_quota_override = NULLIF($8, 0)
		WHERE id = $1
	`
	res, err := r.s.db.ExecContext(ctx, q,
		a.ID, a.Name, a.BillingEmail, a.StripeCustomerID,
		string(a.Tier), string(a.Status),
		a.RateLimitPerMinOverride, a.MonthlyRequestQuotaOverride,
	)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Suspend sets status=suspended + reason. Idempotent — calling
// on an already-suspended account just rewrites the reason.
func (r *AccountStore) Suspend(ctx context.Context, id uuid.UUID, reason string) error {
	const q = `
		UPDATE accounts SET
			status = 'suspended',
			suspended_at = COALESCE(suspended_at, now()),
			suspended_reason = $2
		WHERE id = $1
	`
	res, err := r.s.db.ExecContext(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("suspend account: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Unsuspend clears suspension state. Idempotent.
func (r *AccountStore) Unsuspend(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE accounts SET
			status = 'active',
			suspended_at = NULL,
			suspended_reason = NULL
		WHERE id = $1
	`
	res, err := r.s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("unsuspend account: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Compile-time interface check.
var _ platform.AccountStore = (*AccountStore)(nil)
