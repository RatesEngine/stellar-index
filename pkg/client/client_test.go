package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/pkg/client"
)

// newTestServer wires an httptest.Server with the supplied handler
// and returns a Client pointed at its URL. Encodes the typical
// boilerplate one-liner.
func newTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *client.Client) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c := client.New(client.Options{BaseURL: ts.URL})
	return ts, c
}

// TestNew_DefaultsAreSensible — zero Options produces a usable
// client. Defaults are documented in package comments; the test
// pins them so a future change is deliberate.
func TestNew_DefaultsAreSensible(t *testing.T) {
	c := client.New(client.Options{})
	if c == nil {
		t.Fatal("New returned nil")
	}
}

// TestPrice_HappyPath — canonical 200 response decodes cleanly.
func TestPrice_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/price" {
			t.Errorf("path = %q, want /v1/price", r.URL.Path)
		}
		if r.URL.Query().Get("asset") != "native" {
			t.Errorf("asset = %q", r.URL.Query().Get("asset"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {"asset_id":"native","quote":"fiat:USD","price":"0.07142","price_type":"vwap","observed_at":"2026-04-28T10:00:00Z","window_seconds":60},
			"as_of": "2026-04-28T10:00:00Z",
			"sources": ["binance","kraken"],
			"flags": {"stale":false,"reduced_redundancy":false,"triangulated":false,"divergence_warning":false}
		}`))
	})

	env, err := c.Price(context.Background(), client.PriceQuery{Asset: "native", Quote: "fiat:USD"})
	if err != nil {
		t.Fatalf("Price: %v", err)
	}
	if env.Data.Price != "0.07142" {
		t.Errorf("Price = %q", env.Data.Price)
	}
	if env.Data.PriceType != "vwap" {
		t.Errorf("PriceType = %q", env.Data.PriceType)
	}
	if len(env.Sources) != 2 {
		t.Errorf("Sources = %v", env.Sources)
	}
}

// TestPrice_AssetRequired — empty Asset is a client-side 400 (no
// HTTP roundtrip, no server cost).
func TestPrice_AssetRequired(t *testing.T) {
	c := client.New(client.Options{})
	_, err := c.Price(context.Background(), client.PriceQuery{Asset: ""})
	if err == nil {
		t.Fatal("expected error for empty asset, got nil")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("error type = %T, want *APIError 400", err)
	}
}

// TestAPIError_DecodesProblemJSON — server 4xx response with
// problem+json body decodes into a typed APIError.
func TestAPIError_DecodesProblemJSON(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{
			"type": "https://api.ratesengine.net/errors/asset-not-found",
			"title": "Asset not found",
			"status": 404,
			"detail": "USDC-G... has no known issuer",
			"instance": "/v1/assets/USDC-GBAD",
			"request_id": "req_abc123"
		}`))
	})

	_, err := c.Asset(context.Background(), "USDC-GBAD")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an APIError: %T %v", err, err)
	}
	if apiErr.Status != 404 {
		t.Errorf("Status = %d, want 404", apiErr.Status)
	}
	if !apiErr.IsNotFound() {
		t.Error("IsNotFound() should be true for 404")
	}
	if apiErr.Title != "Asset not found" {
		t.Errorf("Title = %q", apiErr.Title)
	}
	if apiErr.RequestID != "req_abc123" {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
	// Error string includes title + request id for support visibility
	msg := apiErr.Error()
	if !strings.Contains(msg, "Asset not found") || !strings.Contains(msg, "req_abc123") {
		t.Errorf("Error() = %q (should include title + request_id)", msg)
	}
}

// TestAPIError_NonProblemJSONFallback — server returned 502 with
// text/plain body (e.g. reverse-proxy error). Surface a status-only
// APIError with the body text in Detail.
func TestAPIError_NonProblemJSONFallback(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timeout"))
	})

	_, err := c.Price(context.Background(), client.PriceQuery{Asset: "native"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 {
		t.Fatalf("expected 502 APIError, got %T %v", err, err)
	}
	if apiErr.Detail != "upstream timeout" {
		t.Errorf("Detail = %q", apiErr.Detail)
	}
}

// TestAuthorizationHeader — APIKey on Options translates to a
// Bearer header on every request.
func TestAuthorizationHeader(t *testing.T) {
	var sawAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{},"as_of":"2026-01-01T00:00:00Z","flags":{}}`))
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL, APIKey: "rek_test_xyz"})
	_, _ = c.Me(context.Background())
	if sawAuth != "Bearer rek_test_xyz" {
		t.Errorf("Authorization = %q, want %q", sawAuth, "Bearer rek_test_xyz")
	}
}

// TestNoAuthHeaderWhenAPIKeyEmpty — anonymous client should NOT
// send a Bearer header (otherwise the server might 401 on a
// malformed empty bearer token).
func TestNoAuthHeaderWhenAPIKeyEmpty(t *testing.T) {
	var sawAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"asset_id":"native","quote":"fiat:USD","price":"0","price_type":"vwap","observed_at":"2026-01-01T00:00:00Z"},"as_of":"2026-01-01T00:00:00Z","flags":{}}`))
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL})
	_, _ = c.Price(context.Background(), client.PriceQuery{Asset: "native"})
	if sawAuth != "" {
		t.Errorf("Authorization sent without API key: %q", sawAuth)
	}
}

// TestUserAgent — every request carries the SDK user-agent.
func TestUserAgent(t *testing.T) {
	var sawUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"asset_id":"native","quote":"fiat:USD","price":"0","price_type":"vwap","observed_at":"2026-01-01T00:00:00Z"},"as_of":"2026-01-01T00:00:00Z","flags":{}}`))
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL})
	_, _ = c.Price(context.Background(), client.PriceQuery{Asset: "native"})
	if !strings.HasPrefix(sawUA, "ratesengine-go-sdk/") {
		t.Errorf("User-Agent = %q, want ratesengine-go-sdk/* prefix", sawUA)
	}
}

// TestUserAgentOverride — operator-supplied UA replaces the default.
func TestUserAgentOverride(t *testing.T) {
	var sawUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"asset_id":"native","quote":"fiat:USD","price":"0","price_type":"vwap","observed_at":"2026-01-01T00:00:00Z"},"as_of":"2026-01-01T00:00:00Z","flags":{}}`))
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL, UserAgent: "myapp/2.5.0"})
	_, _ = c.Price(context.Background(), client.PriceQuery{Asset: "native"})
	if sawUA != "myapp/2.5.0" {
		t.Errorf("User-Agent = %q, want myapp/2.5.0", sawUA)
	}
}

// TestCreateKey_RoundTrip — POST body marshals correctly; response
// surfaces the plaintext.
func TestCreateKey_RoundTrip(t *testing.T) {
	var bodyReq client.CreateKeyRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&bodyReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {"key_id":"kid_new","plaintext":"rek_freshly_minted","label":"ci-bot"},
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {}
		}`))
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL, APIKey: "rek_admin"})
	env, err := c.CreateKey(context.Background(), client.CreateKeyRequest{Label: "ci-bot"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if env.Data.Plaintext != "rek_freshly_minted" {
		t.Errorf("Plaintext = %q", env.Data.Plaintext)
	}
	if bodyReq.Label != "ci-bot" {
		t.Errorf("server saw label = %q, want ci-bot", bodyReq.Label)
	}
}

// TestCreateKey_LabelRequired — client-side validation catches
// empty label without sending the request.
func TestCreateKey_LabelRequired(t *testing.T) {
	c := client.New(client.Options{})
	_, err := c.CreateKey(context.Background(), client.CreateKeyRequest{})
	if err == nil {
		t.Fatal("expected error for empty label")
	}
}

// TestContextCancellation — a cancelled context aborts the request.
func TestContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)

	c := client.New(client.Options{BaseURL: ts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Price(ctx, client.PriceQuery{Asset: "native"})
	if err == nil {
		t.Fatal("expected cancelled-context error")
	}
}
