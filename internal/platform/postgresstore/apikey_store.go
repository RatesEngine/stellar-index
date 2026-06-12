package postgresstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// APIKeyStore implements [platform.APIKeyStore] against Postgres
// (`api_keys` table from migration 0027).
//
// The runtime auth validator at `internal/auth` keeps reading
// from Redis until the cutover migration mirrors records here;
// today this store is exercised exclusively by the dashboard's
// list/create/revoke surface — the validator picks it up when
// `cmd/ratesengine-api` swaps the Redis-only store for a
// Postgres-backed read-through one.
type APIKeyStore struct{ s *Store }

// NewAPIKeyStore returns the Postgres-backed implementation.
func NewAPIKeyStore(s *Store) *APIKeyStore { return &APIKeyStore{s: s} }

const apiKeyColumns = `
	id, account_id,
	created_by_user_id,
	name,
	COALESCE(description, ''),
	key_hash, key_prefix, tier,
	rate_limit_per_min,
	COALESCE(monthly_quota, 0),
	permissions,
	ip_allowlist,
	referer_allowlist,
	expires_at,
	revoked_at, revoked_by_user_id, COALESCE(revoked_reason, ''),
	last_used_at, last_used_ip, COALESCE(last_used_user_agent, ''),
	COALESCE(usage_alert_threshold_pct, 0),
	created_at
`

func scanAPIKey(row interface {
	Scan(...any) error
},
) (platform.APIKey, error) {
	var k platform.APIKey
	var (
		createdByUserID, revokedByUserID sql.NullString
		expiresAt, revokedAt, lastUsedAt sql.NullTime
		lastUsedIP                       sql.NullString
		permissions                      []byte
		ipAllowlist                      pq.StringArray
		refererAllowlist                 pq.StringArray
	)
	if err := row.Scan(
		&k.ID, &k.AccountID, &createdByUserID,
		&k.Name, &k.Description,
		&k.KeyHash, &k.KeyPrefix, &k.Tier,
		&k.RateLimitPerMin, &k.MonthlyQuota,
		&permissions, &ipAllowlist, &refererAllowlist,
		&expiresAt,
		&revokedAt, &revokedByUserID, &k.RevokedReason,
		&lastUsedAt, &lastUsedIP, &k.LastUsedUserAgent,
		&k.UsageAlertThresholdPct,
		&k.CreatedAt,
	); err != nil {
		return platform.APIKey{}, err
	}
	k.CreatedByUserID = parseUUIDNullString(createdByUserID)
	k.RevokedByUserID = parseUUIDNullString(revokedByUserID)
	if expiresAt.Valid {
		k.ExpiresAt = expiresAt.Time
	}
	if revokedAt.Valid {
		k.RevokedAt = revokedAt.Time
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = lastUsedAt.Time
	}
	if lastUsedIP.Valid && lastUsedIP.String != "" {
		k.LastUsedIP = net.ParseIP(lastUsedIP.String)
	}
	if len(permissions) > 0 {
		if err := json.Unmarshal(permissions, &k.Permissions); err != nil {
			return platform.APIKey{}, fmt.Errorf("decode permissions: %w", err)
		}
	}
	k.RefererAllowlist = []string(refererAllowlist)
	k.IPAllowlist = parseCIDRArray(ipAllowlist)
	return k, nil
}

// parseUUIDNullString returns the zero UUID when the column was
// NULL or the value didn't parse — both cases the caller treats
// the same: "no creator / revoker on this row".
func parseUUIDNullString(s sql.NullString) uuid.UUID {
	if !s.Valid || s.String == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s.String)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// parseCIDRArray decodes the textual cidr[] form Postgres ships.
// Malformed entries are skipped rather than failing the whole row;
// the dashboard surfaces "X invalid prefixes" via a separate
// validation pass when the operator edits the allowlist.
func parseCIDRArray(in pq.StringArray) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	out := make([]netip.Prefix, 0, len(in))
	for _, raw := range in {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Create inserts a new key. Caller has already hashed the
// plaintext + computed the prefix. Enforces the per-account
// `maxActiveKeysPerAccount` cap atomically. F-1257 (codex audit-
// 2026-05-12): the prior unlocked count-CTE could let two concurrent
// callers at the cap-1 boundary each see the same snapshot (n=24)
// and both insert under MVCC, ending at 26. Now the Create runs
// inside a transaction guarded by
// `pg_advisory_xact_lock(hashtext('apikey:'||account_id::text))`,
// so concurrent calls for the same account serialise through one
// critical section. The lock keyspace is disjoint from F-1248's
// `'webhook:'`-prefixed keys so the two quotas don't interfere.
// Returns [platform.ErrAPIKeyQuotaExceeded] when the cap is met.
//
// `maxActiveKeysPerAccount` is passed by the handler
// (MaxKeysPerAccount = 25 at time of writing). Zero or negative
// disables the cap (used by ratesengine-ops staff seeding).
func (r *APIKeyStore) Create(ctx context.Context, k platform.APIKey, maxActiveKeysPerAccount int) (platform.APIKey, error) {
	args, err := buildAPIKeyCreateArgs(k)
	if err != nil {
		return platform.APIKey{}, err
	}
	q := apiKeyInsertUncapped
	if maxActiveKeysPerAccount > 0 {
		q = apiKeyInsertCapped
		args = append(args, maxActiveKeysPerAccount)
	}

	// Uncapped path: no quota race to defend against; skip the
	// advisory lock + tx wrapper.
	if maxActiveKeysPerAccount <= 0 {
		row := r.s.db.QueryRowContext(ctx, q, args...)
		return finalizeAPIKeyCreate(scanAPIKey(row))
	}

	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.APIKey{}, fmt.Errorf("postgresstore: APIKeyStore.Create: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('apikey:' || $1::text))`,
		k.AccountID); err != nil {
		return platform.APIKey{}, fmt.Errorf("postgresstore: APIKeyStore.Create: advisory lock: %w", err)
	}
	row := tx.QueryRowContext(ctx, q, args...)
	out, err := finalizeAPIKeyCreate(scanAPIKey(row))
	if err != nil {
		return platform.APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.APIKey{}, fmt.Errorf("postgresstore: APIKeyStore.Create: commit: %w", err)
	}
	return out, nil
}

// buildAPIKeyCreateArgs marshals the platform.APIKey fields into
// the positional args the apiKeyInsert* statements expect. Split
// out of [APIKeyStore.Create] so that function stays under the
// funlen lint cap.
func buildAPIKeyCreateArgs(k platform.APIKey) ([]any, error) {
	permissionsJSON, err := encodePermissions(k.Permissions)
	if err != nil {
		return nil, fmt.Errorf("encode permissions: %w", err)
	}
	return []any{
		k.ID, k.AccountID, uuidOrEmpty(k.CreatedByUserID),
		k.Name, k.Description,
		k.KeyHash, k.KeyPrefix, string(k.Tier),
		k.RateLimitPerMin, k.MonthlyQuota,
		permissionsJSON,
		ipAllowlistArray(k.IPAllowlist),
		nonNilStringArray(k.RefererAllowlist),
		nullTime(k.ExpiresAt),
		k.UsageAlertThresholdPct,
	}, nil
}

// finalizeAPIKeyCreate maps the row-scan result to the public
// (APIKey, sentinel-error) contract. ErrNoRows → quota exceeded,
// unique-violation → ErrConflict, anything else → wrapped error.
func finalizeAPIKeyCreate(out platform.APIKey, err error) (platform.APIKey, error) {
	if err == nil {
		return out, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return platform.APIKey{}, platform.ErrAPIKeyQuotaExceeded
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == pgErrUniqueViolation {
		return platform.APIKey{}, fmt.Errorf("create api key: %w", platform.ErrConflict)
	}
	return platform.APIKey{}, fmt.Errorf("create api key: %w", err)
}

// apiKeyInsertCapped is the gated INSERT used when the caller
// passes maxActiveKeysPerAccount > 0. Combined with the
// pg_advisory_xact_lock the per-account quota cannot be
// exceeded by concurrent callers.
const apiKeyInsertCapped = `
	WITH active_count AS (
	    SELECT COUNT(*) AS n
	      FROM api_keys
	     WHERE account_id = $2 AND revoked_at IS NULL
	)
	INSERT INTO api_keys (
		id, account_id, created_by_user_id,
		name, description,
		key_hash, key_prefix, tier,
		rate_limit_per_min, monthly_quota,
		permissions,
		ip_allowlist,
		referer_allowlist,
		expires_at,
		usage_alert_threshold_pct
	)
	SELECT $1, $2, NULLIF($3::text, '')::uuid,
	       $4, NULLIF($5, ''),
	       $6, $7, $8,
	       $9, NULLIF($10, 0)::bigint,
	       $11::jsonb,
	       $12::cidr[],
	       $13::text[],
	       $14,
	       NULLIF($15, 0)
	  FROM active_count
	 WHERE active_count.n < $16
	RETURNING ` + apiKeyColumns

// apiKeyInsertUncapped is the unfenced INSERT path used by
// staff-seeding callers that bypass the per-account cap (e.g.
// ratesengine-ops).
const apiKeyInsertUncapped = `
	INSERT INTO api_keys (
		id, account_id, created_by_user_id,
		name, description,
		key_hash, key_prefix, tier,
		rate_limit_per_min, monthly_quota,
		permissions,
		ip_allowlist,
		referer_allowlist,
		expires_at,
		usage_alert_threshold_pct
	)
	VALUES ($1, $2, NULLIF($3::text, '')::uuid,
	        $4, NULLIF($5, ''),
	        $6, $7, $8,
	        $9, NULLIF($10, 0)::bigint,
	        $11::jsonb,
	        $12::cidr[],
	        $13::text[],
	        $14,
	        NULLIF($15, 0))
	RETURNING ` + apiKeyColumns

func (r *APIKeyStore) Get(ctx context.Context, id string) (platform.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE id = $1`
	row := r.s.db.QueryRowContext(ctx, q, id)
	out, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.APIKey{}, platform.ErrNotFound
		}
		return platform.APIKey{}, fmt.Errorf("get api key: %w", err)
	}
	return out, nil
}

func (r *APIKeyStore) GetByHash(ctx context.Context, keyHash []byte) (platform.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = $1`
	row := r.s.db.QueryRowContext(ctx, q, keyHash)
	out, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.APIKey{}, platform.ErrNotFound
		}
		return platform.APIKey{}, fmt.Errorf("get api key by hash: %w", err)
	}
	return out, nil
}

func (r *APIKeyStore) ListForAccount(ctx context.Context, accountID uuid.UUID) ([]platform.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE account_id = $1 ORDER BY created_at ASC`
	rows, err := r.s.db.QueryContext(ctx, q, accountID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("list api keys scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Update writes the editable fields. The schema's CHECK
// constraints catch malformed tiers / quotas; we surface those
// generically rather than mapping to platform.ErrConflict
// (a Postgres CHECK violation isn't a uniqueness conflict).
func (r *APIKeyStore) Update(ctx context.Context, k platform.APIKey) error {
	permissionsJSON, err := encodePermissions(k.Permissions)
	if err != nil {
		return fmt.Errorf("encode permissions: %w", err)
	}
	const q = `
		UPDATE api_keys SET
			name = $2,
			description = NULLIF($3, ''),
			rate_limit_per_min = $4,
			monthly_quota = NULLIF($5, 0)::bigint,
			permissions = $6::jsonb,
			ip_allowlist = $7::cidr[],
			referer_allowlist = $8::text[],
			expires_at = $9,
			usage_alert_threshold_pct = NULLIF($10, 0)
		WHERE id = $1
	`
	res, err := r.s.db.ExecContext(ctx, q,
		k.ID, k.Name, k.Description,
		k.RateLimitPerMin, k.MonthlyQuota,
		permissionsJSON,
		ipAllowlistArray(k.IPAllowlist),
		nonNilStringArray(k.RefererAllowlist),
		nullTime(k.ExpiresAt),
		k.UsageAlertThresholdPct,
	)
	if err != nil {
		return fmt.Errorf("update api key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Revoke soft-deletes by setting revoked_at + reason. Idempotent
// — calling on an already-revoked key just rewrites the reason
// and re-stamps the timestamp.
func (r *APIKeyStore) Revoke(ctx context.Context, id string, byUserID uuid.UUID, reason string) error {
	const q = `
		UPDATE api_keys SET
			revoked_at = COALESCE(revoked_at, now()),
			revoked_by_user_id = NULLIF($2::text, '')::uuid,
			revoked_reason = NULLIF($3, '')
		WHERE id = $1
	`
	res, err := r.s.db.ExecContext(ctx, q, id, uuidOrEmpty(byUserID), reason)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// TouchUsage updates the last-seen fields. Caller debounces to
// once-per-minute to avoid hot-row contention; this method itself
// is unconditional.
func (r *APIKeyStore) TouchUsage(ctx context.Context, id string, ip net.IP, userAgent string) error {
	const q = `
		UPDATE api_keys SET
			last_used_at = now(),
			last_used_ip = NULLIF($2, '')::inet,
			last_used_user_agent = NULLIF($3, '')
		WHERE id = $1
	`
	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}
	res, err := r.s.db.ExecContext(ctx, q, id, ipStr, userAgent)
	if err != nil {
		return fmt.Errorf("touch api key usage: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────

// encodePermissions serialises to JSON bytes ready for the jsonb
// column. Empty Permissions{} encodes to {"all":false} which is
// distinct from the schema default {"all":true}; callers who
// want the default must set All=true explicitly before Create.
func encodePermissions(p platform.KeyPermissions) ([]byte, error) {
	return json.Marshal(p)
}

// uuidOrEmpty returns the UUID's text form for non-zero values,
// empty string for the zero UUID. Lets us write
// `NULLIF($1::text, ”)::uuid` in SQL to bind NULL when the
// caller meant "absent".
func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// nullTime maps the Go zero time.Time to a SQL NULL. Postgres
// timestamptz columns reject the Go zero so we gate at the
// driver boundary rather than in every column update.
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// ipAllowlistArray serialises a netip.Prefix slice into a Postgres
// cidr[] driver value. pq doesn't ship a typed cidr-array helper,
// so we hand-roll the textual `{cidr,cidr}` form which Postgres
// accepts for parameterised inserts via array_in.
type cidrArray []netip.Prefix

func ipAllowlistArray(prefixes []netip.Prefix) cidrArray { return cidrArray(prefixes) }

// Value formats the prefix list as a Postgres array literal —
// `{10.0.0.0/8,192.168.1.0/24}`. Empty slice → `{}` matching the
// schema default.
func (a cidrArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	parts := make([]string, len(a))
	for i, p := range a {
		parts[i] = p.String()
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

// nonNilStringArray defends the api_keys insert path against
// `lib/pq` emitting SQL NULL for a `pq.StringArray(nil)` value.
// Migration 0027 declares `referer_allowlist text[] NOT NULL
// DEFAULT '{}'`, so a NULL insert violates the column's
// NOT NULL constraint with `pq: null value in column
// "referer_allowlist" violates not-null constraint (23502)`.
//
// F-1262 (codex audit-2026-05-13): the dashboard `keys/page.tsx`
// + the auth.CreateAPIKeyRequest path both leave the optional
// `referer_allowlist` field nil when callers don't set it, and
// the prior `pq.StringArray(k.RefererAllowlist)` direct conversion
// surfaced as a 500 on the default request shape. Wrap the raw
// slice through this helper at every Postgres-bound `text[]` call
// site so a nil slice persists as the schema-default empty array.
func nonNilStringArray(in []string) pq.StringArray {
	if in == nil {
		return pq.StringArray{}
	}
	return pq.StringArray(in)
}

// Compile-time interface check.
var _ platform.APIKeyStore = (*APIKeyStore)(nil)
