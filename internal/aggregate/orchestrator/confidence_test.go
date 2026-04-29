package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/divergence"
)

// stubBaselineSource implements orchestrator.BaselineSource with a
// fixed return.
type stubBaselineSource struct {
	multi      baseline.MultiBaseline
	computedAt time.Time
	err        error
}

func (s stubBaselineSource) LatestBaseline(_ context.Context, _ canonical.Pair) (baseline.MultiBaseline, time.Time, error) {
	return s.multi, s.computedAt, s.err
}

// xlmUSDPair returns an XLM/fiat:USD pair — distinct from the
// XLM/USDT pair the existing orchestrator tests use, so we don't
// fight the package-level fixture choices when we want USD volume
// approximation to fire.
func xlmUSDPair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, err := canonical.ParseAsset("native")
	if err != nil {
		t.Fatalf("parse native: %v", err)
	}
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse fiat:USD: %v", err)
	}
	pair, err := canonical.NewPair(xlm, usd)
	if err != nil {
		t.Fatalf("new pair: %v", err)
	}
	return pair
}

// makeXLMUSDTrade builds a trade on the XLM/fiat:USD pair with the
// given source + amounts. Reuses the existing test fixture pattern.
func makeXLMUSDTrade(t *testing.T, source string, base, quote int64, ts time.Time) canonical.Trade {
	t.Helper()
	return canonical.Trade{
		Source:      source,
		Ledger:      uint32(ts.Unix() % 1_000_000),
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        xlmUSDPair(t),
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

// TestConfidence_ScoreFlowsToCacheKey — happy path: orchestrator
// runs two ticks (the first warms prevVWAP, the second produces a
// real return). After tick 2 the confidence: cache key is present
// and parses as a valid Score JSON.
func TestConfidence_ScoreFlowsToCacheKey(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()

	store := &mockStore{
		trades: []canonical.Trade{
			makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_242_000, now.Add(-30*time.Second)),
			makeXLMUSDTrade(t, "phoenix", 1_000_000, 1_245_000, now.Add(-20*time.Second)),
		},
	}
	rdb, _ := newTestRedis(t)

	bsrc := stubBaselineSource{
		multi: baseline.MultiBaseline{
			Day30: &baseline.Baseline{Median: 0.0001, MAD: 0.001, N: 1000},
		},
		computedAt: now,
	}

	orch := New(store, rdb, Config{
		Pairs:     []canonical.Pair{pair},
		Windows:   []time.Duration{1 * time.Minute},
		Interval:  1 * time.Hour,
		Baselines: bsrc,
	})

	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	confKey := cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)
	body, err := rdb.Get(context.Background(), confKey).Bytes()
	if err != nil {
		t.Fatalf("confidence key %q missing from cache: %v", confKey, err)
	}

	var score confidence.Score
	if err := json.Unmarshal(body, &score); err != nil {
		t.Fatalf("confidence value not valid JSON: %v\nraw: %s", err, body)
	}
	if score.Confidence < 0 || score.Confidence > 1 {
		t.Errorf("Confidence = %v, want in [0, 1]", score.Confidence)
	}
	if score.Factors.SourceCount == 0 {
		t.Error("Factors.SourceCount = 0, expected non-zero (2 distinct sources)")
	}
}

// TestConfidence_SkipsWhenBaselinesNil — Baselines field nil →
// confidence step is a no-op; no key written.
func TestConfidence_SkipsWhenBaselinesNil(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()

	store := &mockStore{
		trades: []canonical.Trade{
			makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_242_000, now.Add(-30*time.Second)),
		},
	}
	rdb, _ := newTestRedis(t)

	orch := New(store, rdb, Config{
		Pairs:    []canonical.Pair{pair},
		Windows:  []time.Duration{1 * time.Minute},
		Interval: 1 * time.Hour,
		// Baselines: nil
	})

	_ = orch.Tick(context.Background())
	_ = orch.Tick(context.Background())

	confKey := cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)
	if exists, _ := rdb.Exists(context.Background(), confKey).Result(); exists != 0 {
		t.Errorf("confidence key %q present despite nil Baselines", confKey)
	}
}

// TestConfidence_DivergenceWiredFromCache — pre-seed a cached
// divergence Result in Redis (the same shape `internal/divergence`
// writes), tick the orchestrator, and confirm the resulting
// confidence factor reflects the cached divergence.
//
// We can't directly read `Factors.CrossOracle` from inside the
// orchestrator (it's part of the JSON-encoded Score), so we do the
// inverse: set up two scenarios — one with no cached divergence
// (sentinel = -1 → factor = 0.7), one with a low-divergence cached
// result (factor → 1.0). Confirm the second has higher confidence.
func TestConfidence_DivergenceWiredFromCache(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()
	bsrc := stubBaselineSource{
		multi: baseline.MultiBaseline{
			Day30: &baseline.Baseline{Median: 0.0001, MAD: 0.001, N: 1000},
		},
		computedAt: now,
	}

	runOnce := func(t *testing.T, seedDiv *divergence.CachedResult) confidence.Score {
		t.Helper()
		store := &mockStore{
			trades: []canonical.Trade{
				makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_242_000, now.Add(-30*time.Second)),
				makeXLMUSDTrade(t, "phoenix", 1_000_000, 1_245_000, now.Add(-20*time.Second)),
			},
		}
		rdb, _ := newTestRedis(t)
		if seedDiv != nil {
			body, err := json.Marshal(seedDiv)
			if err != nil {
				t.Fatalf("seed marshal: %v", err)
			}
			if err := rdb.Set(context.Background(),
				cachekeys.Divergence(pair.Base), body, 5*time.Minute).Err(); err != nil {
				t.Fatalf("seed cache set: %v", err)
			}
		}
		orch := New(store, rdb, Config{
			Pairs:     []canonical.Pair{pair},
			Windows:   []time.Duration{1 * time.Minute},
			Interval:  1 * time.Hour,
			Baselines: bsrc,
		})
		_ = orch.Tick(context.Background())
		_ = orch.Tick(context.Background())
		body, err := rdb.Get(context.Background(),
			cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)).Bytes()
		if err != nil {
			t.Fatalf("confidence read: %v", err)
		}
		var s confidence.Score
		if err := json.Unmarshal(body, &s); err != nil {
			t.Fatalf("confidence unmarshal: %v", err)
		}
		return s
	}

	noCache := runOnce(t, nil)
	withinTolerance := runOnce(t, &divergence.CachedResult{
		PairID:        pair.String(),
		DivergencePct: 0.3, // within 1% tolerance → factor 1.0
		SuccessCount:  3,
	})

	// "No data" returns the neutral 0.7; "within tolerance" returns
	// 1.0 — so the wired-up scenario must score the CrossOracle
	// factor strictly higher. Asserting on the per-factor value
	// (rather than the combined Confidence) sidesteps the
	// LiquidityFactor's behaviour for our small fixture trades —
	// the wiring is what's under test, not the combiner output.
	if withinTolerance.Factors.CrossOracle <= noCache.Factors.CrossOracle {
		t.Errorf("CrossOracle factor: with-cache=%v should exceed no-cache=%v",
			withinTolerance.Factors.CrossOracle, noCache.Factors.CrossOracle)
	}
	if noCache.Factors.CrossOracle != 0.7 {
		t.Errorf("no-cache CrossOracle = %v, want 0.7 (neutral)", noCache.Factors.CrossOracle)
	}
	if withinTolerance.Factors.CrossOracle != 1.0 {
		t.Errorf("within-tolerance CrossOracle = %v, want 1.0", withinTolerance.Factors.CrossOracle)
	}
}

// TestConfidence_DivergenceLowSuccessCountIgnored — a cached
// Result with SuccessCount < 2 doesn't count: the divergence input
// passes the "no data" sentinel even when DivergencePct is set.
//
// This guards against scoring a single reference's hiccup as a
// trustworthy multi-source signal.
func TestConfidence_DivergenceLowSuccessCountIgnored(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()
	bsrc := stubBaselineSource{
		multi: baseline.MultiBaseline{
			Day30: &baseline.Baseline{Median: 0.0001, MAD: 0.001, N: 1000},
		},
		computedAt: now,
	}

	store := &mockStore{
		trades: []canonical.Trade{
			makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_242_000, now.Add(-30*time.Second)),
		},
	}
	rdb, _ := newTestRedis(t)

	body, _ := json.Marshal(&divergence.CachedResult{
		PairID:        pair.String(),
		DivergencePct: 0.3, // would be in-tolerance, but…
		SuccessCount:  1,   // … below trust floor; should be ignored
	})
	if err := rdb.Set(context.Background(),
		cachekeys.Divergence(pair.Base), body, 5*time.Minute).Err(); err != nil {
		t.Fatalf("seed cache set: %v", err)
	}

	orch := New(store, rdb, Config{
		Pairs:     []canonical.Pair{pair},
		Windows:   []time.Duration{1 * time.Minute},
		Interval:  1 * time.Hour,
		Baselines: bsrc,
	})
	_ = orch.Tick(context.Background())
	_ = orch.Tick(context.Background())

	scoreBody, err := rdb.Get(context.Background(),
		cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)).Bytes()
	if err != nil {
		t.Fatalf("confidence read: %v", err)
	}
	var s confidence.Score
	if err := json.Unmarshal(scoreBody, &s); err != nil {
		t.Fatalf("confidence unmarshal: %v", err)
	}
	// 0.7 is the documented "no cross-oracle data" neutral. With a
	// single-source cached result we expect the same value.
	const wantNeutral = 0.7
	if s.Factors.CrossOracle < wantNeutral-1e-6 || s.Factors.CrossOracle > wantNeutral+1e-6 {
		t.Errorf("CrossOracle factor = %v, want %v (neutral; single-source ignored)",
			s.Factors.CrossOracle, wantNeutral)
	}
}

// TestDistinctSourceClassCount — exchange + oracle + aggregator
// are three distinct classes in the registry. Verifies the
// registry-backed implementation collapses same-class trades
// (two CEXes count as 1) and counts cross-class trades correctly.
func TestDistinctSourceClassCount(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()

	cases := []struct {
		name    string
		sources []string
		want    int
	}{
		{"empty", nil, 0},
		// Both CEXes — same Class:Subclass bucket → 1.
		{"two CEXes", []string{"binance", "coinbase"}, 1},
		// CEX + DEX — same Class but distinct Subclass → 2.
		// This is the ADR-0019 worked example case the Subclass
		// field exists for.
		{"CEX + DEX", []string{"binance", "soroswap"}, 2},
		// CEX + DEX + FX — three Subclasses under ClassExchange → 3.
		{"CEX + DEX + FX", []string{"binance", "soroswap", "polygon-forex"}, 3},
		// CEX + Oracle — distinct parent classes → 2.
		{"CEX + Oracle", []string{"binance", "reflector-dex"}, 2},
		// Four buckets: CEX + Oracle + Aggregator + AuthoritySanity.
		{"four buckets", []string{"binance", "reflector-dex", "coingecko", "ecb"}, 4},
		// Unknown source falls into the registry's default
		// (ClassExchange + empty Subclass). If no other exchange:cex
		// is present it counts as its own bucket.
		{"unknown alone", []string{"unknown_source"}, 1},
		// Two unknowns collapse (same fallback bucket).
		{"two unknowns", []string{"unknown_a", "unknown_b"}, 1},
		// Two same-DEX-subclass sources collapse to 1.
		{"two DEXes", []string{"soroswap", "phoenix"}, 1},
	}
	for _, tc := range cases {
		trades := make([]canonical.Trade, 0, len(tc.sources))
		for _, s := range tc.sources {
			trades = append(trades, makeXLMUSDTradeWithSource(t, pair, s, now))
		}
		got := distinctSourceClassCount(trades)
		if got != tc.want {
			t.Errorf("%s: distinctSourceClassCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// makeXLMUSDTradeWithSource is mkObservationTrade variant — same
// shape but with the given pair already resolved.
func makeXLMUSDTradeWithSource(t *testing.T, pair canonical.Pair, source string, ts time.Time) canonical.Trade {
	t.Helper()
	return canonical.Trade{
		Source:      source,
		Ledger:      uint32(ts.Unix() % 1_000_000),
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(1)),
		QuoteAmount: canonical.NewAmount(big.NewInt(1)),
	}
}

// TestPhase2Freeze_BlocksVWAPPublish — when the 3-signal AND
// fires, the orchestrator BAILS BEFORE the VWAP cache write. The
// previous bucket's value (or absence) stays in cache.
//
// Setup forces all three signals to fire:
//   - z >> 5: a return that's huge vs the baseline median+MAD
//   - source_count == 1: single-source trade slice
//   - low confidence: small bucket volume + single source
func TestPhase2Freeze_BlocksVWAPPublish(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()

	// First-tick prevVWAP comparator setup: tight price.
	// Second tick has a huge return relative to the prev →
	// z dominates, source_count=1, confidence collapses.
	tightPair := []canonical.Trade{
		makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_000_000, now.Add(-90*time.Second)), // tick 1
	}
	bigSpike := []canonical.Trade{
		makeXLMUSDTrade(t, "soroswap", 1_000_000, 5_000_000, now.Add(-30*time.Second)), // tick 2: 5x jump, single source
	}

	store := &mockStore{}
	rdb, _ := newTestRedis(t)

	// Tight baseline so any meaningful return reads as a huge z.
	bsrc := stubBaselineSource{
		multi: baseline.MultiBaseline{
			Day30: &baseline.Baseline{Median: 0.0, MAD: 0.0001, N: 1000},
		},
		computedAt: now,
	}

	orch := New(store, rdb, Config{
		Pairs:     []canonical.Pair{pair},
		Windows:   []time.Duration{1 * time.Minute},
		Interval:  1 * time.Hour,
		Baselines: bsrc,
		// No Anomaly checker → Phase 1 runs in skip mode; we're
		// isolating Phase 2 here.
	})

	// Tick 1: warms prevVWAP. No freeze possible (no comparator yet).
	store.trades = tightPair
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	vwapKey := cachekeys.VWAP(pair.Base, pair.Quote, time.Minute)
	if exists, _ := rdb.Exists(context.Background(), vwapKey).Result(); exists != 1 {
		t.Fatalf("VWAP key missing after tick 1 — confidence skip shouldn't block tick 1 publish")
	}

	// Tick 2: huge spike, single source. Phase 2 should fire.
	// The tick-1 VWAP stays in cache (overwrite suppressed).
	tick1Value, err := rdb.Get(context.Background(), vwapKey).Result()
	if err != nil {
		t.Fatalf("read tick-1 VWAP: %v", err)
	}
	store.trades = bigSpike
	if err := orch.Tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	tick2Value, err := rdb.Get(context.Background(), vwapKey).Result()
	if err != nil {
		t.Fatalf("read tick-2 VWAP: %v", err)
	}
	if tick2Value != tick1Value {
		t.Errorf("VWAP cache changed across a Phase 2 freeze: tick1=%q tick2=%q (Phase 2 should preserve LKG)",
			tick1Value, tick2Value)
	}

	// Confidence cache must NOT carry forward the spike's score.
	confKey := cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)
	bodyAfter, err := rdb.Get(context.Background(), confKey).Bytes()
	if err == nil {
		// Some entry exists — must be from tick 1 (a fresh tick-1 publish
		// can write a confidence entry; that's fine). What matters is the
		// value is NOT from tick 2.
		var s confidence.Score
		if err := json.Unmarshal(bodyAfter, &s); err != nil {
			t.Fatalf("decode confidence: %v", err)
		}
		// Tick 1 had no prev → confidence step skipped → no key written.
		// So the cache should have NO confidence: entry at all.
		t.Errorf("confidence cache key present after Phase 2 freeze — should not have been written: %v", s)
	}
}

// TestConfidence_BaselineMissingDoesNotBlockVWAP — when the
// Baselines source returns an error, the VWAP cache write must
// still succeed. Confidence is enrichment, not a publish gate.
func TestConfidence_BaselineMissingDoesNotBlockVWAP(t *testing.T) {
	pair := xlmUSDPair(t)
	now := time.Now().UTC()

	store := &mockStore{
		trades: []canonical.Trade{
			makeXLMUSDTrade(t, "soroswap", 1_000_000, 1_242_000, now.Add(-30*time.Second)),
		},
	}
	rdb, _ := newTestRedis(t)

	bsrc := stubBaselineSource{err: errors.New("not found")}

	orch := New(store, rdb, Config{
		Pairs:     []canonical.Pair{pair},
		Windows:   []time.Duration{1 * time.Minute},
		Interval:  1 * time.Hour,
		Baselines: bsrc,
	})

	_ = orch.Tick(context.Background())
	_ = orch.Tick(context.Background())

	vwapKey := cachekeys.VWAP(pair.Base, pair.Quote, time.Minute)
	if exists, _ := rdb.Exists(context.Background(), vwapKey).Result(); exists == 0 {
		t.Errorf("VWAP key missing despite baseline-source error: confidence failure must not block VWAP")
	}
	confKey := cachekeys.Confidence(pair.Base, pair.Quote, time.Minute)
	if exists, _ := rdb.Exists(context.Background(), confKey).Result(); exists != 0 {
		t.Errorf("confidence key present despite baseline error")
	}
}
