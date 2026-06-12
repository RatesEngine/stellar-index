package binance

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DefaultPairs returns the built-in pair map for Binance — the
// common set we stream when the operator enables Binance in config
// without specifying a pair list. Covers the largest XLM markets,
// the two reference crypto anchors, and the top-cap globals that
// every CoinGecko-class consumer expects.
//
// Returned map is Binance-symbol (uppercase, no separator) →
// canonical.Pair. Passed into NewStreamer. Extending the set is a
// one-line addition here; per-operator overrides land in config
// in a follow-up PR.
//
// Pair rationale:
//   - XLMUSDT — largest XLM market globally; canonical "XLM/USD" proxy
//     once the aggregator's stablecoin-fiat table maps USDT→USD.
//   - XLMBTC  — deep BTC-pair liquidity; triangulates for pairs we
//     don't have direct routes to.
//   - BTCUSDT — anchor for every crypto-derived rate in our triangulation
//     graph.
//   - ETHUSDT — second crypto anchor; needed for any ETH-quoted Soroban
//     DEX pair.
//   - {ADA,ATOM,AVAX,BCH,BNB,DASH,DOGE,DOT,LINK,LTC,NEAR,SHIB,SOL,
//     TON,TRX,UNI,XRP}USDT — top-cap globals for cross-venue VWAP
//     coverage. All verified TRADING on Binance via /exchangeInfo
//     2026-05-05.
func DefaultPairs() (map[string]canonical.Pair, error) {
	xlm, err := canonical.NewCryptoAsset("XLM")
	if err != nil {
		return nil, fmt.Errorf("XLM: %w", err)
	}
	btc, err := canonical.NewCryptoAsset("BTC")
	if err != nil {
		return nil, fmt.Errorf("BTC: %w", err)
	}
	eth, err := canonical.NewCryptoAsset("ETH")
	if err != nil {
		return nil, fmt.Errorf("ETH: %w", err)
	}
	usdt, err := canonical.NewCryptoAsset("USDT")
	if err != nil {
		return nil, fmt.Errorf("USDT: %w", err)
	}
	eur, err := canonical.NewFiatAsset("EUR")
	if err != nil {
		return nil, fmt.Errorf("EUR: %w", err)
	}
	gbp, err := canonical.NewFiatAsset("GBP")
	if err != nil {
		return nil, fmt.Errorf("GBP: %w", err)
	}

	majors := []string{
		"ADA", "ATOM", "AVAX", "BCH", "BNB", "DASH", "DOGE", "DOT",
		"LINK", "LTC", "NEAR", "SHIB", "SOL", "TON", "TRX", "UNI", "XRP",
	}
	majorAssets := make(map[string]canonical.Asset, len(majors))
	for _, code := range majors {
		a, err := canonical.NewCryptoAsset(code)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", code, err)
		}
		majorAssets[code] = a
	}

	pairs := []struct {
		symbol string
		base   canonical.Asset
		quote  canonical.Asset
	}{
		{"XLMUSDT", xlm, usdt},
		{"XLMBTC", xlm, btc},
		// BTC + ETH cross-fiat (2026-05-14): Binance supports BTCEUR
		// + ETHEUR + BTCGBP + ETHGBP natively. Adding so multi-source
		// VWAP works on the most-asked-for fiat conversions (the only
		// pre-fix venue publishing BTC/EUR was Bitstamp).
		{"BTCUSDT", btc, usdt},
		{"BTCEUR", btc, eur},
		{"BTCGBP", btc, gbp},
		{"ETHUSDT", eth, usdt},
		{"ETHEUR", eth, eur},
		{"ETHGBP", eth, gbp},
	}
	for _, code := range majors {
		pairs = append(pairs, struct {
			symbol string
			base   canonical.Asset
			quote  canonical.Asset
		}{code + "USDT", majorAssets[code], usdt})
	}

	out := make(map[string]canonical.Pair, len(pairs))
	for _, p := range pairs {
		pair, err := canonical.NewPair(p.base, p.quote)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.symbol, err)
		}
		out[p.symbol] = pair
	}
	return out, nil
}

// DefaultPairList returns the same set as DefaultPairs but as a
// []canonical.Pair — the shape Streamer.Start expects. Convenience
// for callers that just want "stream everything the default config
// covers."
func DefaultPairList() ([]canonical.Pair, error) {
	m, err := DefaultPairs()
	if err != nil {
		return nil, err
	}
	out := make([]canonical.Pair, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	return out, nil
}
