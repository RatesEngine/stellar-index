package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// UserStore implements [platform.UserStore] against Postgres.
type UserStore struct{ s *Store }

// NewUserStore returns the Postgres-backed implementation.
func NewUserStore(s *Store) *UserStore { return &UserStore{s: s} }

const userColumns = `
	id, account_id, email,
	COALESCE(display_name, ''),
	role,
	email_verified_at, last_login_at,
	mfa_enabled, mfa_secret_enc, mfa_recovery_codes_hashed,
	is_staff, created_at
`

func scanUser(row interface {
	Scan(...any) error
},
) (platform.User, error) {
	var u platform.User
	var emailVerifiedAt, lastLoginAt sql.NullTime
	if err := row.Scan(
		&u.ID,
		&u.AccountID,
		&u.Email,
		&u.DisplayName,
		&u.Role,
		&emailVerifiedAt,
		&lastLoginAt,
		&u.MFAEnabled,
		&u.MFASecretEnc,
		(*pq.ByteaArray)(&u.MFARecoveryCodesHashed),
		&u.IsStaff,
		&u.CreatedAt,
	); err != nil {
		return platform.User{}, err
	}
	if emailVerifiedAt.Valid {
		u.EmailVerifiedAt = emailVerifiedAt.Time
	}
	if lastLoginAt.Valid {
		u.LastLoginAt = lastLoginAt.Time
	}
	return u, nil
}

// CreateUser inserts a new user row.
func (r *UserStore) CreateUser(ctx context.Context, u platform.User) (platform.User, error) {
	const q = `
		INSERT INTO users (
			account_id, email, display_name, role,
			mfa_enabled, mfa_secret_enc, mfa_recovery_codes_hashed,
			is_staff
		)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8)
		RETURNING ` + userColumns

	row := r.s.db.QueryRowContext(ctx, q,
		u.AccountID, u.Email, u.DisplayName, string(u.Role),
		u.MFAEnabled, u.MFASecretEnc,
		pq.ByteaArray(u.MFARecoveryCodesHashed),
		u.IsStaff,
	)
	out, err := scanUser(row)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgErrUniqueViolation {
			return platform.User{}, fmt.Errorf("create user: %w", platform.ErrConflict)
		}
		return platform.User{}, fmt.Errorf("create user: %w", err)
	}
	return out, nil
}

func (r *UserStore) GetUserByID(ctx context.Context, id uuid.UUID) (platform.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	row := r.s.db.QueryRowContext(ctx, q, id)
	out, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.User{}, platform.ErrNotFound
		}
		return platform.User{}, fmt.Errorf("get user by id: %w", err)
	}
	return out, nil
}

func (r *UserStore) GetUserByEmail(ctx context.Context, email string) (platform.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = $1`
	row := r.s.db.QueryRowContext(ctx, q, email)
	out, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.User{}, platform.ErrNotFound
		}
		return platform.User{}, fmt.Errorf("get user by email: %w", err)
	}
	return out, nil
}

func (r *UserStore) ListUsersForAccount(ctx context.Context, accountID uuid.UUID) ([]platform.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE account_id = $1 ORDER BY created_at ASC`
	rows, err := r.s.db.QueryContext(ctx, q, accountID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list users scan: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser writes the mutable fields. Immutable (id,
// account_id, email, created_at, is_staff) are ignored. Staff
// flips go through a dedicated admin path (not in this PR).
func (r *UserStore) UpdateUser(ctx context.Context, u platform.User) error {
	const q = `
		UPDATE users SET
			display_name = NULLIF($2, ''),
			role = $3,
			email_verified_at = $4,
			last_login_at = $5,
			mfa_enabled = $6,
			mfa_secret_enc = $7,
			mfa_recovery_codes_hashed = $8
		WHERE id = $1
	`
	var emailVerifiedAt, lastLoginAt sql.NullTime
	if !u.EmailVerifiedAt.IsZero() {
		emailVerifiedAt = sql.NullTime{Time: u.EmailVerifiedAt, Valid: true}
	}
	if !u.LastLoginAt.IsZero() {
		lastLoginAt = sql.NullTime{Time: u.LastLoginAt, Valid: true}
	}
	res, err := r.s.db.ExecContext(ctx, q,
		u.ID, u.DisplayName, string(u.Role),
		emailVerifiedAt, lastLoginAt,
		u.MFAEnabled, u.MFASecretEnc,
		pq.ByteaArray(u.MFARecoveryCodesHashed),
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// ─── Sessions ─────────────────────────────────────────────────────

const sessionColumns = `
	id, user_id, expires_at, revoked_at, created_at, last_seen_at,
	ip_first_seen, ip_last_seen, user_agent,
	COALESCE(geo_first_seen, ''), COALESCE(geo_last_seen, '')
`

func scanSession(row interface {
	Scan(...any) error
},
) (platform.Session, error) {
	var s platform.Session
	var revokedAt sql.NullTime
	var ipFirst, ipLast string
	if err := row.Scan(
		&s.ID,
		&s.UserID,
		&s.ExpiresAt,
		&revokedAt,
		&s.CreatedAt,
		&s.LastSeenAt,
		&ipFirst,
		&ipLast,
		&s.UserAgent,
		&s.GeoFirstSeen,
		&s.GeoLastSeen,
	); err != nil {
		return platform.Session{}, err
	}
	if revokedAt.Valid {
		s.RevokedAt = revokedAt.Time
	}
	s.IPFirstSeen = net.ParseIP(ipFirst)
	s.IPLastSeen = net.ParseIP(ipLast)
	return s, nil
}

func (r *UserStore) CreateSession(ctx context.Context, s platform.Session) (platform.Session, error) {
	const q = `
		INSERT INTO sessions (
			user_id, expires_at,
			ip_first_seen, ip_last_seen, user_agent,
			geo_first_seen, geo_last_seen
		)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))
		RETURNING ` + sessionColumns

	row := r.s.db.QueryRowContext(ctx, q,
		s.UserID, s.ExpiresAt,
		ipString(s.IPFirstSeen), ipString(s.IPLastSeen), s.UserAgent,
		s.GeoFirstSeen, s.GeoLastSeen,
	)
	out, err := scanSession(row)
	if err != nil {
		return platform.Session{}, fmt.Errorf("create session: %w", err)
	}
	return out, nil
}

func (r *UserStore) GetSession(ctx context.Context, id uuid.UUID) (platform.Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions WHERE id = $1 AND revoked_at IS NULL`
	row := r.s.db.QueryRowContext(ctx, q, id)
	out, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Session{}, platform.ErrNotFound
		}
		return platform.Session{}, fmt.Errorf("get session: %w", err)
	}
	return out, nil
}

// TouchSession updates the last-seen fields. Caller debounces
// to once-per-minute to avoid hot-row contention; this method
// itself is unconditional — every call writes.
func (r *UserStore) TouchSession(ctx context.Context, id uuid.UUID, ip net.IP, userAgent string) error {
	const q = `
		UPDATE sessions SET
			last_seen_at = now(),
			ip_last_seen = $2,
			user_agent = $3
		WHERE id = $1 AND revoked_at IS NULL
	`
	res, err := r.s.db.ExecContext(ctx, q, id, ipString(ip), userAgent)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// RevokeSession sets revoked_at. Idempotent: re-revoking is a no-op.
func (r *UserStore) RevokeSession(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := r.s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllUserSessions logs the user out everywhere. Idempotent.
func (r *UserStore) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	const q = `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := r.s.db.ExecContext(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("revoke all user sessions: %w", err)
	}
	return nil
}

// ipString renders nil → empty so the column accepts the
// caller's intent. Postgres `inet` rejects empty string, so we
// pass "0.0.0.0" as a sentinel for unknown — but this only
// happens for tests.
func ipString(ip net.IP) string {
	if ip == nil {
		return "0.0.0.0"
	}
	return ip.String()
}

// Compile-time interface check.
var _ platform.UserStore = (*UserStore)(nil)
