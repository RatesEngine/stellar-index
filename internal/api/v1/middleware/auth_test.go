package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// stubAPIKeyValidator returns a fixed Subject for one specific
// known key; every other key triggers ErrUnauthorized. Lets us
// drive the apikey-mode middleware without needing a real Redis.
type stubAPIKeyValidator struct {
	knownKey string
	subject  auth.Subject
	err      error // set per-test to override the success path
}

func (s stubAPIKeyValidator) Lookup(_ context.Context, key string) (auth.Subject, error) {
	if s.err != nil {
		return auth.Subject{}, s.err
	}
	if key != s.knownKey {
		return auth.Subject{}, auth.ErrUnauthorized
	}
	return s.subject, nil
}

// stubSEP10Validator analogue for the JWT path. Only VerifyJWT is
// exercised by the middleware; the other methods land on the
// challenge/verify HTTP handlers (out of scope for this test).
type stubSEP10Validator struct {
	auth.NoopSEP10Validator // embed to inherit Challenge/Verify stubs

	knownJWT string
	subject  auth.Subject
}

func (s stubSEP10Validator) VerifyJWT(_ context.Context, jwt string) (auth.Subject, error) {
	if jwt != s.knownJWT {
		return auth.Subject{}, auth.ErrUnauthorized
	}
	return s.subject, nil
}

// captureSubject is the inner handler the middleware wraps; it
// records the Subject from context so tests can assert it.
func captureSubject(captured *auth.Subject) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, _ := auth.SubjectFrom(r.Context())
		*captured = s
		w.WriteHeader(http.StatusOK)
	}
}

// TestAuth_ModeNone is the default-on path: every request gets
// an anonymous Subject keyed by RemoteIP+UA hash. No 401s, no
// validator calls. The identifier is non-empty so the rate-limit
// middleware downstream has something to bucket against.
func TestAuth_ModeNone(t *testing.T) {
	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{Mode: middleware.AuthModeNone})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	r.Header.Set("User-Agent", "test-client/1.0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if captured.Tier != auth.TierAnonymous {
		t.Errorf("tier = %q, want anonymous", captured.Tier)
	}
	if captured.Identifier == "" {
		t.Error("anonymous identifier is empty — rate-limit middleware needs a key")
	}
	// Identifier must be deterministic for the same (IP, UA): a
	// second request from the same caller hits the same bucket.
	r2 := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r2.RemoteAddr = "203.0.113.5:54321"
	r2.Header.Set("User-Agent", "test-client/1.0")
	var captured2 auth.Subject
	h2 := mw(captureSubject(&captured2))
	h2.ServeHTTP(httptest.NewRecorder(), r2)
	if captured.Identifier != captured2.Identifier {
		t.Errorf("identifier non-deterministic: %q != %q", captured.Identifier, captured2.Identifier)
	}
}

func TestAuth_ModeNone_UntrustedPeerIgnoresXForwardedFor(t *testing.T) {
	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{Mode: middleware.AuthModeNone})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.RemoteAddr = "198.51.100.9:80"
	r.Header.Set("User-Agent", "test-client/1.0")
	r.Header.Set("X-Forwarded-For", "203.0.113.42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var direct auth.Subject
	h2 := mw(captureSubject(&direct))
	r2 := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r2.RemoteAddr = "198.51.100.9:80"
	r2.Header.Set("User-Agent", "test-client/1.0")
	h2.ServeHTTP(httptest.NewRecorder(), r2)

	if captured.Identifier != direct.Identifier {
		t.Fatalf("identifier changed based on untrusted XFF: %q != %q", captured.Identifier, direct.Identifier)
	}
}

func TestAuth_ModeNone_TrustedProxyUsesXForwardedFor(t *testing.T) {
	if err := middleware.SetTrustedProxyCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetTrustedProxyCIDRs: %v", err)
	}
	t.Cleanup(func() {
		if err := middleware.SetTrustedProxyCIDRs(nil); err != nil {
			t.Fatalf("reset trusted proxies: %v", err)
		}
	})

	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{Mode: middleware.AuthModeNone})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.RemoteAddr = "10.0.0.1:80"
	r.Header.Set("User-Agent", "test-client/1.0")
	r.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var direct auth.Subject
	h2 := mw(captureSubject(&direct))
	r2 := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r2.RemoteAddr = "203.0.113.42:80"
	r2.Header.Set("User-Agent", "test-client/1.0")
	h2.ServeHTTP(httptest.NewRecorder(), r2)

	if captured.Identifier != direct.Identifier {
		t.Fatalf("trusted XFF should resolve to client identity: %q != %q", captured.Identifier, direct.Identifier)
	}
}

// TestAuth_ModeAPIKey_HappyPath confirms a valid key passes auth
// + the validator's Subject reaches the handler.
func TestAuth_ModeAPIKey_HappyPath(t *testing.T) {
	want := auth.Subject{Identifier: "acct-42", Tier: auth.TierAPIKey}
	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:   middleware.AuthModeAPIKey,
		APIKey: stubAPIKeyValidator{knownKey: "k1", subject: want},
	})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("Authorization", "Bearer k1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if captured.Identifier != want.Identifier || captured.Tier != want.Tier {
		t.Errorf("captured = %+v, want %+v", captured, want)
	}
}

// TestAuth_ModeAPIKey_XAPIKeyHeader covers the alt header. Some
// SDKs / curl users prefer X-API-Key; we accept both. Authorization
// wins when both are present (tested in HappyPath above).
func TestAuth_ModeAPIKey_XAPIKeyHeader(t *testing.T) {
	want := auth.Subject{Identifier: "acct-42", Tier: auth.TierAPIKey}
	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:   middleware.AuthModeAPIKey,
		APIKey: stubAPIKeyValidator{knownKey: "k1", subject: want},
	})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("X-API-Key", "k1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if captured.Identifier != want.Identifier {
		t.Errorf("X-API-Key path didn't authenticate: %+v", captured)
	}
}

// TestAuth_ModeAPIKey_RejectsMissing: 401 with WWW-Authenticate
// hint when no credential is present + apikey mode is on.
func TestAuth_ModeAPIKey_RejectsMissing(t *testing.T) {
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:   middleware.AuthModeAPIKey,
		APIKey: stubAPIKeyValidator{knownKey: "k1"},
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not run on missing-credential path")
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if !contains(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("WWW-Authenticate missing Bearer hint: %q", w.Header().Get("WWW-Authenticate"))
	}
}

// TestAuth_ModeAPIKey_RejectsInvalid: 401 when the key bytes don't
// match the validator. Confirms the validator's ErrUnauthorized
// reaches the response.
func TestAuth_ModeAPIKey_RejectsInvalid(t *testing.T) {
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:   middleware.AuthModeAPIKey,
		APIKey: stubAPIKeyValidator{knownKey: "k1"},
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler ran with invalid key")
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestAuth_ModeAPIKey_NotImplemented503: a deployment that enabled
// apikey mode but didn't wire a validator returns 503 — fail-loud.
// Operator sees the misconfiguration on the first request rather
// than discovering it from a security audit.
func TestAuth_ModeAPIKey_NotImplemented503(t *testing.T) {
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:   middleware.AuthModeAPIKey,
		APIKey: nil, // misconfiguration
	})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// TestAuth_ModeSEP10_HappyPath confirms a valid JWT passes auth +
// the validator's Subject reaches the handler.
func TestAuth_ModeSEP10_HappyPath(t *testing.T) {
	want := auth.Subject{Identifier: "GAB123", Tier: auth.TierSEP10}
	var captured auth.Subject
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:  middleware.AuthModeSEP10,
		SEP10: stubSEP10Validator{knownJWT: "good.jwt.string", subject: want},
	})
	h := mw(captureSubject(&captured))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("Authorization", "Bearer good.jwt.string")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if captured.Identifier != want.Identifier {
		t.Errorf("captured = %+v, want %+v", captured, want)
	}
}

// TestAuth_ModeSEP10_RejectsXAPIKey: SEP-10 mode does NOT accept
// X-API-Key (that's apikey-mode). A client mixing modes should
// get 401 cleanly rather than silently demoting.
func TestAuth_ModeSEP10_RejectsXAPIKey(t *testing.T) {
	mw := middleware.Auth(middleware.AuthOptions{
		Mode:  middleware.AuthModeSEP10,
		SEP10: stubSEP10Validator{knownJWT: "good"},
	})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler ran on cross-mode credential")
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	r.Header.Set("X-API-Key", "good") // wrong header for sep10 mode
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (X-API-Key not honoured under sep10)", w.Code)
	}
}

// TestAuth_ErrorBodyIsProblemJSON pins the wire shape every auth
// failure path now emits — application/problem+json per
// docs/reference/api-design.md §11. Pre-2026-05-02 these paths
// emitted text/plain bodies, which broke the "every 4xx/5xx is
// problem+json" client contract.
func TestAuth_ErrorBodyIsProblemJSON(t *testing.T) {
	cases := []struct {
		name       string
		opts       middleware.AuthOptions
		setHeader  func(*http.Request)
		wantStatus int
		wantType   string
	}{
		{
			name: "missing credential — 401 unauthorized",
			opts: middleware.AuthOptions{
				Mode:   middleware.AuthModeAPIKey,
				APIKey: stubAPIKeyValidator{knownKey: "k1"},
			},
			setHeader:  func(*http.Request) {},
			wantStatus: http.StatusUnauthorized,
			wantType:   "https://api.ratesengine.net/errors/unauthorized",
		},
		{
			name: "no validator wired — 503 not configured",
			opts: middleware.AuthOptions{
				Mode:   middleware.AuthModeAPIKey,
				APIKey: nil,
			},
			setHeader: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer x")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   "https://api.ratesengine.net/errors/auth-not-configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := middleware.Auth(tc.opts)
			h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

			r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
			tc.setHeader(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			var body struct {
				Type   string `json:"type"`
				Title  string `json:"title"`
				Status int    `json:"status"`
				Detail string `json:"detail"`
			}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Type != tc.wantType {
				t.Errorf("type = %q, want %q", body.Type, tc.wantType)
			}
			if body.Status != tc.wantStatus {
				t.Errorf("body.status = %d, want %d", body.Status, tc.wantStatus)
			}
			if body.Title == "" {
				t.Error("title is empty; should be human-readable")
			}
			if body.Detail == "" {
				t.Error("detail is empty; should explain the failure")
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
