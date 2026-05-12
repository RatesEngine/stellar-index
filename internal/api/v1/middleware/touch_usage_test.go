// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
)

// fakeToucher records every TouchUsage call so the test can
// assert which key/IP/UA the middleware passed through.
type fakeToucher struct {
	mu    sync.Mutex
	calls []touchCall
	err   error
}

type touchCall struct {
	keyID string
	ip    net.IP
	ua    string
}

func (f *fakeToucher) TouchUsage(_ context.Context, id string, ip net.IP, ua string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, touchCall{keyID: id, ip: ip, ua: ua})
	return f.err
}

// fakeDebouncer toggles ShouldTouch based on a counter and an
// optional error.
type fakeDebouncer struct {
	allows int64 // bool that flips false after the first true
	err    error
}

func (f *fakeDebouncer) ShouldTouch(_ context.Context, _ string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if atomic.AddInt64(&f.allows, -1) >= 0 {
		return true, nil
	}
	return false, nil
}

func runTouch(t *testing.T, mw middleware.Middleware, sub auth.Subject, ua string) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req = req.WithContext(auth.WithSubject(req.Context(), sub))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
}

// TestTouchUsage_FiresOnAuthenticatedRequest — Subject with a
// KeyID + an open debounce window calls TouchUsage exactly once
// with the expected fields.
func TestTouchUsage_FiresOnAuthenticatedRequest(t *testing.T) {
	t.Setenv("TZ", "UTC")
	toucher := &fakeToucher{}
	debouncer := &fakeDebouncer{allows: 1}
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	runTouch(t, mw, auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1"}, "curl/8.0")

	if got := len(toucher.calls); got != 1 {
		t.Fatalf("TouchUsage calls = %d, want 1", got)
	}
	if toucher.calls[0].keyID != "K1" {
		t.Errorf("keyID = %q, want K1", toucher.calls[0].keyID)
	}
	if toucher.calls[0].ua != "curl/8.0" {
		t.Errorf("ua = %q, want curl/8.0", toucher.calls[0].ua)
	}
}

// TestTouchUsage_DebounceSkips — second request inside the
// debounce window must NOT call TouchUsage.
func TestTouchUsage_DebounceSkips(t *testing.T) {
	toucher := &fakeToucher{}
	debouncer := &fakeDebouncer{allows: 1} // first allowed, second denied
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	sub := auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1"}
	runTouch(t, mw, sub, "ua-a")
	runTouch(t, mw, sub, "ua-b")
	if got := len(toucher.calls); got != 1 {
		t.Errorf("calls = %d, want 1 (debouncer denied second)", got)
	}
}

// TestTouchUsage_AnonymousSkipped — anonymous Subjects don't
// have a KeyID; TouchUsage is a no-op.
func TestTouchUsage_AnonymousSkipped(t *testing.T) {
	toucher := &fakeToucher{}
	debouncer := &fakeDebouncer{allows: 100}
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	runTouch(t, mw, auth.Subject{Tier: auth.TierAnonymous, Identifier: "ip:1.2.3.4"}, "x")
	if got := len(toucher.calls); got != 0 {
		t.Errorf("calls = %d, want 0 (anonymous)", got)
	}
}

// TestTouchUsage_NoKeyID_Skipped — Subject with no KeyID (e.g.
// SEP-10 tier without a per-request key row to update) is a
// no-op too.
func TestTouchUsage_NoKeyID_Skipped(t *testing.T) {
	toucher := &fakeToucher{}
	debouncer := &fakeDebouncer{allows: 100}
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	runTouch(t, mw, auth.Subject{Tier: auth.TierAPIKey, KeyID: ""}, "x")
	if got := len(toucher.calls); got != 0 {
		t.Errorf("calls = %d, want 0 (no KeyID)", got)
	}
}

// TestTouchUsage_NilDeps_Passthrough — Redis-less deployments
// pass nil for the debouncer / toucher (the helper in main.go
// returns a nil middleware in that case, but the middleware
// itself must also short-circuit if wired manually).
func TestTouchUsage_NilDeps_Passthrough(t *testing.T) {
	mw := middleware.TouchUsage(nil, nil, nil)
	// Just make sure the wrapped handler still runs.
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.WithSubject(req.Context(), auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1"}))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if !called {
		t.Error("next handler did not run")
	}
}

// TestTouchUsage_DebouncerErrorIsNonFatal — a debouncer-side
// failure must NOT block the customer's request and must NOT
// fire TouchUsage either (we don't know if the previous touch
// succeeded).
func TestTouchUsage_DebouncerErrorIsNonFatal(t *testing.T) {
	toucher := &fakeToucher{}
	debouncer := &fakeDebouncer{err: errors.New("redis blip")}
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	runTouch(t, mw, auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1"}, "x")
	if got := len(toucher.calls); got != 0 {
		t.Errorf("calls = %d, want 0 (debouncer failed → skip)", got)
	}
}

// TestTouchUsage_ToucherErrorSwallowed — TouchUsage failures
// must NOT propagate to the customer. The middleware logs at
// debug and the response stays 200.
func TestTouchUsage_ToucherErrorSwallowed(t *testing.T) {
	toucher := &fakeToucher{err: errors.New("postgres timeout")}
	debouncer := &fakeDebouncer{allows: 1}
	mw := middleware.TouchUsage(toucher, debouncer, nil)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.WithSubject(req.Context(), auth.Subject{Tier: auth.TierAPIKey, KeyID: "K1"}))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if !called {
		t.Error("next handler did not run despite toucher error")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
