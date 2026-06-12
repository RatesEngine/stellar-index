package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// SignupTracker is the v1 boundary for "has this email already
// claimed an account?" The implementation persists the email-hash
// → key-id mapping in Redis so a duplicate signup for the same
// email returns a 409 instead of minting a second key.
//
// Wired by the api binary when Redis is reachable; nil disables
// duplicate detection entirely (signup still works, just isn't
// idempotent on the email — operator-side cleanup if abused).
type SignupTracker interface {
	// LookupByEmailHash returns the key_id of the account previously
	// signed up with this email-hash, or "" if none exists.
	LookupByEmailHash(ctx context.Context, emailHash string) (string, error)

	// ReserveEmail atomically claims the email-hash for an in-flight
	// signup. Returns [auth.ErrSignupEmailReserved] if the email is
	// already reserved (pending) or has a confirmed mapping. On
	// success the caller proceeds to mint the key and then calls
	// MarkSignup to upgrade the reservation. F-1218 (codex
	// audit-2026-05-12).
	ReserveEmail(ctx context.Context, emailHash string) error

	// MarkSignup persists the email-hash → key-id mapping so a
	// future LookupByEmailHash returns it. Called only AFTER
	// Account.Create succeeds. Idempotent over a prior
	// ReserveEmail: the reservation TTL is cleared and the key_id
	// becomes the durable value.
	MarkSignup(ctx context.Context, emailHash, keyID string) error
}

// SignupIPThrottle is the v1 boundary for the per-IP signup
// rate-limit. Production wires a Redis-backed token bucket with
// a tight cap (default 5/hour); nil disables the check entirely
// (legacy behaviour, relies only on the global rate-limit
// middleware).
//
// Designed as a separate seam from the global rate limit so a
// future deployment can swap in a stricter / different policy
// (e.g. CAPTCHA, proof-of-work, federated denylist) without
// touching the global path.
//
// F-1232 (audit-2026-05-12): the global anonymous bucket allows
// 60/min per IP — plenty for browsing the public surfaces but
// 60 signups/min/IP is also 3,600 keys/hour per IP, well above
// any legitimate signup rate. Tightening here closes the
// bulk-mint vector without affecting other anonymous traffic.
type SignupIPThrottle interface {
	// CheckIP returns nil when the IP is below its signup quota,
	// or [auth.ErrSignupRateLimited] when the quota is exhausted.
	// Other errors (typically Redis unavailable) propagate so the
	// handler can fall open with a 503 — better than silently
	// disabling the throttle.
	CheckIP(ctx context.Context, ip string) error
}

// signupRequest is the inbound POST /v1/signup body.
type signupRequest struct {
	Email string `json:"email"`
	Label string `json:"label,omitempty"`
}

// SignupResult is the wire shape for /v1/signup replies. The
// plaintext key appears here exactly once — clients that drop the
// response can never recover it. The identifier surfaces so the
// caller can correlate this account with future /v1/account/me
// responses and (eventually) Stripe-paid upgrades.
type SignupResult struct {
	Plaintext       string `json:"plaintext"`
	KeyID           string `json:"key_id"`
	KeyPrefix       string `json:"key_prefix,omitempty"`
	Identifier      string `json:"identifier"`
	Label           string `json:"label,omitempty"`
	Tier            string `json:"tier"`
	RateLimitPerMin int    `json:"rate_limit_per_min"`

	// EmailVerificationSent is true when the deployment is wired
	// with both a SignupVerifier and a SignupVerifyEmailer (F-1218
	// wave 44). Customers see this field as the cue to expect the
	// verification email; legacy / Redis-less / Sender-less
	// deployments report `false` and the wire shape is
	// backwards-compatible for clients that ignore the field.
	EmailVerificationSent bool `json:"email_verification_sent"`
}

// signupBodyMaxBytes caps the request body size at 4 KiB. Email +
// label + JSON wrapper fits in 256 bytes comfortably; the cap is
// purely abuse-prevention.
const signupBodyMaxBytes = 4 * 1024

// signupDefaultRateLimitPerMin — the Starter-tier budget. Matches
// `[api].key_rate_limit_per_min` default in the config schema and
// the RFP's "≥ 1000 requests per minute per client" commitment.
// Operator can override via Stripe-paid upgrades that mutate the
// per-key RateLimitPerMin.
const signupDefaultRateLimitPerMin = 1000

// handleSignup serves POST /v1/signup.
//
// Public, anonymous-tier endpoint: a customer hits this once with
// their email + an optional label, gets back a freshly-minted API
// key, and uses that key on subsequent requests for the higher
// per-key rate-limit budget. Self-service alternative to the
// operator-side `ratesengine-ops mint-key` flow (which exists for
// bootstrap — see cmd/ratesengine-ops/mint_key.go).
//
// The endpoint defends against three kinds of abuse:
//   - **Per-IP volume**: the standard rate-limit middleware (anon
//     tier, 60/min) caps signup attempts per IP at 60/min. Past
//     60 the caller gets a 429 from the middleware before this
//     handler runs.
//   - **Per-email volume**: emailHash is checked against the
//     SignupTracker; a second signup for the same email returns
//     409 with the existing key_id surfaced. (Tracker is nil-safe
//     — without it the check is skipped, which is acceptable for
//     deployments that don't have Redis up.)
//   - **Garbage emails**: net/mail.ParseAddress + heuristic
//     strip-and-lower normalisation. Bounces are not detected
//     (we don't send confirmation email here); a follow-up Stripe-
//     gated upgrade flow will require email verification.
//
// Stores nil → 503 (no AccountStore wired); same shape as
// /v1/account/keys per Server.handleAccountKeysCreate.
//
// Authenticated callers (anyone whose Subject.Tier != anonymous)
// are routed to /v1/account/keys instead — they should rotate keys
// through that endpoint, not sign up again.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	req, ok := s.parseAndValidateSignup(w, r)
	if !ok {
		return
	}

	if !s.signupIPThrottleOK(w, r) {
		return
	}

	// Email-hash → identifier. SHA-256 of the lowercased address;
	// truncate hex to 16 chars (= 64 bits, ample collision
	// resistance for the population size).
	sum := sha256.Sum256([]byte(req.Email))
	emailHash := hex.EncodeToString(sum[:])
	identifier := "signup-" + emailHash[:16]

	// 7. Atomic email reservation (best-effort if no tracker wired).
	// F-1218 (codex audit-2026-05-12): the prior shape was
	// `LookupByEmailHash → mint → MarkSignup`, which let two
	// parallel signups for the same email both pass the lookup
	// and each mint a key. ReserveEmail uses SETNX with a
	// "pending" placeholder so only the first caller proceeds;
	// the second sees auth.ErrSignupEmailReserved before any
	// mint side-effect. The reservation has a 5-minute TTL so a
	// crashed handler doesn't strand the email.
	if s.signups != nil {
		err := s.signups.ReserveEmail(r.Context(), emailHash)
		if errors.Is(err, auth.ErrSignupEmailReserved) {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/already-signed-up",
				"Already signed up", http.StatusConflict,
				"this email already has an account; use POST /v1/account/keys with that account's key to mint additional keys, or contact support to recover access")
			return
		}
		if err != nil {
			s.logger.Error("signup tracker reserve failed",
				"err", err, "identifier", identifier)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/internal",
				"Internal error", http.StatusInternalServerError,
				"signup reservation failed; try again in a moment")
			return
		}
	}

	// 8. Mint the key.
	rec, plaintext, err := s.accounts.Create(r.Context(), auth.CreateAPIKeyRequest{
		Identifier:      identifier,
		Label:           req.Label,
		Tier:            auth.TierAPIKey,
		RateLimitPerMin: signupDefaultRateLimitPerMin,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.logger.Error("signup mint failed", "err", err, "identifier", identifier)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError,
			"signup failed; try again in a moment")
		return
	}

	// 9. Persist the email-hash → key-id mapping.
	//    Failure here is best-effort — the key is already minted;
	//    the customer can still use it. Logged so operators see the
	//    duplicate-detection drift and can reconcile out-of-band.
	if s.signups != nil {
		if err := s.signups.MarkSignup(r.Context(), emailHash, rec.KeyID); err != nil {
			s.logger.Warn("signup tracker mark failed (key minted but duplicate-detection disabled for this email)",
				"err", err, "key_id", rec.KeyID, "identifier", identifier)
		}
	}

	// 10. F-1218 wave 44 (codex audit-2026-05-12): issue an
	//     email-ownership-proof token and send the verification
	//     email. Best-effort: the key is already minted and the
	//     plaintext is about to be returned to the customer; a
	//     verifier or sender failure here logs at warn and drops
	//     `email_verification_sent: false` on the wire. The full
	//     close (validator gate that rejects unverified keys)
	//     ships in wave 45 behind a config flag.
	emailSent := s.issueSignupVerification(r, rec.KeyID, req.Email)

	// 11. Reply with plaintext (shown ONCE) + audit record.
	writeJSON(w, SignupResult{
		Plaintext:             plaintext,
		KeyID:                 rec.KeyID,
		KeyPrefix:             rec.KeyPrefix,
		Identifier:            rec.Identifier,
		Label:                 rec.Label,
		Tier:                  string(rec.Tier),
		RateLimitPerMin:       rec.RateLimitPerMin,
		EmailVerificationSent: emailSent,
	}, Flags{})
}

// issueSignupVerification reserves a fresh token against the
// just-minted keyID and emails the click-through verify link
// to the signup-supplied address. Returns true when both legs
// succeed. Either leg failing — verifier nil, emailer nil,
// token-gen err, Reserve err, Send err — returns false; the
// signup-handler caller treats `false` as the cue to set
// `email_verification_sent: false` on the wire (the customer
// can still use the key today; a future validator-gate wave
// will start enforcing).
//
// All failure paths log at warn so operators see drift; none
// short-circuit the customer's signup response (the audit's
// remediation is to MAKE the proof available, not to take
// signup down when the email infra is unhealthy).
func (s *Server) issueSignupVerification(r *http.Request, keyID, toEmail string) bool {
	if s.signupVerifier == nil || s.signupVerifyEmailer == nil {
		return false
	}
	token, err := auth.NewSignupVerifyToken()
	if err != nil {
		s.logger.Warn("signup verification: token generation failed",
			"err", err, "key_id", keyID)
		return false
	}
	if err := s.signupVerifier.Reserve(r.Context(), token, keyID, auth.DefaultSignupVerifyTTL); err != nil {
		s.logger.Warn("signup verification: token reservation failed",
			"err", err, "key_id", keyID)
		return false
	}
	verifyURL := buildSignupVerifyURL(r, token)
	if err := s.signupVerifyEmailer.SendSignupVerification(r.Context(), toEmail, verifyURL); err != nil {
		s.logger.Warn("signup verification: send failed",
			"err", err, "key_id", keyID, "to", toEmail)
		return false
	}
	return true
}

// buildSignupVerifyURL constructs the absolute click-through
// URL the customer sees in the verification email. Built from
// the request's scheme + Host so deployments don't have to
// plumb a separate base URL config — the same scheme the
// customer used for /v1/signup will work for the verify GET.
//
// Defence-in-depth: scheme defaults to `https` when TLS isn't
// terminated by Caddy upstream (in which case `r.TLS` is nil
// even though the public traffic is TLS). Production deployments
// always front through Caddy so this is the typical case;
// `http://` URLs only surface in `localhost` dev.
func buildSignupVerifyURL(r *http.Request, plaintextToken string) string {
	scheme := "https"
	if r.TLS == nil && (r.Host == "localhost" || strings.HasPrefix(r.Host, "127.0.0.1") || strings.HasPrefix(r.Host, "localhost:")) {
		scheme = "http"
	}
	// X-Forwarded-Proto from Caddy / Cloudflare wins when present
	// — that's the source of truth for the original-request scheme.
	if xfp := r.Header.Get("X-Forwarded-Proto"); xfp == "http" || xfp == "https" {
		scheme = xfp
	}
	return scheme + "://" + r.Host + "/v1/signup/verify?token=" + plaintextToken
}

// signupIPThrottleOK runs the F-1232 per-IP signup throttle check.
// Returns true when the request should proceed, false when the
// handler has already written the response (429 on quota
// exhaustion). Falls open on Redis errors so a transient backend
// blip doesn't take signup offline; the global rate-limit
// middleware still applies as a safety net.
//
// Extracted from handleSignup to keep that function under the
// gocognit threshold.
func (s *Server) signupIPThrottleOK(w http.ResponseWriter, r *http.Request) bool {
	if s.signupIPThrottle == nil {
		return true
	}
	ip := middleware.RemoteIP(r)
	if ip == "" {
		return true
	}
	err := s.signupIPThrottle.CheckIP(r.Context(), ip)
	if err == nil {
		return true
	}
	if errors.Is(err, auth.ErrSignupRateLimited) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/signup-rate-limited",
			"Signup rate limit exceeded", http.StatusTooManyRequests,
			"too many signups from this IP recently; wait an hour and try again, or contact support if you're a legitimate operator bulk-onboarding a team")
		return false
	}
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		// Sustained Redis outage: fail-CLOSED rather than disabling
		// abuse-prevention indefinitely. Retry-After MUST be set
		// BEFORE writeProblem because writeProblem calls
		// w.WriteHeader(status) internally — headers added after
		// that point are silently dropped by net/http. The 30s
		// hint matches [auth.DefaultSignupThrottleDwellTime];
		// clients that obey Retry-After will naturally space
		// retries far enough apart to ride out a typical Redis
		// fail-over. F-0049 / F-0149 (audit-2026-05-27).
		w.Header().Set("Retry-After", "30")
		s.logger.Warn("signup IP throttle unavailable; failing closed (sustained Redis errors)",
			"err", err, "ip", ip)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/throttle-unavailable",
			"Throttle layer unavailable", http.StatusServiceUnavailable,
			"the abuse-prevention layer has been unreachable for an extended period; retry in a moment")
		return false
	}
	s.logger.Warn("signup IP throttle check failed; falling open",
		"err", err, "ip", ip)
	// Fall open — Redis blip shouldn't take signup offline, and the
	// global rate limit still applies.
	return true
}

// parseAndValidateSignup runs the auth-required + body-shape +
// email-shape + label-length checks. Returns the (normalised)
// request and ok=true on success. On failure the response has
// already been written and ok=false signals the caller to bail.
//
// Extracted so handleSignup's cognitive complexity stays under
// the gocognit threshold; this is straight-line validation that
// gocognit doesn't reward grouping.
func (s *Server) parseAndValidateSignup(w http.ResponseWriter, r *http.Request) (signupRequest, bool) {
	if subject, ok := auth.SubjectFrom(r.Context()); ok &&
		subject.Tier != auth.TierAnonymous && subject.Tier != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/already-authenticated",
			"Already authenticated", http.StatusBadRequest,
			"this endpoint is for first-time signups; authenticated callers should use POST /v1/account/keys")
		return signupRequest{}, false
	}

	if s.accounts == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/account-store-unavailable",
			"Account store not configured", http.StatusServiceUnavailable,
			"this deployment has no AccountStore wired — typically because Redis is unavailable")
		return signupRequest{}, false
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, signupBodyMaxBytes))
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/body-too-large",
			"Request body too large", http.StatusBadRequest,
			"/v1/signup body must be under 4 KiB")
		return signupRequest{}, false
	}
	if len(body) == 0 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-body",
			"Missing request body", http.StatusBadRequest,
			"/v1/signup requires a JSON body containing an email")
		return signupRequest{}, false
	}
	var req signupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-body",
			"Malformed JSON body", http.StatusBadRequest,
			"could not parse request body as JSON")
		return signupRequest{}, false
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-email",
			"Email is required", http.StatusBadRequest,
			"the signup body must include an 'email' field")
		return signupRequest{}, false
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-email",
			"Invalid email", http.StatusBadRequest,
			"the email field could not be parsed as a valid address")
		return signupRequest{}, false
	}

	if len(req.Label) > 128 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/label-too-long",
			"Label too long", http.StatusBadRequest,
			"label must be 128 characters or fewer")
		return signupRequest{}, false
	}
	if req.Label == "" {
		req.Label = "self-service signup"
	}

	return req, true
}
