// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/RatesEngine/rates-engine/internal/auth"
)

// RequireEmailVerified returns a [Middleware] that 403s API-key
// callers whose `EmailVerifiedAt` timestamp is still zero —
// i.e. the customer signed up via `POST /v1/signup` but hasn't
// clicked the verification link in the email yet.
//
// F-1218 wave 45 (codex audit-2026-05-12): closes the audit's
// concern that "a valid-looking email string yields a usable
// plaintext Starter key with no ownership proof". With this
// middleware wired, the key remains 403'd until the customer
// proves they own the email by clicking the link, which flips
// the flag via `/v1/signup/verify`.
//
// Behaviour:
//
//   - Anonymous Subjects pass through (no key to gate; the
//     signup endpoint itself is anonymous-only, so anonymous
//     callers can't have an unverified key by definition).
//   - Subjects whose `Identifier` does NOT start with `signup-`
//     pass through unconditionally. Legacy operator-minted
//     keys (`customer-acme-corp`, etc.) and dashboard-minted
//     keys (`acct:<slug>`) didn't go through the email-link
//     flow at all and are scoped out so the gate doesn't break
//     them. Only `/v1/signup`-issued keys hit the check.
//   - Subjects with `EmailVerifiedAt.IsZero() == true` AND a
//     `signup-` identifier prefix get 403 + Problem-JSON with
//     a clear pointer at the verify endpoint.
//
// Opt-in per deployment: the api binary mounts this middleware
// only when `cfg.API.SignupRequireEmailVerification` is true
// (the config flag defaults to false so existing customer keys
// keep working through the rollout window).
//
// Wire AFTER `Auth` (so SubjectFrom returns) and AFTER
// `KeyPolicy` (which has its own per-key gates) but BEFORE
// `RateLimit` so a verification-failed request doesn't also
// spend a rate-limit token.
func RequireEmailVerified() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := auth.SubjectFrom(r.Context())
			if !ok || subject.Tier == auth.TierAnonymous || subject.Tier == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !strings.HasPrefix(subject.Identifier, "signup-") {
				next.ServeHTTP(w, r)
				return
			}
			if !subject.EmailVerifiedAt.IsZero() {
				next.ServeHTTP(w, r)
				return
			}
			writeEmailUnverified(w, r)
		})
	}
}

// writeEmailUnverified emits a 403 Problem-JSON pointing the
// customer at the verify endpoint. The body intentionally
// does NOT echo back the key_id (operators distinguish
// individual customer flows via /v1/account/me); the keyword
// `signup-verify-required` lets dashboard clients render a
// resend-link CTA.
func writeEmailUnverified(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{
		"type":     "https://api.ratesengine.net/errors/signup-verify-required",
		"title":    "Email verification required",
		"status":   http.StatusForbidden,
		"detail":   "this API key was minted via /v1/signup but the post-signup verification email hasn't been confirmed yet. Click the link in the email we sent, or contact support if you didn't receive it.",
		"instance": r.URL.Path,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{"title":"Email verification required","status":403}`)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-RatesEngine-Signup-Verify-Required", "true")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(body)
}
