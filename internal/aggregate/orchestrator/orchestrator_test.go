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
