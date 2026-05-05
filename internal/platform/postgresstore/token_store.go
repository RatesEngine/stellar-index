package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/RatesEngine/rates-engine/internal/platform"
)

// TokenStore implements [platform.TokenStore] against Postgres.
//
// Magic-link tokens and invites share a 32-byte sha256 token-
// hash key shape but live in distinct tables — the
// authorisation envelope (purpose / role / acceptance) differs
// enough that one-table-many-purposes would force callers to
// remember which fields apply to which row.
type TokenStore struct {
	s   *Store
	now func() time.Time
}

// NewTokenStore returns the Postgres-backed implementation.
// `now` defaults to time.Now.UTC; tests inject a fixed clock.
func NewTokenStore(s *Store) *TokenStore {
	return &TokenStore{s: s, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the time source. Tests that pin
// "expired five minutes ago" semantics use this.
func (r *TokenStore) WithClock(now func() time.Time) *TokenStore {
	r.now = now
	return r
}

// CreateMagicLinkToken inserts a new row. Caller already
// generated the random plaintext + computed sha256 and is
// responsible for emailing the plaintext to the user.
func (r *TokenStore) CreateMagicLinkToken(ctx context.Context, t platform.MagicLinkToken) error {
	const q = `
		INSERT INTO magic_link_tokens (
			token_hash, email, purpose, expires_at, requested_ip
		)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.s.db.ExecContext(ctx, q,
		t.TokenHash, t.Email, string(t.Purpose), t.ExpiresAt, ipString(t.RequestedIP),
	)
	if err != nil {
		return fmt.Errorf("create magic link token: %w", err)
	}
	return nil
}

// ConsumeMagicLinkToken atomically marks the row consumed and
// returns it. Three terminal states surface as distinct errors:
//
//   - row absent → platform.ErrNotFound
//   - row already consumed → platform.ErrNotFound (treated the
//     same so a leaked token reused after consumption isn't
//     distinguishable from a typo'd one)
//   - row exists but past expires_at → platform.ErrTokenExpired
//
// The atomic UPDATE...RETURNING combines the lookup, expiry
// check, and consumption-marking so two concurrent requests
// can't both succeed.
func (r *TokenStore) ConsumeMagicLinkToken(ctx context.Context, tokenHash []byte) (platform.MagicLinkToken, error) {
	now := r.now()
	const q = `
		UPDATE magic_link_tokens
		SET consumed_at = $2
		WHERE token_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > $2
		RETURNING token_hash, email, purpose, expires_at, consumed_at,
		          requested_ip, created_at
	`
	row := r.s.db.QueryRowContext(ctx, q, tokenHash, now)

	var (
		out        platform.MagicLinkToken
		consumedAt sql.NullTime
		ipText     string
	)
	if err := row.Scan(
		&out.TokenHash, &out.Email, &out.Purpose,
		&out.ExpiresAt, &consumedAt, &ipText, &out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Distinguish expired-but-present from absent: a second
			// query without the expiry guard. Cheap because the
			// happy path is the UPDATE above; this only runs on
			// failure.
			return platform.MagicLinkToken{}, r.classifyMagicLinkMiss(ctx, tokenHash)
		}
		return platform.MagicLinkToken{}, fmt.Errorf("consume magic link token: %w", err)
	}
	if consumedAt.Valid {
		out.ConsumedAt = consumedAt.Time
	}
	return out, nil
}

// classifyMagicLinkMiss runs after the consume UPDATE found no
// rows. Distinguishes:
//   - row exists, past expiry → ErrTokenExpired
//   - row exists, already consumed → ErrNotFound (intentional;
//     re-use after consumption looks identical to "wrong token")
//   - row absent → ErrNotFound
func (r *TokenStore) classifyMagicLinkMiss(ctx context.Context, tokenHash []byte) error {
	now := r.now()
	const q = `
		SELECT expires_at, consumed_at
		FROM magic_link_tokens
		WHERE token_hash = $1
	`
	var expiresAt time.Time
	var consumedAt sql.NullTime
	err := r.s.db.QueryRowContext(ctx, q, tokenHash).Scan(&expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify magic link miss: %w", err)
	}
	if consumedAt.Valid {
		return platform.ErrNotFound
	}
	if !expiresAt.After(now) {
		return platform.ErrTokenExpired
	}
	// Shouldn't reach here — the row was consumable but the
	// UPDATE missed it. Concurrent consumption from another
	// process. Treat as not-found.
	return platform.ErrNotFound
}

// ─── Invites ─────────────────────────────────────────────────────

const inviteColumns = `
	token_hash, account_id, email, role,
	invited_by_user_id, expires_at,
	accepted_at, revoked_at, created_at
`

func scanInvite(row interface {
	Scan(...any) error
},
) (platform.Invite, error) {
	var i platform.Invite
	var acceptedAt, revokedAt sql.NullTime
	if err := row.Scan(
		&i.TokenHash,
		&i.AccountID,
		&i.Email,
		&i.Role,
		&i.InvitedByUserID,
		&i.ExpiresAt,
		&acceptedAt,
		&revokedAt,
		&i.CreatedAt,
	); err != nil {
		return platform.Invite{}, err
	}
	if acceptedAt.Valid {
		i.AcceptedAt = acceptedAt.Time
	}
	if revokedAt.Valid {
		i.RevokedAt = revokedAt.Time
	}
	return i, nil
}

func (r *TokenStore) CreateInvite(ctx context.Context, i platform.Invite) error {
	const q = `
		INSERT INTO invites (
			token_hash, account_id, email, role,
			invited_by_user_id, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.s.db.ExecContext(ctx, q,
		i.TokenHash, i.AccountID, i.Email, string(i.Role),
		i.InvitedByUserID, i.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	return nil
}

// AcceptInvite atomically marks accepted + returns the row.
// Same expired/not-found classification as ConsumeMagicLinkToken.
func (r *TokenStore) AcceptInvite(ctx context.Context, tokenHash []byte) (platform.Invite, error) {
	now := r.now()
	const q = `
		UPDATE invites
		SET accepted_at = $2
		WHERE token_hash = $1
		  AND accepted_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > $2
		RETURNING ` + inviteColumns

	row := r.s.db.QueryRowContext(ctx, q, tokenHash, now)
	out, err := scanInvite(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Invite{}, r.classifyInviteMiss(ctx, tokenHash)
		}
		return platform.Invite{}, fmt.Errorf("accept invite: %w", err)
	}
	return out, nil
}

func (r *TokenStore) classifyInviteMiss(ctx context.Context, tokenHash []byte) error {
	now := r.now()
	const q = `
		SELECT expires_at, accepted_at, revoked_at
		FROM invites
		WHERE token_hash = $1
	`
	var expiresAt time.Time
	var acceptedAt, revokedAt sql.NullTime
	err := r.s.db.QueryRowContext(ctx, q, tokenHash).Scan(&expiresAt, &acceptedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify invite miss: %w", err)
	}
	if acceptedAt.Valid || revokedAt.Valid {
		return platform.ErrNotFound
	}
	if !expiresAt.After(now) {
		return platform.ErrTokenExpired
	}
	return platform.ErrNotFound
}

// RevokeInvite marks revoked. Owner / admin uses this to
// cancel pending invites. Idempotent.
func (r *TokenStore) RevokeInvite(ctx context.Context, tokenHash []byte) error {
	const q = `
		UPDATE invites
		SET revoked_at = now()
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND accepted_at IS NULL
	`
	_, err := r.s.db.ExecContext(ctx, q, tokenHash)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	return nil
}

// ListInvitesForAccount returns active (unrevoked, unaccepted)
// invites — used by the team-management UI.
func (r *TokenStore) ListInvitesForAccount(ctx context.Context, accountID uuid.UUID) ([]platform.Invite, error) {
	const q = `
		SELECT ` + inviteColumns + `
		FROM invites
		WHERE account_id = $1
		  AND accepted_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > now()
		ORDER BY created_at DESC
	`
	rows, err := r.s.db.QueryContext(ctx, q, accountID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, fmt.Errorf("list invites scan: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Compile-time interface check.
var _ platform.TokenStore = (*TokenStore)(nil)
