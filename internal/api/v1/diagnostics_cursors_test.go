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

// 503 when no reader is wired — preserves the legacy contract.
func TestHandleCursors_NoReaderReturns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/diagnostics/cursors")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (no Cursors reader)", resp.StatusCode)
	}
}
