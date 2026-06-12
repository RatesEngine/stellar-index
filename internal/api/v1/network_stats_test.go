package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// stubNetworkStatsReader is the in-memory test seam — same pattern
// as the per-handler stubs elsewhere in this package. Captures the
// last-call context for the upstream-error path test.
type stubNetworkStatsReader struct {
	stats timescale.NetworkStats
	err   error
}

func (r *stubNetworkStatsReader) GetNetworkStats(_ context.Context) (timescale.NetworkStats, error) {
	if r.err != nil {
		return timescale.NetworkStats{}, r.err
	}
	return r.stats, nil
}

// TestNetworkStats_503WhenReaderNil pins the "feature-gated reader"
// degradation. /v1/network/stats backs the explorer's home network
// strip, so a 503 is the right signal — the strip can hide rather
// than render zeroes.
func TestNetworkStats_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/network/stats")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestNetworkStats_HappyPath threads a populated stub through the
// handler and pins the wire shape — the explorer's HomeNetworkStrip
// reads these field names verbatim. Volume24hUSD comes through as a
// pointer-to-string per ADR-0003.
func TestNetworkStats_HappyPath(t *testing.T) {
	vol := "3958193034.60"
	reader := &stubNetworkStatsReader{
		stats: timescale.NetworkStats{
			Volume24hUSD:    &vol,
			MarketsCount24h: 22158,
			AssetsIndexed:   86114,
			LatestLedger:    62484113,
		},
	}
	srv := v1.New(v1.Options{NetworkStats: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/network/stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.NetworkStats `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if env.Data.Volume24hUSD == nil || *env.Data.Volume24hUSD != vol {
		t.Errorf("Volume24hUSD = %v, want %q", env.Data.Volume24hUSD, vol)
	}
	if env.Data.MarketsCount24h != 22158 {
		t.Errorf("MarketsCount24h = %d, want 22158", env.Data.MarketsCount24h)
	}
	if env.Data.AssetsIndexed != 86114 {
		t.Errorf("AssetsIndexed = %d, want 86114", env.Data.AssetsIndexed)
	}
	if env.Data.LatestLedger != 62484113 {
		t.Errorf("LatestLedger = %d, want 62484113", env.Data.LatestLedger)
	}
	// Source counts come from the in-memory external.Registry —
	// can't pin exact values (they grow when sources land) but the
	// invariant is exchanges ≤ total and total > 0.
	if env.Data.TotalSources <= 0 {
		t.Errorf("TotalSources = %d, want > 0", env.Data.TotalSources)
	}
	if env.Data.ExchangeSources < 0 || env.Data.ExchangeSources > env.Data.TotalSources {
		t.Errorf("ExchangeSources = %d should be in [0, %d]",
			env.Data.ExchangeSources, env.Data.TotalSources)
	}
}

// TestNetworkStats_NullVolumeOmitted pins the omitempty behaviour:
// when prod has no USD-equivalent trades in the trailing 24h, the
// volume field is absent from the JSON (callers can distinguish
// "no data" from "0").
func TestNetworkStats_NullVolumeOmitted(t *testing.T) {
	reader := &stubNetworkStatsReader{
		stats: timescale.NetworkStats{
			Volume24hUSD:    nil, // no USD-equivalent trades
			MarketsCount24h: 0,
			AssetsIndexed:   86114,
			LatestLedger:    62484113,
		},
	}
	srv := v1.New(v1.Options{NetworkStats: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/network/stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, `"volume_24h_usd"`) {
		t.Errorf("volume_24h_usd should be absent (omitempty), got: %s", body)
	}
}

// TestNetworkStats_ReaderError500 — storage failure surfaces as a
// 500 problem+json so the explorer's 4xx/5xx branch fires rather
// than a confusing "data: null" success. Logged at WARN; not WARN-
// asserted here (test binary doesn't tap the logger).
func TestNetworkStats_ReaderError500(t *testing.T) {
	reader := &stubNetworkStatsReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{NetworkStats: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/network/stats")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "network-stats-error") {
		t.Errorf("error type missing: %s", body)
	}
}
