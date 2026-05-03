package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/auth"
)

// AccountStore is the v1 boundary against [auth.APIKeyStore]. Its
// only consumer is [Server.handleAccountKeysCreate]; the interface
// stays narrow so the handler test can substitute a fake without
// pulling in miniredis.
type AccountStore interface {
	Create(ctx context.Context, req auth.CreateAPIKeyRequest) (auth.APIKeyRecord, string, error)
}

// Account is the wire shape for /v1/account/me responses. Mirrors
// the OpenAPI Account schema; the field set is the public-safe
// projection of [auth.APIKeyRecord] (no expires_at / scopes
// surfaced — those are implementation detail until /v1/account/keys
// list returns them).
type Account struct {
	KeyID           string    `json:"key_id"`
	Label           string    `json:"label,omitempty"`
	Tier            string    `json:"tier"`
	RateLimitPerMin int       `json:"rate_limit_per_min,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
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
// Returns the authenticated caller's account info. Anonymous
// callers receive 401 — /me is meaningless without a credential.
func (s *Server) handleAccountMe(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.SubjectFrom(r.Context())
	if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorised",
			"Authentication required", http.StatusUnauthorized,
			"/v1/account/me requires an API key or SEP-10 token")
		return
	}

	out := Account{
		KeyID:           subject.KeyID,
		Label:           subject.Label,
		Tier:            string(subject.Tier),
		RateLimitPerMin: subject.RateLimitPerMin,
		CreatedAt:       subject.CreatedAt,
	}
	writeJSON(w, out, Flags{})
}

// handleAccountUsage serves GET /v1/account/usage.
//
// Placeholder per-day usage rollups for the authenticated caller.
// The counter store doesn't exist yet, so the handler returns an
// empty list behind the same envelope shape. When the counters
// land, the only change is the implementation; the wire shape is
// already locked.
//
// Anonymous callers receive 401. Date-range parameters are reserved
// for the future implementation and are ignored at this point.
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
			Label:     rec.Label,
		},
		AsOf:  rec.CreatedAt,
		Flags: Flags{},
	})
}
