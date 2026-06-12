package v1_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// stubCursorsReader is declared in diagnostics_cursors_test.go;
// reused here for the F-0095 fix's degraded-on-cursors-error path.

// decodeIngestionEnvelope reads the envelope produced by
// /v1/diagnostics/ingestion. Data is left as json.RawMessage because
// IngestionDiagnostics is large and the tests below only need the
// flag block + a few ledger fields.
type ingestionEnvelope struct {
	Data struct {
		Ledger struct {
			LatestLedger    int64 `json:"latest_ledger"`
			MarketsCount24h int64 `json:"markets_count_24h"`
			AssetsIndexed   int64 `json:"assets_indexed"`
		} `json:"ledger"`
	} `json:"data"`
	Flags struct {
		Stale bool `json:"stale"`
	} `json:"flags"`
}

// TestDiagnosticsIngestion_StaleWhenNetworkStatsErrors pins the
// F-0095 fix: when the underlying GetNetworkStats reader errors
// (e.g. the F-0039 Redis cascade caused a Postgres timeout
// downstream), the snapshot's ledger fields stay at zero — and the
// response MUST flip flags.stale:true rather than serve fresh-
// looking zeros. Pre-fix this returned flags.stale:false with every
// counter at 0; peer endpoints like /v1/network/stats either
// returned the same error as 500 problem+json or, under partial
// availability, served real data — the contradiction was the
// audit's "different storage path" symptom.
func TestDiagnosticsIngestion_StaleWhenNetworkStatsErrors(t *testing.T) {
	reader := &stubNetworkStatsReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{NetworkStats: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/diagnostics/ingestion")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (soft-fail with stale flag)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	var env ingestionEnvelope
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if !env.Flags.Stale {
		t.Errorf("flags.stale = false, want true when GetNetworkStats errors (body=%s)", body)
	}
	if env.Data.Ledger.LatestLedger != 0 {
		t.Errorf("LatestLedger = %d, want 0 on soft-fail", env.Data.Ledger.LatestLedger)
	}
}

// TestDiagnosticsIngestion_StaleWhenReadersNotWired pins the
// degraded-handler path for a cold deployment that never wired
// the readers — same condition /v1/network/stats handles by
// returning 503. The ingestion endpoint composes multiple sub-
// readers so a 503 would be wrong (partial data is still useful);
// instead we mark flags.stale:true to signal the consumer.
func TestDiagnosticsIngestion_StaleWhenReadersNotWired(t *testing.T) {
	// No NetworkStats, no Cursors → both critical fillers no-op.
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/diagnostics/ingestion")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	var env ingestionEnvelope
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if !env.Flags.Stale {
		t.Errorf("flags.stale = false, want true when readers are not wired (body=%s)", body)
	}
}

// TestDiagnosticsIngestion_FreshWhenReadersOK pins the happy path:
// every wired reader returns data → flags.stale:false. Regression
// guard so the fix doesn't over-fire stale:true on healthy
// production responses.
func TestDiagnosticsIngestion_FreshWhenReadersOK(t *testing.T) {
	vol := "3958193034.60"
	netReader := &stubNetworkStatsReader{
		stats: timescale.NetworkStats{
			Volume24hUSD:    &vol,
			MarketsCount24h: 22158,
			AssetsIndexed:   86114,
			LatestLedger:    62484113,
		},
	}
	cursorReader := &stubCursorsReader{rows: []timescale.Cursor{}}
	srv := v1.New(v1.Options{
		NetworkStats: netReader,
		Cursors:      cursorReader,
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/diagnostics/ingestion")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	var env ingestionEnvelope
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if env.Flags.Stale {
		t.Errorf("flags.stale = true, want false when all readers return data (body=%s)", body)
	}
	if env.Data.Ledger.LatestLedger != 62484113 {
		t.Errorf("LatestLedger = %d, want 62484113", env.Data.Ledger.LatestLedger)
	}
}

// TestDiagnosticsIngestion_StaleWhenCursorsErrors pins the second
// critical filler: a Postgres-level error on ListCursors flips
// flags.stale:true even when network stats came through OK. Under
// the F-0039 cascade a partial outage could surface this exact
// shape: ledger fields populated, backfill section empty.
func TestDiagnosticsIngestion_StaleWhenCursorsErrors(t *testing.T) {
	vol := "100.00"
	netReader := &stubNetworkStatsReader{
		stats: timescale.NetworkStats{
			Volume24hUSD: &vol,
			LatestLedger: 1,
		},
	}
	cursorReader := &stubCursorsReader{err: errors.New("cursors broke")}
	srv := v1.New(v1.Options{
		NetworkStats: netReader,
		Cursors:      cursorReader,
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/diagnostics/ingestion")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	var env ingestionEnvelope
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if !env.Flags.Stale {
		t.Errorf("flags.stale = false, want true when ListCursors errors (body=%s)", body)
	}
}
