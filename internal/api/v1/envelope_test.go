package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// TestClientAborted classifies cancellation states a handler may
// observe while reading from the request body or upstream calls.
// The clientAborted predicate gates the "skip writeProblem, let
// HTTPMetrics label this 499" path — false negatives turn into
// misleading 500s in metrics.
func TestClientAborted(t *testing.T) {
	t.Run("ctx canceled error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if !clientAborted(req, context.Canceled) {
			t.Error("clientAborted(ctx.Canceled) = false, want true")
		}
	})
	t.Run("deadline exceeded error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if !clientAborted(req, context.DeadlineExceeded) {
			t.Error("clientAborted(DeadlineExceeded) = false, want true")
		}
	})
	t.Run("wrapped ctx canceled via request context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		// Even with an unrelated err arg, request-context-done is
		// the second branch.
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

func contains(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
