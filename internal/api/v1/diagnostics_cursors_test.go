package v1_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// stubCursorsReader returns a fixed slice on ListCursors.
type stubCursorsReader struct {
	rows []timescale.Cursor
	err  error
}

func (s *stubCursorsReader) ListCursors(_ context.Context) ([]timescale.Cursor, error) {
	return s.rows, s.err
}

func mkCursor(source, sub string, ledger uint32, age time.Duration) timescale.Cursor {
	return timescale.Cursor{
		Source:     source,
		Sub:        sub,
		LastLedger: ledger,
		UpdatedAt:  time.Now().UTC().Add(-age),
	}
}

// Happy path: no max_age → all rows returned, lag computed.
func TestHandleCursors_AllRows(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 100, 5*time.Second),
				mkCursor("backfill", "0-1000:soroswap", 50, 7*24*time.Hour),
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Cursor
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 2 {
		t.Errorf("got %d rows, want 2 (no max_age filter)", len(env.Data))
	}
}

// max_age=1h excludes the 7-day-old backfill row.
func TestHandleCursors_MaxAgeExcludesStale(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 100, 5*time.Second),
				mkCursor("backfill", "0-1000:soroswap", 50, 7*24*time.Hour),
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors?max_age=1h")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Cursor
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("got %d rows, want 1 (only the live ledgerstream row)", len(env.Data))
	}
	if env.Data[0].Source != "ledgerstream" {
		t.Errorf("source = %q, want ledgerstream", env.Data[0].Source)
	}
}

// max_age accepts every Go-duration unit operators reach for.
func TestHandleCursors_MaxAgeUnits(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 100, 30*time.Second),
			},
		},
	})
	ts := httpTestServer(t, srv)

	for _, max := range []string{"1m", "60s", "0.001h"} {
		resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors?max_age="+max)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("max_age=%q status = %d, want 200", max, resp.StatusCode)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// Invalid max_age → 400 with the documented type URL.
func TestHandleCursors_InvalidMaxAge(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{rows: nil},
	})
	ts := httpTestServer(t, srv)

	for _, bad := range []string{"garbage", "0", "-5m"} {
		resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors?max_age="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("max_age=%q: status = %d, want 400", bad, resp.StatusCode)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), "/invalid-max-age") {
			t.Errorf("max_age=%q: body should reference invalid-max-age, got %q", bad, string(body))
		}
	}
}

// ?source=<name> isolates one source — the live indexer cursor
// without the ~50 backfill rows alongside it. Caught from a r1
// audit: the param was being silently ignored, so an operator
// asking for ?source=ledgerstream got everything.
func TestHandleCursors_SourceFilter(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 100, 5*time.Second),
				mkCursor("backfill", "0-1000:soroswap", 50, 1*time.Hour),
				mkCursor("backfill", "1000-2000:soroswap", 75, 30*time.Minute),
			},
		},
	})
	ts := httpTestServer(t, srv)

	// source=ledgerstream → only the live cursor.
	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors?source=ledgerstream")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Cursor
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("source=ledgerstream: got %d rows, want 1", len(env.Data))
	}
	if env.Data[0].Source != "ledgerstream" {
		t.Errorf("Source = %q, want ledgerstream", env.Data[0].Source)
	}

	// source=backfill → only backfill rows.
	resp = mustGet(t, ts.URL+"/v1/diagnostics/cursors?source=backfill")
	env.Data = nil
	json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Data) != 2 {
		t.Errorf("source=backfill: got %d rows, want 2", len(env.Data))
	}
	for _, c := range env.Data {
		if c.Source != "backfill" {
			t.Errorf("Source = %q, want backfill", c.Source)
		}
	}

	// source=unknown → empty array (not 400). Keeps the surface
	// predictable when an operator typos vs. a brand-new source we
	// haven't seen yet.
	resp = mustGet(t, ts.URL+"/v1/diagnostics/cursors?source=does-not-exist")
	env.Data = nil
	json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Data) != 0 {
		t.Errorf("source=does-not-exist: got %d rows, want 0 (empty array)", len(env.Data))
	}
}

// source + max_age compose: only ledgerstream rows AND only fresh.
func TestHandleCursors_SourceAndMaxAgeCompose(t *testing.T) {
	srv := v1.New(v1.Options{
		Cursors: &stubCursorsReader{
			rows: []timescale.Cursor{
				mkCursor("ledgerstream", "", 100, 5*time.Second),  // fresh, kept
				mkCursor("backfill", "stale", 50, 7*24*time.Hour), // wrong source, dropped
				mkCursor("ledgerstream", "old", 75, 24*time.Hour), // right source but stale, dropped
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors?source=ledgerstream&max_age=1h")
	var env struct{ Data []v1.Cursor }
	json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d rows, want 1 (fresh ledgerstream only)", len(env.Data))
	}
	if env.Data[0].LastLedger != 100 {
		t.Errorf("LastLedger = %d, want 100 (the fresh row)", env.Data[0].LastLedger)
	}
}

// 503 when no reader is wired — preserves the legacy contract.
func TestHandleCursors_NoReaderReturns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (no Cursors reader)", resp.StatusCode)
	}
}
