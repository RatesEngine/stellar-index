package orchestrator

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// mockStore is a hand-controlled Store for deterministic tick tests.
type mockStore struct {
	// trades is the fixture the store returns for every call,
	// regardless of (pair, from, to). Tests set this to simulate
	// whatever window content they're asserting on.
	trades []canonical.Trade
	// returnErr, if set, is returned from TradesInRange — used to
	// exercise the error path without a live Timescale.
	returnErr error
	// calls counts invocations for assertions.
	calls int
}

func (m *mockStore) TradesInRange(ctx context.Context, p canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	m.calls++
	if m.returnErr != nil {
		return nil, m.returnErr
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
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(xlm, usdt)
	return canonical.Trade{
		Source:      "binance",
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
