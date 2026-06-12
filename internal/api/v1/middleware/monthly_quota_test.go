// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// fakeMTDReader implements middleware.MonthToDateReader with a
// canned per-subject month-to-date count + an optional error.
type fakeMTDReader struct {
	counts map[string]int64
	err    error
}

func (f *fakeMTDReader) MonthToDate(_ context.Context, subject string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[subject], nil
}

func runWithSubject(t *testing.T, mw middleware.Middleware, sub auth.Subject) (status int, headers http.Header, body string) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
	req = req.WithContext(auth.WithSubject(req.Context(), sub))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code == http.StatusOK && !called {
		t.Fatal("status 200 reported but next handler never ran")
	}
	return w.Code, w.Header(), w.Body.String()
}

// TestMonthlyQuota_PassThroughWhenQuotaUnset — Subject with
// MonthlyQuota == 0 must skip the check entirely (the cap is
// opt-in per key).
func TestMonthlyQuota_PassThroughWhenQuotaUnset(t *testing.T) {
	reader := &fakeMTDReader{counts: map[string]int64{"key:K1": 999_999}}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 0}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (quota unset → no check)", status)
	}
}

// TestMonthlyQuota_PassThroughBelowCap — Subject with quota 100,
// MTD 50 → request should succeed.
func TestMonthlyQuota_PassThroughBelowCap(t *testing.T) {
	reader := &fakeMTDReader{counts: map[string]int64{"key:K1": 50}}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 100}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (50 < 100)", status)
	}
}

// TestMonthlyQuota_RejectsAtCap — when MTD reaches the quota, the
// middleware returns 429 with the documented Problem+JSON shape +
// the X-RatesEngine-Monthly-* observability headers.
func TestMonthlyQuota_RejectsAtCap(t *testing.T) {
	reader := &fakeMTDReader{counts: map[string]int64{"key:K1": 100}}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 100}
	status, headers, body := runWithSubject(t, mw, sub)
	if status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", status)
	}
	if got := headers.Get("X-RatesEngine-Monthly-Quota"); got != "100" {
		t.Errorf("X-RatesEngine-Monthly-Quota = %q, want 100", got)
	}
	if got := headers.Get("X-RatesEngine-Monthly-Used"); got != "100" {
		t.Errorf("X-RatesEngine-Monthly-Used = %q, want 100", got)
	}
	if !strings.Contains(body, `"monthly_quota":100`) {
		t.Errorf("body missing monthly_quota: %s", body)
	}
	if !strings.Contains(body, `"month_to_date":100`) {
		t.Errorf("body missing month_to_date: %s", body)
	}
}

// TestMonthlyQuota_RejectsAboveCap — used > quota also rejects
// (a delayed counter increment after a previous tick at-cap can
// land here).
func TestMonthlyQuota_RejectsAboveCap(t *testing.T) {
	reader := &fakeMTDReader{counts: map[string]int64{"key:K1": 250}}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 100}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 (over-cap)", status)
	}
}

// TestMonthlyQuota_FailOpenOnReaderError — usage caps must NEVER
// 500 paying customers when the underlying counter is briefly
// unavailable. Reader errors log + pass through.
func TestMonthlyQuota_FailOpenOnReaderError(t *testing.T) {
	reader := &fakeMTDReader{err: errors.New("redis blip")}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 1}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on reader error)", status)
	}
}

// TestMonthlyQuota_NilReader_PassThrough — operator deployment
// without a usage counter (Redis-less) must pass through cleanly.
func TestMonthlyQuota_NilReader_PassThrough(t *testing.T) {
	mw := middleware.MonthlyQuota(nil, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1", MonthlyQuota: 100}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (nil reader → no check)", status)
	}
}

// TestMonthlyQuota_AnonymousPassThrough — anonymous Subjects
// don't carry quotas; the middleware short-circuits.
func TestMonthlyQuota_AnonymousPassThrough(t *testing.T) {
	reader := &fakeMTDReader{counts: map[string]int64{"key:K1": 10000}}
	mw := middleware.MonthlyQuota(reader, nil)
	sub := auth.Subject{Tier: auth.TierAnonymous, Identifier: "ip:1.2.3.4"}
	status, _, _ := runWithSubject(t, mw, sub)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (anon has no quota)", status)
	}
}
