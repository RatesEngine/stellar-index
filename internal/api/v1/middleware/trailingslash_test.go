package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TrailingSlashRedirect happy path: /v1/coins/native/ → 308 Location
// /v1/coins/native (preserves query string), and the inner handler
// is NOT called.

func TestTrailingSlashRedirect_redirectsAndSkipsHandler(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	mw := TrailingSlashRedirect(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/coins/native/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/coins/native" {
		t.Errorf("Location = %q, want /v1/coins/native", loc)
	}
	if called {
		t.Error("inner handler should not have been called")
	}
}

func TestTrailingSlashRedirect_preservesQueryString(t *testing.T) {
	mw := TrailingSlashRedirect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/v1/coins/?cursor=abc&limit=10", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/coins?cursor=abc&limit=10" {
		t.Errorf("Location = %q, want /v1/coins?cursor=abc&limit=10", loc)
	}
}

func TestTrailingSlashRedirect_rootIsExempt(t *testing.T) {
	// "/" must not redirect to "" — that would be a broken loop.
	called := false
	mw := TrailingSlashRedirect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default)", rec.Code)
	}
	if !called {
		t.Error("inner handler should have been called for root")
	}
}

func TestTrailingSlashRedirect_noSlashPassesThrough(t *testing.T) {
	called := false
	mw := TrailingSlashRedirect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/coins", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler should have been called for no-slash path")
	}
}

// 308 (rather than 301/302) preserves method and body for POST/DELETE.
// Pin the redirect status itself so a refactor can't silently weaken
// the redirect to a method-changing 301.
func TestTrailingSlashRedirect_methodAgnostic(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			mw := TrailingSlashRedirect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("inner handler should not have been called")
			}))
			req := httptest.NewRequest(method, "/v1/account/keys/", nil)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if rec.Code != http.StatusPermanentRedirect {
				t.Errorf("status = %d, want 308 for %s", rec.Code, method)
			}
			if loc := rec.Header().Get("Location"); loc != "/v1/account/keys" {
				t.Errorf("Location = %q, want /v1/account/keys", loc)
			}
		})
	}
}
