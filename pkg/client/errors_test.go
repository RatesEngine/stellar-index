package client

import (
	"errors"
	"strings"
	"testing"
)

// TestAPIError_StatusPredicates pins each Is* predicate behaviour.
// They're trivial accessors (Status == X) but they're documented
// public API surface that consumers branch on; a regression in
// their boolean polarity would silently break consumer-side
// switch statements.
func TestAPIError_StatusPredicates(t *testing.T) {
	cases := []struct {
		status       int
		notFound     bool
		unauthorized bool
		forbidden    bool
		rateLimited  bool
		serverError  bool
	}{
		{status: 200, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: false},
		{status: 400, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: false},
		{status: 401, notFound: false, unauthorized: true, forbidden: false, rateLimited: false, serverError: false},
		{status: 403, notFound: false, unauthorized: false, forbidden: true, rateLimited: false, serverError: false},
		{status: 404, notFound: true, unauthorized: false, forbidden: false, rateLimited: false, serverError: false},
		{status: 429, notFound: false, unauthorized: false, forbidden: false, rateLimited: true, serverError: false},
		{status: 500, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: true},
		{status: 502, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: true},
		{status: 503, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: true},
		{status: 599, notFound: false, unauthorized: false, forbidden: false, rateLimited: false, serverError: true},
	}
	for _, tc := range cases {
		e := &APIError{Status: tc.status}
		if got := e.IsNotFound(); got != tc.notFound {
			t.Errorf("status=%d IsNotFound=%v, want %v", tc.status, got, tc.notFound)
		}
		if got := e.IsUnauthorized(); got != tc.unauthorized {
			t.Errorf("status=%d IsUnauthorized=%v, want %v", tc.status, got, tc.unauthorized)
		}
		if got := e.IsForbidden(); got != tc.forbidden {
			t.Errorf("status=%d IsForbidden=%v, want %v", tc.status, got, tc.forbidden)
		}
		if got := e.IsRateLimited(); got != tc.rateLimited {
			t.Errorf("status=%d IsRateLimited=%v, want %v", tc.status, got, tc.rateLimited)
		}
		if got := e.IsServerError(); got != tc.serverError {
			t.Errorf("status=%d IsServerError=%v, want %v", tc.status, got, tc.serverError)
		}
	}
}

// TestAPIError_ErrorString covers the Error() rendering — what
// consumers see in log messages and what `errors.Is/As` chains
// surface to humans.
func TestAPIError_ErrorString(t *testing.T) {
	cases := []struct {
		name string
		e    *APIError
		want []string // substrings the rendered Error() must contain
	}{
		{
			name: "minimal — status only",
			e:    &APIError{Status: 503},
			want: []string{"503"},
		},
		{
			name: "status + title",
			e:    &APIError{Status: 404, Title: "Asset not found"},
			want: []string{"404", "Asset not found"},
		},
		{
			name: "status + title + detail",
			e:    &APIError{Status: 400, Title: "Bad Request", Detail: "asset is required"},
			want: []string{"400", "Bad Request", "asset is required"},
		},
		{
			name: "status + request id (no title/detail)",
			e:    &APIError{Status: 500, RequestID: "req-abc-123"},
			want: []string{"500", "request_id=req-abc-123"},
		},
		{
			name: "status + title + detail + request id",
			e:    &APIError{Status: 429, Title: "Rate limited", Detail: "retry in 60s", RequestID: "req-z"},
			want: []string{"429", "Rate limited", "retry in 60s", "request_id=req-z"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.e.Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Error()=%q missing %q", got, want)
				}
			}
		})
	}
}

// TestParseAPIError covers the four shape branches: small non-JSON
// body lands in Detail; oversize non-JSON body is ignored;
// problem+json decodes into all fields; malformed JSON falls
// through to a status-only error with errEmptyJSON's message.
func TestParseAPIError(t *testing.T) {
	t.Run("non-JSON small body lands in Detail", func(t *testing.T) {
		e := parseAPIError(502, "text/plain", []byte("upstream timeout"))
		if e.Status != 502 {
			t.Errorf("Status=%d, want 502", e.Status)
		}
		if e.Detail != "upstream timeout" {
			t.Errorf("Detail=%q, want \"upstream timeout\"", e.Detail)
		}
	})

	t.Run("non-JSON small body trims whitespace", func(t *testing.T) {
		e := parseAPIError(502, "text/plain", []byte("  oops  \n"))
		if e.Detail != "oops" {
			t.Errorf("Detail=%q, want \"oops\"", e.Detail)
		}
	})

	t.Run("non-JSON empty body — Detail empty", func(t *testing.T) {
		e := parseAPIError(502, "text/plain", []byte(""))
		if e.Detail != "" {
			t.Errorf("Detail=%q, want empty", e.Detail)
		}
	})

	t.Run("non-JSON oversize body — Detail empty", func(t *testing.T) {
		// 257-byte body — just past the 256 cap.
		body := make([]byte, 257)
		for i := range body {
			body[i] = 'x'
		}
		e := parseAPIError(502, "text/plain", body)
		if e.Detail != "" {
			t.Errorf("Detail=%q, want empty (oversize body should not leak through)", e.Detail)
		}
	})

	t.Run("problem+json populates all fields", func(t *testing.T) {
		body := []byte(`{
			"type": "https://api.ratesengine.net/errors/missing-asset",
			"title": "Missing asset",
			"status": 400,
			"detail": "asset is required",
			"instance": "/v1/price?quote=fiat:USD",
			"request_id": "req-xyz-789"
		}`)
		e := parseAPIError(400, "application/problem+json", body)
		if e.Status != 400 {
			t.Errorf("Status=%d, want 400", e.Status)
		}
		if e.Type != "https://api.ratesengine.net/errors/missing-asset" {
			t.Errorf("Type=%q", e.Type)
		}
		if e.Title != "Missing asset" {
			t.Errorf("Title=%q", e.Title)
		}
		if e.Detail != "asset is required" {
			t.Errorf("Detail=%q", e.Detail)
		}
		if e.Instance != "/v1/price?quote=fiat:USD" {
			t.Errorf("Instance=%q", e.Instance)
		}
		if e.RequestID != "req-xyz-789" {
			t.Errorf("RequestID=%q", e.RequestID)
		}
	})

	t.Run("application/json (non-problem) also decoded", func(t *testing.T) {
		// parseAPIError treats any json content-type as problem+json
		// candidate. A plain application/json body that happens to
		// have problem+json shape decodes the same.
		body := []byte(`{"title": "Internal","status": 500}`)
		e := parseAPIError(500, "application/json", body)
		if e.Title != "Internal" {
			t.Errorf("Title=%q", e.Title)
		}
	})

	t.Run("malformed JSON — Detail = errEmptyJSON message", func(t *testing.T) {
		e := parseAPIError(500, "application/problem+json", []byte("not json at all"))
		if !strings.Contains(e.Detail, "non-problem+json") {
			t.Errorf("Detail=%q, want errEmptyJSON's message", e.Detail)
		}
	})
}

// TestAPIError_ErrorsAs proves the typed-error pattern documented
// in the APIError doc comment actually works — consumers using
// `errors.As(err, &apiErr)` get the typed pointer back.
func TestAPIError_ErrorsAs(t *testing.T) {
	original := &APIError{Status: 404, Title: "Not found"}
	var wrapped error = original

	var apiErr *APIError
	if !errors.As(wrapped, &apiErr) {
		t.Fatal("errors.As should match *APIError")
	}
	if apiErr.Status != 404 {
		t.Errorf("Status=%d, want 404", apiErr.Status)
	}
}
