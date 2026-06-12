package v1_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// Happy path: the ledgerstream cursor is projected into a tip view —
// latest_ledger from last_ledger, lag_seconds from the cursor age.
// Backfill rows in the same table must be ignored (non-empty
// sub_source).
func TestHandleLedgerTip_Happy(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 62685226, 4*time.Second),
				mkCursor("backfill", "0-1000:soroswap", 999, 7*24*time.Hour),
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ledger/tip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=2" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=2")
	}

	var env struct {
		Data v1.LedgerTipView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.LatestLedger != 62685226 {
		t.Errorf("latest_ledger = %d, want 62685226", env.Data.LatestLedger)
	}
	// mkCursor backdates UpdatedAt by 4s; allow slack for test runtime.
	if env.Data.LagSeconds < 3 || env.Data.LagSeconds > 9 {
		t.Errorf("lag_seconds = %d, want ~4", env.Data.LagSeconds)
	}
	if env.Data.IngestedAt.IsZero() {
		t.Error("ingested_at is zero")
	}
}

// No CursorsReader wired → 503, not a panic or empty 200.
func TestHandleLedgerTip_NoReader(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ledger/tip")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// The cursors table has rows but none is the live ledgerstream
// cursor (cold start before the indexer's first commit) → 503.
func TestHandleLedgerTip_NoLedgerstreamCursor(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("backfill", "0-1000:soroswap", 999, time.Hour),
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ledger/tip")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
