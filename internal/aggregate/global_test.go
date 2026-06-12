package aggregate

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// stubGlobalReader fakes the three storage seams ComputeGlobalPrice
// reads against. Tests configure the returns per-tier and assert
// which tier won.
type stubGlobalReader struct {
	vwap struct {
		price      string
		asOf       time.Time
		tradeCount int64
		sources    []string
		ok         bool
		err        error
	}
	agg struct {
		rows []canonical.OracleUpdate
		err  error
	}
	tri struct {
		price string
		asOf  time.Time
		ok    bool
		err   error
	}

	// call counters — let tests verify higher tiers short-circuit
	// without invoking the lower ones.
	vwapCalls, aggCalls, triCalls int
}

func (s *stubGlobalReader) LatestVWAP(_ context.Context, _, _ canonical.Asset) (string, time.Time, int64, []string, bool, error) {
	s.vwapCalls++
	return s.vwap.price, s.vwap.asOf, s.vwap.tradeCount, s.vwap.sources, s.vwap.ok, s.vwap.err
}

func (s *stubGlobalReader) LatestAggregatorPrices(_ context.Context, _, _ canonical.Asset, _ []string) ([]canonical.OracleUpdate, error) {
	s.aggCalls++
	return s.agg.rows, s.agg.err
}

func (s *stubGlobalReader) LookupTriangulated(_ context.Context, _, _ canonical.Asset, _ time.Duration) (string, time.Time, bool, error) {
	s.triCalls++
	return s.tri.price, s.tri.asOf, s.tri.ok, s.tri.err
}

func usdcUSDPair(t *testing.T) (canonical.Asset, canonical.Asset) {
	t.Helper()
	base, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("classic: %v", err)
	}
	quote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("fiat: %v", err)
	}
	return base, quote
}

func TestComputeGlobalPrice_VWAPTierWins(t *testing.T) {
	reader := &stubGlobalReader{}
	reader.vwap.price = "1.00050000000000"
	reader.vwap.asOf = time.Now().UTC()
	reader.vwap.tradeCount = 12
	reader.vwap.sources = []string{"coinbase", "binance"}
	reader.vwap.ok = true

	base, quote := usdcUSDPair(t)
	res, err := ComputeGlobalPrice(context.Background(), base, quote, reader, DefaultGlobalPriceOptions())
	if err != nil {
		t.Fatalf("ComputeGlobalPrice: %v", err)
	}
	if res.Authority != AuthorityVWAPNative {
		t.Errorf("authority = %q, want vwap_native", res.Authority)
	}
	if res.Price != "1.00050000000000" {
		t.Errorf("price = %q, want 1.00050000000000", res.Price)
	}
	if res.TradeCount != 12 {
		t.Errorf("trade_count = %d, want 12", res.TradeCount)
	}
	// Should short-circuit — no aggregator or triangulated call.
	if reader.aggCalls != 0 || reader.triCalls != 0 {
		t.Errorf("higher tiers should short-circuit; agg=%d tri=%d", reader.aggCalls, reader.triCalls)
	}
}

func TestComputeGlobalPrice_VWAPBelowThreshold_FallsThrough(t *testing.T) {
	// VWAP exists but trade_count=3 < default 5 threshold → fall
	// through to aggregator tier.
	reader := &stubGlobalReader{}
	reader.vwap.price = "0.99000000000000"
	reader.vwap.tradeCount = 3
	reader.vwap.ok = true

	price, _ := new(big.Int).SetString("100000000", 10) // 1.00 @ 8dp
	reader.agg.rows = []canonical.OracleUpdate{
		{
			Source:    "coingecko",
			Timestamp: time.Now().UTC(),
			Price:     canonical.NewAmount(price),
			Decimals:  8,
		},
		{
			Source:    "coinmarketcap",
			Timestamp: time.Now().UTC().Add(-30 * time.Second),
			Price:     canonical.NewAmount(price),
			Decimals:  8,
		},
	}

	base, quote := usdcUSDPair(t)
	opts := DefaultGlobalPriceOptions()
	opts.AggregatorSources = []string{"coingecko", "coinmarketcap"}
	res, err := ComputeGlobalPrice(context.Background(), base, quote, reader, opts)
	if err != nil {
		t.Fatalf("ComputeGlobalPrice: %v", err)
	}
	if res.Authority != AuthorityAggregatorAvg {
		t.Errorf("authority = %q, want aggregator_avg", res.Authority)
	}
	// 100M @ 8dp scales to 10_000_000_000_000 @ 14dp → "1.00000000000000".
	if res.Price != "1.00000000000000" {
		t.Errorf("price = %q, want 1.00000000000000", res.Price)
	}
	if len(res.Sources) != 2 {
		t.Errorf("sources = %v, want 2", res.Sources)
	}
	// Triangulation never called.
	if reader.triCalls != 0 {
		t.Errorf("triangulated tier should short-circuit; calls=%d", reader.triCalls)
	}
}

func TestComputeGlobalPrice_AggregatorStale_FallsThrough(t *testing.T) {
	// VWAP misses, aggregator rows exist but are all older than
	// MaxAggregatorAge → fall through to triangulated.
	reader := &stubGlobalReader{}
	reader.vwap.ok = false

	price, _ := new(big.Int).SetString("100000000", 10)
	reader.agg.rows = []canonical.OracleUpdate{
		{
			Source:    "coingecko",
			Timestamp: time.Now().UTC().Add(-1 * time.Hour), // stale
			Price:     canonical.NewAmount(price),
			Decimals:  8,
		},
	}

	reader.tri.price = "0.99875000000000"
	reader.tri.asOf = time.Now().UTC()
	reader.tri.ok = true

	base, quote := usdcUSDPair(t)
	opts := DefaultGlobalPriceOptions()
	opts.AggregatorSources = []string{"coingecko"}
	res, err := ComputeGlobalPrice(context.Background(), base, quote, reader, opts)
	if err != nil {
		t.Fatalf("ComputeGlobalPrice: %v", err)
	}
	if res.Authority != AuthorityTriangulated {
		t.Errorf("authority = %q, want triangulated", res.Authority)
	}
	if res.Price != "0.99875000000000" {
		t.Errorf("price = %q, want 0.99875000000000", res.Price)
	}
}

func TestComputeGlobalPrice_AllTiersMiss(t *testing.T) {
	reader := &stubGlobalReader{}
	reader.vwap.ok = false
	reader.tri.ok = false

	base, quote := usdcUSDPair(t)
	opts := DefaultGlobalPriceOptions()
	opts.AggregatorSources = []string{"coingecko"}
	_, err := ComputeGlobalPrice(context.Background(), base, quote, reader, opts)
	if !errors.Is(err, ErrNoPrice) {
		t.Errorf("err = %v, want ErrNoPrice", err)
	}
}

func TestComputeGlobalPrice_VWAPErrorPropagates(t *testing.T) {
	// A storage failure in tier 1 must NOT silently degrade to
	// tier 2 — the operator wants to see the failure and the
	// aggregator tier might mask a broken VWAP path.
	reader := &stubGlobalReader{}
	reader.vwap.err = errors.New("simulated DB failure")

	base, quote := usdcUSDPair(t)
	opts := DefaultGlobalPriceOptions()
	opts.AggregatorSources = []string{"coingecko"}
	_, err := ComputeGlobalPrice(context.Background(), base, quote, reader, opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Lower tiers must not have been called.
	if reader.aggCalls != 0 || reader.triCalls != 0 {
		t.Errorf("error should short-circuit lower tiers; agg=%d tri=%d", reader.aggCalls, reader.triCalls)
	}
}

func TestComputeGlobalPrice_NoAggregatorsConfigured_SkipsTier(t *testing.T) {
	// When opts.AggregatorSources is empty, tier 2 is skipped
	// entirely — tier 1 falls straight through to tier 3.
	reader := &stubGlobalReader{}
	reader.vwap.ok = false
	reader.tri.price = "0.500"
	reader.tri.ok = true

	base, quote := usdcUSDPair(t)
	opts := DefaultGlobalPriceOptions()
	opts.AggregatorSources = nil
	res, err := ComputeGlobalPrice(context.Background(), base, quote, reader, opts)
	if err != nil {
		t.Fatalf("ComputeGlobalPrice: %v", err)
	}
	if res.Authority != AuthorityTriangulated {
		t.Errorf("authority = %q, want triangulated", res.Authority)
	}
	if reader.aggCalls != 0 {
		t.Errorf("aggregator tier should be skipped when no sources configured; calls=%d", reader.aggCalls)
	}
}

func TestComputeGlobalPrice_NilReader_Errors(t *testing.T) {
	base, quote := usdcUSDPair(t)
	_, err := ComputeGlobalPrice(context.Background(), base, quote, nil, DefaultGlobalPriceOptions())
	if err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestAverageAggregatorPrices_DifferentDecimals(t *testing.T) {
	// Verify the cross-decimal scaling works: CG-style 8dp +
	// hypothetical 6dp source averaging to a sensible value.
	price8, _ := new(big.Int).SetString("100050000", 10) // 1.00050 @ 8dp
	price6, _ := new(big.Int).SetString("999000", 10)    // 0.99900 @ 6dp

	rows := []canonical.OracleUpdate{
		{Source: "a", Timestamp: time.Now(), Price: canonical.NewAmount(price8), Decimals: 8},
		{Source: "b", Timestamp: time.Now(), Price: canonical.NewAmount(price6), Decimals: 6},
	}
	avg, _, ok := averageAggregatorPrices(rows)
	if !ok {
		t.Fatal("averageAggregatorPrices: ok=false")
	}
	// avg = (1.00050 + 0.99900) / 2 = 0.99975
	if avg != "0.99975000000000" {
		t.Errorf("avg = %q, want 0.99975000000000", avg)
	}
}

func TestAverageAggregatorPrices_RejectsZeroPrices(t *testing.T) {
	zero := big.NewInt(0)
	rows := []canonical.OracleUpdate{
		{Source: "a", Timestamp: time.Now(), Price: canonical.NewAmount(zero), Decimals: 8},
	}
	_, _, ok := averageAggregatorPrices(rows)
	if ok {
		t.Error("all-zero-prices input must return ok=false")
	}
}

// aliasAwareReader returns a VWAP keyed by the exact base form, so a
// test can prove tryVWAPTier loops the asset aliases. Only the base
// listed in `byBase` returns a hit; every other form misses.
type aliasAwareReader struct {
	byBase    map[string]int64 // base.String() → tradeCount
	vwapCalls []string         // base forms queried, in order
}

func (r *aliasAwareReader) LatestVWAP(_ context.Context, base, _ canonical.Asset) (string, time.Time, int64, []string, bool, error) {
	r.vwapCalls = append(r.vwapCalls, base.String())
	if tc, ok := r.byBase[base.String()]; ok {
		return "0.12340000000000", time.Now().UTC(), tc, []string{"binance"}, true, nil
	}
	return "", time.Time{}, 0, nil, false, nil
}

func (r *aliasAwareReader) LatestAggregatorPrices(_ context.Context, _, _ canonical.Asset, _ []string) ([]canonical.OracleUpdate, error) {
	return nil, nil
}

func (r *aliasAwareReader) LookupTriangulated(_ context.Context, _, _ canonical.Asset, _ time.Duration) (string, time.Time, bool, error) {
	return "", time.Time{}, false, nil
}

// TestComputeGlobalPrice_VWAPTierLoopsAliases pins F-1340 (G14-04):
// the global view must find the XLM VWAP regardless of which
// canonical form (`native` vs `crypto:XLM`) the configured pair set
// publishes under. Pre-fix, tryVWAPTier queried only the literal
// base; if the caller passed `native` but the VWAP lived under
// `crypto:XLM`, the tier missed and the view degraded to
// aggregator_avg.
func TestComputeGlobalPrice_VWAPTierLoopsAliases(t *testing.T) {
	quote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("fiat: %v", err)
	}

	// VWAP only exists under crypto:XLM, but the caller queries native.
	reader := &aliasAwareReader{byBase: map[string]int64{"crypto:XLM": 20}}
	res, err := ComputeGlobalPrice(context.Background(), canonical.NativeAsset(), quote, reader, DefaultGlobalPriceOptions())
	if err != nil {
		t.Fatalf("ComputeGlobalPrice: %v", err)
	}
	if res.Authority != AuthorityVWAPNative {
		t.Errorf("authority = %q, want vwap_native (alias loop should find the crypto:XLM VWAP)", res.Authority)
	}
	if res.TradeCount != 20 {
		t.Errorf("trade_count = %d, want 20", res.TradeCount)
	}
	// The literal form must be tried first, then the alias.
	if len(reader.vwapCalls) != 2 || reader.vwapCalls[0] != "native" || reader.vwapCalls[1] != "crypto:XLM" {
		t.Errorf("alias query order = %v, want [native crypto:XLM]", reader.vwapCalls)
	}
}

// TestComputeGlobalPrice_VWAPTierAliasUnderThreshold — an alias hit
// that's below the trade-count floor must NOT win the VWAP tier; the
// loop keeps trying and ultimately falls through to a lower tier.
func TestComputeGlobalPrice_VWAPTierAliasUnderThreshold(t *testing.T) {
	quote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("fiat: %v", err)
	}
	// crypto:XLM has a VWAP but only 2 trades < default floor of 5.
	reader := &aliasAwareReader{byBase: map[string]int64{"crypto:XLM": 2}}
	_, err = ComputeGlobalPrice(context.Background(), canonical.NativeAsset(), quote, reader, DefaultGlobalPriceOptions())
	if !errors.Is(err, ErrNoPrice) {
		t.Errorf("err = %v, want ErrNoPrice (alias below threshold, no other tier)", err)
	}
}

// TestAssetAliases mirrors the v1.assetAliases contract so the two
// stay in lock-step (F-1340).
func TestAssetAliases(t *testing.T) {
	cases := map[string][]string{
		"native":     {"native", "crypto:XLM"},
		"crypto:XLM": {"crypto:XLM", "native"},
	}
	for in, want := range cases {
		a, err := canonical.ParseAsset(in)
		if err != nil {
			t.Fatalf("parse %s: %v", in, err)
		}
		got := assetAliases(a)
		if len(got) != len(want) {
			t.Fatalf("assetAliases(%s) len = %d, want %d", in, len(got), len(want))
		}
		for i := range want {
			if got[i].String() != want[i] {
				t.Errorf("assetAliases(%s)[%d] = %q, want %q", in, i, got[i].String(), want[i])
			}
		}
	}

	// A non-XLM asset returns only itself.
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("classic: %v", err)
	}
	if got := assetAliases(usdc); len(got) != 1 || !got[0].Equal(usdc) {
		t.Errorf("assetAliases(USDC) = %v, want just itself", got)
	}
}
