package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
)

// TestWriteJSON_DefaultEnvelopeShape pins the wire shape every
// 2xx handler relies on: 200 status, application/json
// Content-Type, AsOf populated, Sources omitted when empty,
// Flags present (zero-valued).
func TestWriteJSON_DefaultEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	before := time.Now().UTC()
	writeJSON(rec, map[string]any{"k": "v"}, Flags{})
	after := time.Now().UTC()

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var got Envelope
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if got.AsOf.Before(before) || got.AsOf.After(after) {
		t.Errorf("AsOf %v outside [%v, %v]", got.AsOf, before, after)
	}
	if got.Sources != nil {
		t.Errorf("Sources = %v, want nil (omitempty)", got.Sources)
	}
	if got.Pagination != nil {
		t.Errorf("Pagination = %v, want nil (omitempty)", got.Pagination)
	}
	// Flags zero-value MUST appear on the wire (no omitempty on
	// Stale/ReducedRedundancy/Triangulated/DivergenceWarning) so
	// clients can rely on the field always being present.
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal envelope: %v", err)
	}
	if !contains(body, []byte(`"flags":{`)) {
		t.Errorf("envelope body %q missing flags object", body)
	}
}

// TestWriteJSON_WithSourcesIncluded — when sources are supplied,
// they appear on the wire (non-omitempty path).
func TestWriteJSON_WithSourcesIncluded(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, "data", Flags{}, "binance", "kraken", "soroswap")

	var got Envelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 3 {
		t.Fatalf("len(Sources) = %d, want 3", len(got.Sources))
	}
	if got.Sources[0] != "binance" || got.Sources[1] != "kraken" || got.Sources[2] != "soroswap" {
		t.Errorf("Sources = %v, want [binance kraken soroswap]", got.Sources)
	}
}

// TestWriteEnvelope_PreservesAsOf checks that a pre-set AsOf is
// honoured (writeEnvelopeStatus only fills in when zero). Handlers
// with their own clock — bucket-end timestamps, observed_at carry-
// forward — depend on this.
func TestWriteEnvelope_PreservesAsOf(t *testing.T) {
	custom := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := httptest.NewRecorder()
	writeEnvelope(rec, Envelope{
		Data: "x",
		AsOf: custom,
	})

	var got Envelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.AsOf.Equal(custom) {
		t.Errorf("AsOf = %v, want %v (writeEnvelope must preserve pre-set value)", got.AsOf, custom)
	}
}

// TestWriteEnvelope_FillsZeroAsOf — when AsOf is zero on the way
// in, writeEnvelopeStatus stamps it with now(). Mirrors what
// writeJSON does so handlers that pass an Envelope but forget to
// set AsOf still produce a valid response.
func TestWriteEnvelope_FillsZeroAsOf(t *testing.T) {
	rec := httptest.NewRecorder()
	before := time.Now().UTC()
	writeEnvelope(rec, Envelope{Data: "x"})
	after := time.Now().UTC()

	var got Envelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AsOf.IsZero() {
		t.Fatal("AsOf is zero — writeEnvelope should have filled it")
	}
	if got.AsOf.Before(before) || got.AsOf.After(after) {
		t.Errorf("AsOf %v outside [%v, %v]", got.AsOf, before, after)
	}
}

// TestWriteEnvelopeStatus_RespectsExplicitStatus — the 201
// path for /v1/account/keys POST relies on this. Adding a regression
// test pins it after F-0012's 200→201 fix.
func TestWriteEnvelopeStatus_RespectsExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeEnvelopeStatus(rec, http.StatusCreated, Envelope{Data: "k"})
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
}

// TestWriteEnvelope_PreservesPagination checks that list endpoints
// (assets / markets / sources / oracle/latest) get their Pagination
// cursor through the envelope intact.
func TestWriteEnvelope_PreservesPagination(t *testing.T) {
	rec := httptest.NewRecorder()
	writeEnvelope(rec, Envelope{
		Data:       []string{"a", "b"},
		AsOf:       time.Unix(1700000000, 0).UTC(),
		Pagination: &Pagination{Next: "cursor-xyz"},
	})

	var got Envelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Pagination == nil || got.Pagination.Next != "cursor-xyz" {
		t.Errorf("Pagination = %+v, want {Next: cursor-xyz}", got.Pagination)
	}
}

// TestWriteProblem_RFC9457Shape pins the error wire contract per
// docs/reference/api-design.md §5: type/title/status mandatory,
// detail + instance + request_id present when populated.
func TestWriteProblem_RFC9457Shape(t *testing.T) {
	// Run inside RequestID middleware so RequestIDFrom returns a
	// non-empty value the writeProblem path will then echo back.
	var captured *Problem
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/test",
			"Test error",
			http.StatusBadRequest,
			"a thing went wrong",
		)
	})
	h := middleware.RequestID(inner)
	req := httptest.NewRequest(http.MethodGet, "/v1/test?x=1", nil)
	req.Header.Set("X-Request-ID", "fixed-id-1234")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	var p Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	captured = &p
	if captured.Type != "https://api.ratesengine.net/errors/test" {
		t.Errorf("Type = %q", captured.Type)
	}
	if captured.Title != "Test error" {
		t.Errorf("Title = %q", captured.Title)
	}
	if captured.Status != http.StatusBadRequest {
		t.Errorf("Status = %d", captured.Status)
	}
	if captured.Detail != "a thing went wrong" {
		t.Errorf("Detail = %q", captured.Detail)
	}
	if captured.Instance != "/v1/test?x=1" {
		t.Errorf("Instance = %q, want /v1/test?x=1", captured.Instance)
	}
	if captured.RequestID != "fixed-id-1234" {
		t.Errorf("RequestID = %q, want fixed-id-1234", captured.RequestID)
	}
}

// TestWriteProblem_401SetsWWWAuthenticate pins the RFC 7235 §3.1
// guarantee that every 401 advertises a Bearer challenge so
// programmatic clients can discover the accepted auth scheme.
// The header is also tested at the auth-middleware layer; this
// covers the handler-level writeProblem path used by /v1/account/*
// for not-yet-authenticated requests.
func TestWriteProblem_401SetsWWWAuthenticate(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/unauthorized",
			"Authentication required",
			http.StatusUnauthorized,
			"sign in",
		)
	})
	h := middleware.RequestID(inner)
	req := httptest.NewRequest(http.MethodGet, "/v1/account/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	got := rec.Header().Get("WWW-Authenticate")
	if got == "" {
		t.Fatal("WWW-Authenticate header missing on 401")
	}
	if !strings.Contains(got, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want it to advertise Bearer scheme", got)
	}
}

// TestWriteProblem_NonAuthDoesNotSetWWWAuthenticate guards the
// inverse: a 400 / 404 / 500 / 503 problem must NOT emit
// WWW-Authenticate (RFC 7235's MUST applies to 401 only). Pre-fix
// the helper had no condition; this pin keeps the conditional in
// place.
func TestWriteProblem_NonAuthDoesNotSetWWWAuthenticate(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeProblem(w, r, "https://api.ratesengine.net/errors/x", "x", status, "x")
		})
		h := middleware.RequestID(inner)
		req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("status %d: WWW-Authenticate = %q, want empty", status, got)
		}
	}
}

// TestClientAborted classifies cancellation states a handler may
// observe while reading from the request body or upstream calls.
// The clientAborted predicate gates the "skip writeProblem, let
// HTTPMetrics label this 499" path. The decision rule is
// req-context-done → true; everything else (including bare ctx
// errors when r.Context() is alive) is false so that server-side
// context.WithTimeout deadlines (#1082, #1099-#1105) flow into
// each handler's 503 timeout-response branch instead of being
// silently swallowed.
func TestClientAborted(t *testing.T) {
	t.Run("ctx canceled error with live request ctx", func(t *testing.T) {
		// Bare context.Canceled with the request still alive is a
		// server-internal cancel — not a client abort.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if clientAborted(req, context.Canceled) {
			t.Error("clientAborted(ctx.Canceled, alive req ctx) = true, want false")
		}
	})
	t.Run("deadline exceeded error with live request ctx", func(t *testing.T) {
		// THE bug fix: server-side context.WithTimeout(8s) deadlines
		// must flow through to the handler's 503 path, not get
		// swallowed as a client abort.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if clientAborted(req, context.DeadlineExceeded) {
			t.Error("clientAborted(DeadlineExceeded, alive req ctx) = true, want false (must flow to 503)")
		}
	})
	t.Run("wrapped ctx canceled via request context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		// Even with an unrelated err arg, a done request context is
		// the authoritative "client gone" signal.
		if !clientAborted(req, errors.New("downstream wrapped error")) {
			t.Error("clientAborted with done request context = false, want true")
		}
	})
	t.Run("plain non-context error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if clientAborted(req, errors.New("boom")) {
			t.Error("clientAborted(plain err) = true, want false")
		}
	})
	t.Run("nil error and live context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if clientAborted(req, nil) {
			t.Error("clientAborted(nil, live ctx) = true, want false")
		}
	})
}

func TestHandlerTimedOut(t *testing.T) {
	t.Run("err wraps DeadlineExceeded", func(t *testing.T) {
		ctx := context.Background()
		if !handlerTimedOut(ctx, context.DeadlineExceeded) {
			t.Error("handlerTimedOut(live ctx, DeadlineExceeded) = false, want true")
		}
	})
	t.Run("call ctx deadline fired, err is pq cancel", func(t *testing.T) {
		// THE R-021 case: lib/pq returns its own
		// `pq: canceling statement due to user request` error string
		// after our context.WithTimeout fires. errors.Is misses it
		// because pq.Error doesn't wrap context.DeadlineExceeded;
		// the per-call context Err() is the authoritative signal.
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
		defer cancel()
		pqErr := errors.New("pq: canceling statement due to user request")
		if !handlerTimedOut(ctx, pqErr) {
			t.Error("handlerTimedOut(deadline-passed ctx, pq cancel) = false, want true")
		}
	})
	t.Run("call ctx canceled (not deadlined), arbitrary err", func(t *testing.T) {
		// context.Canceled (e.g. an explicit cancel()) is NOT a
		// timeout — the handler shouldn't 503 on this branch.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if handlerTimedOut(ctx, errors.New("downstream cancelled")) {
			t.Error("handlerTimedOut(canceled-not-timed-out ctx) = true, want false")
		}
	})
	t.Run("everything healthy", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if handlerTimedOut(ctx, errors.New("storage broke")) {
			t.Error("handlerTimedOut(live ctx, plain err) = true, want false")
		}
	})
}

func contains(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
