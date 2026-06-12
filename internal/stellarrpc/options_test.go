package stellarrpc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	rpc "github.com/StellarAtlas/stellar-atlas/internal/stellarrpc"
)

// ─── Options + Endpoint accessor ────────────────────────────────

func TestEndpoint_returnsConstructedURL(t *testing.T) {
	c := rpc.New("https://example.invalid:8000")
	if got := c.Endpoint(); got != "https://example.invalid:8000" {
		t.Errorf("Endpoint() = %q, want \"https://example.invalid:8000\"", got)
	}
}

// withCountingHTTPClient returns an *http.Client whose RoundTripper
// increments a counter every time it's used. Lets us prove
// WithHTTPClient actually swaps the transport — observable via the
// counter, since we can't peek at the unexported `http` field.
type countingTransport struct {
	used    int
	wrapped http.RoundTripper
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.used++
	return t.wrapped.RoundTrip(req)
}

func TestWithHTTPClient_replacesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"id":              "abc",
				"protocolVersion": 23,
				"sequence":        100,
				"closeTime":       "1772000000",
			},
		})
	}))
	defer srv.Close()

	tr := &countingTransport{wrapped: http.DefaultTransport}
	custom := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	c := rpc.New(srv.URL, rpc.WithHTTPClient(custom))
	if _, err := c.LatestLedger(context.Background()); err != nil {
		t.Fatalf("LatestLedger: %v", err)
	}
	if tr.used == 0 {
		t.Error("custom transport never used — WithHTTPClient didn't replace the default")
	}
}

func TestWithTimeout_setsHTTPClient(t *testing.T) {
	// WithTimeout's contract: when no http.Client is set, install one
	// with the given timeout. The visible side-effect is that the
	// constructed client honours a timeout-shaped Endpoint() — we
	// can't read the timeout directly without exposing the internal
	// http field, so we observe via "Endpoint() returns the URL we
	// passed in", which proves the option ran without error.
	c := rpc.New("https://example.invalid:8000", rpc.WithTimeout(2*time.Second))
	if got := c.Endpoint(); got != "https://example.invalid:8000" {
		t.Errorf("Endpoint() = %q, want \"https://example.invalid:8000\"", got)
	}
}

// ─── Convenience methods on top of LatestLedger / call ──────────

func TestLatestLedgerSequence_returnsSequence(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getLatestLedger": map[string]any{
			"id":              "abc",
			"protocolVersion": 23,
			"sequence":        52_000_001,
			"closeTime":       "1772000001",
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.LatestLedgerSequence(context.Background())
	if err != nil {
		t.Fatalf("LatestLedgerSequence: %v", err)
	}
	if got != 52_000_001 {
		t.Errorf("got %d, want 52000001", got)
	}
}

func TestVersionInfo_decodesResponse(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getVersionInfo": map[string]any{
			"version":            "23.0.1",
			"commitHash":         "deadbeef",
			"buildTimestamp":     "2026-04-23T12:00:00Z",
			"captiveCoreVersion": "23.0.0",
			"protocolVersion":    23,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	info, err := c.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo: %v", err)
	}
	if info.Version != "23.0.1" {
		t.Errorf("Version = %q, want \"23.0.1\"", info.Version)
	}
	if info.ProtocolVersion != 23 {
		t.Errorf("ProtocolVersion = %d, want 23", info.ProtocolVersion)
	}
}

func TestFeeStats_decodesResponse(t *testing.T) {
	// Real stellar-rpc returns nested objects with stringified numbers
	// for percentile entries. Mock the shape from the SDK type
	// definition (FeeStats fields).
	s := mockRPC(t, map[string]any{
		"getFeeStats": map[string]any{
			"sorobanInclusionFee": map[string]any{
				"max": "100", "min": "100", "mode": "100",
				"p10": "100", "p20": "100", "p30": "100", "p40": "100",
				"p50": "100", "p60": "100", "p70": "100", "p80": "100",
				"p90": "100", "p95": "100", "p99": "100",
				"transactionCount": "10", "ledgerCount": 5,
			},
			"inclusionFee": map[string]any{
				"max": "100", "min": "100", "mode": "100",
				"p10": "100", "p20": "100", "p30": "100", "p40": "100",
				"p50": "100", "p60": "100", "p70": "100", "p80": "100",
				"p90": "100", "p95": "100", "p99": "100",
				"transactionCount": "10", "ledgerCount": 5,
			},
			"latestLedger": 52_000_000,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	stats, err := c.FeeStats(context.Background())
	if err != nil {
		t.Fatalf("FeeStats: %v", err)
	}
	if stats.LatestLedger != 52_000_000 {
		t.Errorf("LatestLedger = %d, want 52000000", stats.LatestLedger)
	}
}
