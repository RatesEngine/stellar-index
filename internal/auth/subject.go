package auth

import (
	"context"
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
