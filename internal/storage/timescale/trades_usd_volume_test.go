package timescale

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// TestQuoteIsUSDOrUSDPegged covers the predicate's three branches:
// fiat:USD literal, USD-pegged stablecoin via FiatProxy, and the
// fall-through.
func TestQuoteIsUSDOrUSDPegged(t *testing.T) {
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	usdc, _ := canonical.NewCryptoAsset("USDC")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	eurc, _ := canonical.NewCryptoAsset("EURC")
	xlm, _ := canonical.NewCryptoAsset("XLM")

	cases := []struct {
		name string
		a    canonical.Asset
		want bool
	}{
		{"fiat:USD literal", usd, true},
		{"crypto:USDC pegged → USD", usdc, true},
		{"crypto:USDT pegged → USD", usdt, true},
		{"fiat:EUR not USD-pegged", eur, false},
		{"crypto:EURC pegs to EUR not USD", eurc, false},
		{"crypto:XLM no peg", xlm, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteIsUSDOrUSDPegged(tc.a); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTradeUSDVolume_PopulatedForExternalUSDPaths exercises the
// off-chain happy paths: CEX/FX source + USD-or-pegged quote
// → numeric string at the uniform 10^8 external scale.
func TestTradeUSDVolume_PopulatedForExternalUSDPaths(t *testing.T) {
	usd, _ := canonical.NewFiatAsset("USD")
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlm, _ := canonical.NewCryptoAsset("XLM")

	mkTrade := func(source string, quote canonical.Asset, quoteAmt int64) canonical.Trade {
		pair, err := canonical.NewPair(xlm, quote)
		if err != nil {
			t.Fatalf("NewPair: %v", err)
		}
		return canonical.Trade{
			Source:      source,
			Pair:        pair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100_000_000)),
			QuoteAmount: canonical.NewAmount(big.NewInt(quoteAmt)),
		}
	}

	cases := []struct {
		name   string
		source string
		quote  canonical.Asset
		amt    int64
		want   string // expected NUMERIC string
	}{
		{
			name:   "binance + fiat:USD → 1e8 → $1.00",
			source: "binance",
			quote:  usd,
			amt:    100_000_000,
			want:   "1.00000000",
		},
		{
			name:   "polygon-forex + fiat:USD → $0.005",
			source: "polygon-forex",
			quote:  usd,
			amt:    500_000,
			want:   "0.00500000",
		},
		{
			name:   "kraken + crypto:USDC peg → $42.50",
			source: "kraken",
			quote:  usdc,
			amt:    4_250_000_000,
			want:   "42.50000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Off-chain path doesn't consult the spec — pass nil.
			got := tradeUSDVolume(context.Background(), mkTrade(tc.source, tc.quote, tc.amt), nil, nil)
			if got == nil {
				t.Fatalf("got nil, want %q", tc.want)
			}
			if *got != tc.want {
				t.Errorf("got %q, want %q", *got, tc.want)
			}
		})
	}
}

// TestTradeUSDVolume_PopulatedForOnChainDEX exercises the phase-1
// on-chain path: DEX source + operator-declared USD-pegged quote
// → numeric string at the Stellar classic 10^7 scale.
func TestTradeUSDVolume_PopulatedForOnChainDEX(t *testing.T) {
	xlm := canonical.NativeAsset()

	circleUSDC, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("NewClassicAsset USDC: %v", err)
	}
	// USDC's SAC contract — an example C-strkey Soroswap would
	// stamp on a Soroswap XLM/USDC trade.
	usdcSAC, err := canonical.NewSorobanAsset("CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75")
	if err != nil {
		t.Fatalf("NewSorobanAsset USDC SAC: %v", err)
	}

	spec, err := NewUSDVolumeQuoteSpec(
		[]string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		map[string]string{
			"CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75": "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
		},
	)
	if err != nil {
		t.Fatalf("NewUSDVolumeQuoteSpec: %v", err)
	}

	mkTrade := func(source string, base, quote canonical.Asset, quoteAmt int64) canonical.Trade {
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			t.Fatalf("NewPair: %v", err)
		}
		return canonical.Trade{
			Source:      source,
			Pair:        pair,
			BaseAmount:  canonical.NewAmount(big.NewInt(10_000_000)), // 1.0 XLM at 10^7
			QuoteAmount: canonical.NewAmount(big.NewInt(quoteAmt)),
		}
	}

	cases := []struct {
		name   string
		source string
		base   canonical.Asset
		quote  canonical.Asset
		amt    int64
		want   string
	}{
		{
			name:   "sdex XLM/classic-USDC at 10^7 → $1.00",
			source: "sdex",
			base:   xlm,
			quote:  circleUSDC,
			amt:    10_000_000,
			want:   "1.00000000",
		},
		{
			name:   "soroswap XLM/USDC-SAC at 10^7 → $0.42",
			source: "soroswap",
			base:   xlm,
			quote:  usdcSAC,
			amt:    4_200_000,
			want:   "0.42000000",
		},
		{
			name:   "phoenix XLM/USDC-SAC big number → $1234.56",
			source: "phoenix",
			base:   xlm,
			quote:  usdcSAC,
			amt:    12_345_600_000,
			want:   "1234.56000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tradeUSDVolume(context.Background(), mkTrade(tc.source, tc.base, tc.quote, tc.amt), spec, nil)
			if got == nil {
				t.Fatalf("got nil, want %q", tc.want)
			}
			if *got != tc.want {
				t.Errorf("got %q, want %q", *got, tc.want)
			}
		})
	}
}

// TestTradeUSDVolume_NilForOutOfScope nails down what does NOT
// produce a usd_volume — the column stays NULL for these so callers
// don't get a misleadingly-precise figure.
func TestTradeUSDVolume_NilForOutOfScope(t *testing.T) {
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	xlm, _ := canonical.NewCryptoAsset("XLM")
	xlmNative := canonical.NativeAsset()
	circleUSDC, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	// A different (operator-untrusted) issuer of USDC. Real
	// validated G-strkey, just not Circle's.
	unknownClassicUSDC, err := canonical.NewClassicAsset("USDC", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")
	if err != nil {
		t.Fatalf("NewClassicAsset unknown USDC: %v", err)
	}
	pureSEP41, _ := canonical.NewSorobanAsset("CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7")

	specWithCircle, err := NewUSDVolumeQuoteSpec(
		[]string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("NewUSDVolumeQuoteSpec: %v", err)
	}

	mkTrade := func(source string, base, quote canonical.Asset, quoteAmt int64) canonical.Trade {
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			t.Fatalf("NewPair: %v", err)
		}
		return canonical.Trade{
			Source:      source,
			Pair:        pair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100_000_000)),
			QuoteAmount: canonical.NewAmount(big.NewInt(quoteAmt)),
		}
	}

	cases := []struct {
		name   string
		source string
		base   canonical.Asset
		quote  canonical.Asset
		amt    int64
		spec   *USDVolumeQuoteSpec
	}{
		{"on-chain DEX with no spec installed (default)", "soroswap", xlmNative, circleUSDC, 1_000_000_000, nil},
		{"on-chain DEX + classic-USDC NOT in operator allow-list", "soroswap", xlmNative, unknownClassicUSDC, 1_000_000_000, specWithCircle},
		{"on-chain DEX + pure SEP-41 (no classic counterpart)", "soroswap", xlmNative, pureSEP41, 1_000_000_000, specWithCircle},
		{"binance + EUR quote — not USD-pegged", "binance", xlm, eur, 1_000_000_000, nil},
		{"unknown source — fail-closed", "unregistered-venue", xlm, usd, 1_000_000_000, nil},
		{"oracle-class source (reflector)", "reflector-cex", xlm, usd, 1_000_000_000, nil},
		{"aggregator-class source (coingecko)", "coingecko", xlm, usd, 1_000_000_000, nil},
		{"zero quote_amount", "binance", xlm, usd, 0, nil},
		{"on-chain zero quote_amount even with spec", "soroswap", xlmNative, circleUSDC, 0, specWithCircle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tradeUSDVolume(context.Background(), mkTrade(tc.source, tc.base, tc.quote, tc.amt), tc.spec, nil)
			if got != nil {
				t.Errorf("got %q, want nil", *got)
			}
		})
	}
}

// mkClassicDEXTrade is a tiny helper for the Phase 2 tests that
// don't already have a closure-scoped mkTrade. Returns a XLM/quote
// trade at the soroswap source with the supplied quote-amount.
func mkClassicDEXTrade(t *testing.T, source string, base, quote canonical.Asset, quoteAmt int64) canonical.Trade {
	t.Helper()
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return canonical.Trade{
		Source:      source,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(10_000_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quoteAmt)),
	}
}

// stubFXResolver implements USDVolumeFXResolver for tests. The
// price map is keyed by canonical asset string and consulted on
// every USDPriceAt call; stale time-checks are the resolver's
// concern, not the test's, so we always return ok=true when the
// asset is present.
type stubFXResolver struct {
	prices map[string]string
	err    error
}

func (s stubFXResolver) USDPriceAt(_ context.Context, asset canonical.Asset, _ time.Time) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	p, ok := s.prices[asset.String()]
	if !ok {
		return "", false, nil
	}
	return p, true, nil
}

// TestTradeUSDVolume_Phase2FXFallback exercises the L2.2 Phase 2
// path: on-chain DEX trade quoted in a non-USD-pegged asset, FX
// resolver returns a price, tradeUSDVolume multiplies through.
//
// Pinned at scale 7 (Stellar classic) regardless of the quote
// asset's actual decimals — the L2.2 design treats every classic +
// SAC quote as a 7-decimal Stellar invariant; pure SEP-41 with
// non-7 decimals is L7 future-scope.
func TestTradeUSDVolume_Phase2FXFallback(t *testing.T) {
	t.Parallel()
	xlm := canonical.NativeAsset()
	aqua, err := canonical.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")
	if err != nil {
		t.Fatalf("NewClassicAsset AQUA: %v", err)
	}

	// XLM/AQUA trade: 100 XLM (1_000_000_000 stroops base) for
	// 5_000 AQUA (50_000_000_000 stroops quote). Operator's FX
	// resolver knows AQUA = $0.001 → $5.00 USD volume.
	resolver := stubFXResolver{prices: map[string]string{
		aqua.String(): "0.001",
	}}

	got := tradeUSDVolume(
		context.Background(),
		mkClassicDEXTrade(t, "soroswap", xlm, aqua, 50_000_000_000),
		nil, // no Phase 1 spec
		resolver,
	)
	if got == nil {
		t.Fatal("Phase 2 fallback returned nil; want non-nil")
	}
	// 50_000_000_000 stroops / 10^7 = 5_000 AQUA × $0.001 = $5.00.
	want := "5.00000000"
	if *got != want {
		t.Errorf("got %q, want %q", *got, want)
	}
}

// TestTradeUSDVolume_Phase1WinsBeforePhase2 — when Phase 1 matches
// (USD-pegged classic in spec), the FX resolver MUST NOT be
// consulted. Pinned because a regression that calls the resolver
// every trade would double the trade-insert hot-path cost.
func TestTradeUSDVolume_Phase1WinsBeforePhase2(t *testing.T) {
	t.Parallel()
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("NewClassicAsset USDC: %v", err)
	}
	spec, err := NewUSDVolumeQuoteSpec([]string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}, nil)
	if err != nil {
		t.Fatalf("NewUSDVolumeQuoteSpec: %v", err)
	}

	// Resolver is set up to PANIC if called — proves Phase 1
	// short-circuits before the resolver runs.
	resolver := panicFXResolver{}

	got := tradeUSDVolume(
		context.Background(),
		mkClassicDEXTrade(t, "soroswap", xlm, usdc, 70_000_000),
		spec,
		resolver,
	)
	if got == nil {
		t.Fatal("Phase 1 returned nil; expected USD-pegged classic to populate")
	}
	// 70_000_000 stroops / 10^7 = $7.00.
	if *got != "7.00000000" {
		t.Errorf("got %q, want 7.00000000", *got)
	}
}

// TestTradeUSDVolume_Phase2_NoResolver — when fxResolver is nil
// (the default for tests + ops binary + every deployment that
// hasn't enabled Phase 2), the Phase 1 fall-through stays NULL.
// This is the L2.2 "preserves existing behaviour exactly" guarantee.
func TestTradeUSDVolume_Phase2_NoResolver(t *testing.T) {
	t.Parallel()
	xlm := canonical.NativeAsset()
	aqua, _ := canonical.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")

	got := tradeUSDVolume(
		context.Background(),
		mkClassicDEXTrade(t, "soroswap", xlm, aqua, 50_000_000_000),
		nil, // no Phase 1
		nil, // no Phase 2
	)
	if got != nil {
		t.Errorf("got %q, want nil (no resolver should preserve Phase 1 NULL fall-through)", *got)
	}
}

// TestTradeUSDVolume_Phase2_ResolverNoHit — resolver wired but
// doesn't have a rate for this asset (asset isn't on the
// operator's covered set, or its cache hasn't warmed up). Stays
// NULL — never silently fabricates a rate.
func TestTradeUSDVolume_Phase2_ResolverNoHit(t *testing.T) {
	t.Parallel()
	xlm := canonical.NativeAsset()
	aqua, _ := canonical.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")

	resolver := stubFXResolver{prices: map[string]string{}} // empty

	got := tradeUSDVolume(
		context.Background(),
		mkClassicDEXTrade(t, "soroswap", xlm, aqua, 50_000_000_000),
		nil, resolver,
	)
	if got != nil {
		t.Errorf("got %q, want nil (resolver miss must not fabricate USD volume)", *got)
	}
}

// TestTradeUSDVolume_Phase2_ResolverError — resolver errors fall
// through to NULL silently. Best-effort posture matches Phase 1's
// "trade still inserts; just no USD column".
func TestTradeUSDVolume_Phase2_ResolverError(t *testing.T) {
	t.Parallel()
	xlm := canonical.NativeAsset()
	aqua, _ := canonical.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")

	resolver := stubFXResolver{err: errors.New("postgres unreachable")}

	got := tradeUSDVolume(
		context.Background(),
		mkClassicDEXTrade(t, "soroswap", xlm, aqua, 50_000_000_000),
		nil, resolver,
	)
	if got != nil {
		t.Errorf("got %q, want nil (resolver error must not fabricate USD volume)", *got)
	}
}

// panicFXResolver fails the test loudly if its USDPriceAt is
// called. Proves Phase 1 short-circuits before Phase 2 runs.
type panicFXResolver struct{}

func (panicFXResolver) USDPriceAt(_ context.Context, _ canonical.Asset, _ time.Time) (string, bool, error) {
	panic("Phase 2 resolver should not be consulted when Phase 1 matches")
}

// TestNewUSDVolumeQuoteSpec_RejectsBadInput — typos in operator
// config fail at startup rather than silently match no trade.
func TestNewUSDVolumeQuoteSpec_RejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		classicUSDPegs []string
		sacWrappers    map[string]string
	}{
		{"non-classic in classic peg list (fiat)", []string{"fiat:USD"}, nil},
		{"non-classic in classic peg list (native)", []string{"native"}, nil},
		{"non-classic in classic peg list (soroban)", []string{"CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"}, nil},
		{"unparseable string", []string{"definitely-not-an-asset"}, nil},
		{"sac_wrapper points at non-classic", nil, map[string]string{"CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75": "fiat:USD"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewUSDVolumeQuoteSpec(tc.classicUSDPegs, tc.sacWrappers)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestUSDVolumeQuoteSpec_QuoteUSDPegInfo — direct unit coverage of
// the lookup primitive, both happy and not-found paths.
func TestUSDVolumeQuoteSpec_QuoteUSDPegInfo(t *testing.T) {
	t.Parallel()
	circleUSDCKey := "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	circleUSDCSAC := "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"

	spec, err := NewUSDVolumeQuoteSpec(
		[]string{circleUSDCKey},
		map[string]string{circleUSDCSAC: "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
	)
	if err != nil {
		t.Fatalf("NewUSDVolumeQuoteSpec: %v", err)
	}

	circleUSDC, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	sacAsset, _ := canonical.NewSorobanAsset(circleUSDCSAC)
	otherClassic, _ := canonical.NewClassicAsset("USDC", "GBADOTHERISSUER000000000000000000000000000000000000000000")
	unwrappedSEP41, _ := canonical.NewSorobanAsset("CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7")
	xlm := canonical.NativeAsset()

	cases := []struct {
		name        string
		asset       canonical.Asset
		wantOK      bool
		wantDecimal int
	}{
		{"classic Circle USDC → 7 decimals", circleUSDC, true, 7},
		{"SAC of Circle USDC (transitive) → 7 decimals", sacAsset, true, 7},
		{"different USDC issuer not in allow-list", otherClassic, false, 0},
		{"pure SEP-41 contract not in sac_wrappers", unwrappedSEP41, false, 0},
		{"native XLM is not USD-pegged", xlm, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := spec.QuoteUSDPegInfo(tc.asset)
			if ok != tc.wantOK || d != tc.wantDecimal {
				t.Errorf("got (%d, %v), want (%d, %v)", d, ok, tc.wantDecimal, tc.wantOK)
			}
		})
	}

	// nil receiver behaves as "no spec configured".
	if _, ok := (*USDVolumeQuoteSpec)(nil).QuoteUSDPegInfo(circleUSDC); ok {
		t.Errorf("nil spec should always return ok=false")
	}
}

// TestStore_WouldPopulateUSDVolume — predicate the pipeline sink
// uses to label the trade-inserts coverage metric. Wraps the same
// decision as tradeUSDVolume but exposed publicly so the sink
// doesn't need the unexported helper.
func TestStore_WouldPopulateUSDVolume(t *testing.T) {
	t.Parallel()
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlm, _ := canonical.NewCryptoAsset("XLM")
	circleUSDC, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")

	mkTrade := func(source string, base, quote canonical.Asset, quoteAmt int64) canonical.Trade {
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			t.Fatalf("NewPair: %v", err)
		}
		return canonical.Trade{
			Source:      source,
			Pair:        pair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100_000_000)),
			QuoteAmount: canonical.NewAmount(big.NewInt(quoteAmt)),
		}
	}

	cases := []struct {
		name     string
		store    *Store
		trade    canonical.Trade
		wantTrue bool
	}{
		{
			name:     "off-chain CEX + USD-pegged → populated",
			store:    &Store{},
			trade:    mkTrade("kraken", xlm, usdc, 4_250_000_000),
			wantTrue: true,
		},
		{
			name:     "on-chain DEX with no spec → not populated",
			store:    &Store{},
			trade:    mkTrade("soroswap", canonical.NativeAsset(), circleUSDC, 1_000_000_000),
			wantTrue: false,
		},
		{
			name: "on-chain DEX with spec that recognises the quote → populated",
			store: func() *Store {
				s := &Store{}
				spec, _ := NewUSDVolumeQuoteSpec(
					[]string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
					nil,
				)
				s.SetUSDVolumeQuoteSpec(spec)
				return s
			}(),
			trade:    mkTrade("soroswap", canonical.NativeAsset(), circleUSDC, 10_000_000),
			wantTrue: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.store.WouldPopulateUSDVolume(context.Background(), tc.trade)
			if got != tc.wantTrue {
				t.Errorf("got %v, want %v", got, tc.wantTrue)
			}
		})
	}
}
