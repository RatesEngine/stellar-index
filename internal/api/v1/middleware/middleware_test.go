package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mw "github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
)

// ─── Chain ────────────────────────────────────────────────────────

func TestChain_OrderIsOutermostFirst(t *testing.T) {
	// Build three middleware that append to a shared slice so we
	// can observe the order on the request + response path.
	var order []string

	mkMW := func(name string) mw.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":pre")
				next.ServeHTTP(w, r)
				order = append(order, name+":post")
			})
		}
	}

	h := mw.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
		}),
		mkMW("A"), mkMW("B"), mkMW("C"),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := []string{
		"A:pre", "B:pre", "C:pre", "handler",
		"C:post", "B:post", "A:post",
	}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("step %d = %q, want %q (full: %v)", i, order[i], v, order)
		}
	}
}

// ─── RequestID ────────────────────────────────────────────────────

func TestRequestID_MintsWhenAbsent(t *testing.T) {
	var gotID string
	h := mw.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = mw.RequestIDFrom(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if gotID == "" {
		t.Fatal("no request id in context")
	}
	if len(gotID) != 32 {
		t.Errorf("id len = %d, want 32 hex chars", len(gotID))
	}
	if rec.Header().Get("X-Request-ID") != gotID {
		t.Errorf("response header != context id")
	}
}

func TestRequestID_PreservesClientValue(t *testing.T) {
	h := mw.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-supplied-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "client-supplied-123" {
		t.Errorf("id = %q, want 'client-supplied-123'", got)
	}
}

func TestRequestID_RejectsOversizeValue(t *testing.T) {
	// Oversize (> 128 bytes) → replaced with a freshly-minted 32-hex
	// ID rather than truncated. Truncation risked cutting a client's
	// ID in the middle, yielding an ID that matches neither what
	// they sent nor anything else — the fresh mint is clearer.
	oversize := strings.Repeat("x", 200)
	h := mw.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", oversize)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-ID")
	if len(got) != 32 {
		t.Errorf("id len = %d, want 32 (freshly minted)", len(got))
	}
	if got == oversize[:32] {
		t.Errorf("id looks like a truncation of the client-supplied value")
	}
}

func TestRequestID_RejectsUnsafeChars(t *testing.T) {
	// IDs with CR/LF, spaces, or exotic chars get replaced with a
	// freshly-minted one rather than propagated into response
	// headers and logs.
	cases := []string{
		"has space",
		"has\ttab",
		"crlf\r\ninjection",
		`quoted "value"`,
		"unicode-é",
		"slash/path",
	}
	for _, bad := range cases {
		h := mw.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		// Bypass net/http's header validation by setting via the map
		// directly. Real HTTP parsers would reject the connection
		// before this middleware sees it, but once set programmatically
		// (e.g. reverse-proxy misconfig) the middleware is the
		// remaining safety net.
		req.Header["X-Request-Id"] = []string{bad}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		got := rec.Header().Get("X-Request-ID")
		if got == bad {
			t.Errorf("unsafe id %q propagated to response", bad)
		}
		if len(got) != 32 {
			t.Errorf("expected freshly minted 32-char id for %q, got %q", bad, got)
		}
	}
}

// ─── Logger ───────────────────────────────────────────────────────

func TestLogger_EmitsStructuredLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := mw.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
		mw.RequestID,
		mw.Logger(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	req.RemoteAddr = "1.2.3.4:56789"
	req.Header.Set("User-Agent", "test-agent/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// One JSON log line expected.
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v (%s)", err, buf.String())
	}
	for _, f := range []string{"method", "path", "status", "latency_ms", "request_id", "remote_ip", "user_agent"} {
		if _, ok := entry[f]; !ok {
			t.Errorf("log missing field %q: %v", f, entry)
		}
	}
	if entry["method"] != "GET" || entry["path"] != "/v1/healthz" {
		t.Errorf("method/path wrong: %v", entry)
	}
	if entry["remote_ip"] != "1.2.3.4" {
		t.Errorf("remote_ip = %v, want 1.2.3.4 (port stripped)", entry["remote_ip"])
	}
}

func TestLogger_XForwardedForWins(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:80" // "direct" (our proxy)
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry)
	if entry["remote_ip"] != "203.0.113.42" {
		t.Errorf("remote_ip = %v, want 203.0.113.42 (first XFF hop)", entry["remote_ip"])
	}
}

func TestLogger_ServerErrorLogsAtErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var entry map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry)
	if entry["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR for 5xx", entry["level"])
	}
}

// ─── Recoverer ────────────────────────────────────────────────────

func TestRecoverer_CatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := mw.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}),
		mw.RequestID,
		mw.Recoverer(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/explode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want problem+json", ct)
	}

	// Body must NOT contain "boom" (attack-surface rule).
	body := rec.Body.String()
	if strings.Contains(body, "boom") {
		t.Errorf("panic value leaked to client: %s", body)
	}
	// Log MUST contain the panic + stack for debug.
	logStr := buf.String()
	if !strings.Contains(logStr, "boom") {
		t.Errorf("panic not logged: %s", logStr)
	}
	if !strings.Contains(logStr, "handler panic") {
		t.Errorf("log should mention 'handler panic': %s", logStr)
	}
}

func TestRecoverer_PropagatesAbortHandler(t *testing.T) {
	// http.ErrAbortHandler is the stdlib signal "cancel, don't
	// recover" — tests assert we re-raise it.
	defer func() {
		rec := recover()
		if recErr, ok := rec.(error); !ok || !errors.Is(recErr, http.ErrAbortHandler) {
			t.Errorf("expected re-raised ErrAbortHandler, got %v", rec)
		}
	}()

	h := mw.Recoverer(slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}),
	)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecoverer_NormalHandlerUntouched(t *testing.T) {
	h := mw.Recoverer(slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`ok`))
		}),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("non-panicking handler was mangled: %d %q", rec.Code, rec.Body.String())
	}
}

// ─── SecurityHeaders ──────────────────────────────────────────────

func TestSecurityHeaders_SetsNosniff(t *testing.T) {
	// Every response carries X-Content-Type-Options: nosniff, even
	// if the handler wrote its own headers first.
	h := mw.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q, want nosniff", got)
	}
}

func TestSecurityHeaders_IdempotentWithEdgeProxy(t *testing.T) {
	// Wrap a handler that pretends the edge proxy already set
	// nosniff. Verify middleware's set leaves the final value
	// equal to "nosniff" (last-write-wins in net/http, but same
	// value either way).
	h := mw.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Edge already set it — middleware's own set should run
		// BEFORE the handler writes, which it does via the outer
		// wrapper, so this is just belt-and-braces.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q, want nosniff", got)
	}
}
