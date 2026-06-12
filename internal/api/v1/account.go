package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// AccountStore is the v1 boundary against [auth.APIKeyStore].
// Two consumers today: [Server.handleAccountKeysCreate] (POST)
// and [Server.handleAccountKeysList] (GET). Production wiring is
// [auth.RedisAPIKeyStore] which provides both methods.
type AccountStore interface {
	Create(ctx context.Context, req auth.CreateAPIKeyRequest) (auth.APIKeyRecord, string, error)
	ListKeysForIdentifier(ctx context.Context, identifier string) ([]auth.APIKeyRecord, error)
	RevokeKeyByID(ctx context.Context, identifier, keyID string) error
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
	KeyID           string       `json:"key_id,omitempty"`
	Label           string       `json:"label,omitempty"`
	KeyPrefix       string       `json:"key_prefix,omitempty"`
	Tier            string       `json:"tier,omitempty"`
	RateLimitPerMin int          `json:"rate_limit_per_min,omitempty"`
	CreatedAt       time.Time    `json:"created_at,omitempty"`
	User            *AccountUser `json:"user,omitempty"`
	AccountInfo     *AccountInfo `json:"account,omitempty"`
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

// UsageRow is the wire shape for /v1/account/usage entries. Backed
// by the per-day Redis counters the UsageTracker middleware writes.
// Errors / throttled stay zero today — they're reserved fields for
// a follow-up that wires the rate-limit fail-open + non-2xx
// response counters into this shape.
type UsageRow struct {
	Date      string `json:"date"` // YYYY-MM-DD
	Requests  int    `json:"requests"`
	Errors    int    `json:"errors"`
	Throttled int    `json:"throttled"`
}

// UsageReader is the storage seam for /v1/account/usage. The
// internal/usage package's *Counter implements via its Read method;
// main.go's adapter bridges so this package stays free of the
// usage package import.
type UsageReader interface {
	Read(ctx context.Context, subject string, days int) ([]UsageDay, error)
}

// UsageDay mirrors usage.Day on the v1 boundary. Date is
// YYYY-MM-DD UTC; Requests is the daily INCR count.
type UsageDay struct {
	Date     string
	Requests int64
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
			"https://api.ratesengine.net/errors/unauthorized",
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
// Returns per-day request counts for the authenticated caller over
// the trailing 30 days. The usage middleware (`middleware.UsageTracker`)
// increments per-day counters in Redis on every successful request;
// this handler reads them back via the [UsageReader] seam.
//
// F-1259 (codex audit-2026-05-12): the prior "placeholder" doc-block
// described the pre-launch stub shape; the live runtime now wires
// `UsageReader: usageReaderOrNil(usageCounter)` whenever Redis is
// reachable, and the handler short-circuits to the empty-list wire
// shape only when the reader is nil (Redis-less deployment) or the
// subject derivation can't pick a stable counter key.
//
// Subject keying matches `middleware.UsageTracker`'s
// `usageKeyForSubject` so the writer + reader stay in lock-step
// (`key:<KeyID>` for API-key callers; `id:<Identifier>` when KeyID
// is empty). Anonymous callers receive 401. The `?from=` / `?to=`
// query params are reserved in the OpenAPI spec but ignored — every
// successful response is the trailing 30-day window today; full
// from/to honouring lands when an operator surface needs it.
//
// Redis-absent posture: the handler returns `[]` in the wire-shape
// envelope (200 OK with an empty data array). Callers that
// distinguish "no usage reported" from "usage backend not wired"
// can probe `/v1/readyz` (NOT `/healthz` — the per-dependency
// `checks` field is `/readyz`-only).
func (s *Server) handleAccountUsage(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorized",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/usage requires an API key or SEP-10 token")
		return
	}
	if s.usageReader == nil {
		// Reader not wired (older deployment / test) — preserve
		// the locked wire shape with an empty list rather than
		// 503; clients integrate against the contract regardless.
		writeJSON(w, []UsageRow{}, Flags{})
		return
	}
	// Mirror UsageTracker's subject derivation (key:<KeyID> or
	// id:<Identifier>). MUST stay in sync — if the writer and
	// reader pick different keys, /v1/account/usage returns []
	// despite incoming requests being recorded.
	key := ""
	switch {
	case subject.KeyID != "":
		key = "key:" + subject.KeyID
	case subject.Identifier != "":
		key = "id:" + subject.Identifier
	}
	if key == "" {
		writeJSON(w, []UsageRow{}, Flags{})
		return
	}
	// Default 30-day window; cap to the reader's retention. The
	// `?from=` / `?to=` params are reserved in OpenAPI but
	// unused for now — clients always get the trailing window.
	days, err := s.usageReader.Read(r.Context(), key, 30)
	if err != nil {
		s.logger.Warn("usage read", "err", err, "subject", key)
		writeJSON(w, []UsageRow{}, Flags{})
		return
	}
	out := make([]UsageRow, len(days))
	for i, d := range days {
		out[i] = UsageRow{
			Date:     d.Date,
			Requests: int(d.Requests),
		}
	}
	writeJSON(w, out, Flags{})
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
			"https://api.ratesengine.net/errors/unauthorized",
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
			"https://api.ratesengine.net/errors/unauthorized",
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

// handleAccountKeysRevoke serves DELETE /v1/account/keys/{keyID}.
//
// Revokes the API key whose KeyID matches the path parameter,
// scoped to the authenticated caller's Identifier. Anonymous → 401;
// missing keyID → 400; store unwired → 503; everything else → 204
// (including "key not found" — we don't leak whether a keyID
// exists for a different account).
//
// Caller cannot revoke the key they're authenticated with — that
// would orphan the connection mid-request. We return 409 in that
// case so the UI can prompt for an alternate key + retry.
func (s *Server) handleAccountKeysRevoke(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorized",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/keys requires an API key or SEP-10 token")
		return
	}
	keyID := r.PathValue("keyID")
	if keyID == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-key-id",
			"Missing key id", http.StatusBadRequest,
			"path must be /v1/account/keys/{keyID}")
		return
	}
	if subject.KeyID == keyID {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/cannot-revoke-self",
			"Can't revoke the key you're using", http.StatusConflict,
			"authenticate with a different key (or SEP-10 token) and retry")
		return
	}
	if s.accounts == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-store-unavailable",
			"Account store not configured", http.StatusServiceUnavailable,
			"this deployment has no AccountStore wired — typically because Redis is unavailable")
		return
	}
	if err := s.accounts.RevokeKeyByID(r.Context(), subject.Identifier, keyID); err != nil {
		s.logger.Error("account keys revoke failed", "err", err,
			"identifier", subject.Identifier, "key_id", keyID)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-revoke-failed",
			"Could not revoke key", http.StatusInternalServerError,
			"see X-Request-ID in server logs")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
