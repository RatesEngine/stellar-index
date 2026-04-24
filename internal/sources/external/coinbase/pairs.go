package coinbase

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// DefaultPairs returns the Coinbase Exchange product → canonical.Pair
// map for the default set. US-listed XLM-USD is the flagship pair
// for this venue in our fleet; the reference anchors round out the
// triangulation graph.
//
// Coinbase product IDs use "-" separators: "XLM-USD", "BTC-USD".
//
// Coverage rationale:
//   - XLM-USD — US price discovery for XLM; Coinbase is the
//     regulated-venue XLM reference.
//   - BTC-USD, ETH-USD — anchors.
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

	spec := []struct {
		symbol string
		base   canonical.Asset
		quote  canonical.Asset
	}{
		{"XLM-USD", xlm, usd},
		{"BTC-USD", btc, usd},
		{"ETH-USD", eth, usd},
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
