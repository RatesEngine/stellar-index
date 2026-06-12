package orchestrator

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// TestEmitStalenessGauges_growsAcrossTicks asserts the documented
// contract from F-1306: a pair that has never had a successful VWAP
// write should see its staleness gauge grow on every tick. The first
// emit seeds lastWriteAt to "now" (so the metric is present but not
// pageing immediately), and subsequent emits compute now - first-seen.
//
// This test exists because the live r1 deployment shows the gauge
// stuck at 0 for assets that never get a write (e.g. crypto:BTC with
// no CEX connectors enabled), suggesting the seed-on-first-sighting
// logic isn't persisting between ticks.
func TestEmitStalenessGauges_growsAcrossTicks(t *testing.T) {
	btc, err := canonical.NewCryptoAsset("BTC")
	if err != nil {
		t.Fatalf("BTC: %v", err)
	}
	usd, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("USD: %v", err)
	}
	pair, err := canonical.NewPair(btc, usd)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	orch := New(nil, nil, Config{Pairs: []canonical.Pair{pair}})

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	orch.emitStalenessGauges(t0)

	got := testutil.ToFloat64(obs.PriceStalenessSeconds.WithLabelValues("crypto:BTC"))
	if got != 0 {
		t.Fatalf("after first emit: want stale=0 (first-sighting seed), got %v", got)
	}

	// 60 seconds later — second emit should observe the seeded value
	// and compute stale=60, not re-seed.
	orch.emitStalenessGauges(t0.Add(60 * time.Second))

	got = testutil.ToFloat64(obs.PriceStalenessSeconds.WithLabelValues("crypto:BTC"))
	if got != 60 {
		t.Errorf("after second emit (60s later): want stale=60, got %v", got)
	}

	// 5 minutes after t0 — staleness should keep growing.
	orch.emitStalenessGauges(t0.Add(5 * time.Minute))
	got = testutil.ToFloat64(obs.PriceStalenessSeconds.WithLabelValues("crypto:BTC"))
	if got != 300 {
		t.Errorf("after third emit (300s later): want stale=300, got %v", got)
	}
}

// TestEmitStalenessGauges_xlmNativeMirrorOrderIndependent asserts the
// fix to the order-dependent mirror bug: regardless of whether the
// `native` pair or the `crypto:XLM` pair iterates first, both labels
// surface the freshest staleness across the two forms.
//
// Pre-fix, the mirror code wrote the *current* pair's stale value to
// the other label as a side-effect, so iteration order picked the
// winner. If `native` was fresh (SDEX writing) and `crypto:XLM` was
// stale (no CEX), the metric for both reported native's 0 OR
// crypto:XLM's high value, depending on which appeared last in
// cfg.Pairs. Post-fix, both labels carry MIN(native_stale, ticker_stale).
func TestEmitStalenessGauges_xlmNativeMirrorOrderIndependent(t *testing.T) {
	xlm, err := canonical.NewCryptoAsset("XLM")
	if err != nil {
		t.Fatalf("XLM: %v", err)
	}
	usd, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("USD: %v", err)
	}
	xlmTickerPair, err := canonical.NewPair(xlm, usd)
	if err != nil {
		t.Fatalf("xlm-ticker pair: %v", err)
	}
	xlmNativePair, err := canonical.NewPair(canonical.NativeAsset(), usd)
	if err != nil {
		t.Fatalf("xlm-native pair: %v", err)
	}

	for _, tc := range []struct {
		name  string
		pairs []canonical.Pair
	}{
		{"ticker first then native", []canonical.Pair{xlmTickerPair, xlmNativePair}},
		{"native first then ticker", []canonical.Pair{xlmNativePair, xlmTickerPair}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reset gauge to avoid cross-test leakage.
			obs.PriceStalenessSeconds.WithLabelValues("native").Set(-1)
			obs.PriceStalenessSeconds.WithLabelValues("crypto:XLM").Set(-1)

			orch := New(nil, nil, Config{Pairs: tc.pairs})

			t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			// Seed both forms on the first emit at t0.
			orch.emitStalenessGauges(t0)

			// 10 minutes later: native gets a fresh "write" via the
			// VWAP path. crypto:XLM keeps its t0 first-sighting time.
			orch.lastWriteAt["native"] = t0.Add(10 * time.Minute)

			emitAt := t0.Add(10 * time.Minute)
			orch.emitStalenessGauges(emitAt)

			gotNative := testutil.ToFloat64(obs.PriceStalenessSeconds.WithLabelValues("native"))
			gotTicker := testutil.ToFloat64(obs.PriceStalenessSeconds.WithLabelValues("crypto:XLM"))
			if gotNative != 0 {
				t.Errorf("native stale = %v, want 0 (just written)", gotNative)
			}
			if gotTicker != 0 {
				t.Errorf("crypto:XLM stale = %v, want 0 (mirror of fresh native)", gotTicker)
			}
		})
	}
}
