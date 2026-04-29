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
