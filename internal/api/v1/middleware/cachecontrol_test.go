package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPolicyForPath_PinsDirectives — the policy table is part of
// the API contract (CDN configs reference these strings). Pinning
// every documented path against its expected directive guards
// against a typo flipping a public-cacheable endpoint to
// no-store at scale.
func TestPolicyForPath_PinsDirectives(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// Operator endpoints
		{"/v1/healthz", "no-store"},
		{"/v1/readyz", "no-store"},
		{"/v1/version", "no-store"},
		{"/metrics", "no-store"},

		// Account — auth-tied
		{"/v1/account/me", "private, no-store"},
		{"/v1/account/usage", "private, no-store"},
		{"/v1/account/keys", "private, no-store"},

		// SEP-10 Web Auth — credential exchange MUST NOT hit CDN
		{"/v1/auth/sep10/challenge", "private, no-store"},
		{"/v1/auth/sep10/token", "private, no-store"},

		// Tip + observations — private surfaces
		{"/v1/price/tip", "private, no-cache, must-revalidate"},
		{"/v1/price/tip/stream", "private, no-cache, must-revalidate"},
		{"/v1/observations", "private, no-cache, must-revalidate"},
		{"/v1/observations/stream", "private, no-cache, must-revalidate"},

		// Current price + asset detail — short cache
		{"/v1/price", "public, max-age=30, s-maxage=60"},
		{"/v1/price/batch", "public, max-age=30, s-maxage=60"},
		{"/v1/assets", "public, max-age=30, s-maxage=60"},
		{"/v1/assets/native", "public, max-age=30, s-maxage=60"},
		{"/v1/assets/USDC-GA5Z/metadata", "public, max-age=30, s-maxage=60"},

		// Historical / closed-bucket
		{"/v1/history", "public, max-age=60, s-maxage=300"},
		{"/v1/history/since-inception", "public, max-age=60, s-maxage=300"},
		{"/v1/ohlc", "public, max-age=60, s-maxage=300"},
		{"/v1/vwap", "public, max-age=60, s-maxage=300"},
		{"/v1/twap", "public, max-age=60, s-maxage=300"},
		{"/v1/markets", "public, max-age=60, s-maxage=300"},
		{"/v1/pairs", "public, max-age=60, s-maxage=300"},
		{"/v1/sources", "public, max-age=60, s-maxage=300"},
		{"/v1/oracle/latest", "public, max-age=60, s-maxage=300"},
		{"/v1/oracle/lastprice", "public, max-age=60, s-maxage=300"},
		{"/v1/oracle/prices", "public, max-age=60, s-maxage=300"},

		// Unknown — conservative default
		{"/v1/something-new", "private, no-store"},
		{"/", "private, no-store"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			if got := policyForPath(tc.path, true); got != tc.want {
				t.Errorf("policyForPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestPolicyForPath_TipBeatsPriceGenericPrefix — /v1/price/tip
// shares the /v1/price prefix; the tip rule MUST match first
// (private, no-cache) so a tip request never lands in a public CDN
// cache. Regression guard against re-ordering the switch.
func TestPolicyForPath_TipBeatsPriceGenericPrefix(t *testing.T) {
	tip := policyForPath("/v1/price/tip", true)
	price := policyForPath("/v1/price", true)
	if tip == price {
		t.Errorf("tip and price share directive %q — tip rule must run first", tip)
	}
	if tip != "private, no-cache, must-revalidate" {
		t.Errorf("/v1/price/tip = %q, want private no-cache", tip)
	}
}

// TestCacheControl_Middleware_SetsHeaderBeforeHandler — handlers
// see the header already on the writer; they CAN override it but
// the default is in place by the time they run.
func TestCacheControl_Middleware_SetsHeaderBeforeHandler(t *testing.T) {
	var observedAtHandler string
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		observedAtHandler = w.Header().Get("Cache-Control")
		w.WriteHeader(http.StatusOK)
	})
	mw := CacheControl(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/markets", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if observedAtHandler == "" {
		t.Error("handler saw empty Cache-Control; middleware must set it before next.ServeHTTP")
	}
	want := "public, max-age=60, s-maxage=300"
	if observedAtHandler != want {
		t.Errorf("handler saw %q, want %q", observedAtHandler, want)
	}
	if got := rec.Header().Get("Cache-Control"); got != want {
		t.Errorf("response Cache-Control = %q, want %q", got, want)
	}
}

// TestCacheControl_Middleware_HandlerOverrideWins — handlers that
// need to deviate (e.g. Etag-driven 304s) can overwrite the
// directive after the middleware ran. Verify the override survives.
func TestCacheControl_Middleware_HandlerOverrideWins(t *testing.T) {
	const override = "public, max-age=86400, immutable"
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", override)
		w.WriteHeader(http.StatusOK)
	})
	mw := CacheControl(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != override {
		t.Errorf("override lost: Cache-Control = %q, want %q", got, override)
	}
}

// TestCacheControl_Middleware_AppliesToErrorResponses — a handler
// that 4xxs still gets the route's cache directive applied. CDNs
// are expected to refuse to cache 5xx via origin config; this test
// pins that the middleware itself doesn't strip the directive on
// non-2xx responses.
func TestCacheControl_Middleware_AppliesToErrorResponses(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad input", http.StatusBadRequest)
	})
	mw := CacheControl(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/markets", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60, s-maxage=300" {
		t.Errorf("Cache-Control on 400 = %q, want public max-age=60 s-maxage=300", got)
	}
}

// TestPolicyForPath_CDNDisabled — operators without a CDN in front
// of the API set api.cdn_enabled=false; the middleware must drop
// `s-maxage` from cacheable directives so a CDN they don't have
// can't cache anything. private + no-store directives are
// unaffected (they were never CDN-cacheable anyway).
func TestPolicyForPath_CDNDisabled(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Cacheable routes lose s-maxage.
		{"/v1/price", "public, max-age=30"},
		{"/v1/price/batch", "public, max-age=30"},
		{"/v1/assets", "public, max-age=30"},
		{"/v1/assets/native", "public, max-age=30"},
		{"/v1/history", "public, max-age=60"},
		{"/v1/ohlc", "public, max-age=60"},
		{"/v1/vwap", "public, max-age=60"},
		{"/v1/twap", "public, max-age=60"},
		{"/v1/markets", "public, max-age=60"},
		{"/v1/pairs", "public, max-age=60"},
		{"/v1/sources", "public, max-age=60"},
		{"/v1/oracle/lastprice", "public, max-age=60"},
		// Non-cacheable directives unchanged.
		{"/v1/healthz", "no-store"},
		{"/v1/account/me", "private, no-store"},
		{"/v1/auth/sep10/challenge", "private, no-store"},
		{"/v1/price/tip", "private, no-cache, must-revalidate"},
		{"/v1/observations", "private, no-cache, must-revalidate"},
		{"/v1/something-new", "private, no-store"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := policyForPath(tc.path, false); got != tc.want {
				t.Errorf("policyForPath(%q, cdn=false) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestCacheControlWithCDN_FalseDropsSMaxAge confirms the middleware
// constructor honours the cdnEnabled flag end-to-end (handler-side
// header observation, not just policyForPath).
func TestCacheControlWithCDN_FalseDropsSMaxAge(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := CacheControlWithCDN(false)(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/markets", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control with cdn=false = %q, want \"public, max-age=60\"", got)
	}
}
