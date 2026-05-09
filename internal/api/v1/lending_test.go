package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// stubLendingReader is the in-memory test seam.
type stubLendingReader struct {
	pools []timescale.BlendPoolSummary
	err   error
}

func (r *stubLendingReader) ListBlendPools(_ context.Context) ([]timescale.BlendPoolSummary, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.pools, nil
}

// TestLendingPools_EmptyArrayWhenReaderNil — feature-gated reader.
// 200 + empty array (NOT 503) so the explorer's /lending page can
// render an empty state without an error toast.
func TestLendingPools_EmptyArrayWhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("expected `\"data\":[]` in body, got: %s", body)
	}
}

// TestLendingPools_HappyPath threads a populated stub and pins the
// per-row wire shape the explorer's /lending page reads (Protocol
// is hard-coded "blend" today; surfaces in the row regardless of
// what the storage struct's name field says, since the storage
// type doesn't have a Protocol field).
func TestLendingPools_HappyPath(t *testing.T) {
	lastSeen := time.Date(2026, 5, 9, 10, 15, 52, 0, time.UTC)
	reader := &stubLendingReader{
		pools: []timescale.BlendPoolSummary{
			{
				Pool:           "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD",
				Auctions24h:    30,
				AuctionsTotal:  5687,
				UniqueUsers30d: 4,
				LastSeen:       lastSeen,
			},
			{
				Pool:           "CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS",
				Auctions24h:    2,
				AuctionsTotal:  1544,
				UniqueUsers30d: 3,
				LastSeen:       lastSeen.Add(-1 * time.Hour),
			},
		},
	}
	srv := v1.New(v1.Options{Lending: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.LendingPool `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(env.Data) != 2 {
		t.Fatalf("len = %d, want 2 (body=%s)", len(env.Data), body)
	}
	first := env.Data[0]
	if first.Protocol != "blend" {
		t.Errorf("Protocol = %q, want \"blend\" (handler hard-codes per-PR-comment-#1110)", first.Protocol)
	}
	if first.Pool != "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD" {
		t.Errorf("Pool = %q", first.Pool)
	}
	if first.AuctionsTotal != 5687 || first.Auctions24h != 30 {
		t.Errorf("auction counts = (24h=%d, total=%d)", first.Auctions24h, first.AuctionsTotal)
	}
	if first.UniqueUsers30d != 4 {
		t.Errorf("UniqueUsers30d = %d", first.UniqueUsers30d)
	}
	if !first.LastSeen.Equal(lastSeen) {
		t.Errorf("LastSeen = %v, want %v", first.LastSeen, lastSeen)
	}
}

// TestLendingPools_ReaderError500 — storage error surfaces as a 500
// problem+json. The handler logs at ERROR (production grep target).
func TestLendingPools_ReaderError500(t *testing.T) {
	reader := &stubLendingReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{Lending: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestLendingPools_TimeoutReturns503 — the 8s deadline fires when
// the per-pool aggregates take too long. Returns 503 with a
// `lending-timeout` problem type so callers can retry rather than
// treat it as an opaque internal error.
func TestLendingPools_TimeoutReturns503(t *testing.T) {
	reader := &stubLendingReader{err: context.DeadlineExceeded}
	srv := v1.New(v1.Options{Lending: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "lending-timeout") {
		t.Errorf("expected `lending-timeout` problem type in body, got: %s", body)
	}
}

// TestLendingPools_NilSliceFromReaderMarshalsAsEmptyArray —
// regression guard: a reader that returns (nil, nil) shouldn't
// surface as `data: null`. The handler's `make([]LendingPool, 0)`
// path keeps the wire-shape consistent.
func TestLendingPools_NilSliceFromReaderMarshalsAsEmptyArray(t *testing.T) {
	reader := &stubLendingReader{pools: nil}
	srv := v1.New(v1.Options{Lending: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("expected `\"data\":[]`, got: %s", body)
	}
}
