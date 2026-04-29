package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

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
