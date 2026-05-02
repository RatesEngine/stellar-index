package timescale

import (
	"math/big"
	"testing"

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
			got := tradeUSDVolume(mkTrade(tc.source, tc.quote, tc.amt), nil)
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
			got := tradeUSDVolume(mkTrade(tc.source, tc.base, tc.quote, tc.amt), spec)
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
			got := tradeUSDVolume(mkTrade(tc.source, tc.base, tc.quote, tc.amt), tc.spec)
			if got != nil {
				t.Errorf("got %q, want nil", *got)
			}
		})
	}
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
