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
