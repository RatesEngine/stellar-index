package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecoverer_HappyPath — non-panicking handler passes through
// unchanged. The recoverer adds no headers / body / status when
// the wrapped handler succeeds.
func TestRecoverer_HappyPath(t *testing.T) {
	mw := Recoverer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "set-by-handler")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (handler-supplied)", rec.Code)
	}
	if got := rec.Header().Get("X-Test"); got != "set-by-handler" {
		t.Errorf("X-Test header = %q, want set-by-handler", got)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q, want {\"ok\":true}", rec.Body.String())
	}
}

// TestRecoverer_CatchesPanic — a panicking handler produces a 500
// problem+json response instead of crashing the server.
func TestRecoverer_CatchesPanic(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mw := Recoverer(logger)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/explode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["title"] != "Internal error" {
		t.Errorf("body title = %v, want \"Internal error\"", body["title"])
	}
	if body["status"] != float64(500) { // JSON numbers decode as float64
		t.Errorf("body status = %v, want 500", body["status"])
	}
	if body["type"] != "https://api.ratesengine.net/errors/internal" {
		t.Errorf("body type = %v", body["type"])
	}
	if body["instance"] != "/v1/explode" {
		t.Errorf("body instance = %v, want /v1/explode", body["instance"])
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "handler panic") {
		t.Errorf("log missing 'handler panic': %s", logged)
	}
	if !strings.Contains(logged, "kaboom") {
		t.Errorf("log missing panic value: %s", logged)
	}
	// Stack trace is logged.
	if !strings.Contains(logged, "stack=") {
		t.Errorf("log missing stack trace: %s", logged)
	}

	// Critically: the panic value MUST NOT leak into the response body.
	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Errorf("panic value leaked into response body: %q", rec.Body.String())
	}
}

// TestRecoverer_AbortHandlerRePanics — the stdlib's http.ErrAbortHandler
// is the documented signal "abort the response, don't log". Recoverer
// must re-panic with it so net/http's own handling kicks in.
func TestRecoverer_AbortHandlerRePanics(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	mw := Recoverer(logger)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatal("expected the recoverer to re-panic with http.ErrAbortHandler; got nil")
		}
		err, ok := rec.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered value = %#v, want http.ErrAbortHandler", rec)
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/v1/abort", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // expect this to re-panic

	t.Fatal("ServeHTTP should have re-panicked with http.ErrAbortHandler but returned normally")
}

// TestRecoverer_NilLoggerFallsBackToDefault — passing nil shouldn't
// crash the constructor; it should fall back to slog.Default().
// We can't easily assert the fallback's destination, but we can
// confirm the middleware still functions on a panicking handler.
func TestRecoverer_NilLoggerFallsBackToDefault(t *testing.T) {
	mw := Recoverer(nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil-logger-test")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/explode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestRecoverer_RequestIDIncludedWhenSet — when a request id is
// present in the context (set by the request-id middleware
// upstream), the problem+json body includes it. This is the
// operator-facing thread that ties a 500 in the response to a
// log line.
func TestRecoverer_RequestIDIncludedWhenSet(t *testing.T) {
	mw := Recoverer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("with-rid")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/explode", nil)
	// Inject a request id into the context the same way the
	// request-id middleware does upstream — keeps this test
	// independent of the constant's exact name.
	ctx := withRequestIDForTest(req.Context(), "req-abc-123")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["request_id"] != "req-abc-123" {
		t.Errorf("body.request_id = %v, want req-abc-123", body["request_id"])
	}
}

// withRequestIDForTest is a tiny shim around the package-internal
// withString helper so the recoverer test can simulate the upstream
// request-id middleware injecting an ID into the context. Kept
// here (rather than in the production code) because exporting a
// real WithRequestID function would let downstream code bypass the
// request-id middleware entirely — undesirable.
func withRequestIDForTest(ctx context.Context, id string) context.Context {
	return withString(ctx, ctxKeyRequestID, id)
}
