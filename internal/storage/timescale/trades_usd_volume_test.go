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
// happy paths: external (CEX/FX) source + USD-or-pegged quote
// → numeric string at scale-corrected USD value.
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
			got := tradeUSDVolume(mkTrade(tc.source, tc.quote, tc.amt))
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
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlm, _ := canonical.NewCryptoAsset("XLM")

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
	}{
		{"on-chain DEX (soroswap) — quote scale unknowable here", "soroswap", xlm, usdc, 1_000_000_000},
		{"binance + EUR quote — not USD-pegged", "binance", xlm, eur, 1_000_000_000},
		{"unknown source — fail-closed", "unregistered-venue", xlm, usd, 1_000_000_000},
		{"oracle-class source (reflector)", "reflector-cex", xlm, usd, 1_000_000_000},
		{"aggregator-class source (coingecko)", "coingecko", xlm, usd, 1_000_000_000},
		{"zero quote_amount", "binance", xlm, usd, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tradeUSDVolume(mkTrade(tc.source, tc.base, tc.quote, tc.amt))
			if got != nil {
				t.Errorf("got %q, want nil", *got)
			}
		})
	}
}
