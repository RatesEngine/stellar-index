package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/RatesEngine/rates-engine/internal/auth"
)

// AccountStore is the v1 boundary against [auth.APIKeyStore].
// Two consumers today: [Server.handleAccountKeysCreate] (POST)
// and [Server.handleAccountKeysList] (GET). Production wiring is
// [auth.RedisAPIKeyStore] which provides both methods.
type AccountStore interface {
	Create(ctx context.Context, req auth.CreateAPIKeyRequest) (auth.APIKeyRecord, string, error)
	ListKeysForIdentifier(ctx context.Context, identifier string) ([]auth.APIKeyRecord, error)
}

// Account is the wire shape for /v1/account/me responses. Mirrors
// the OpenAPI Account schema; the field set is the public-safe
// projection of [auth.APIKeyRecord] (no expires_at / scopes
// surfaced — those are implementation detail until /v1/account/keys
// list returns them).
//
// The shape is a union: API-key callers populate the top-level
// key_* / tier / rate_limit_per_min / created_at fields and leave
// `user` + `account` null. Magic-link session callers populate the
// nested `user` + `account` objects (and leave the API-key fields
// empty). Clients can detect which mode by checking which slice is
// populated. Both shapes coexist forever — bumping a major version
// for an additive field would be silly.
type Account struct {
	KeyID           string         `json:"key_id,omitempty"`
	Label           string         `json:"label,omitempty"`
	KeyPrefix       string         `json:"key_prefix,omitempty"`
	Tier            string         `json:"tier,omitempty"`
	RateLimitPerMin int            `json:"rate_limit_per_min,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	User            *AccountUser   `json:"user,omitempty"`
	AccountInfo     *AccountInfo   `json:"account,omitempty"`
}

// AccountUser is the magic-link-session caller's user info.
type AccountUser struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	DisplayName     string    `json:"display_name,omitempty"`
	Role            string    `json:"role,omitempty"`
	IsStaff         bool      `json:"is_staff"`
	EmailVerifiedAt time.Time `json:"email_verified_at,omitempty"`
	LastLoginAt     time.Time `json:"last_login_at,omitempty"`
}

// AccountInfo is the magic-link-session caller's parent account.
type AccountInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Slug   string `json:"slug,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Status string `json:"status,omitempty"`
}

// SessionInfo is the wire-shape projection of a magic-link
// session. Defined in v1 so this package doesn't import
// dashboardauth directly; the binary's wiring (main.go) converts
// dashboardauth's SessionContext into this shape.
type SessionInfo struct {
	UserID          string
	Email           string
	DisplayName     string
	Role            string
	IsStaff         bool
	EmailVerifiedAt time.Time
	LastLoginAt     time.Time

	AccountID     string
	AccountName   string
	AccountSlug   string
	AccountTier   string
	AccountStatus string
}

// SessionPeeker reads the magic-link session bound to the request
// context. Implementations come from the dashboardauth bundle via
// main.go's wiring; v1 holds the interface so the dependency
// flows the right way.
type SessionPeeker interface {
	SessionFromContext(ctx context.Context) (SessionInfo, bool)
}

// UsageRow is the wire shape for /v1/account/usage entries. The
// rate-limit middleware records per-key request counts in Redis,
// but nothing rolls them up into the daily UsageRow shape yet —
// the handler currently returns an empty list behind the locked
// wire shape so a future rollup writer (separate PR) can fill in
// the data without a wire-format change.
type UsageRow struct {
	Date      string `json:"date"` // YYYY-MM-DD
	Requests  int    `json:"requests"`
	Errors    int    `json:"errors"`
	Throttled int    `json:"throttled"`
}

// KeyCreated is the wire shape for /v1/account/keys (POST) replies.
// The plaintext appears here exactly once — clients that drop the
// response can never recover it.
type KeyCreated struct {
	KeyID     string `json:"key_id"`
	Plaintext string `json:"plaintext"`
	KeyPrefix string `json:"key_prefix,omitempty"`
	Label     string `json:"label,omitempty"`
}

// createKeyRequest is the inbound POST body. The server adopts the
// caller's Identifier (so callers can only mint keys that share
// their owner reference) and ignores Tier — the new key inherits
// the caller's tier verbatim. Operator callers can mint any tier
// via a separate admin endpoint that ships later.
type createKeyRequest struct {
	Label string `json:"label"`
}

// handleAccountMe serves GET /v1/account/me.
//
// Returns the authenticated caller's account info. Magic-link
// session callers populate the nested user/account objects;
// API-key callers populate the top-level key_* fields. Both
// flows can coexist on a request — session takes precedence
// because it identifies a real user, while a key only
// identifies a credential.
//
// Anonymous callers receive 401 — /me is meaningless without
// any credential.
func (s *Server) handleAccountMe(w http.ResponseWriter, r *http.Request) {
	// Magic-link session takes precedence when both are present.
	if s.sessionPeeker != nil {
		if sess, ok := s.sessionPeeker.SessionFromContext(r.Context()); ok {
			out := Account{
				User: &AccountUser{
					ID:              sess.UserID,
					Email:           sess.Email,
					DisplayName:     sess.DisplayName,
					Role:            sess.Role,
					IsStaff:         sess.IsStaff,
					EmailVerifiedAt: sess.EmailVerifiedAt,
					LastLoginAt:     sess.LastLoginAt,
				},
				AccountInfo: &AccountInfo{
					ID:     sess.AccountID,
					Name:   sess.AccountName,
					Slug:   sess.AccountSlug,
					Tier:   sess.AccountTier,
					Status: sess.AccountStatus,
				},
			}
			writeJSON(w, out, Flags{})
			return
		}
	}

	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorised",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/me requires a magic-link session, API key, or SEP-10 token")
		return
	}

	out := Account{
		KeyID:           subject.KeyID,
		Label:           subject.Label,
		KeyPrefix:       subject.KeyPrefix,
		Tier:            string(subject.Tier),
		RateLimitPerMin: subject.RateLimitPerMin,
		CreatedAt:       subject.CreatedAt,
	}
	writeJSON(w, out, Flags{})
}

// handleAccountUsage serves GET /v1/account/usage.
//
// **Placeholder for launch.** The endpoint always returns an empty
// `[]` for authenticated callers — the per-day usage rollup is not
// yet implemented. The rate-limit middleware records request counts
// in Redis, but nothing aggregates them into the daily UsageRow
// shape. The wire contract (envelope shape, UsageRow fields) is
// locked so SDK consumers can integrate against it, but the data is
// always empty until the rollup worker ships.
//
// Day-1 contract: callers SHOULD treat an empty array as "no usage
// reported," not as "no usage in the queried window." Operators
// reading their own usage today must inspect Redis counters
// directly.
//
// Anonymous callers receive 401. The `?from=` / `?to=` query params
// are reserved in the OpenAPI spec but ignored — every successful
// response is `[]` regardless of range.
func (s *Server) handleAccountUsage(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorised",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/usage requires an API key or SEP-10 token")
		return
	}
	// Empty list is correct today — the rate-limit middleware
	// records counts in Redis but nothing rolls them up into the
	// daily UsageRow shape yet.
	writeJSON(w, []UsageRow{}, Flags{})
}

// handleAccountKeysCreate serves POST /v1/account/keys.
//
// Issues a fresh API key for the authenticated caller. The new key
// inherits the caller's Identifier and Tier — a paid customer
// rotates their own credentials without escalating; an operator
// uses a separate admin path (not yet shipped) to mint keys for
// other identifiers.
//
// Anonymous → 401. Missing/empty body → 400. Store unavailable →
// 503 (the binary didn't wire one because Redis was missing).
func (s *Server) handleAccountKeysCreate(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorised",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/keys requires an API key or SEP-10 token")
		return
	}
	if s.accounts == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-store-unavailable",
			"Account store not configured", http.StatusServiceUnavailable,
			"this deployment has no AccountStore wired — typically because Redis is unavailable")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4*1024))
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/body-too-large",
			"Request body too large", http.StatusBadRequest,
			"/v1/account/keys body must be under 4 KiB")
		return
	}
	var req createKeyRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-body",
				"Malformed JSON body", http.StatusBadRequest,
				"could not parse request body as JSON")
			return
		}
	}
	if req.Label == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-label",
			"Label is required", http.StatusBadRequest,
			"the new key needs a label so the customer can identify it later")
		return
	}
	if len(req.Label) > 128 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/label-too-long",
			"Label too long", http.StatusBadRequest,
			"label must be 128 characters or fewer")
		return
	}

	rec, plaintext, err := s.accounts.Create(r.Context(), auth.CreateAPIKeyRequest{
		Identifier: subject.Identifier,
		Label:      req.Label,
		Tier:       subject.Tier,
		// Inherit the caller's per-key budget when set; otherwise
		// leave zero so the per-tier default applies.
		RateLimitPerMin: subject.RateLimitPerMin,
	})
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("account key create failed", "err", err, "identifier", subject.Identifier)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-create-failed",
			"Could not issue key", http.StatusInternalServerError,
			"see X-Request-ID in server logs")
		return
	}

	writeEnvelopeStatus(w, http.StatusCreated, Envelope{
		Data: KeyCreated{
			KeyID:     rec.KeyID,
			Plaintext: plaintext,
			KeyPrefix: rec.KeyPrefix,
			Label:     rec.Label,
		},
		AsOf:  rec.CreatedAt,
		Flags: Flags{},
	})
}

// handleAccountKeysList serves GET /v1/account/keys.
//
// Returns every API key whose Identifier matches the authenticated
// caller's. Mirrors the /v1/account/me wire shape but as a list —
// each entry is a public-safe APIKeyRecord projection (no plaintext
// — that's only retrievable at Create time, by design).
//
// Anonymous → 401. Store unavailable → 503. Authenticated callers
// always get a list (possibly empty if all their keys were
// previously revoked, though revocation isn't shipped today).
//
// Sorted by CreatedAt ascending so customers see their original
// signup key first and rotated keys later.
func (s *Server) handleAccountKeysList(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorised",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/keys requires an API key or SEP-10 token")
		return
	}
	if s.accounts == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-store-unavailable",
			"Account store not configured", http.StatusServiceUnavailable,
			"this deployment has no AccountStore wired — typically because Redis is unavailable")
		return
	}

	keys, err := s.accounts.ListKeysForIdentifier(r.Context(), subject.Identifier)
	if err != nil {
		s.logger.Error("account keys list failed", "err", err,
			"identifier", subject.Identifier)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-list-failed",
			"Could not list keys", http.StatusInternalServerError,
			"see X-Request-ID in server logs")
		return
	}

	// Sort by CreatedAt ascending — oldest first, so a customer sees
	// their original signup key before any rotations.
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].CreatedAt.Before(keys[j].CreatedAt)
	})

	out := make([]Account, 0, len(keys))
	for _, k := range keys {
		out = append(out, Account{
			KeyID:           k.KeyID,
			Label:           k.Label,
			KeyPrefix:       k.KeyPrefix,
			Tier:            string(k.Tier),
			RateLimitPerMin: k.RateLimitPerMin,
			CreatedAt:       k.CreatedAt,
		})
	}
	writeJSON(w, out, Flags{})
}
