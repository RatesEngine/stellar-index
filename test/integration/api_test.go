//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestAPI_EndToEnd is the first integration test that proves the
// HTTP query path works end-to-end:
//
//	Timescale → Store.TradesInRange → v1.HistoryReader adapter
//	  → /v1/history + /v1/vwap + /v1/ohlc + /v1/markets handlers
//
// Catches regressions where unit-test stubs mask real storage /
// schema / adapter drift. Builds the same stack the ratesengine-api
// binary builds (minus Redis, rate limit, SEP-1 metadata).
func TestAPI_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed 4 trades of XLM/USDC across a 30-minute window.
	usdc, err := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}
	pair, _ := c.NewPair(c.NativeAsset(), usdc)

	// Anchor trades well in the past to make the from/to window math
	// deterministic regardless of the test's wall clock.
	t0 := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	trades := []c.Trade{
		mkAPITrade(1, t0.Add(0*time.Minute), pair, 1_000_000_000, 12_000_000),
		mkAPITrade(2, t0.Add(10*time.Minute), pair, 1_000_000_000, 12_100_000),
		mkAPITrade(3, t0.Add(20*time.Minute), pair, 1_000_000_000, 12_200_000),
		mkAPITrade(4, t0.Add(30*time.Minute), pair, 1_000_000_000, 12_050_000),
	}
	for _, tr := range trades {
		if err := store.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("InsertTrade: %v", err)
		}
	}

	// Build the same v1.Server the ratesengine-api binary builds —
	// minus the adapters we don't need here.
	srv := v1.New(v1.Options{
		History: apiHistoryAdapter{s: store},
		Markets: apiMarketsAdapter{s: store},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Build the window to cover the seeded trades.
	from := t0.Add(-1 * time.Minute).Format(time.RFC3339)
	to := t0.Add(31 * time.Minute).Format(time.RFC3339)
	pairQS := "base=native&quote=USDC-" + "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	windowQS := "&from=" + from + "&to=" + to

	t.Run("/v1/history", func(t *testing.T) {
		var env struct {
			Data []v1.TradeRow `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/history?"+pairQS+windowQS, &env)
		if len(env.Data) != 4 {
			t.Fatalf("history returned %d rows, want 4", len(env.Data))
		}
		// Must be chronological.
		for i := 1; i < len(env.Data); i++ {
			if !env.Data[i-1].Timestamp.Before(env.Data[i].Timestamp) {
				t.Errorf("history not chronological at i=%d: %v >= %v",
					i, env.Data[i-1].Timestamp, env.Data[i].Timestamp)
			}
		}
	})

	t.Run("/v1/ohlc", func(t *testing.T) {
		var env struct {
			Data v1.OHLCBar `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/ohlc?"+pairQS+windowQS, &env)
		// Base=1e9 means price = quote/1e9. Amounts 12.0M to 12.2M →
		// prices 0.012, 0.0121, 0.0122, 0.01205. Open first, close last.
		if env.Data.Open != "0.0120000000" {
			t.Errorf("Open = %q, want 0.0120000000", env.Data.Open)
		}
		if env.Data.Close != "0.0120500000" {
			t.Errorf("Close = %q, want 0.0120500000", env.Data.Close)
		}
		if env.Data.High != "0.0122000000" {
			t.Errorf("High = %q, want 0.0122000000", env.Data.High)
		}
		if env.Data.Low != "0.0120000000" {
			t.Errorf("Low = %q, want 0.0120000000", env.Data.Low)
		}
		if env.Data.TradeCount != 4 {
			t.Errorf("TradeCount = %d, want 4", env.Data.TradeCount)
		}
	})

	t.Run("/v1/vwap", func(t *testing.T) {
		var env struct {
			Data v1.VWAPResult `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/vwap?"+pairQS+windowQS, &env)
		// VWAP = Σ(Q)/Σ(B) = (12M+12.1M+12.2M+12.05M) / (4×1e9)
		//     = 48_350_000 / 4_000_000_000 = 0.012087500...
		if env.Data.Price != "0.0120875000" {
			t.Errorf("Price = %q, want 0.0120875000", env.Data.Price)
		}
		if env.Data.TradeCount != 4 {
			t.Errorf("TradeCount = %d, want 4", env.Data.TradeCount)
		}
	})

	t.Run("/v1/markets", func(t *testing.T) {
		var env struct {
			Data []v1.Market `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/markets", &env)
		if len(env.Data) != 1 {
			t.Fatalf("expected 1 market, got %d", len(env.Data))
		}
		m := env.Data[0]
		if m.Base != "native" || m.Quote != "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" {
			t.Errorf("market pair mismatch: %+v", m)
		}
	})

	t.Run("/v1/history cursor drain", func(t *testing.T) {
		// Walk the 4 seeded trades with limit=1 to exercise cursor
		// pagination end-to-end. Must return all 4 in chronological
		// order with no duplicates and no losses — this is the path
		// that exercises the full-PK tiebreak (otherwise a page
		// break mid-ledger could drop a row).
		var collected []v1.TradeRow
		seenKeys := map[string]bool{}
		cursor := ""
		for page := 0; page < 10; page++ {
			qs := pairQS + windowQS + "&limit=1"
			if cursor != "" {
				qs += "&cursor=" + cursor
			}
			var env struct {
				Data       []v1.TradeRow `json:"data"`
				Pagination *struct {
					Next string `json:"next"`
				} `json:"pagination"`
			}
			getJSON(t, ts.URL+"/v1/history?"+qs, &env)
			if len(env.Data) == 0 {
				break
			}
			for _, row := range env.Data {
				// Trade.ID()-equivalent uniqueness check.
				key := row.Source + ":" + row.TxHash + ":" +
					rowOpIndexString(row)
				if seenKeys[key] {
					t.Errorf("duplicate row across pages: %s", key)
				}
				seenKeys[key] = true
				collected = append(collected, row)
			}
			if env.Pagination == nil || env.Pagination.Next == "" {
				break
			}
			cursor = env.Pagination.Next
		}
		if len(collected) != 4 {
			t.Fatalf("drain returned %d rows, want 4", len(collected))
		}
		for i := 1; i < len(collected); i++ {
			if !collected[i-1].Timestamp.Before(collected[i].Timestamp) {
				t.Errorf("drain not chronological at i=%d", i)
			}
		}
	})

	t.Run("/v1/history empty window → empty array", func(t *testing.T) {
		// Window before all seeded trades → 0 rows.
		emptyFrom := t0.Add(-2 * time.Hour).Format(time.RFC3339)
		emptyTo := t0.Add(-1 * time.Hour).Format(time.RFC3339)
		var env struct {
			Data []v1.TradeRow `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/history?"+pairQS+"&from="+emptyFrom+"&to="+emptyTo, &env)
		if len(env.Data) != 0 {
			t.Errorf("empty window returned %d rows", len(env.Data))
		}
		if env.Data == nil {
			t.Error("empty result must be [] not null")
		}
	})

	// Separate insert + drain for the "multiple trades share
	// (ts, ledger)" case. The previous drain test had trades at
	// distinct (ts, ledger) so the (ts, ledger)-only cursor would
	// pass — but real pagination across high-volume ledgers needs
	// the full-PK tiebreak. Seed a mini-cluster and prove no row
	// is dropped when the page break falls mid-cluster.
	t.Run("/v1/history cursor tiebreak — same (ts, ledger)", func(t *testing.T) {
		sharedTS := t0.Add(45 * time.Minute)
		for nonce := 10; nonce < 13; nonce++ {
			tr := mkAPITrade(nonce, sharedTS, pair, 1_000_000_000, 12_000_000)
			// Force same Ledger so the (ts, ledger) pair is shared.
			tr.Ledger = 60_000_000
			if err := store.InsertTrade(ctx, tr); err != nil {
				t.Fatalf("InsertTrade tiebreak trade %d: %v", nonce, err)
			}
		}

		// Narrow window + limit=1 → three separate pages.
		from := sharedTS.Add(-time.Second).Format(time.RFC3339)
		to := sharedTS.Add(time.Second).Format(time.RFC3339)
		seenTxs := map[string]bool{}
		cursor := ""
		for page := 0; page < 5; page++ {
			qs := pairQS + "&from=" + from + "&to=" + to + "&limit=1"
			if cursor != "" {
				qs += "&cursor=" + cursor
			}
			var env struct {
				Data       []v1.TradeRow `json:"data"`
				Pagination *struct {
					Next string `json:"next"`
				} `json:"pagination"`
			}
			getJSON(t, ts.URL+"/v1/history?"+qs, &env)
			if len(env.Data) == 0 {
				break
			}
			for _, row := range env.Data {
				if seenTxs[row.TxHash] {
					t.Errorf("duplicate tx_hash across pages: %s", row.TxHash)
				}
				seenTxs[row.TxHash] = true
			}
			if env.Pagination == nil || env.Pagination.Next == "" {
				break
			}
			cursor = env.Pagination.Next
		}
		if len(seenTxs) != 3 {
			t.Errorf("drain saw %d unique trades, want 3 — PK tiebreak likely broken", len(seenTxs))
		}
	})

	t.Run("/v1/vwap empty window → 404", func(t *testing.T) {
		emptyFrom := t0.Add(-2 * time.Hour).Format(time.RFC3339)
		emptyTo := t0.Add(-1 * time.Hour).Format(time.RFC3339)
		resp, err := http.Get(ts.URL + "/v1/vwap?" + pairQS + "&from=" + emptyFrom + "&to=" + emptyTo)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestAPI_Readyz stands up the same server with a real Timescale
// ReadyChecker and asserts /v1/readyz reports `ok` + the check
// round-trip covers the production Ping path. Before: readyz was
// only unit-tested with stubs — a regression in
// timescale.Store.DB().PingContext (e.g. a driver swap) wouldn't
// be caught here.
func TestAPI_Readyz(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := v1.New(v1.Options{
		ReadyChecks: []v1.ReadyChecker{pgReadyChecker{s: store}},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("readyz status = %d, want 200", resp.StatusCode)
	}

	var env struct {
		Data struct {
			Status string `json:"status"`
			Checks []struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode readyz: %v", err)
	}
	if env.Data.Status != "ok" {
		t.Errorf("status = %q, want ok; env=%+v", env.Data.Status, env.Data)
	}
	found := false
	for _, ch := range env.Data.Checks {
		if ch.Name == "postgres" {
			found = true
			if !ch.OK {
				t.Errorf("postgres check reported not-OK against a live container")
			}
		}
	}
	if !found {
		t.Errorf("postgres check not in readyz response: %+v", env.Data.Checks)
	}
}

// pgReadyChecker mirrors cmd/ratesengine-api/main.go's
// storeChecker so the readyz integration exercises the exact Ping
// path production uses.
type pgReadyChecker struct{ s *timescale.Store }

func (c pgReadyChecker) Name() string                   { return "postgres" }
func (c pgReadyChecker) Ping(ctx context.Context) error { return c.s.DB().PingContext(ctx) }

// TestAPI_OracleLatest stands up the v1 handler against a real
// Timescale with seeded oracle updates and walks the full path:
// InsertOracleUpdate → DISTINCT ON SQL → canonical parse round-
// trip → oracleReadingFrom rendering. Covers the behaviour the
// unit tests can only stub.
func TestAPI_OracleLatest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed two observations of XLM/USDC: reflector-dex older,
	// reflector-cex newer. /v1/oracle/latest returns the most-
	// recent reading per source — so both show up.
	usdc, _ := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	price, _ := new(big.Int).SetString("12420000000000", 10)
	ts := time.Now().UTC().Truncate(time.Second)

	seeds := []c.OracleUpdate{
		{
			Source:    "reflector-dex",
			Ledger:    52_430_001,
			TxHash:    "1111111111111111111111111111111111111111111111111111111111111111",
			OpIndex:   0,
			Timestamp: ts.Add(-1 * time.Minute),
			Asset:     c.NativeAsset(), Quote: usdc,
			Price: c.NewAmount(price), Decimals: 14,
		},
		{
			Source:    "reflector-cex",
			Ledger:    52_430_002,
			TxHash:    "2222222222222222222222222222222222222222222222222222222222222222",
			OpIndex:   0,
			Timestamp: ts,
			Asset:     c.NativeAsset(), Quote: usdc,
			Price: c.NewAmount(price), Decimals: 14,
		},
	}
	for _, u := range seeds {
		if err := store.InsertOracleUpdate(ctx, u); err != nil {
			t.Fatalf("InsertOracleUpdate: %v", err)
		}
	}

	srv := v1.New(v1.Options{Oracle: oracleAdapter{s: store}})
	hts := httptest.NewServer(srv.Handler())
	t.Cleanup(hts.Close)

	// No source filter — expect both sources back.
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	getJSON(t, hts.URL+"/v1/oracle/latest?asset=native", &env)
	if len(env.Data) != 2 {
		t.Fatalf("got %d readings, want 2 (dex + cex)", len(env.Data))
	}
	gotSources := map[string]bool{}
	for _, r := range env.Data {
		gotSources[r.Source] = true
		if r.Price != "0.12420000000000" {
			t.Errorf("source %q price = %q, want 0.12420000000000",
				r.Source, r.Price)
		}
	}
	if !gotSources["reflector-dex"] || !gotSources["reflector-cex"] {
		t.Errorf("missing sources in response: %v", gotSources)
	}

	// With source filter — exactly one reading back.
	env = struct {
		Data []v1.OracleReading `json:"data"`
	}{}
	getJSON(t, hts.URL+"/v1/oracle/latest?asset=native&source=reflector-cex", &env)
	if len(env.Data) != 1 || env.Data[0].Source != "reflector-cex" {
		t.Fatalf("filtered response = %+v", env.Data)
	}
}

type oracleAdapter struct{ s *timescale.Store }

func (a oracleAdapter) LatestOracleUpdatesForAsset(ctx context.Context, asset c.Asset, sourceFilter string) ([]c.OracleUpdate, error) {
	return a.s.LatestOracleUpdatesForAsset(ctx, asset, sourceFilter)
}

// ─── Adapters + helpers ───────────────────────────────────────────

// apiHistoryAdapter mirrors cmd/ratesengine-api/main.go's
// storeHistoryReader so the integration test exercises the same
// code path production does.
type apiHistoryAdapter struct{ s *timescale.Store }

func (r apiHistoryAdapter) TradesInRange(ctx context.Context, pair c.Pair, from, to time.Time, limit int) ([]c.Trade, error) {
	return r.s.TradesInRange(ctx, pair, from, to, limit)
}

func (r apiHistoryAdapter) TradesInRangeAfter(ctx context.Context, pair c.Pair, from, to, afterTs time.Time, afterLedger uint32, afterTxHash, afterSource string, afterOpIndex uint32, limit int) ([]c.Trade, error) {
	return r.s.TradesInRangeAfter(ctx, pair, from, to, afterTs, afterLedger, afterTxHash, afterSource, afterOpIndex, limit)
}

type apiMarketsAdapter struct{ s *timescale.Store }

func (r apiMarketsAdapter) DistinctPairs(ctx context.Context, cursor string, limit int) ([]v1.Market, string, error) {
	rows, next, err := r.s.DistinctPairs(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]v1.Market, len(rows))
	for i, m := range rows {
		out[i] = v1.Market{
			Base:          m.Pair.Base.String(),
			Quote:         m.Pair.Quote.String(),
			LastTradeAt:   m.LastTradeAt,
			TradeCount24h: m.TradeCount24h,
		}
	}
	return out, next, nil
}

func (r apiMarketsAdapter) PairMarket(ctx context.Context, base, quote c.Asset) (v1.Market, bool, error) {
	m, ok, err := r.s.PairMarket(ctx, base, quote)
	if err != nil || !ok {
		return v1.Market{}, ok, err
	}
	return v1.Market{
		Base:          m.Pair.Base.String(),
		Quote:         m.Pair.Quote.String(),
		LastTradeAt:   m.LastTradeAt,
		TradeCount24h: m.TradeCount24h,
	}, true, nil
}

// mkAPITrade builds a Trade with a unique TxHash per (ledger, nonce).
// Reuses the integration-test hex-encoding trick from
// trades_range_test.go — keeps trade IDs distinct so the primary key
// doesn't collide.
func mkAPITrade(nonce int, ts time.Time, pair c.Pair, base, quote int64) c.Trade {
	h := make([]byte, 64)
	for i := range h {
		h[i] = '0'
	}
	const hex = "0123456789abcdef"
	h[62] = hex[(nonce>>4)&0xf]
	h[63] = hex[nonce&0xf]

	return c.Trade{
		Source:      "integ-api",
		Ledger:      uint32(50_000_000 + nonce),
		TxHash:      string(h),
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  c.NewAmount(big.NewInt(base)),
		QuoteAmount: c.NewAmount(big.NewInt(quote)),
	}
}

// rowOpIndexString gives a deterministic key component for the
// cursor-drain dedup check. TradeRow.OpIndex is uint32; we just
// want a stable string form.
func rowOpIndexString(r v1.TradeRow) string {
	return fmt.Sprintf("%d", r.OpIndex)
}

// getJSON fetches URL and decodes the response body into out. The
// body is always the Envelope shape our API serves (`{data: ...}`).
func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d, body: %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %s: %v (body: %s)", url, err, body)
	}
}
