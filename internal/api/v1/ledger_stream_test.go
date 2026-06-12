package v1_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// advancingCursorsReader returns a ledgerstream cursor whose ledger
// increments by 1 on every ListCursors call — simulating the live
// indexer committing a new ledger between producer polls.
type advancingCursorsReader struct {
	mu     sync.Mutex
	ledger uint32
}

func (a *advancingCursorsReader) ListCursors(context.Context) ([]timescale.Cursor, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := timescale.Cursor{
		Source:     "ledgerstream",
		LastLedger: a.ledger,
		UpdatedAt:  time.Now().UTC(),
	}
	a.ledger++
	return []timescale.Cursor{c}, nil
}

// No CursorsReader wired → 503 BEFORE the response switches into SSE
// mode (an SSE body has no way to carry a non-200 status).
func TestLedgerStream_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/ledger/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// Cursors table exists but carries no live ledgerstream row → 503
// pre-flight.
func TestLedgerStream_NoLedgerstreamCursor_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("backfill", "0-1000:soroswap", 999, time.Hour),
			},
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/ledger/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// The initial ledger_update event lands synchronously on connect —
// the client must not have to wait a full poll interval for first
// data.
func TestLedgerStream_InitialEventSynchronous(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 62685000, 3*time.Second),
			},
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/ledger/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if data == "" {
		t.Fatal("no event received within 2s — initial emit failed")
	}
	view := decodeLedgerEvent(t, data)
	if view.LatestLedger != 62685000 {
		t.Errorf("latest_ledger = %d, want 62685000", view.LatestLedger)
	}
}

// As the indexer commits new ledgers, the stream emits a fresh
// ledger_update per advance — the second event must carry a higher
// ledger than the first.
func TestLedgerStream_EmitsOnAdvance(t *testing.T) {
	srv := v1.New(v1.Options{Cursors: &advancingCursorsReader{ledger: 1000}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/ledger/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	first := decodeLedgerEvent(t, readTipStreamEvent(t, br, 2*time.Second))
	second := decodeLedgerEvent(t, readTipStreamEvent(t, br, 4*time.Second))
	if second.LatestLedger <= first.LatestLedger {
		t.Errorf("ledger did not advance: first=%d second=%d",
			first.LatestLedger, second.LatestLedger)
	}
}

// decodeLedgerEvent parses one ledger_update SSE data payload and
// returns the embedded LedgerTipView, failing the test on a
// malformed payload.
func decodeLedgerEvent(t *testing.T, data string) v1.LedgerTipView {
	t.Helper()
	if data == "" {
		t.Fatal("empty SSE data payload")
	}
	var payload struct {
		Data v1.LedgerTipView `json:"data"`
		AsOf time.Time        `json:"as_of"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v\nraw: %s", err, data)
	}
	if payload.AsOf.IsZero() {
		t.Errorf("payload missing as_of: %s", data)
	}
	return payload.Data
}
