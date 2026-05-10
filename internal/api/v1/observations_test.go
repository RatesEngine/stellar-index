package v1_test

import (
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// mkObservationTrade builds a native/fiat:USD trade with a specific
// source and timestamp — fixture builder for observations tests.
func mkObservationTrade(source string, ts time.Time, base, quote int64) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source:      source,
		Ledger:      uint32(ts.Unix() % 1_000_000),
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestObservations_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/observations?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestObservations_RejectsTierParams — ADR-0018 URL discipline:
// granularity (closed-bucket) and window_seconds (tip) must NOT be
// accepted on /v1/observations.
func TestObservations_RejectsTierParams(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	for _, q := range []string{"granularity=1m", "window_seconds=5"} {
		resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&"+q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("query %q → %d, want 400", q, resp.StatusCode)
			continue
		}
		body, _ := readAll(resp)
		if !strings.Contains(body, "invalid-observations-param") {
			t.Errorf("query %q error type missing: %s", q, body)
		}
	}
}

func TestObservations_MissingAsset_Returns400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestObservations_IdentityPair_Returns400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestObservations_UnknownSource400 — `?source=` with a name that
// isn't in the in-memory `external.Registry` returns 400 instead
// of an empty page. The silent-empty-page anti-pattern (a typo
// looking identical to "this source has no trades for the pair")
// sends callers chasing nonexistent data; same fail-fast guard
// added to /v1/markets.
func TestObservations_UnknownSource400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&source=fake-venue")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/unknown-source" {
		t.Errorf("Type = %q", p.Type)
	}
}

// TestObservations_HappyPath_AllSources — every source's most-recent
// observation flows through to the response, ordered as the reader
// returned them. Single-source flag false (multiple sources).
func TestObservations_HappyPath_AllSources(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", now.Add(-2*time.Second), 1, 100),
			mkObservationTrade("phoenix", now.Add(-5*time.Second), 2, 250),
			mkObservationTrade("sdex", now.Add(-1*time.Second), 1, 105),
		},
	}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"source":"soroswap"`,
		`"source":"phoenix"`,
		`"source":"sdex"`,
		`"stale":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
	// Flags{SingleSource:false} ships with `omitempty`, so the
	// negative assertion is "the flag must NOT be present" (its
	// presence would mean single_source=true).
	if strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source should not be set with multiple sources: %s", body)
	}
}

// TestObservations_SourceFilter — ?source=phoenix returns only that
// source's row. Reader receives the filter so the SQL-side narrowing
// happens (tests that the handler forwards it).
func TestObservations_SourceFilter(t *testing.T) {
	now := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", now.Add(-2*time.Second), 1, 100),
			mkObservationTrade("phoenix", now.Add(-5*time.Second), 2, 250),
		},
	}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&source=phoenix")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"source":"phoenix"`) {
		t.Errorf("expected phoenix row: %s", body)
	}
	if strings.Contains(body, `"source":"soroswap"`) {
		t.Errorf("source filter leaked soroswap: %s", body)
	}
	if !strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source not set with one row: %s", body)
	}
	if hist.lastCall.sourceFilter != "phoenix" {
		t.Errorf("reader sourceFilter = %q, want phoenix", hist.lastCall.sourceFilter)
	}
}

// TestObservations_AggregateLatest — collapse to the single newest
// trade across sources. The wire shape stays an array (length 1).
func TestObservations_AggregateLatest(t *testing.T) {
	base := time.Unix(1745000000, 0).UTC()
	hist := &stubHistoryReader{
		observations: []canonical.Trade{
			mkObservationTrade("soroswap", base.Add(-10*time.Second), 1, 100),
			// Newest — should win the collapse.
			mkObservationTrade("phoenix", base.Add(-1*time.Second), 2, 250),
			mkObservationTrade("sdex", base.Add(-5*time.Second), 1, 105),
		},
	}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&aggregate=latest")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"source":"phoenix"`) {
		t.Errorf("collapse didn't pick newest source phoenix: %s", body)
	}
	if strings.Contains(body, `"source":"soroswap"`) || strings.Contains(body, `"source":"sdex"`) {
		t.Errorf("collapse leaked older sources: %s", body)
	}
}

// TestObservations_AggregateInvalid — anything other than "latest"
// or empty is a 400 — the handler rejects unknowns rather than
// silently ignoring (keeps the surface honest).
func TestObservations_AggregateInvalid(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&aggregate=median")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestObservations_EmptyArrayWhenNoData — the pair has no trades.
// Response is 200 + empty array, NOT 404. The contract treats
// "no observations" as a legitimate steady-state outcome, especially
// for divergence-detection callers polling for source coverage.
func TestObservations_EmptyArrayWhenNoData(t *testing.T) {
	hist := &stubHistoryReader{observations: nil}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even on empty pair", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("expected empty data array: %s", body)
	}
	if !strings.Contains(body, `"stale":false`) {
		t.Errorf("stale flag should be false on observations: %s", body)
	}
}

// TestObservations_EmptyHintsTriangulationWhenAvailable exercises
// R-011. /v1/observations is the raw per-source surface (ADR-0018
// Surface 3), so a triangulated pair has no rows to return — but
// the empty result is genuinely confusing when /v1/price WOULD
// have served a value via the same Redis VWAP fallback.
//
// Pre-fix (verified live on r1): an empty `data: []` and
// `triangulated: false` looked indistinguishable from "this pair
// is unpriced", which sent integrators chasing nonexistent data.
// Now the handler probes the triangulation cache when its own
// result is empty + no source filter, and surfaces
// `triangulated: true` so consumers know to query /v1/price for
// the proxied value.
func TestObservations_EmptyHintsTriangulationWhenAvailable(t *testing.T) {
	hist := &stubHistoryReader{observations: nil}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.16800000000000000000",
		isTriangulated: true,
		found:          true,
	}
	srv := v1.New(v1.Options{History: hist, Triangulated: looker})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("expected empty data array: %s", body)
	}
	if !strings.Contains(body, `"triangulated":true`) {
		t.Errorf("expected triangulated=true hint when empty + cache-hit: %s", body)
	}
}

// TestObservations_EmptyDoesNotHintWhenSourceFiltered confirms the
// triangulation hint never fires when the caller asked for a
// specific source. A source-filtered query is asking "did THIS
// venue see a trade?" — answering "no, but a triangulated price
// exists" would be irrelevant noise (triangulated values aren't
// attributable to any single source).
func TestObservations_EmptyDoesNotHintWhenSourceFiltered(t *testing.T) {
	hist := &stubHistoryReader{observations: nil}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.16800000000000000000",
		isTriangulated: true,
		found:          true,
	}
	srv := v1.New(v1.Options{History: hist, Triangulated: looker})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD&source=binance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"triangulated":false`) {
		t.Errorf("source-filtered empty result must keep triangulated=false: %s", body)
	}
}

// TestObservations_InternalError — reader error propagates as 500
// without leaking the underlying message.
func TestObservations_InternalError(t *testing.T) {
	hist := &stubHistoryReader{err: errors.New("hypertable timeout")}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, "hypertable timeout") {
		t.Errorf("internal error leaked: %s", body)
	}
}
