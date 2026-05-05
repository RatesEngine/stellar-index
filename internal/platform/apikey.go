package platform

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// APIKey is the extended replacement for the Redis-only
// `auth.APIKeyRecord`. Phase 1 Week 4 ships the cutover where
// the runtime auth path consults this table (mirrored to Redis
// for read latency); until then the existing Redis store stays
// canonical and the dashboard merely writes through.
//
// The plaintext is NEVER stored — KeyHash is sha256 of the
// plaintext, KeyPrefix is the first 12 chars (safe to display).
type APIKey struct {
	ID                     string // "kid_<hex>"
	AccountID              uuid.UUID
	CreatedByUserID        uuid.UUID // zero when system-minted (e.g. /v1/signup pre-dashboard)
	Name                   string
	Description            string
	KeyHash                []byte
	KeyPrefix              string
	Tier                   APIKeyTier
	RateLimitPerMin        int
	MonthlyQuota           int64 // 0 = inherit from plan
	Permissions            KeyPermissions
	IPAllowlist            []netip.Prefix
	RefererAllowlist       []string
	ExpiresAt              time.Time // zero = no expiry
	RevokedAt              time.Time
	RevokedByUserID        uuid.UUID
	RevokedReason          string
	LastUsedAt             time.Time
	LastUsedIP             net.IP
	LastUsedUserAgent      string
	UsageAlertThresholdPct int // 0 = no alert; valid range 1..100
	CreatedAt              time.Time
}

// APIKeyTier is the tier the key authenticates as. Distinct
// from [Tier] (the account's billing tier) — a key can be
// pinned to a specific runtime tier independent of the account
// plan, e.g. for operator credentials or partner integrations.
type APIKeyTier string

const (
	APIKeyTierAPIKey   APIKeyTier = "apikey"   // standard customer key
	APIKeyTierPartner  APIKeyTier = "partner"  // higher rate limit, custom contract
	APIKeyTierOperator APIKeyTier = "operator" // staff-issued; unlocks admin endpoints
)

// KeyPermissions is the per-key capability set. Stored as JSONB
// in Postgres. v1 default is {All: true} (no enforcement); the
// scoped-keys feature in Phase 3 wires the per-endpoint
// allow/deny check.
type KeyPermissions struct {
	All   bool                 `json:"all"`
	Allow []KeyPermissionEntry `json:"allow,omitempty"`
	Deny  []KeyPermissionEntry `json:"deny,omitempty"`
}

// KeyPermissionEntry matches one allow/deny rule. Either Endpoint
// (exact pattern, e.g. "GET /v1/price") or EndpointPrefix
// (e.g. "/v1/account/") — never both.
type KeyPermissionEntry struct {
	Endpoint       string `json:"endpoint,omitempty"`
	EndpointPrefix string `json:"endpoint_prefix,omitempty"`
}

// IsActive reports whether the key is currently valid: not
// revoked, not expired (or expiry zero), and assumes the parent
// account is active (caller is responsible for checking that).
func (k APIKey) IsActive(now time.Time) bool {
	if !k.RevokedAt.IsZero() {
		return false
	}
	if !k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt) {
		return false
	}
	return true
}

// APIKeyStore persists [APIKey] in Postgres. The runtime auth
// validator will consult this store via a Redis-cached read-
// through wrapper once the Phase 1 Week 4 cutover ships.
type APIKeyStore interface {
	// Create inserts. Caller has already hashed the plaintext
	// and computed the prefix. Returns ErrConflict if the hash
	// or ID collide (extremely rare; key_id is 8 hex bytes).
	Create(ctx context.Context, k APIKey) (APIKey, error)

	// Get by key ID; ErrNotFound if absent.
	Get(ctx context.Context, id string) (APIKey, error)

	// GetByHash is the hot path used by the auth validator.
	// ErrNotFound if absent; caller checks IsActive on the
	// returned record.
	GetByHash(ctx context.Context, keyHash []byte) (APIKey, error)

	// ListForAccount returns every key (active + revoked)
	// belonging to the account, sorted CreatedAt asc — matches
	// the existing /v1/account/keys ordering.
	ListForAccount(ctx context.Context, accountID uuid.UUID) ([]APIKey, error)

	// Update writes the editable fields: name, description,
	// rate_limit_per_min, monthly_quota, permissions,
	// ip_allowlist, referer_allowlist, expires_at,
	// usage_alert_threshold_pct.
	Update(ctx context.Context, k APIKey) error

	// Revoke soft-deletes by setting revoked_at + reason.
	// Idempotent: revoking an already-revoked key is a no-op.
	Revoke(ctx context.Context, id string, byUserID uuid.UUID, reason string) error

	// TouchUsage updates LastUsedAt + LastUsedIP + LastUsedUserAgent.
	// Debounced caller-side to once-per-minute.
	TouchUsage(ctx context.Context, id string, ip net.IP, userAgent string) error
}
