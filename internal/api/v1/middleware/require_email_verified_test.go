// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
)

func runRequireEmailVerified(t *testing.T, sub auth.Subject) (status int, body string) {
	t.Helper()
	mw := middleware.RequireEmailVerified()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
	req = req.WithContext(auth.WithSubject(req.Context(), sub))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestRequireEmailVerified_AnonymousPassThrough — anonymous
// Subjects bypass the gate; they don't carry a key to verify.
func TestRequireEmailVerified_AnonymousPassThrough(t *testing.T) {
	status, _ := runRequireEmailVerified(t, auth.Subject{Tier: auth.TierAnonymous, Identifier: "ip:1.2.3.4"})
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (anon)", status)
	}
}

// TestRequireEmailVerified_NonSignupKeyPassThrough — operator-
// minted keys (`customer-…`) and dashboard-minted keys
// (`acct:<slug>`) didn't go through the email-link flow at
// all; the gate scopes them out so existing keys keep working.
func TestRequireEmailVerified_NonSignupKeyPassThrough(t *testing.T) {
	for _, ident := range []string{
		"acct:dashboard-customer",
		"customer-acme-corp",
		"operator-staff-1",
	} {
		t.Run(ident, func(t *testing.T) {
			status, _ := runRequireEmailVerified(t, auth.Subject{
				Tier:       auth.TierAPIKey,
				KeyID:      "kid_legacy",
				Identifier: ident,
				// EmailVerifiedAt deliberately zero — must NOT block.
			})
			if status != http.StatusOK {
				t.Errorf("status = %d, want 200 (non-signup identifier scoped out)", status)
			}
		})
	}
}

// TestRequireEmailVerified_VerifiedSignupKeyPassThrough —
// signup-minted key with a non-zero EmailVerifiedAt clears the
// gate.
func TestRequireEmailVerified_VerifiedSignupKeyPassThrough(t *testing.T) {
	status, _ := runRequireEmailVerified(t, auth.Subject{
		Tier:            auth.TierAPIKey,
		KeyID:           "kid_verified",
		Identifier:      "signup-abcdef0123456789",
		EmailVerifiedAt: time.Now().UTC(),
	})
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (verified)", status)
	}
}

// TestRequireEmailVerified_UnverifiedSignupKey403 — the gate's
// failure mode: signup-minted key, EmailVerifiedAt zero → 403
// + Problem-JSON.
func TestRequireEmailVerified_UnverifiedSignupKey403(t *testing.T) {
	status, body := runRequireEmailVerified(t, auth.Subject{
		Tier:       auth.TierAPIKey,
		KeyID:      "kid_unverified",
		Identifier: "signup-abcdef0123456789",
		// EmailVerifiedAt deliberately zero.
	})
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if !strings.Contains(body, `signup-verify-required`) {
		t.Errorf("body missing signup-verify-required type tag: %s", body)
	}
	if !strings.Contains(body, "verification email") {
		t.Errorf("body missing verification-email guidance: %s", body)
	}
}
