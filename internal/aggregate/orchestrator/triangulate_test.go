package orchestrator

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// fakeFXStore is a minimal in-memory FXStore for unit tests. Records
// the queries it was asked to satisfy and returns canned responses.
type fakeFXStore struct {
	// quote, when non-nil, is returned for every FXQuoteAtOrBefore call.
	quote      *big.Rat
	observedAt time.Time
	source     string
	// err, when non-nil, is returned instead.
	err error
	// calls records each call so tests can assert on `cutoff` plumbing.
	calls []fxCall
}

type fxCall struct {
	pair      canonical.Pair
	cutoff    time.Time
	fxSources []string
}

func (f *fakeFXStore) FXQuoteAtOrBefore(_ context.Context, pair canonical.Pair, cutoff time.Time, fxSources []string) (*big.Rat, time.Time, string, error) {
	f.calls = append(f.calls, fxCall{pair: pair, cutoff: cutoff, fxSources: fxSources})
	if f.err != nil {
		return nil, time.Time{}, "", f.err
	}
	if f.quote == nil {
		return nil, time.Time{}, "", timescale.ErrNoFXQuote
	}
	return new(big.Rat).Set(f.quote), f.observedAt, f.source, nil
}

// helper: build canonical.Pair without test boilerplate.
func mkPair(t *testing.T, baseT, baseCode, quoteT, quoteCode string) canonical.Pair {
	t.Helper()
	mk := func(typ, code string) canonical.Asset {
		t.Helper()
		switch typ {
		case "fiat":
			a, err := canonical.ParseAsset("fiat:" + code)
			if err != nil {
				t.Fatalf("ParseAsset fiat:%s: %v", code, err)
			}
			return a
		case "crypto":
			a, err := canonical.NewCryptoAsset(code)
			if err != nil {
				t.Fatalf("NewCryptoAsset %s: %v", code, err)
			}
			return a
		}
		t.Fatalf("unknown asset type %s", typ)
		return canonical.Asset{}
	}
	p, err := canonical.NewPair(mk(baseT, baseCode), mk(quoteT, quoteCode))
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return p
}

// TestValidateTriangulationChain_HappyPath — well-formed chain
// passes validation.
func TestValidateTriangulationChain_HappyPath(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")

	chain := TriangulationChain{
		Target: xlmEUR,
		Legs:   []canonical.Pair{xlmUSD, usdEUR},
	}
	if err := ValidateTriangulationChain(chain); err != nil {
		t.Errorf("happy path failed: %v", err)
	}
}

// TestValidateTriangulationChain_BadStructure — naming the
// specific violation lets operators correct config without
// guessing.
func TestValidateTriangulationChain_BadStructure(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	xlmGBP := mkPair(t, "crypto", "XLM", "fiat", "GBP")

	tests := []struct {
		name     string
		chain    TriangulationChain
		wantWord string
	}{
		{
			name:     "single-leg chain",
			chain:    TriangulationChain{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD}},
			wantWord: "1 legs",
		},
		{
			name:     "first-leg base mismatch",
			chain:    TriangulationChain{Target: xlmEUR, Legs: []canonical.Pair{usdEUR, xlmUSD}},
			wantWord: "first leg base",
		},
		{
			name:     "last-leg quote mismatch",
			chain:    TriangulationChain{Target: xlmGBP, Legs: []canonical.Pair{xlmUSD, usdEUR}},
			wantWord: "last leg quote",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTriangulationChain(tc.chain)
			if err == nil {
				t.Fatal("expected error; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Errorf("error message missing %q: %v", tc.wantWord, err)
			}
		})
	}
}

// TestTick_Triangulation_HappyPath — all legs cached → orchestrator
// computes the implied target VWAP and writes it to cache.
func TestTick_Triangulation_HappyPath(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	// Pre-populate leg VWAPs as if the per-pair refresh just ran.
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	mr.Set(cachekeys.VWAP(usdEUR.Base, usdEUR.Quote, window), "0.900000000000")

	o := New(nil, cache, Config{
		Pairs:   []canonical.Pair{}, // no per-pair refresh; just exercise the triangulation pass
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
	})

	before := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	after := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	if after-before != 1 {
		t.Errorf("ok counter delta = %v, want 1", after-before)
	}

	// 0.08 × 0.90 = 0.072.
	got, err := mr.Get(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window))
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got != "0.072000000000" {
		t.Errorf("target VWAP = %q, want 0.072000000000", got)
	}
}

// TestTick_Triangulation_MissingLeg — a leg's window was empty so
// the cache key is absent. Outcome counter increments
// missing_leg, target key is NOT written.
func TestTick_Triangulation_MissingLeg(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	// Only first leg cached; second leg absent.
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
	})

	before := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("missing_leg"))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	after := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("missing_leg"))
	if after-before != 1 {
		t.Errorf("missing_leg counter delta = %v, want 1", after-before)
	}

	if mr.Exists(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window)) {
		t.Error("target VWAP should not exist when a leg is missing")
	}
}

// TestTick_Triangulation_ParseError — a malformed cached value
// (Postgres / upstream regression) surfaces as parse_error rather
// than panicking the tick.
func TestTick_Triangulation_ParseError(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	mr.Set(cachekeys.VWAP(usdEUR.Base, usdEUR.Quote, window), "not-a-number")

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
	})

	before := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("parse_error"))
	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	after := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("parse_error"))
	if after-before != 1 {
		t.Errorf("parse_error counter delta = %v, want 1", after-before)
	}
}

// TestIsFXLeg_StructuralPredicate exercises the snap-rule's per-leg
// classification: only fiat-vs-fiat legs (e.g. USD/EUR) qualify.
// Crypto-vs-fiat (XLM/USD) and crypto-vs-crypto (XLM/USDT) stay on
// the cached-VWAP path.
func TestIsFXLeg_StructuralPredicate(t *testing.T) {
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	xlmUSDT := mkPair(t, "crypto", "XLM", "crypto", "USDT")

	if !isFXLeg(usdEUR) {
		t.Error("isFXLeg(USD/EUR) = false; want true (both sides fiat)")
	}
	if isFXLeg(xlmUSD) {
		t.Error("isFXLeg(XLM/USD) = true; want false (crypto base)")
	}
	if isFXLeg(xlmUSDT) {
		t.Error("isFXLeg(XLM/USDT) = true; want false (no fiat side)")
	}
}

// TestTick_Triangulation_FXSnap_HappyPath — when FXStore is wired and
// returns a quote for the FX leg, the orchestrator uses the snap
// price (not the leg's cached VWAP) and bypasses the fallback counter.
// Asserts the bucket-end timestamp passed to FXStore is the most-
// recent UTC-aligned boundary of the window.
func TestTick_Triangulation_FXSnap_HappyPath(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	// Note: NO cached VWAP for usdEUR — proves the snap path is what
	// supplies the FX leg's price.

	fx := &fakeFXStore{
		quote:      new(big.Rat).SetFrac(big.NewInt(90), big.NewInt(100)),
		observedAt: time.Now().UTC().Add(-1 * time.Minute),
		source:     "polygon-forex",
	}

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
		FXStore: fx,
	})

	beforeOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	beforeFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	afterOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	afterFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if afterOK-beforeOK != 1 {
		t.Errorf("ok counter delta = %v, want 1", afterOK-beforeOK)
	}
	if afterFB != beforeFB {
		t.Errorf("fx-snap fallback counter incremented on happy path: %v→%v", beforeFB, afterFB)
	}

	// 0.08 (cached) × 0.90 (snap) = 0.072.
	got, err := mr.Get(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window))
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got != "0.072000000000" {
		t.Errorf("target VWAP = %q, want 0.072000000000", got)
	}

	if len(fx.calls) != 1 {
		t.Fatalf("FXStore called %d times, want 1", len(fx.calls))
	}
	call := fx.calls[0]
	if !call.pair.Equal(usdEUR) {
		t.Errorf("FXStore queried with pair %s, want %s", call.pair, usdEUR)
	}
	// bucketEnd must be window-aligned (Truncate to 5m boundary).
	if !call.cutoff.Equal(call.cutoff.Truncate(window)) {
		t.Errorf("cutoff %v not aligned to %v boundary", call.cutoff, window)
	}
	// fxSources must be the deterministic ordered set.
	if len(call.fxSources) < 2 {
		t.Errorf("FXStore called with %d FX sources, want at least 2", len(call.fxSources))
	}
}

// TestTick_Triangulation_FXSnap_FallbackOnNoQuote — when the snap
// path has no row at-or-before bucketEnd, the orchestrator falls back
// to the cached-VWAP path AND increments the fallback counter. The
// chain still publishes (degraded but functional).
func TestTick_Triangulation_FXSnap_FallbackOnNoQuote(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	mr.Set(cachekeys.VWAP(usdEUR.Base, usdEUR.Quote, window), "0.900000000000")

	fx := &fakeFXStore{} // quote==nil → returns ErrNoFXQuote

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
		FXStore: fx,
	})

	beforeOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	beforeFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	afterOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	afterFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if afterOK-beforeOK != 1 {
		t.Errorf("ok counter delta = %v, want 1 (chain still publishes via cached-VWAP fallback)", afterOK-beforeOK)
	}
	if afterFB-beforeFB != 1 {
		t.Errorf("fallback counter delta = %v, want 1", afterFB-beforeFB)
	}
	got, err := mr.Get(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window))
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got != "0.072000000000" {
		t.Errorf("target VWAP = %q, want 0.072000000000 (computed from cached-VWAP fallback)", got)
	}
}

// TestTick_Triangulation_FXSnap_DBErrorAborts — non-ErrNoFXQuote
// errors from the FX store mean we can't trust ANY chained-fiat
// output this tick. The chain skips publish and surfaces redis_error;
// the fallback counter does NOT increment (this isn't a planned
// fallback, it's an outage signal).
func TestTick_Triangulation_FXSnap_DBErrorAborts(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	mr.Set(cachekeys.VWAP(usdEUR.Base, usdEUR.Quote, window), "0.900000000000")

	fx := &fakeFXStore{err: errors.New("connection refused")}

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
		FXStore: fx,
	})

	beforeErr := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("redis_error"))
	beforeFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	afterErr := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("redis_error"))
	afterFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if afterErr-beforeErr != 1 {
		t.Errorf("redis_error counter delta = %v, want 1", afterErr-beforeErr)
	}
	if afterFB != beforeFB {
		t.Errorf("fallback counter incremented on hard DB error: %v→%v", beforeFB, afterFB)
	}
	if mr.Exists(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window)) {
		t.Error("target VWAP should not exist when FX-store errors")
	}
}

// TestTick_Triangulation_FXStoreNil_LegsUseCachedVWAP — when no
// FXStore is wired, FX legs read from the cached-VWAP path same as
// non-FX legs. Pre-X2.5 behaviour is preserved as the safe default.
func TestTick_Triangulation_FXStoreNil_LegsUseCachedVWAP(t *testing.T) {
	xlmUSD := mkPair(t, "crypto", "XLM", "fiat", "USD")
	usdEUR := mkPair(t, "fiat", "USD", "fiat", "EUR")
	xlmEUR := mkPair(t, "crypto", "XLM", "fiat", "EUR")
	window := 5 * time.Minute

	cache, mr := newTestRedis(t)
	mr.Set(cachekeys.VWAP(xlmUSD.Base, xlmUSD.Quote, window), "0.080000000000")
	mr.Set(cachekeys.VWAP(usdEUR.Base, usdEUR.Quote, window), "0.900000000000")

	o := New(nil, cache, Config{
		Windows: []time.Duration{window},
		Triangulations: []TriangulationChain{
			{Target: xlmEUR, Legs: []canonical.Pair{xlmUSD, usdEUR}},
		},
		// FXStore omitted
	})

	beforeFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	afterFB := testutil.ToFloat64(obs.AggregatorFXSnapFallbackTotal.WithLabelValues(usdEUR.String()))

	if afterFB != beforeFB {
		t.Errorf("fallback counter incremented when FXStore is nil: %v→%v", beforeFB, afterFB)
	}
	got, err := mr.Get(cachekeys.VWAP(xlmEUR.Base, xlmEUR.Quote, window))
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got != "0.072000000000" {
		t.Errorf("target VWAP = %q, want 0.072000000000", got)
	}
}

// TestTick_Triangulation_NoChainsConfigured — the Tick proceeds
// normally and never touches the triangulation path. No counter
// increments.
func TestTick_Triangulation_NoChainsConfigured(t *testing.T) {
	cache, _ := newTestRedis(t)
	o := New(nil, cache, Config{
		Windows: []time.Duration{5 * time.Minute},
		// Triangulations omitted
	})

	beforeOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	beforeMiss := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("missing_leg"))

	if err := o.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	afterOK := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("ok"))
	afterMiss := testutil.ToFloat64(obs.AggregatorTriangulationsTotal.WithLabelValues("missing_leg"))

	if afterOK != beforeOK || afterMiss != beforeMiss {
		t.Errorf("triangulation counters changed without configured chains: ok %v→%v, missing %v→%v",
			beforeOK, afterOK, beforeMiss, afterMiss)
	}
}
