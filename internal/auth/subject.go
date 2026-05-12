package auth

import (
	"context"
	"net/netip"
	"time"
)

// Tier identifies the rate-limit + feature-access bucket a caller
// belongs to. Stable string values — appear in metrics labels and
// in the rate-limit middleware key, so changing them is a wire
// break.
type Tier string

const (
	// TierAnonymous — no credential presented (or auth_mode=none).
	// Lowest rate-limit budget. Default.
	TierAnonymous Tier = "anonymous"

	// TierAPIKey — caller presented a valid API key.
	TierAPIKey Tier = "apikey"

	// TierSEP10 — caller presented a valid SEP-10 JWT.
	TierSEP10 Tier = "sep10"

	// TierOperator — internal operator credential. Reserved for
	// admin endpoints (`/v1/account/*` self-service + future
	// /v1/admin/*); never granted to public callers.
	TierOperator Tier = "operator"
)

// String makes Tier usable as a Prometheus label value without a
// type assertion.
func (t Tier) String() string { return string(t) }

// Subject is the authenticated identity attached to a request. The
// middleware sets a Subject on the request context BEFORE the
// rate-limit middleware runs so per-tier limits work.
//
// For anonymous requests Identifier is the request's RemoteIP +
// User-Agent hash (set by the middleware), so the rate-limit
// middleware still has a stable key to bucket against.
type Subject struct {
	// Identifier — for apikey/sep10 the subject's stable identity
	// (e.g. Stellar G-strkey for SEP-10, the API key's owner-account
	// reference for APIKey). For anonymous, a per-IP+UA hash —
	// stable for the duration of one window, not across deploys.
	Identifier string

	// Tier — see above. Drives rate-limit budget + feature gating.
	Tier Tier

	// Scopes — optional capability list (e.g. ["price:read",
	// "history:read", "admin:*"]). Empty for v1; reserved for
	// per-endpoint scope checks once auth lands.
	Scopes []string

	// KeyID — public-safe identifier for the credential the caller
	// presented. For apikey, populated from APIKeyRecord.KeyID
	// (distinct from the secret hash so it's safe to appear in
	// logs / /v1/account/me responses). Empty for anonymous and
	// pre-#190 SEP-10 stubs.
	KeyID string

	// RateLimitPerMin — per-tier budget the rate-limit middleware
	// applies. Zero means "use the deployment default for this
	// tier"; a non-zero value overrides at the per-key level
	// (paid customers on a custom plan).
	RateLimitPerMin int

	// CreatedAt — when the credential was issued. Zero for
	// anonymous. Surfaced via /v1/account/me; not load-bearing
	// elsewhere.
	CreatedAt time.Time

	// Label — customer-supplied human-readable name for the
	// credential (set at /v1/account/keys POST time). Surfaced via
	// /v1/account/me so the UI can show "your key 'ci-bot'"; never
	// consulted by auth or rate-limit logic. Empty for anonymous
	// and for records seeded without a label.
	Label string

	// KeyPrefix — first 12 chars of the plaintext key (e.g.
	// `rek_4f9c1d8b`). Set on records minted after the key-prefix
	// feature shipped; empty on legacy records and on anonymous
	// subjects. Customers see this in dashboard listings to
	// identify which key matches a row in their secret manager.
	KeyPrefix string

	// IPAllowlist — when non-empty, the request's resolved client
	// IP must fall in at least one prefix or the request is
	// rejected with 403. F-1226 (codex audit-2026-05-12): pre-fix
	// the dashboard accepted this field but no middleware
	// enforced it. Populated only by API-key validators that have
	// the field from the Postgres platform store (the legacy
	// Redis-only validator leaves it empty so allowlist
	// enforcement is opt-in per validator).
	IPAllowlist []netip.Prefix

	// RefererAllowlist — when non-empty, the request's `Referer`
	// header's host must exactly match one entry or the request
	// is rejected with 403. Populated like IPAllowlist; empty
	// disables the check. F-1226 (codex audit-2026-05-12).
	RefererAllowlist []string

	// AllowAllPermissions — if true, the per-endpoint permission
	// check is skipped (legacy posture). When false, the
	// Allow/Deny lists below decide. Populated by the validator
	// from the persisted KeyPermissions.All flag. F-1226 (codex
	// audit-2026-05-12).
	AllowAllPermissions bool

	// AllowPermissions — exact (METHOD PATH) or prefix-on-path
	// rules that grant access. Non-empty with AllowAllPermissions
	// false enables allow-list mode. F-1226 (codex audit-2026-05-12).
	AllowPermissions []SubjectPermissionEntry

	// DenyPermissions — exact / prefix rules that deny access
	// even when the allow-list would otherwise admit them. Always
	// consulted (even when AllowAllPermissions is true).
	// F-1226 (codex audit-2026-05-12).
	DenyPermissions []SubjectPermissionEntry

	// MonthlyQuota — when > 0, the [middleware.MonthlyQuota]
	// enforcer 429s authenticated requests for this Subject once
	// the calendar-month request count reaches the value. Zero
	// (the default) disables the check. F-1226 (codex audit-
	// 2026-05-12): pre-fix the dashboard accepted this field and
	// `/v1/account/keys` POST persisted it, but no runtime
	// middleware enforced the cap — paid customers on metered
	// plans could keep spending indefinitely. Populated by the
	// Postgres-backed validator (Postgres is the only store that
	// has this column at time of writing).
	MonthlyQuota int64

	// EmailVerifiedAt is the timestamp the customer confirmed
	// ownership of the email they signed up with by clicking the
	// link emailed by the F-1218 wave 44 producer step. Zero =
	// never verified. The optional `middleware.RequireEmailVerified`
	// (F-1218 wave 45) gates /v1/* access on this field when the
	// operator opts in via config — unverified API-key Subjects
	// 403 with a clear message pointing at the verify endpoint.
	// Without the middleware, the field is informational only.
	EmailVerifiedAt time.Time
}

// SubjectPermissionEntry mirrors platform.KeyPermissionEntry so
// the auth layer can carry the policy without importing the
// platform package. Either Endpoint (exact `<METHOD> <PATH>`) or
// EndpointPrefix (`<PATH-PREFIX>`) — never both. F-1226 (codex
// audit-2026-05-12).
//
// JSON tags are present so this type can round-trip through the
// `APIKeyRecord` cache entry the PostgresAPIKeyValidator writes
// into Redis. Without them the cache-hit Subject would have
// empty permission fields, silently bypassing KeyPolicy.
type SubjectPermissionEntry struct {
	Endpoint       string `json:"endpoint,omitempty"`
	EndpointPrefix string `json:"endpoint_prefix,omitempty"`
}

// Anonymous returns the subject the middleware attaches when no
// credential is presented (or auth_mode=none). The identifier is
// caller-supplied so the rate-limit middleware can key on it
// (typically RemoteIP + UA hash).
func Anonymous(identifier string) Subject {
	return Subject{Identifier: identifier, Tier: TierAnonymous}
}

// ─── Context helpers ──────────────────────────────────────────────

// subjectKeyType is unexported so callers can't accidentally read
// or write a Subject under the wrong key type.
type subjectKeyType struct{}

var subjectKey subjectKeyType

// WithSubject returns a new context carrying the Subject. The auth
// middleware calls this on every authenticated request; downstream
// handlers + middleware (rate-limit, request logger) read it back
// via [SubjectFrom].
func WithSubject(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, subjectKey, s)
}

// SubjectFrom extracts the Subject from ctx. The boolean is `true`
// when a subject was attached (always the case after the auth
// middleware runs). Tests that bypass middleware should expect
// `false` and treat that as anonymous-with-empty-id.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	v := ctx.Value(subjectKey)
	if v == nil {
		return Subject{}, false
	}
	s, ok := v.(Subject)
	return s, ok
}
