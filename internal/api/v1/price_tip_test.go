package v1_test

import (
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// mkTipTrade builds a simple native/fiat:USD trade with the given
// timestamp, integer base, integer quote, and source. Used to seed
// stubHistoryReader fixtures in tip-window tests.
func mkTipTrade(ts time.Time, base, quote int64, source string) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source:      source,
		Ledger:      1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestPriceTip_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestPriceTip_RejectsGranularity — ADR-0018 URL-discipline rule:
// granularity is a closed-bucket concept; accepting it on the tip
// URL would let a stray query param silently change the consistency
// contract. 400 with a tip-specific error type.
func TestPriceTip_RejectsGranularity(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD&granularity=1m")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "invalid-tip-param") {
		t.Errorf("error type missing: %s", body)
	}
}

func TestPriceTip_MissingAsset_Returns400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceTip_IdentityPair_Returns400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestPriceTip_InvalidWindowSeconds — boundaries on the [1, 60]
// clamp. Anything else is a 400.
func TestPriceTip_InvalidWindowSeconds(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	for _, raw := range []string{"0", "61", "-1", "abc", "9999999999999999999"} {
		resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD&window_seconds="+raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("window_seconds=%q → %d, want 400", raw, resp.StatusCode)
		}
	}
}

// TestPriceTip_WindowVWAP — happy path: history reader returns
// trades inside the rolling window, handler computes a VWAP and the
// response carries price_type="vwap" with the requested
// window_seconds.
func TestPriceTip_WindowVWAP(t *testing.T) {
	now := time.Now().UTC()
	hist := &stubHistoryReader{
		trades: []canonical.Trade{
			// VWAP = (50 + 50) / (5 + 5) = 10
			mkTipTrade(now.Add(-3*time.Second), 5, 50, "soroswap"),
			mkTipTrade(now.Add(-1*time.Second), 5, 50, "soroswap"),
		},
	}
	prices := &stubPriceReader{}
	srv := v1.New(v1.Options{Prices: prices, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD&window_seconds=5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"price_type":"vwap"`,
		`"window_seconds":5`,
		`"sources":["soroswap"]`,
		`"single_source":true`,
		`"stale":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
	// VWAP of (5,50) + (5,50) = 100/10 = 10 → "10.0000000000" at
	// ohlcPriceDigits=10.
	if !strings.Contains(body, `"price":"10.0000000000"`) {
		t.Errorf("VWAP price wrong: %s", body)
	}
}

// TestPriceTip_FallbackWhenWindowEmpty — when the rolling window has
// no trades, the handler falls back to PriceReader.LatestPrice and
// returns whatever shape it gives (price_type="last_trade" in the
// MVP). flags.stale stays FALSE — the fallback is in-contract on
// /v1/price/tip per ADR-0018, even though the same fallback
// triggers stale=true on /v1/price.
func TestPriceTip_FallbackWhenWindowEmpty(t *testing.T) {
	hist := &stubHistoryReader{trades: nil}
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID:    "native",
				Quote:      "fiat:USD",
				Price:      "0.1242",
				PriceType:  "last_trade",
				ObservedAt: time.Unix(1745000000, 0).UTC(),
			},
		},
		sources: map[string][]string{"native/fiat:USD": {"sdex"}},
		// Reader's "stale" bit is set — but on /v1/price/tip it must
		// NOT propagate to the envelope flag (ADR-0018).
		stale: map[string]bool{"native/fiat:USD": true},
	}
	srv := v1.New(v1.Options{Prices: prices, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD&window_seconds=5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"price":"0.1242"`,
		`"price_type":"last_trade"`,
		`"sources":["sdex"]`,
		// Critical: the reader's stale=true is INTENTIONALLY ignored
		// on this surface.
		`"stale":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestPriceTip_FallbackWhenNoHistoryWired — same fallback path
// triggers when the deployment hasn't wired a HistoryReader at all
// (e.g. early bring-up, or PriceReader-only deployments). PriceReader
// alone is sufficient to serve the tip surface; window VWAP is just
// an enrichment when history is available.
func TestPriceTip_FallbackWhenNoHistoryWired(t *testing.T) {
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.55", PriceType: "last_trade"},
		},
	}
	srv := v1.New(v1.Options{Prices: prices})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.55"`) {
		t.Errorf("fallback body missing: %s", body)
	}
}

// TestPriceTip_AliasResolvesXLM pins F-1340 on the tip surface:
// asset=native must resolve a LatestPrice observation published under
// the crypto:XLM alias key (the rolling-window VWAP path being empty),
// exactly like handlePrice's primary read. Pre-fix the tip fallback
// queried the literal form only and 404'd while /v1/price served fresh.
func TestPriceTip_AliasResolvesXLM(t *testing.T) {
	prices := &stubPriceReader{
		// Only the crypto:XLM form is populated; native is absent.
		snapshots: map[string]v1.PriceSnapshot{
			"crypto:XLM/fiat:USD": {
				AssetID: "crypto:XLM", Quote: "fiat:USD",
				Price: "0.55", PriceType: "last_trade",
			},
		},
	}
	srv := v1.New(v1.Options{Prices: prices})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 via crypto:XLM alias", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.55"`) {
		t.Errorf("tip alias body missing price 0.55: %s", body)
	}
}

// TestPriceTip_RedisFallbackForRewrittenPair — when both the
// rolling-window VWAP path AND PriceReader.LatestPrice come up empty
// (typical for an aggregator-rewritten pair like XLM/fiat:USD whose
// literal form isn't in prices_1m), the handler falls through to the
// Redis VWAP cache. Same shape as /v1/price's tryRedisVWAPFallback —
// the two surfaces serve the same underlying data so a customer
// switching between them sees consistent prices.
func TestPriceTip_RedisFallbackForRewrittenPair(t *testing.T) {
	hist := &stubHistoryReader{trades: nil}
	prices := &stubPriceReader{err: v1.ErrPriceNotFound} // CAGG miss
	looker := &stubTriangulatedPriceLooker{
		value:          "0.157384502084",
		isTriangulated: false, // direct rewrite — no marker
		found:          true,
	}
	srv := v1.New(v1.Options{
		Prices:       prices,
		History:      hist,
		Triangulated: looker,
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD&window_seconds=5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — Redis fallback should serve direct rewrites", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.157384502084"`) {
		t.Errorf("Redis-fallback price missing: %s", body)
	}
	// stale stays false on /v1/price/tip per ADR-0018, regardless of
	// which fallback path produced the snapshot.
	if !strings.Contains(body, `"stale":false`) {
		t.Errorf("stale flag wrong: %s", body)
	}
}

// TestPriceTip_StablecoinFiatProxyFallback — when window VWAP +
// LatestPrice + Redis VWAP cache all miss but the operator has
// declared classic USD pegs, the handler rewrites X/fiat:USD to
// X/<peg> at request time. Same shape as /v1/price's
// tryStablecoinFiatProxy fallback (#1217). Without this
// /v1/price/tip?asset=native&quote=fiat:USD 404s out of the box on
// every fresh deployment.
func TestPriceTip_StablecoinFiatProxyFallback(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	hist := &stubHistoryReader{trades: nil}
	prices := &stubPriceReader{
		// Literal native/fiat:USD missing → ErrPriceNotFound.
		// native/<USDC-classic> serves the actual VWAP.
		snapshots: map[string]v1.PriceSnapshot{
			"native/" + usdcClassic.String(): {
				AssetID:    "native",
				Quote:      usdcClassic.String(),
				Price:      "0.1626",
				PriceType:  "vwap",
				ObservedAt: time.Unix(1745000000, 0).UTC(),
			},
		},
		sources: map[string][]string{
			"native/" + usdcClassic.String(): {"sdex"},
		},
	}
	srv := v1.New(v1.Options{
		Prices:            prices,
		History:           hist,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — stablecoin-fiat-proxy fallback should serve", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"price":"0.1626"`,
		`"quote":"fiat:USD"`,
		`"sources":["sdex"]`,
		`"stale":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestPriceTip_HistoryErrorFallsThroughToFallback — a hypertable
// hiccup must NOT take down the tip surface when LatestPrice can
// still serve. The handler logs the error and quietly drops to the
// fallback path.
func TestPriceTip_HistoryErrorFallsThroughToFallback(t *testing.T) {
	hist := &stubHistoryReader{err: errors.New("hypertable bounced")}
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.99", PriceType: "last_trade"},
		},
	}
	srv := v1.New(v1.Options{Prices: prices, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — hypertable error must NOT take down tip", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.99"`) {
		t.Errorf("fallback didn't fire: %s", body)
	}
	if strings.Contains(body, "hypertable bounced") {
		t.Errorf("internal error leaked to client: %s", body)
	}
}

// TestPriceTip_404WhenNothingAvailable — empty window AND
// LatestPrice 404s. Handler returns 404 (the pair has no
// observations at all, not just no recent ones).
func TestPriceTip_404WhenNothingAvailable(t *testing.T) {
	hist := &stubHistoryReader{trades: nil}
	prices := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{Prices: prices, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPriceTip_FallbackInternalError — non-404 error from
// LatestPrice on the fallback path returns 500 + does not leak the
// internal error string.
func TestPriceTip_FallbackInternalError(t *testing.T) {
	hist := &stubHistoryReader{trades: nil}
	prices := &stubPriceReader{err: errors.New("redis exploded")}
	srv := v1.New(v1.Options{Prices: prices, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, "redis exploded") {
		t.Errorf("internal error leaked: %s", body)
	}
}

// TestPriceTip_DivergenceFlagPropagates — divergence is asset-level
// and applies on the tip surface (ADR-0018 only excludes Frozen, not
// DivergenceWarning). Verifies the flag fires through both branches:
// the rolling-window VWAP and the LatestPrice fallback.
func TestPriceTip_DivergenceFlagPropagates(t *testing.T) {
	now := time.Now().UTC()

	t.Run("window-VWAP branch", func(t *testing.T) {
		hist := &stubHistoryReader{
			trades: []canonical.Trade{mkTipTrade(now.Add(-2*time.Second), 1, 1, "soroswap")},
		}
		prices := &stubPriceReader{}
		div := &stubDivergenceLooker{firing: true}
		srv := v1.New(v1.Options{Prices: prices, History: hist, Divergence: div})
		ts := startHTTPTest(t, srv.Handler())

		resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
		body, _ := readAll(resp)
		if !strings.Contains(body, `"divergence_warning":true`) {
			t.Errorf("divergence flag not set on window-VWAP branch: %s", body)
		}
	})

	t.Run("fallback branch", func(t *testing.T) {
		prices := &stubPriceReader{
			snapshots: map[string]v1.PriceSnapshot{
				"native/fiat:USD": {Price: "0.5", PriceType: "last_trade"},
			},
		}
		div := &stubDivergenceLooker{firing: true}
		srv := v1.New(v1.Options{Prices: prices, Divergence: div})
		ts := startHTTPTest(t, srv.Handler())

		resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
		body, _ := readAll(resp)
		if !strings.Contains(body, `"divergence_warning":true`) {
			t.Errorf("divergence flag not set on fallback branch: %s", body)
		}
	})
}

// TestPriceTip_DefaultWindowIs5s — when window_seconds is omitted,
// the handler uses the ADR's default of 5 seconds.
func TestPriceTip_DefaultWindowIs5s(t *testing.T) {
	now := time.Now().UTC()
	hist := &stubHistoryReader{
		trades: []canonical.Trade{mkTipTrade(now.Add(-1*time.Second), 1, 7, "soroswap")},
	}
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}, History: hist})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"window_seconds":5`) {
		t.Errorf("default window not 5: %s", body)
	}
	// stubHistoryReader.lastCall captures from/to — verify the
	// duration matches a 5s window.
	delta := hist.lastCall.to.Sub(hist.lastCall.from)
	// Tolerate scheduling jitter — should be exactly 5s but the
	// time.Now() in the handler runs after the test's `now` capture
	// so an exact-equality check would be flaky on a slow CI host.
	if delta < 4500*time.Millisecond || delta > 5500*time.Millisecond {
		t.Errorf("TradesInRange window = %v, want ~5s", delta)
	}
}
