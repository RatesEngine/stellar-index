// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package v1

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// SignupVerifier is the v1 boundary for the email-ownership-
// proof flow added in F-1218 (codex audit-2026-05-12). Mirrors
// `auth.SignupVerifier` but kept in the v1 package so callers
// don't have to drag the auth package onto v1's public surface.
//
// Production wiring: `auth.RedisSignupVerifier` (SETNX +
// GETDEL on `signup:verify:<sha256(token)>`).
type SignupVerifier interface {
	Reserve(ctx context.Context, token, keyID string, ttl time.Duration) error
	Consume(ctx context.Context, token string) (string, error)
}

// SignupVerifyEmailer is the v1 boundary for sending the
// verification email on POST /v1/signup. Production wiring is
// a thin adapter around `notify.Sender` (Resend in production,
// NoopSender in dev). Kept narrow so the v1 package doesn't
// drag the full notify surface onto its public API.
//
// `verifyURL` is the absolute click-through URL the customer
// sees in the email — `https://api.example.com/v1/signup/verify
// ?token=<plaintext>`. The handler builds it from the request's
// scheme + Host so deployments don't have to plumb a separate
// base URL config; nil-safe in the handler so a Sender-less
// deployment skips the send and returns the response with
// `email_sent: false`.
type SignupVerifyEmailer interface {
	SendSignupVerification(ctx context.Context, toEmail, verifyURL string) error
}

// APIKeyEmailVerifier is the v1 boundary for flipping a Redis-
// stored API key's `EmailVerifiedAt` timestamp. Production
// wiring is `auth.RedisAPIKeyStore.MarkEmailVerified`. F-1218
// wave 45 (codex audit-2026-05-12): the `/v1/signup/verify`
// handler calls this after Consume so the optional
// `middleware.RequireEmailVerified` gate can rely on the flag.
// Nil-safe — when no marker is wired (Redis-less / SAC-style
// deployment) the verify handler still succeeds, the wire
// shape stays the same, but downstream gates can't honour the
// signal.
type APIKeyEmailVerifier interface {
	MarkEmailVerified(ctx context.Context, keyID string, at time.Time) error
}

// SignupVerifyResult is the wire shape for `GET /v1/signup/
// verify?token=…` responses. The key_id surfaces so the
// dashboard / CLI can correlate the verified key with the
// account's other metadata; no plaintext is returned (the
// original signup response carried that exactly once).
type SignupVerifyResult struct {
	Verified bool   `json:"verified"`
	KeyID    string `json:"key_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// handleSignupVerify serves `GET /v1/signup/verify?token=…`.
//
// F-1218 (codex audit-2026-05-12): proves the customer owns
// the email address they signed up with by consuming the
// token the signup handler emailed them. Single-use semantics
// via Redis GETDEL — the second click on the link returns
// 404, the same shape as a forged token.
//
// Surfaces:
//
//   - 200 + `{"verified":true,"key_id":"…"}` on success
//   - 404 + Problem-JSON on unknown / consumed / expired token
//   - 503 + Problem-JSON when no SignupVerifier is configured
//     (Redis-less deployment); customers see a clear "this
//     deployment doesn't run the verification flow" instead of
//     the silent-no-op surprise
//
// In subsequent waves the success path also flips the API key
// row's `EmailVerified` flag and (when the operator opts in
// via config) gates the validator on that flag. This wave
// just lands the consumer; the gate ships separately.
func (s *Server) handleSignupVerify(w http.ResponseWriter, r *http.Request) {
	if s.signupVerifier == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/signup-verify-unavailable",
			"Signup verification not configured", http.StatusServiceUnavailable,
			"this deployment doesn't run the email-ownership-proof flow; the original signup response is the only proof of issuance")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-token",
			"Missing token", http.StatusBadRequest,
			"the verify endpoint requires a `token` query parameter (the value emailed to you on signup)")
		return
	}
	keyID, err := s.signupVerifier.Consume(r.Context(), token)
	if err != nil {
		if errors.Is(err, auth.ErrSignupVerifyNotFound) {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/signup-verify-not-found",
				"Verification token not found", http.StatusNotFound,
				"the token is unknown, has already been consumed, or has expired; sign up again to receive a fresh link")
			return
		}
		s.logger.Error("signup verify: store error", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError,
			"verification failed; try again in a moment")
		return
	}
	// F-1218 wave 45 (codex audit-2026-05-12): flip the
	// `EmailVerifiedAt` timestamp on the key record so the
	// optional `RequireEmailVerified` middleware can let this
	// caller through on subsequent requests. Best-effort —
	// a marker failure logs at warn but doesn't fail the
	// verify response (the token has already been consumed;
	// surfacing 500 here would leave the customer stuck).
	if s.apiKeyEmailVerifier != nil {
		if err := s.apiKeyEmailVerifier.MarkEmailVerified(r.Context(), keyID, time.Time{}); err != nil {
			s.logger.Warn("signup verify: MarkEmailVerified failed; gate stays unsatisfied for this key",
				"err", err, "key_id", keyID)
		}
	}
	writeJSON(w, SignupVerifyResult{
		Verified: true,
		KeyID:    keyID,
		Detail:   "email ownership confirmed; the API key minted at signup is now flagged as verified",
	}, Flags{})
}
