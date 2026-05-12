package orchestrator

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// mockStore is a hand-controlled Store for deterministic tick tests.
type mockStore struct {
	// trades is the fixture the store returns for every call,
	// regardless of (pair, from, to). Tests set this to simulate
	// whatever window content they're asserting on.
	trades []canonical.Trade
	// perPair, when set, overrides `trades` with a per-pair
	// fixture. The lookup key is canonical.Pair.String(). Missing
	// pairs return an empty slice (simulating "no trades in this
	// window") rather than a store error.
	perPair map[string][]canonical.Trade
	// perPairErr injects a fetch error for a specific pair — used
	// by the stablecoin-expansion tests to verify that a single
	// backer fetch failing does NOT blow up the whole window.
	perPairErr map[string]error
	// returnErr, if set, is returned from TradesInRange — used to
	// exercise the error path without a live Timescale.
	returnErr error
	// calls counts invocations for assertions.
	calls int
	// callsByPair records each pair that was fetched so
	// expansion tests can assert the full fetch set.
	callsByPair map[string]int
}

func (m *mockStore) TradesInRange(ctx context.Context, p canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	m.calls++
	if m.callsByPair == nil {
		m.callsByPair = make(map[string]int)
	}
	m.callsByPair[p.String()]++
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	if m.perPairErr != nil {
		if err, ok := m.perPairErr[p.String()]; ok {
			return nil, err
		}
	}
	if m.perPair != nil {
		return m.perPair[p.String()], nil
	}
	return m.trades, nil
}

// newTestRedis spins up a miniredis + go-redis client.
func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

// buildTrade constructs a canonical.Trade with the given price +
// volume for a fixed XLM/USDT pair. Keeps each test short.
func buildTrade(t *testing.T, base, quote *big.Int, ts time.Time) canonical.Trade {
	t.Helper()
	return buildTradeFrom(t, "binance", base, quote, ts)
}

// buildTradeFrom is buildTrade with an explicit Source, used by
// class-filter tests that mix exchange / aggregator / oracle rows.
func buildTradeFrom(t *testing.T, source string, base, quote *big.Int, ts time.Time) canonical.Trade {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(xlm, usdt)
	return canonical.Trade{
		Source:      source,
		Ledger:      0,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}
}

func xlmUsdtPair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	p, _ := canonical.NewPair(xlm, usdt)
	return p
}

func TestTick_WritesVWAPKey(t *testing.T) {
	store := &mockStore{
		trades: []canonical.Trade{
			// Two trades: 100 XLM @ 0.17582 and 200 XLM @ 0.17590
			// (at 10^8 scale). VWAP = weighted average.
			buildTrade(t,
				big.NewInt(10_000_000_000), big.NewInt(1_758_200_000),
				time.Now().Add(-2*time.Minute)),
			buildTrade(t,
				big.NewInt(20_000_000_000), big.NewInt(3_518_000_000),
				time.Now().Add(-1*time.Minute)),
		},
	}
	rdb, mr := newTestRedis(t)

	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})

	ctx := context.Background()
	if err := orch.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if store.calls != 1 {
		t.Errorf("store.calls = %d want 1", store.calls)
	}

	// Key shape: vwap:<base>:<quote>:<window-seconds>
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	// Quick sanity check: the value parses as a decimal and is in
	// the expected 0.1758x range.
	if val[:5] != "0.175" {
		t.Errorf("stored VWAP = %q, want prefix 0.175", val)
	}

	stats := orch.Stats()
	if stats.VWAPWrites != 1 {
		t.Errorf("VWAPWrites = %d want 1", stats.VWAPWrites)
	}
}

func TestTick_EmptyWindowSkipsWrite(t *testing.T) {
	store := &mockStore{trades: nil}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})
	ctx := context.Background()
	if err := orch.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// No Redis key should exist.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	if mr.Exists(key) {
		t.Errorf("key %q should not exist after empty window", key)
	}
	if orch.Stats().EmptyWindows != 1 {
		t.Errorf("EmptyWindows = %d want 1", orch.Stats().EmptyWindows)
	}
}

func TestTick_StoreErrorIsPerPairRecoverable(t *testing.T) {
	// One pair returns an error; the orchestrator should count it
	// but not abort the whole tick. With only one pair configured,
	// this means the tick succeeds overall (ticksTotal bumps) but
	// errors increments.
	store := &mockStore{returnErr: context.DeadlineExceeded}
	rdb, _ := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})
	ctx := context.Background()
	if err := orch.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if orch.Stats().Errors != 1 {
		t.Errorf("Errors = %d want 1", orch.Stats().Errors)
	}
	if orch.Stats().VWAPWrites != 0 {
		t.Errorf("VWAPWrites = %d want 0", orch.Stats().VWAPWrites)
	}
}

func TestTick_MultipleWindows(t *testing.T) {
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(1_000_000_000), big.NewInt(175_820_000), time.Now().Add(-time.Minute)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute, 1 * time.Hour, 24 * time.Hour},
	})
	ctx := context.Background()
	if err := orch.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Three keys — one per window.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	for _, secs := range []int{300, 3600, 86400} {
		key := "vwap:" + xlm.String() + ":" + usdt.String() + ":" + intToStr(secs)
		if !mr.Exists(key) {
			t.Errorf("expected key %q", key)
		}
	}
	if orch.Stats().VWAPWrites != 3 {
		t.Errorf("VWAPWrites = %d want 3", orch.Stats().VWAPWrites)
	}
}

func TestTick_NoPairsIsNoOp(t *testing.T) {
	store := &mockStore{}
	rdb, _ := newTestRedis(t)
	orch := New(store, rdb, Config{Pairs: nil})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if store.calls != 0 {
		t.Errorf("store.calls = %d want 0 (no pairs → no fetches)", store.calls)
	}
}

func TestRun_FirstTickFiresImmediately(t *testing.T) {
	// Verify the initial-tick behaviour: Run should invoke Tick
	// once before the ticker's first C fire, so a freshly-launched
	// aggregator has warm keys ASAP.
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(1_000_000_000), big.NewInt(175_820_000), time.Now()),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:    []canonical.Pair{xlmUsdtPair(t)},
		Windows:  []time.Duration{5 * time.Minute},
		Interval: 5 * time.Second, // irrelevant — we cancel before first tick elapses
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = orch.Run(ctx)
		close(done)
	}()

	// Wait briefly for the immediate tick to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	for time.Now().Before(deadline) {
		if mr.Exists(key) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !mr.Exists(key) {
		t.Error("immediate tick did not write Redis key within 500ms")
	}

	cancel()
	<-done
}

func TestFormatRatFixed(t *testing.T) {
	// 1/3 at 4 decimals → 0.3333 (truncated, not rounded).
	r := big.NewRat(1, 3)
	got := formatRatFixed(r, 4)
	if got != "0.3333" {
		t.Errorf("1/3 @4 = %q want 0.3333", got)
	}
	// Integer value round-trips.
	r = big.NewRat(5, 1)
	got = formatRatFixed(r, 2)
	if got != "5.00" {
		t.Errorf("5/1 @2 = %q want 5.00", got)
	}
	// Sub-unit with leading zero in fractional part.
	r = big.NewRat(1, 100)
	got = formatRatFixed(r, 6)
	if got != "0.010000" {
		t.Errorf("1/100 @6 = %q want 0.010000", got)
	}
}

func TestFilterForVWAP_KeepsExchangeClassOnly(t *testing.T) {
	now := time.Now()
	trades := []canonical.Trade{
		buildTradeFrom(t, "binance", big.NewInt(1), big.NewInt(1), now),       // exchange ✓
		buildTradeFrom(t, "coingecko", big.NewInt(2), big.NewInt(2), now),     // aggregator ✗
		buildTradeFrom(t, "coinmarketcap", big.NewInt(3), big.NewInt(3), now), // aggregator ✗
		buildTradeFrom(t, "reflector-dex", big.NewInt(4), big.NewInt(4), now), // oracle ✗
		buildTradeFrom(t, "ecb", big.NewInt(5), big.NewInt(5), now),           // authority_sanity ✗
		buildTradeFrom(t, "kraken", big.NewInt(6), big.NewInt(6), now),        // exchange ✓
		buildTradeFrom(t, "unknown-venue", big.NewInt(7), big.NewInt(7), now), // unregistered → fallback IncludeInVWAP=false ✗
		buildTradeFrom(t, "polygon-forex", big.NewInt(8), big.NewInt(8), now), // exchange ✓ (institutional FX)
	}

	got := filterForVWAP(append([]canonical.Trade(nil), trades...))
	wantSources := []string{"binance", "kraken", "polygon-forex"}
	if len(got) != len(wantSources) {
		t.Fatalf("filterForVWAP: len=%d want %d (%v)", len(got), len(wantSources), got)
	}
	for i, src := range wantSources {
		if got[i].Source != src {
			t.Errorf("filterForVWAP[%d].Source = %q want %q", i, got[i].Source, src)
		}
	}
}

func TestTick_ClassFilter_ExcludesAggregatorAndOracleByDefault(t *testing.T) {
	// Seed a window with trades from three classes at different
	// prices. A no-filter VWAP would skew toward the off-class rows;
	// the default class filter should yield a VWAP computed from the
	// binance row alone (1 XLM → 0.20 USDT).
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			// binance (exchange): 1 XLM @ 0.20 USDT.
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			// coingecko (aggregator): 1 XLM @ 10.00 USDT — excluded.
			buildTradeFrom(t, "coingecko",
				big.NewInt(100_000_000), big.NewInt(1_000_000_000), now.Add(-1*time.Minute)),
			// reflector-dex (oracle): 1 XLM @ 5.00 USDT — excluded.
			buildTradeFrom(t, "reflector-dex",
				big.NewInt(100_000_000), big.NewInt(500_000_000), now.Add(-30*time.Second)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	// Expect VWAP = 0.20 (binance only). A mixed-class VWAP would
	// have landed at (0.20 + 10.00 + 5.00) / 3 ≈ 5.07.
	if val[:4] != "0.20" {
		t.Errorf("class-filtered VWAP = %q, want prefix 0.20 (binance only)", val)
	}
}

func TestTick_DisableClassFilter_IncludesEveryRow(t *testing.T) {
	// Same fixture as above, but with DisableClassFilter=true the
	// aggregator and oracle rows should contribute and the VWAP
	// lands near the 3-row mean rather than binance alone.
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			buildTradeFrom(t, "coingecko",
				big.NewInt(100_000_000), big.NewInt(1_000_000_000), now.Add(-1*time.Minute)),
			buildTradeFrom(t, "reflector-dex",
				big.NewInt(100_000_000), big.NewInt(500_000_000), now.Add(-30*time.Second)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:              []canonical.Pair{xlmUsdtPair(t)},
		Windows:            []time.Duration{5 * time.Minute},
		DisableClassFilter: true,
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	// Equal-volume 3-row VWAP: (0.20 + 10.00 + 5.00) / 3 ≈ 5.0666…
	if val[:4] != "5.06" {
		t.Errorf("disabled-filter VWAP = %q, want prefix 5.06 (all three rows)", val)
	}
}

func TestTick_ClassFilter_EmptyAfterFilterCountsAsEmpty(t *testing.T) {
	// Every row is off-class. Filter should drop them all, yielding
	// the "no trades in window" branch and no Redis write.
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			buildTradeFrom(t, "coingecko",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-1*time.Minute)),
			buildTradeFrom(t, "reflector-dex",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-30*time.Second)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	if mr.Exists(key) {
		t.Errorf("filtered-to-empty window should not write key %q", key)
	}
	if orch.Stats().EmptyWindows != 1 {
		t.Errorf("EmptyWindows = %d want 1 (filtered-empty branch)",
			orch.Stats().EmptyWindows)
	}
}

func TestTick_OutlierFilter_DropsAnomalousPriceRow(t *testing.T) {
	// Three exchange-class XLM/USDT trades at ~0.20 plus one wild
	// outlier at 200.0 (1000× the others). With a 4σ threshold the
	// outlier should be discarded; the resulting VWAP should land
	// near 0.20, not the unfiltered weighted mean.
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-4*time.Minute)),
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-3*time.Minute)),
			buildTradeFrom(t, "kraken",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			// The 200.0-priced anomaly. Same volume as siblings so it
			// can't hide behind a tiny weight.
			buildTradeFrom(t, "kraken",
				big.NewInt(100_000_000), big.NewInt(20_000_000_000), now.Add(-1*time.Minute)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:                 []canonical.Pair{xlmUsdtPair(t)},
		Windows:               []time.Duration{5 * time.Minute},
		OutlierSigmaThreshold: 1.0, // tight enough that the 1000× row falls outside.
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	// Without the outlier filter this would be ≈50.15 — the 200×
	// row dominates. With the filter the three sane rows yield
	// 0.20.
	if val[:4] != "0.20" {
		t.Errorf("outlier-filtered VWAP = %q want prefix 0.20", val)
	}
}

func TestTick_OutlierFilter_ZeroSigmaIsOff(t *testing.T) {
	// Sigma=0 (default) leaves every row in the VWAP. Verify the
	// 200× outlier carries through to the cached value.
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-3*time.Minute)),
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			buildTradeFrom(t, "kraken",
				big.NewInt(100_000_000), big.NewInt(20_000_000_000), now.Add(-1*time.Minute)),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
		// OutlierSigmaThreshold not set → 0 → filter off.
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	key := "vwap:" + xlm.String() + ":" + usdt.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	// Equal-volume mean = (0.20 + 0.20 + 200.00) / 3 = ~66.8.
	if val[:2] == "0." {
		t.Errorf("disabled outlier filter let through small VWAP %q — outlier should still dominate", val)
	}
}

func TestTick_EmitsPrometheusMetrics(t *testing.T) {
	// Snapshot the relevant counters, run a tick that performs one
	// VWAP write + drops one off-class trade, then assert each
	// counter advanced by the expected delta.
	now := time.Now()
	store := &mockStore{
		trades: []canonical.Trade{
			buildTradeFrom(t, "binance",
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			buildTradeFrom(t, "coingecko", // class=aggregator → dropped
				big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-1*time.Minute)),
		},
	}
	rdb, _ := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})

	beforeOK := testutil.ToFloat64(obs.AggregatorTicksTotal.WithLabelValues("ok"))
	beforeWrites := testutil.ToFloat64(obs.AggregatorVWAPWritesTotal)
	beforeDroppedClass := testutil.ToFloat64(obs.AggregatorDroppedTradesTotal.WithLabelValues("class"))

	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := testutil.ToFloat64(obs.AggregatorTicksTotal.WithLabelValues("ok")) - beforeOK; got != 1 {
		t.Errorf("ticks{ok} delta = %v want 1", got)
	}
	if got := testutil.ToFloat64(obs.AggregatorVWAPWritesTotal) - beforeWrites; got != 1 {
		t.Errorf("vwap_writes delta = %v want 1", got)
	}
	if got := testutil.ToFloat64(obs.AggregatorDroppedTradesTotal.WithLabelValues("class")) - beforeDroppedClass; got != 1 {
		t.Errorf("dropped{class} delta = %v want 1 (coingecko row)", got)
	}
}

func TestTick_StoreErrorIncrementsTickErrorOutcome(t *testing.T) {
	store := &mockStore{returnErr: context.DeadlineExceeded}
	rdb, _ := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdtPair(t)},
		Windows: []time.Duration{5 * time.Minute},
	})

	beforeErr := testutil.ToFloat64(obs.AggregatorTicksTotal.WithLabelValues("error"))
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := testutil.ToFloat64(obs.AggregatorTicksTotal.WithLabelValues("error")) - beforeErr; got != 1 {
		t.Errorf("ticks{error} delta = %v want 1", got)
	}
}

// xlmUsdFiatPair builds the XLM/fiat:USD target pair used by the
// stablecoin-expansion tests.
func xlmUsdFiatPair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)
	return p
}

// backerTrade builds a trade for XLM/<stable> at (base, quote)
// volumes with the given source.
func backerTrade(t *testing.T, stable, source string, base, quote *big.Int, ts time.Time) canonical.Trade {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	stableAsset, _ := canonical.NewCryptoAsset(stable)
	pair, _ := canonical.NewPair(xlm, stableAsset)
	return canonical.Trade{
		Source:      source,
		Ledger:      1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}
}

func TestTick_StablecoinExpansion_FetchesAllBackerPairsAndCollapses(t *testing.T) {
	// Populate store with XLM/USDT and XLM/USDC trades only —
	// target is XLM/fiat:USD. Expansion should fetch 6 pairs
	// (direct + 5 backers), and the two populated backers'
	// trades rewrite onto XLM/fiat:USD for VWAP.
	now := time.Now()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	targetPair, _ := canonical.NewPair(xlm, usd)

	store := &mockStore{
		perPair: map[string][]canonical.Trade{
			// 1 XLM @ 0.20 USDT.
			"crypto:XLM/crypto:USDT": {
				backerTrade(t, "USDT", "binance",
					big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			},
			// 1 XLM @ 0.20 USDC (same equal-weight price).
			"crypto:XLM/crypto:USDC": {
				backerTrade(t, "USDC", "coinbase",
					big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-1*time.Minute)),
			},
			// direct XLM/fiat:USD, no FX trades populated here.
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:                     []canonical.Pair{targetPair},
		Windows:                   []time.Duration{5 * time.Minute},
		EnableStablecoinFiatProxy: true,
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// All 6 expanded pairs should have been fetched.
	wantFetched := []string{
		"crypto:XLM/fiat:USD",
		"crypto:XLM/crypto:USDT",
		"crypto:XLM/crypto:USDC",
		"crypto:XLM/crypto:DAI",
		"crypto:XLM/crypto:PYUSD",
		"crypto:XLM/crypto:USDP",
	}
	for _, p := range wantFetched {
		if store.callsByPair[p] == 0 {
			t.Errorf("expected TradesInRange call for %q (got %d)", p, store.callsByPair[p])
		}
	}

	// VWAP should have been written under the target pair's key
	// (fiat:USD quote), not the original backers' keys.
	key := "vwap:" + xlm.String() + ":" + usd.String() + ":300"
	val, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis Get %q: %v", key, err)
	}
	if val[:4] != "0.20" {
		t.Errorf("collapsed VWAP = %q want prefix 0.20", val)
	}

	// No backer-pair-keyed Redis entry should exist — the expansion
	// funnels everything onto the target.
	for _, backer := range []string{"crypto:USDT", "crypto:USDC"} {
		badKey := "vwap:crypto:XLM:" + backer + ":300"
		if mr.Exists(badKey) {
			t.Errorf("unexpected backer-keyed VWAP %q", badKey)
		}
	}
}

func TestTick_StablecoinExpansion_SingleBackerFetchFailureDoesNotAbortWindow(t *testing.T) {
	// Simulate USDC backer throwing — the direct pair and other
	// backers should still fetch, the good trades should land.
	now := time.Now()
	store := &mockStore{
		perPair: map[string][]canonical.Trade{
			"crypto:XLM/crypto:USDT": {
				backerTrade(t, "USDT", "binance",
					big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-2*time.Minute)),
			},
		},
		perPairErr: map[string]error{
			"crypto:XLM/crypto:USDC": errors.New("simulated timescale timeout"),
		},
	}
	rdb, mr := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:                     []canonical.Pair{xlmUsdFiatPair(t)},
		Windows:                   []time.Duration{5 * time.Minute},
		EnableStablecoinFiatProxy: true,
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	key := "vwap:" + xlm.String() + ":" + usd.String() + ":300"
	if !mr.Exists(key) {
		t.Error("VWAP key missing — USDC fetch error should not have aborted the window")
	}
}

func TestTick_StablecoinExpansion_DisabledFetchesOnlyDirectPair(t *testing.T) {
	// With the flag off the orchestrator should issue one fetch
	// per configured pair, regardless of any backer rows in the
	// store.
	now := time.Now()
	store := &mockStore{
		perPair: map[string][]canonical.Trade{
			"crypto:XLM/fiat:USD": {
				buildTradeFrom(t, "exchangeratesapi",
					big.NewInt(100_000_000), big.NewInt(20_000_000), now.Add(-1*time.Minute)),
			},
			"crypto:XLM/crypto:USDT": {
				backerTrade(t, "USDT", "binance",
					big.NewInt(100_000_000), big.NewInt(20_000_000), now),
			},
		},
	}
	rdb, _ := newTestRedis(t)
	orch := New(store, rdb, Config{
		Pairs:   []canonical.Pair{xlmUsdFiatPair(t)},
		Windows: []time.Duration{5 * time.Minute},
		// EnableStablecoinFiatProxy: false (zero value).
	})
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := store.callsByPair["crypto:XLM/fiat:USD"]; got != 1 {
		t.Errorf("direct-pair calls = %d want 1", got)
	}
	if got := store.callsByPair["crypto:XLM/crypto:USDT"]; got != 0 {
		t.Errorf("USDT backer calls = %d want 0 (expansion disabled)", got)
	}
}

// intToStr avoids pulling strconv into the test's import list for
// a single use — matching the style in the package's helpers.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ─── Anomaly evaluator + freeze writer ─────────────────────────────

// recordingFreezeMarker captures Mark calls so tests can assert on
// them without spinning up Redis. Implements FreezeMarker.
type recordingFreezeMarker struct {
	marks []recordedMark
	err   error
}

type recordedMark struct {
	asset       canonical.Asset
	quote       canonical.Asset
	frozenValue string
	decision    anomaly.Decision
}

func (r *recordingFreezeMarker) Mark(_ context.Context, asset, quote canonical.Asset, frozenValue string, decision anomaly.Decision) error {
	r.marks = append(r.marks, recordedMark{
		asset:       asset,
		quote:       quote,
		frozenValue: frozenValue,
		decision:    decision,
	})
	return r.err
}

// newAnomalyChecker builds a Checker with stablecoin-tight thresholds
// (warn 1%, freeze 2%) so tests can flip between Allow and Freeze
// with small price moves. Asset class for the test pair is forced
// to stablecoin via the classifier override.
func newAnomalyChecker(t *testing.T, pair canonical.Pair) *anomaly.Checker {
	t.Helper()
	thresholds := anomaly.DefaultThresholds()
	thresholds[anomaly.ClassStablecoin] = anomaly.Thresholds{
		WarnPct: 1.0, FreezePct: 2.0,
	}
	classifier := anomaly.NewClassifier(map[string]anomaly.AssetClass{
		pair.Base.String(): anomaly.ClassStablecoin,
	})
	c, err := anomaly.NewChecker(thresholds, classifier)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return c
}

// TestTick_AnomalyAllow_PublishesAndUpdatesPrev — when the decision
// is Allow, VWAP is cached AND the orchestrator's prevVWAPs slot
// updates so the next tick has a comparator.
func TestTick_AnomalyAllow_PublishesAndUpdatesPrev(t *testing.T) {
	pair := xlmUsdtPair(t)
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(10_000_000_000), big.NewInt(1_758_200_000), time.Now()),
		},
	}
	cache, mr := newTestRedis(t)
	checker := newAnomalyChecker(t, pair)
	o := New(store, cache, Config{
		Pairs:   []canonical.Pair{pair},
		Windows: []time.Duration{5 * time.Minute},
		Anomaly: checker,
	})

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	stateKey := pair.String() + ":" + (5 * time.Minute).String()
	if o.prevVWAPs[stateKey] == nil {
		t.Error("prevVWAPs slot should populate on first publish")
	}
	if !mr.Exists("vwap:" + pair.Base.String() + ":" + pair.Quote.String() + ":300") {
		t.Error("VWAP key should be present after Allow")
	}
}

// TestTick_AnomalyFreeze_SkipsCacheAndMarks — when the decision is
// Freeze, the cache is NOT overwritten (previous bucket stays valid)
// and FreezeWriter.Mark is invoked with the decision.
func TestTick_AnomalyFreeze_SkipsCacheAndMarks(t *testing.T) {
	pair := xlmUsdtPair(t)
	cache, mr := newTestRedis(t)
	checker := newAnomalyChecker(t, pair)
	marker := &recordingFreezeMarker{}

	o := New(nil, cache, Config{
		Pairs:        []canonical.Pair{pair},
		Windows:      []time.Duration{5 * time.Minute},
		Anomaly:      checker,
		FreezeWriter: marker,
	})

	// Pre-populate prevVWAPs with $1.00 and pre-write a stale value
	// to cache. The bucket-2 trade prices the asset at ~$2.10 (110%
	// deviation, way above 2% freeze threshold) — single-source so
	// freeze fires.
	stateKey := pair.String() + ":" + (5 * time.Minute).String()
	o.prevVWAPs[stateKey] = big.NewRat(1, 1) // prev = 1.00
	const cacheKey = "vwap:crypto:XLM:crypto:USDT:300"
	cache.Set(context.Background(), cacheKey, "1.000000000000", time.Minute)

	store := &mockStore{
		trades: []canonical.Trade{
			// 1 XLM = ~2.10 USDT — single source.
			buildTrade(t, big.NewInt(100_000_000), big.NewInt(210_000_000), time.Now()),
		},
	}
	o.store = store

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Cache value must NOT have been overwritten.
	got, err := mr.Get(cacheKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "1.000000000000" {
		t.Errorf("cache overwritten despite freeze: got %q, want %q", got, "1.000000000000")
	}

	// FreezeWriter.Mark called once.
	if len(marker.marks) != 1 {
		t.Fatalf("Mark called %d times, want 1", len(marker.marks))
	}
	m := marker.marks[0]
	if !m.decision.IsFrozen() {
		t.Errorf("decision passed to Mark wasn't frozen: %+v", m.decision)
	}

	// F-1228 (audit-2026-05-12): the frozenValue arg must carry
	// the LKG VWAP we're freezing on (the prior bucket's
	// formatRatFixed value). prevVWAPs is preset to big.NewRat(1, 1)
	// → 12-decimal formatted form is "1.000000000000".
	if m.frozenValue != "1.000000000000" {
		t.Errorf("frozenValue = %q, want %q (LKG VWAP at 12 decimals)",
			m.frozenValue, "1.000000000000")
	}

	// prevVWAPs slot stayed at 1.00 — the next tick still compares
	// against the LKG, not the bad reading.
	if o.prevVWAPs[stateKey].Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("prevVWAPs slot moved during freeze; got %s, want 1/1",
			o.prevVWAPs[stateKey].FloatString(6))
	}
}

// TestTick_AnomalyNilChecker_PublishesEverything — when the
// orchestrator has no Anomaly checker wired, every bucket publishes.
// Identical to the pre-anomaly behaviour.
func TestTick_AnomalyNilChecker_PublishesEverything(t *testing.T) {
	pair := xlmUsdtPair(t)
	cache, mr := newTestRedis(t)
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(100_000_000), big.NewInt(210_000_000), time.Now()),
		},
	}
	o := New(store, cache, Config{
		Pairs:   []canonical.Pair{pair},
		Windows: []time.Duration{5 * time.Minute},
		// Anomaly nil — feature off
	})

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !mr.Exists("vwap:crypto:XLM:crypto:USDT:300") {
		t.Error("VWAP must publish when Anomaly is nil")
	}
}

// TestTick_AnomalyFreeze_NilFreezeWriter_StillEmitsMetric — when
// Anomaly is wired but FreezeWriter is nil, freeze is observed (log
// + metric) but no marker is written. API won't see flags.frozen.
// Acceptable degradation; covered for clarity.
func TestTick_AnomalyFreeze_NilFreezeWriter_StillEmitsMetric(t *testing.T) {
	pair := xlmUsdtPair(t)
	cache, _ := newTestRedis(t)
	checker := newAnomalyChecker(t, pair)
	o := New(nil, cache, Config{
		Pairs:   []canonical.Pair{pair},
		Windows: []time.Duration{5 * time.Minute},
		Anomaly: checker,
		// FreezeWriter nil
	})
	stateKey := pair.String() + ":" + (5 * time.Minute).String()
	o.prevVWAPs[stateKey] = big.NewRat(1, 1)

	o.store = &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(100_000_000), big.NewInt(210_000_000), time.Now()),
		},
	}

	before := testutil.ToFloat64(obs.AnomalyFreezeEngagedTotal.WithLabelValues("stablecoin"))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	after := testutil.ToFloat64(obs.AnomalyFreezeEngagedTotal.WithLabelValues("stablecoin"))
	if after-before != 1 {
		t.Errorf("AnomalyFreezeEngagedTotal{stablecoin} delta = %v, want 1", after-before)
	}
}

// TestDistinctSourceCount covers the small helper directly.
func TestDistinctSourceCount(t *testing.T) {
	tests := []struct {
		name   string
		trades []canonical.Trade
		want   int
	}{
		{"empty", nil, 0},
		{
			name: "single source",
			trades: []canonical.Trade{
				buildTradeFrom(t, "binance", big.NewInt(1), big.NewInt(1), time.Now()),
				buildTradeFrom(t, "binance", big.NewInt(1), big.NewInt(1), time.Now()),
			},
			want: 1,
		},
		{
			name: "three distinct",
			trades: []canonical.Trade{
				buildTradeFrom(t, "binance", big.NewInt(1), big.NewInt(1), time.Now()),
				buildTradeFrom(t, "kraken", big.NewInt(1), big.NewInt(1), time.Now()),
				buildTradeFrom(t, "coinbase", big.NewInt(1), big.NewInt(1), time.Now()),
				buildTradeFrom(t, "binance", big.NewInt(1), big.NewInt(1), time.Now()),
			},
			want: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := distinctSourceCount(tc.trades); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMinUSDVolumeApplies — only fiat:USD-quoted pairs are in scope
// for the MinUSDVolume threshold; everything else is exempt because
// the cross-decimal arithmetic across mixed sources doesn't reduce
// to a clean USD figure for non-USD-quoted pairs.
func TestMinUSDVolumeApplies(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	usdt, _ := canonical.NewCryptoAsset("USDT")

	xlmUSD, _ := canonical.NewPair(xlm, usd)
	xlmEUR, _ := canonical.NewPair(xlm, eur)
	xlmUSDT, _ := canonical.NewPair(xlm, usdt)
	if !minUSDVolumeApplies(xlmUSD) {
		t.Error("minUSDVolumeApplies(XLM/fiat:USD) = false, want true")
	}
	if minUSDVolumeApplies(xlmEUR) {
		t.Error("minUSDVolumeApplies(XLM/fiat:EUR) = true, want false")
	}
	if minUSDVolumeApplies(xlmUSDT) {
		t.Error("minUSDVolumeApplies(XLM/USDT) = true, want false (crypto:USDT, not fiat:USD)")
	}
}

// TestWindowUSDVolume — the sum/1e8 conversion gives a clean USD
// figure when the input slice's quote_amount values are at the
// uniform 10^8 external-source convention.
func TestWindowUSDVolume(t *testing.T) {
	// 100 trades at $0.01 each = $1.00 total. quote_amount in 1e8
	// scale: $0.01 = 1_000_000.
	var trades []canonical.Trade
	for i := 0; i < 100; i++ {
		trades = append(trades, buildTrade(t,
			big.NewInt(1_000_000_000), // base (irrelevant to USD-volume)
			big.NewInt(1_000_000),     // quote at 1e8 scale = $0.01
			time.Now(),
		))
	}
	got := windowUSDVolume(trades)
	if got < 0.99 || got > 1.01 {
		t.Errorf("got %f, want ~1.00", got)
	}

	// Empty input → 0.
	if got := windowUSDVolume(nil); got != 0 {
		t.Errorf("empty input: got %f, want 0", got)
	}
}

// TestTick_MinUSDVolumeFilter — the threshold gate rejects a thin
// fiat:USD-quoted window and lets a fat one through. Verifies the
// AggregatorDroppedWindowsTotal{reason="min_usd_volume"} counter
// fires + the EmptyWindows counter increments + no Redis key gets
// written for the rejected window.
func TestTick_MinUSDVolumeFilter(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	pair, _ := canonical.NewPair(xlm, usd)

	// Trade from polygon-forex (FX class, registered IncludeInVWAP=true).
	// 1 trade with quote_amount = 100_000 (= $0.001) — way below $10k.
	mkFXTrade := func(q *big.Int, ts time.Time) canonical.Trade {
		return canonical.Trade{
			Source:      "polygon-forex",
			Ledger:      0,
			TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
			OpIndex:     0,
			Timestamp:   ts,
			Pair:        pair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100_000_000)),
			QuoteAmount: canonical.NewAmount(q),
		}
	}

	t.Run("thin window: rejected", func(t *testing.T) {
		store := &mockStore{trades: []canonical.Trade{mkFXTrade(big.NewInt(100_000), time.Now())}}
		rdb, mr := newTestRedis(t)
		orch := New(store, rdb, Config{
			Pairs:        []canonical.Pair{pair},
			Windows:      []time.Duration{5 * time.Minute},
			MinUSDVolume: 10_000,
		})

		before := testutil.ToFloat64(obs.AggregatorDroppedWindowsTotal.WithLabelValues("min_usd_volume"))
		if err := orch.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		after := testutil.ToFloat64(obs.AggregatorDroppedWindowsTotal.WithLabelValues("min_usd_volume"))
		if after-before != 1 {
			t.Errorf("min_usd_volume drop counter delta = %v, want 1", after-before)
		}
		if orch.Stats().VWAPWrites != 0 {
			t.Errorf("VWAPWrites = %d, want 0 (window should be rejected)", orch.Stats().VWAPWrites)
		}
		key := "vwap:" + xlm.String() + ":" + usd.String() + ":300"
		if mr.Exists(key) {
			t.Errorf("key %q exists after rejection", key)
		}
	})

	t.Run("fat window: published", func(t *testing.T) {
		// Single trade carrying $100k worth of quote_amount.
		store := &mockStore{trades: []canonical.Trade{
			mkFXTrade(big.NewInt(10_000_000_000_000), time.Now()),
		}}
		rdb, mr := newTestRedis(t)
		orch := New(store, rdb, Config{
			Pairs:        []canonical.Pair{pair},
			Windows:      []time.Duration{5 * time.Minute},
			MinUSDVolume: 10_000,
		})

		if err := orch.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orch.Stats().VWAPWrites != 1 {
			t.Errorf("VWAPWrites = %d, want 1", orch.Stats().VWAPWrites)
		}
		key := "vwap:" + xlm.String() + ":" + usd.String() + ":300"
		if !mr.Exists(key) {
			t.Errorf("key %q missing — fat window should publish", key)
		}
	})

	t.Run("filter off (MinUSDVolume=0): thin window publishes", func(t *testing.T) {
		store := &mockStore{trades: []canonical.Trade{mkFXTrade(big.NewInt(100_000), time.Now())}}
		rdb, mr := newTestRedis(t)
		orch := New(store, rdb, Config{
			Pairs:        []canonical.Pair{pair},
			Windows:      []time.Duration{5 * time.Minute},
			MinUSDVolume: 0, // off
		})

		if err := orch.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orch.Stats().VWAPWrites != 1 {
			t.Errorf("VWAPWrites = %d, want 1 (filter off should publish thin window)", orch.Stats().VWAPWrites)
		}
		key := "vwap:" + xlm.String() + ":" + usd.String() + ":300"
		if !mr.Exists(key) {
			t.Errorf("key %q missing — filter is off, window should publish", key)
		}
	})

	// F-1213 (codex audit-2026-05-12) regression: classic USD-pegged
	// proxy trades are at 7-decimal scale, not the off-chain 1e8
	// uniform. Pre-fix, the gate divided every QuoteAmount by 1e8 —
	// a $10k classic-USDC window summed to $1k and was dropped under
	// MinUSDVolume=10000. Post-fix, fetchForTarget computes the USD
	// volume against per-source-pair decimals, and dropForMinUSDVolume
	// honours that pre-computed total.
	t.Run("classic USD-pegged proxy: $10k publishes under min=10000", func(t *testing.T) {
		classicUSDC, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
		if err != nil {
			t.Fatalf("NewClassicAsset: %v", err)
		}
		xlmUSDCPair, _ := canonical.NewPair(xlm, classicUSDC)
		// quote_amount = 100_000_000_000 at 7-dec = $10,000.
		classicUSDCTrade := canonical.Trade{
			Source:      "soroswap", // ClassExchange + IncludeInVWAP=true on Stellar DEX sources
			Ledger:      52_500_000,
			TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
			Timestamp:   time.Now(),
			Pair:        xlmUSDCPair,
			BaseAmount:  canonical.NewAmount(big.NewInt(1_000_000_000_000)),
			QuoteAmount: canonical.NewAmount(big.NewInt(100_000_000_000)),
		}
		store := &mockStore{
			perPair: map[string][]canonical.Trade{
				xlmUSDCPair.String(): {classicUSDCTrade},
				// Direct XLM/fiat:USD: no trades.
				pair.String(): nil,
			},
		}
		rdb, _ := newTestRedis(t)
		orch := New(store, rdb, Config{
			Pairs:                     []canonical.Pair{pair},
			Windows:                   []time.Duration{5 * time.Minute},
			MinUSDVolume:              10_000,
			EnableStablecoinFiatProxy: true,
			USDPeggedClassicAssets:    []canonical.Asset{classicUSDC},
			// soroswap → ClassExchange + IncludeInVWAP by registry default;
			// no class-filter override needed.
		})

		before := testutil.ToFloat64(obs.AggregatorDroppedWindowsTotal.WithLabelValues("min_usd_volume"))
		if err := orch.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		after := testutil.ToFloat64(obs.AggregatorDroppedWindowsTotal.WithLabelValues("min_usd_volume"))
		if after-before != 0 {
			t.Errorf("min_usd_volume drop counter incremented %v times; want 0 — $10k classic USDC at 7-dec should pass the $10k threshold", after-before)
		}
		if orch.Stats().VWAPWrites != 1 {
			t.Errorf("VWAPWrites = %d, want 1 (window should publish)", orch.Stats().VWAPWrites)
		}
	})

	t.Run("non-USD pair: filter exempt", func(t *testing.T) {
		// XLM/EUR — quote is fiat:EUR, NOT fiat:USD. Threshold should
		// not apply; thin window publishes.
		eur, _ := canonical.NewFiatAsset("EUR")
		eurPair, _ := canonical.NewPair(xlm, eur)
		thinTrade := canonical.Trade{
			Source:      "polygon-forex",
			Ledger:      0,
			TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
			Timestamp:   time.Now(),
			Pair:        eurPair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100_000_000)),
			QuoteAmount: canonical.NewAmount(big.NewInt(100_000)),
		}
		store := &mockStore{trades: []canonical.Trade{thinTrade}}
		rdb, mr := newTestRedis(t)
		orch := New(store, rdb, Config{
			Pairs:        []canonical.Pair{eurPair},
			Windows:      []time.Duration{5 * time.Minute},
			MinUSDVolume: 10_000,
		})

		if err := orch.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orch.Stats().VWAPWrites != 1 {
			t.Errorf("VWAPWrites = %d, want 1 (non-USD pair exempt from MinUSDVolume)", orch.Stats().VWAPWrites)
		}
		key := "vwap:" + xlm.String() + ":" + eur.String() + ":300"
		if !mr.Exists(key) {
			t.Errorf("key %q missing — non-USD pair should publish", key)
		}
	})
}

// recordingStreamPublisher captures PublishClosedBucket calls for
// L3.9 fan-out tests. Implements [StreamPublisher].
type recordingStreamPublisher struct {
	calls []recordedPublish
	err   error
}

type recordedPublish struct {
	pair       canonical.Pair
	window     time.Duration
	value      string
	observedAt time.Time
}

func (r *recordingStreamPublisher) PublishClosedBucket(
	_ context.Context,
	pair canonical.Pair,
	window time.Duration,
	valueDecimal string,
	observedAt time.Time,
) error {
	r.calls = append(r.calls, recordedPublish{
		pair: pair, window: window, value: valueDecimal, observedAt: observedAt,
	})
	return r.err
}

// TestTick_StreamPublisher_FiresOnSuccessfulPublish — when the
// orchestrator successfully writes a (pair, window) VWAP to cache,
// it also calls StreamPublisher.PublishClosedBucket once with the
// same pair + window + decimal value.
func TestTick_StreamPublisher_FiresOnSuccessfulPublish(t *testing.T) {
	pair := xlmUsdtPair(t)
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(10_000_000_000), big.NewInt(1_758_200_000), time.Now()),
		},
	}
	cache, _ := newTestRedis(t)
	pub := &recordingStreamPublisher{}
	o := New(store, cache, Config{
		Pairs:           []canonical.Pair{pair},
		Windows:         []time.Duration{5 * time.Minute},
		StreamPublisher: pub,
	})

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("PublishClosedBucket called %d times, want 1", len(pub.calls))
	}
	c := pub.calls[0]
	if c.pair != pair {
		t.Errorf("pair = %v, want %v", c.pair, pair)
	}
	if c.window != 5*time.Minute {
		t.Errorf("window = %v, want 5m", c.window)
	}
	if c.value == "" {
		t.Error("value should be a non-empty decimal string")
	}
}

// TestTick_StreamPublisher_NotCalledOnFreeze — a freeze decision
// skips the cache write; the StreamPublisher must NOT fire either,
// since no closed-bucket event is being published.
func TestTick_StreamPublisher_NotCalledOnFreeze(t *testing.T) {
	pair := xlmUsdtPair(t)
	cache, _ := newTestRedis(t)
	checker := newAnomalyChecker(t, pair)
	pub := &recordingStreamPublisher{}

	o := New(nil, cache, Config{
		Pairs:           []canonical.Pair{pair},
		Windows:         []time.Duration{5 * time.Minute},
		Anomaly:         checker,
		FreezeWriter:    &recordingFreezeMarker{},
		StreamPublisher: pub,
	})
	stateKey := pair.String() + ":" + (5 * time.Minute).String()
	o.prevVWAPs[stateKey] = big.NewRat(1, 1)
	o.store = &mockStore{
		trades: []canonical.Trade{
			// 110% deviation, single-source → freeze fires.
			buildTrade(t, big.NewInt(100_000_000), big.NewInt(210_000_000), time.Now()),
		},
	}

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(pub.calls) != 0 {
		t.Errorf("PublishClosedBucket called %d times during freeze, want 0", len(pub.calls))
	}
}

// TestTick_StreamPublisher_ErrorDoesNotPropagate — Publish returning
// an error logs + metric but never fails the tick (the VWAP cache
// write is the source of truth; the stream is enrichment).
func TestTick_StreamPublisher_ErrorDoesNotPropagate(t *testing.T) {
	pair := xlmUsdtPair(t)
	store := &mockStore{
		trades: []canonical.Trade{
			buildTrade(t, big.NewInt(10_000_000_000), big.NewInt(1_758_200_000), time.Now()),
		},
	}
	cache, mr := newTestRedis(t)
	pub := &recordingStreamPublisher{err: errors.New("redis down")}
	o := New(store, cache, Config{
		Pairs:           []canonical.Pair{pair},
		Windows:         []time.Duration{5 * time.Minute},
		StreamPublisher: pub,
	})

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick should swallow Publish errors: %v", err)
	}
	// Cache write still happened — Publish failure is non-blocking.
	if !mr.Exists("vwap:" + pair.Base.String() + ":" + pair.Quote.String() + ":300") {
		t.Error("VWAP key should be present even when stream publish fails")
	}
}
