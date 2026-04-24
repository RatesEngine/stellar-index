//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	externalbinance "github.com/RatesEngine/rates-engine/internal/sources/external/binance"
	externalbitstamp "github.com/RatesEngine/rates-engine/internal/sources/external/bitstamp"
	externalcoingecko "github.com/RatesEngine/rates-engine/internal/sources/external/coingecko"
	externalecb "github.com/RatesEngine/rates-engine/internal/sources/external/ecb"
	externalexchangerates "github.com/RatesEngine/rates-engine/internal/sources/external/exchangeratesapi"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestExternalFleet_EndToEnd is the Phase-2-ingestion closing-
// ceremony test: proves every external-source class (Streamer,
// Poller, aggregator, authority_sanity) is wired end-to-end
// through external.Run → shared consumer.Event channel →
// Timescale inserts → query round-trip.
//
// Five venues covered, one per representative class:
//
//  1. Binance (Streamer, exchange class) — WS aggTrade frames
//     against a scripted httptest WS server.
//  2. Bitstamp (Streamer, exchange class) — proves multi-streamer
//     fan-out; different wire format (live_trades_* channels).
//  3. ExchangeRatesApi (Poller, exchange class, FX) — inverts
//     base→target rates; emits OracleUpdates not Trades.
//  4. CoinGecko (Poller, aggregator class) — divergence-only;
//     emitted OracleUpdates land in oracle_updates with
//     source=coingecko (VWAP exclusion happens at aggregator
//     query time, not at insert).
//  5. ECB (Poller, authority_sanity class) — daily XML; inverts
//     EUR-base to asset-in-EUR form.
//
// The test does NOT exercise Kraken / Coinbase / CoinMarketCap /
// CryptoCompare / Polygon-Forex since they share shape with one
// of the five and would just add runtime without proving
// additional coverage. Their unit tests at the package level
// already pin correctness.
func TestExternalFleet_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)
	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("timescale.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// ─── Mock venues ─────────────────────────────────────────────
	binanceSrv := newBinanceMockWS(t)
	defer binanceSrv.Close()

	bitstampSrv := newBitstampMockWS(t)
	defer bitstampSrv.Close()

	exratesSrv := newExRatesMockREST(t)
	defer exratesSrv.Close()

	coingeckoSrv := newCoinGeckoMockREST(t)
	defer coingeckoSrv.Close()

	ecbSrv := newECBMockREST(t)
	defer ecbSrv.Close()

	// ─── Build connectors pointing at the mocks ─────────────────
	binancePM, _ := externalbinance.DefaultPairs()
	binancePairs, _ := externalbinance.DefaultPairList()
	binanceS := externalbinance.NewStreamer(binancePM)
	binanceS.Endpoint = replaceWSScheme(binanceSrv.URL)

	bitstampPM, _ := externalbitstamp.DefaultPairs()
	bitstampPairs, _ := externalbitstamp.DefaultPairList()
	bitstampS := externalbitstamp.NewStreamer(bitstampPM)
	bitstampS.Endpoint = replaceWSScheme(bitstampSrv.URL)

	exrates, err := externalexchangerates.NewPoller("test_key")
	if err != nil {
		t.Fatalf("NewPoller exrates: %v", err)
	}
	exrates.Endpoint = exratesSrv.URL
	exrates.Interval = 100 * time.Millisecond // accelerate for test

	cg := externalcoingecko.NewPoller()
	cg.Endpoint = coingeckoSrv.URL
	cg.Interval = 100 * time.Millisecond

	ecb := externalecb.NewPoller()
	ecb.Endpoint = ecbSrv.URL
	ecb.Interval = 100 * time.Millisecond

	// Pair list the FX pollers target — G10 subset.
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	gbp, _ := canonical.NewFiatAsset("GBP")
	xlmCrypto, _ := canonical.NewCryptoAsset("XLM")
	btcCrypto, _ := canonical.NewCryptoAsset("BTC")
	eurUSD, _ := canonical.NewPair(eur, usd)
	gbpUSD, _ := canonical.NewPair(gbp, usd)
	xlmUSD, _ := canonical.NewPair(xlmCrypto, usd)
	btcUSD, _ := canonical.NewPair(btcCrypto, usd)

	fxPairs := []canonical.Pair{eurUSD, gbpUSD}
	aggregatorPairs := []canonical.Pair{xlmUSD, btcUSD}

	streamers := []external.StreamerSpec{
		{Streamer: binanceS, Pairs: binancePairs},
		{Streamer: bitstampS, Pairs: bitstampPairs},
	}
	pollers := []external.PollerSpec{
		{Poller: exrates, Pairs: fxPairs},
		{Poller: cg, Pairs: aggregatorPairs},
		{Poller: ecb, Pairs: fxPairs},
	}

	// ─── Drain + persist ─────────────────────────────────────────
	events := make(chan consumer.Event, 128)

	wait, err := external.Run(ctx, streamers, pollers, events, nil)
	if err != nil {
		t.Fatalf("external.Run: %v", err)
	}

	// Persist goroutine — the same shape as cmd/ratesengine-indexer's
	// persistEvents but without panic recovery / obs metrics.
	persistDone := make(chan struct{})
	var insertedTrades, insertedUpdates int
	go func() {
		defer close(persistDone)
		for e := range events {
			switch ev := e.(type) {
			case external.TradeEvent:
				if err := store.InsertTrade(ctx, ev.Trade); err != nil {
					t.Logf("InsertTrade: %v", err)
					continue
				}
				insertedTrades++
			case external.UpdateEvent:
				if err := store.InsertOracleUpdate(ctx, ev.Update); err != nil {
					t.Logf("InsertOracleUpdate: %v", err)
					continue
				}
				insertedUpdates++
			default:
				t.Logf("unhandled event %T", e)
			}
		}
	}()

	// Let the fleet run: 2 seconds is enough for mock streamers to
	// drain their fixture frames and for pollers (100ms interval)
	// to fire 10+ times each.
	time.Sleep(2 * time.Second)
	cancel()
	wait()
	close(events)
	<-persistDone

	// Fresh context for post-drain assertions — the streamer ctx
	// was cancelled to shut down the fleet; using it for SELECTs
	// would surface as "context canceled" in the store layer.
	assertCtx, assertCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer assertCancel()

	// ─── Assertions ─────────────────────────────────────────────
	if insertedTrades < 2 {
		t.Errorf("expected at least 2 trades inserted (Binance + Bitstamp), got %d", insertedTrades)
	}
	if insertedUpdates < 5 {
		// 3 pollers × minimum 1 update each. In practice the FX
		// pollers emit 2 updates (EUR+GBP) and CoinGecko 2
		// (XLM/USD + BTC/USD), so 6+ is typical.
		t.Errorf("expected at least 5 updates inserted (pollers × ~2), got %d", insertedUpdates)
	}

	// Spot-check: Binance XLM/USDT trade landed.
	usdt, _ := canonical.NewCryptoAsset("USDT")
	binancePair, _ := canonical.NewPair(xlmCrypto, usdt)
	binanceTrades, err := store.LatestTradesForPair(assertCtx, binancePair, 10)
	if err != nil {
		t.Fatalf("LatestTradesForPair XLM/USDT: %v", err)
	}
	if len(binanceTrades) == 0 {
		t.Error("expected Binance XLM/USDT trade to have landed")
	}

	// Spot-check: ExchangeRatesApi EUR rate landed.
	exratesLatest, err := store.LatestOracleUpdateForAsset(assertCtx, externalexchangerates.SourceName, eur)
	if err != nil {
		t.Errorf("LatestOracleUpdateForAsset exrates EUR: %v", err)
	} else if exratesLatest.Price.BigInt().Sign() <= 0 {
		t.Errorf("exrates EUR price = %s, expected >0", exratesLatest.Price)
	}

	// Spot-check: ECB USD→EUR anchor landed.
	ecbLatest, err := store.LatestOracleUpdateForAsset(assertCtx, externalecb.SourceName, usd)
	if err != nil {
		t.Errorf("LatestOracleUpdateForAsset ecb USD: %v", err)
	} else if !ecbLatest.Quote.Equal(eur) {
		t.Errorf("ecb USD quote = %+v, expected EUR", ecbLatest.Quote)
	}

	// Spot-check: CoinGecko XLM/USD landed — aggregator class.
	cgLatest, err := store.LatestOracleUpdateForAsset(assertCtx, externalcoingecko.SourceName, xlmCrypto)
	if err != nil {
		t.Errorf("LatestOracleUpdateForAsset coingecko XLM: %v", err)
	} else if cgLatest.Price.BigInt().Sign() <= 0 {
		t.Errorf("coingecko XLM price = %s", cgLatest.Price)
	}

	t.Logf("external-fleet end-to-end: %d trades + %d updates inserted", insertedTrades, insertedUpdates)
}

// ─── Mock servers ───────────────────────────────────────────────

func replaceWSScheme(u string) string {
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}

// newBinanceMockWS returns an httptest server that accepts a
// combined-stream WS connection and writes a single aggTrade frame
// for XLMUSDT before holding the connection open.
func newBinanceMockWS(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "bye") }()
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{
          "stream":"xlmusdt@aggTrade",
          "data":{"e":"aggTrade","E":1745000000000,"s":"XLMUSDT","a":1,"p":"0.17582","q":"152.34","f":1,"l":1,"T":1745000000000,"m":true}
        }`))
		<-r.Context().Done()
	}))
}

// newBitstampMockWS returns an httptest server that accepts the
// Bitstamp subscribe message and replies with one live_trades_xlmusd
// event.
func newBitstampMockWS(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "bye") }()

		// Read all subscribe messages Bitstamp's streamer sends (one per pair).
		// We don't need to ack them; just drain.
		var muSub sync.Mutex
		go func() {
			for {
				_, _, err := conn.Read(r.Context())
				if err != nil {
					muSub.Lock()
					muSub.Unlock()
					return
				}
			}
		}()

		// Wait briefly for subscribe messages to arrive.
		time.Sleep(100 * time.Millisecond)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{
          "event":"trade",
          "channel":"live_trades_xlmusd",
          "data":{
            "id":999,"timestamp":"1745000000","microtimestamp":"1745000000000000",
            "amount":100.0,"amount_str":"100.0","price":0.17580,"price_str":"0.17580",
            "type":0,"buy_order_id":1,"sell_order_id":2
          }
        }`))
		<-r.Context().Done()
	}))
}

// newExRatesMockREST serves ExchangeRatesApi shape.
func newExRatesMockREST(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"success":   true,
			"timestamp": 1745000000,
			"base":      "USD",
			"date":      "2026-04-24",
			"rates":     map[string]any{"EUR": 0.9235, "GBP": 0.7845},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// newCoinGeckoMockREST serves /api/v3/simple/price.
func newCoinGeckoMockREST(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/simple/price") {
			http.NotFound(w, r)
			return
		}
		body := map[string]map[string]float64{
			"stellar": {"usd": 0.17582},
			"bitcoin": {"usd": 50000.0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// newECBMockREST serves the gesmes XML shape.
func newECBMockREST(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube>
    <Cube time="2026-04-23">
      <Cube currency="USD" rate="1.0825"/>
      <Cube currency="GBP" rate="0.8450"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`)
	}))
}
