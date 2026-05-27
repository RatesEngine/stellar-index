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
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// mkSeriesBar builds a fake OHLCSeriesBar for stub fixtures. Volume
// + price strings are formatted exactly as the storage NUMERIC
// passthrough would produce them.
func mkSeriesBar(t time.Time, o, h, l, c, vb, vq string, n int64) v1.OHLCSeriesBar {
	return v1.OHLCSeriesBar{T: t, O: o, H: h, L: l, C: c, VBase: vb, VQuote: vq, N: n}
}

// TestOHLCSeries_ReturnsIntervalsArray — the multi-bar mode wires
// the OHLCSeries reader call and renders the canonical
// {intervals: [...]} wire shape. Pre-fix, /v1/ohlc?interval=...
// returned a single OHLCBar and ignored the param entirely (F-0071).
func TestOHLCSeries_ReturnsIntervalsArray(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	bars := []v1.OHLCSeriesBar{
		mkSeriesBar(t0, "0.16", "0.17", "0.15", "0.165", "1000", "165", 4),
		mkSeriesBar(t0.Add(time.Hour), "0.165", "0.18", "0.16", "0.175", "1200", "200", 5),
	}
	reader := &stubHistoryReader{ohlcBars: bars}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h&limit=24")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Data v1.OHLCSeriesResponse `json:"data"`
	}
	mustDecode(t, resp, &body)
	if got := len(body.Data.Intervals); got != 2 {
		t.Fatalf("len(intervals) = %d, want 2", got)
	}
	if body.Data.Interval != "1h" {
		t.Errorf("interval = %q, want 1h", body.Data.Interval)
	}
	if body.Data.Base != "native" || body.Data.Quote != "fiat:USD" {
		t.Errorf("base/quote = %q/%q, want native/fiat:USD", body.Data.Base, body.Data.Quote)
	}
	if body.Data.Intervals[0].O != "0.16" || body.Data.Intervals[1].C != "0.175" {
		t.Errorf("OHLC values wrong: %+v", body.Data.Intervals)
	}
	if reader.LastInterval() != "1h" {
		t.Errorf("storage call interval = %q, want 1h", reader.LastInterval())
	}
	if reader.LastLimit() != 24 {
		t.Errorf("storage call limit = %d, want 24", reader.LastLimit())
	}
}

// TestOHLCSeries_InvalidInterval400 — unsupported interval values
// 400 with the canonical errors/invalid-interval problem+json.
func TestOHLCSeries_InvalidInterval400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	for _, raw := range []string{"foo", "2h", "1mo", "10s", " 1h"} {
		resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval="+raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("interval=%q: status = %d, want 400", raw, resp.StatusCode)
		}
	}
}

// TestOHLCSeries_LimitTooLarge400 — limit > 1000 / < 1 / non-int
// 400 with errors/limit-too-large.
func TestOHLCSeries_LimitTooLarge400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	for _, raw := range []string{"5000", "0", "-1", "abc", "10001"} {
		resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h&limit="+raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want 400", raw, resp.StatusCode)
		}
	}
}

// TestOHLCSeries_EmptyReturns200WithEmptyIntervals — when the CAGG
// has no closed buckets in the requested window, the series shape
// is still emitted (200, {intervals: []}). Distinct from the
// single-bar 404-on-empty contract.
func TestOHLCSeries_EmptyReturns200WithEmptyIntervals(t *testing.T) {
	reader := &stubHistoryReader{ohlcBars: nil} // zero bars
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty series)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"intervals":[]`) {
		t.Errorf("body missing empty intervals array: %s", body)
	}
}

// TestOHLCSeries_BucketTimestampsAlignedUTC — the handler's
// implicit-`to` snap rounds DOWN to the requested interval's UTC
// boundary so two requests landing in the same closed window
// across regions resolve to identical [from, to). Storage call's
// `to` arg is the snapped value.
func TestOHLCSeries_BucketTimestampsAlignedUTC(t *testing.T) {
	reader := &stubHistoryReader{ohlcBars: []v1.OHLCSeriesBar{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	gotTo := reader.LastTo()
	// `to` must be top-of-hour UTC. The handler snapped now → top-of-hour.
	if gotTo.Minute() != 0 || gotTo.Second() != 0 || gotTo.Nanosecond() != 0 {
		t.Errorf("to not aligned to 1h boundary: %v", gotTo)
	}
	if gotTo.Location() != time.UTC {
		t.Errorf("to not UTC: %v", gotTo.Location())
	}
	// Default limit = 100, so from = to - 100h.
	gotFrom := reader.LastFrom()
	if want := gotTo.Add(-100 * time.Hour); !gotFrom.Equal(want) {
		t.Errorf("from = %v, want %v (to - 100h default limit)", gotFrom, want)
	}
}

// TestOHLCSeries_DailyBoundaryAlignment — same alignment property
// at 1d granularity: `to` snaps to 00:00 UTC.
func TestOHLCSeries_DailyBoundaryAlignment(t *testing.T) {
	reader := &stubHistoryReader{ohlcBars: []v1.OHLCSeriesBar{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1d&limit=7")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	gotTo := reader.LastTo()
	if gotTo.Hour() != 0 || gotTo.Minute() != 0 || gotTo.Second() != 0 {
		t.Errorf("to not aligned to 1d boundary: %v", gotTo)
	}
}

// TestOHLCSeries_ExplicitFromTo — explicit RFC3339 from/to flow
// through verbatim (no clamping); interval validation still
// applies.
func TestOHLCSeries_ExplicitFromTo(t *testing.T) {
	reader := &stubHistoryReader{ohlcBars: []v1.OHLCSeriesBar{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	from := "2026-01-01T00:00:00Z"
	to := "2026-01-02T00:00:00Z"
	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h&from="+from+"&to="+to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	gotFrom, _ := time.Parse(time.RFC3339, from)
	gotTo, _ := time.Parse(time.RFC3339, to)
	if !reader.LastFrom().Equal(gotFrom) {
		t.Errorf("from = %v, want %v", reader.LastFrom(), gotFrom)
	}
	if !reader.LastTo().Equal(gotTo) {
		t.Errorf("to = %v, want %v", reader.LastTo(), gotTo)
	}
}

// TestOHLCSeries_StorageError500 — propagated upstream errors
// (other than ErrUnknownGranularity) render the canonical 500.
func TestOHLCSeries_StorageError500(t *testing.T) {
	reader := &stubHistoryReader{
		ohlcSeriesFn: func(_ context.Context, _ canonical.Pair, _ string, _, _ time.Time, _ int) ([]v1.OHLCSeriesBar, error) {
			return nil, errors.New("storage exploded")
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestOHLCSeries_PreservesSingleBarBackcompat — the single-bar mode
// is reached only when `interval` is absent. With interval set the
// stub's TradesInRange is NEVER called (the series reader fires
// instead). Mirrors the F-0071 back-compat contract: clients that
// haven't migrated still get the single-bar shape, clients passing
// interval get the new series.
func TestOHLCSeries_PreservesSingleBarBackcompat(t *testing.T) {
	// Two readers, two responses:
	//   1. no interval → single-bar mode → TradesInRange called
	//   2. interval=1h → series mode → OHLCSeries called, TradesInRange NOT called
	t0 := time.Unix(1_772_000_000, 0).UTC()
	tradeFixture := []canonical.Trade{mkOHLCTrade(1, 100, t0)}
	reader := &stubHistoryReader{
		trades:   tradeFixture,
		ohlcBars: []v1.OHLCSeriesBar{mkSeriesBar(t0, "0.16", "0.17", "0.15", "0.165", "1000", "165", 4)},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	// 1. No interval → single-bar wire shape.
	resp1 := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("single-bar status = %d", resp1.StatusCode)
	}
	body1, _ := readAll(resp1)
	if !strings.Contains(body1, `"open"`) || strings.Contains(body1, `"intervals"`) {
		t.Errorf("single-bar mode returned wrong shape: %s", body1)
	}

	// 2. With interval → series wire shape.
	resp2 := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d", resp2.StatusCode)
	}
	body2, _ := readAll(resp2)
	if !strings.Contains(body2, `"intervals"`) || strings.Contains(body2, `"trade_count"`) {
		t.Errorf("series mode returned wrong shape: %s", body2)
	}
}

// TestOHLCSeries_AllSupportedIntervals — every valid interval
// reaches the storage layer with the canonical string. Pins the
// enum allow-list against drift.
func TestOHLCSeries_AllSupportedIntervals(t *testing.T) {
	for _, interval := range []string{"1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w"} {
		reader := &stubHistoryReader{ohlcBars: []v1.OHLCSeriesBar{}}
		srv := v1.New(v1.Options{History: reader})
		ts := httpTestServer(t, srv)
		resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval="+interval)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("interval=%s: status = %d", interval, resp.StatusCode)
		}
		if reader.LastInterval() != interval {
			t.Errorf("interval=%s: reader saw %q", interval, reader.LastInterval())
		}
	}
}

// TestOHLCSeries_WireShapeFields — exhaustive field check for the
// wire envelope so subtle JSON-tag changes get flagged.
func TestOHLCSeries_WireShapeFields(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reader := &stubHistoryReader{
		ohlcBars: []v1.OHLCSeriesBar{
			mkSeriesBar(t0, "1.0", "2.0", "0.5", "1.5", "100", "150", 3),
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&interval=1h")
	body, _ := readAll(resp)
	// Series-mode wire field names — CG/CMC parity (`t,o,h,l,c,v_base,v_quote,n`).
	for _, want := range []string{`"t":"`, `"o":"1.0"`, `"h":"2.0"`, `"l":"0.5"`, `"c":"1.5"`, `"v_base":"100"`, `"v_quote":"150"`, `"n":3`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}

	// Confirm the envelope JSON parses cleanly into the typed
	// response struct.
	var env struct {
		Data v1.OHLCSeriesResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Data.Intervals[0].N != 3 {
		t.Errorf("N decoded = %d, want 3", env.Data.Intervals[0].N)
	}
}
