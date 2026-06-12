package coinbase

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DefaultPairs returns the Coinbase Exchange product → canonical.Pair
// map for the default set. US-listed XLM-USD is the flagship pair
// for this venue in our fleet; the reference anchors plus top-cap
// globals round out the triangulation + cross-venue VWAP graph.
//
// Coinbase product IDs use "-" separators: "XLM-USD", "BTC-USD".
//
// Coverage rationale:
//   - XLM-USD — US price discovery for XLM; Coinbase is the
//     regulated-venue XLM reference.
//   - BTC-USD, ETH-USD — anchors.
//   - {ADA,ATOM,AVAX,BCH,BNB,DOGE,DOT,LINK,LTC,NEAR,SHIB,SOL,TON,
//     UNI,XRP}-USD — top-cap globals against USD. All verified live
//     via /products/<symbol> on 2026-05-05.
//
// Notable absences: DASH (Coinbase delisted, US AML pressure) and
// TRX (never listed). The Kraken/Bitstamp/Binance triple covers
// both for cross-venue VWAP.
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
	usd, err := canonical.NewFiatAsset("USD")
	if err != nil {
		return nil, fmt.Errorf("USD: %w", err)
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
		"ADA", "ATOM", "AVAX", "BCH", "BNB", "DOGE", "DOT", "LINK",
		"LTC", "NEAR", "SHIB", "SOL", "TON", "UNI", "XRP",
	}
	majorAssets := make(map[string]canonical.Asset, len(majors))
	for _, code := range majors {
		a, err := canonical.NewCryptoAsset(code)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", code, err)
		}
		majorAssets[code] = a
	}

	spec := []struct {
		symbol string
		base   canonical.Asset
		quote  canonical.Asset
	}{
		{"XLM-USD", xlm, usd},
		// BTC + ETH cross-fiat (2026-05-14): pre-fix BTC-EUR + ETH-EUR
		// were single-source (only Bitstamp publishes), causing
		// permanent Phase 2 freeze fires on those pairs. Coinbase
		// supports BTC-EUR + BTC-GBP + ETH-EUR + ETH-GBP natively;
		// adding to the cross-venue set so VWAP has multi-source
		// corroboration on the most-asked-for fiat conversions.
		{"BTC-USD", btc, usd},
		{"BTC-EUR", btc, eur},
		{"BTC-GBP", btc, gbp},
		{"ETH-USD", eth, usd},
		{"ETH-EUR", eth, eur},
		{"ETH-GBP", eth, gbp},
	}
	for _, code := range majors {
		spec = append(spec, struct {
			symbol string
			base   canonical.Asset
			quote  canonical.Asset
		}{code + "-USD", majorAssets[code], usd})
	}
	out := make(map[string]canonical.Pair, len(spec))
	for _, s := range spec {
		p, err := canonical.NewPair(s.base, s.quote)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.symbol, err)
		}
		out[s.symbol] = p
	}
	return out, nil
}

// DefaultPairList — Streamer.Start friendly view.
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
