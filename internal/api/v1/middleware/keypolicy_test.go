// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// TestKeyPolicy_AnonymousBypasses pins that anonymous traffic
// (no Subject or TierAnonymous) skips every check — the policy
// surfaces are per-key only. F-1226 (codex audit-2026-05-12).
func TestKeyPolicy_AnonymousBypasses(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := middleware.KeyPolicy()
	req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if !called {
		t.Fatalf("anonymous request blocked: status=%d body=%s", w.Code, w.Body.String())
	}
}

func subjectWithPolicy(t *testing.T, build func(*auth.Subject)) auth.Subject {
	t.Helper()
	s := auth.Subject{
		Identifier: "acct:test",
		Tier:       auth.TierAPIKey,
		KeyID:      "kid_test",
	}
	build(&s)
	return s
}

func TestKeyPolicy_IPAllowlist(t *testing.T) {
	cases := []struct {
		name       string
		clientIP   string
		allowed    []string
		wantStatus int
	}{
		{"in cidr", "203.0.113.42:1234", []string{"203.0.113.0/24"}, http.StatusOK},
		{"out of cidr", "198.51.100.5:1234", []string{"203.0.113.0/24"}, http.StatusForbidden},
		{"empty allowlist allows all", "198.51.100.5:1234", nil, http.StatusOK},
		{"v6 in", "[2001:db8::1]:1234", []string{"2001:db8::/32"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prefixes []netip.Prefix
			for _, raw := range tc.allowed {
				p, err := netip.ParsePrefix(raw)
				if err != nil {
					t.Fatalf("parse prefix: %v", err)
				}
				prefixes = append(prefixes, p)
			}
			sub := subjectWithPolicy(t, func(s *auth.Subject) {
				s.IPAllowlist = prefixes
				s.AllowAllPermissions = true
			})

			req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
			req.RemoteAddr = tc.clientIP
			req = req.WithContext(auth.WithSubject(req.Context(), sub))

			w := httptest.NewRecorder()
			middleware.KeyPolicy()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestKeyPolicy_RefererAllowlist(t *testing.T) {
	sub := subjectWithPolicy(t, func(s *auth.Subject) {
		s.RefererAllowlist = []string{"app.example.com", "dashboard.example.com"}
		s.AllowAllPermissions = true
	})
	cases := []struct {
		name       string
		referer    string
		wantStatus int
	}{
		{"match exact", "https://app.example.com/page", http.StatusOK},
		{"match secondary", "https://dashboard.example.com/", http.StatusOK},
		{"wrong host", "https://evil.example.com/", http.StatusForbidden},
		{"missing referer", "", http.StatusForbidden},
		{"malformed referer", "::not a url::", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/price?asset=native&quote=fiat:USD", nil)
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			req = req.WithContext(auth.WithSubject(req.Context(), sub))
			w := httptest.NewRecorder()
			middleware.KeyPolicy()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestKeyPolicy_Permissions(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		method     string
		allowAll   bool
		allow      []auth.SubjectPermissionEntry
		deny       []auth.SubjectPermissionEntry
		wantStatus int
	}{
		{
			name:       "all permitted",
			path:       "/v1/price",
			method:     http.MethodGet,
			allowAll:   true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "allow exact match",
			path:       "/v1/price",
			method:     http.MethodGet,
			allow:      []auth.SubjectPermissionEntry{{Endpoint: "GET /v1/price"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "allow prefix match",
			path:       "/v1/account/keys",
			method:     http.MethodGet,
			allow:      []auth.SubjectPermissionEntry{{EndpointPrefix: "/v1/account/"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "not in allow list",
			path:       "/v1/admin/something",
			method:     http.MethodGet,
			allow:      []auth.SubjectPermissionEntry{{EndpointPrefix: "/v1/price"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "deny overrides allow-all",
			path:       "/v1/admin/something",
			method:     http.MethodGet,
			allowAll:   true,
			deny:       []auth.SubjectPermissionEntry{{EndpointPrefix: "/v1/admin/"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "closed posture",
			path:       "/v1/price",
			method:     http.MethodGet,
			allowAll:   false,
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := subjectWithPolicy(t, func(s *auth.Subject) {
				s.AllowAllPermissions = tc.allowAll
				s.AllowPermissions = tc.allow
				s.DenyPermissions = tc.deny
			})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req = req.WithContext(auth.WithSubject(req.Context(), sub))
			w := httptest.NewRecorder()
			middleware.KeyPolicy()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestKeyPolicy_OperatorSkipsPermissions(t *testing.T) {
	sub := auth.Subject{
		Identifier:          "operator:staff-1",
		Tier:                auth.TierOperator,
		AllowAllPermissions: false,
		// No allow entries — anonymous policy posture would 403,
		// but the operator tier bypasses permissions.
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/freeze", nil)
	req = req.WithContext(auth.WithSubject(req.Context(), sub))
	w := httptest.NewRecorder()
	middleware.KeyPolicy()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (operator should bypass permissions); body=%s", w.Code, w.Body.String())
	}
}
