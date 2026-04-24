package kraken

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// DefaultPairs returns the built-in Kraken symbol → canonical.Pair
// map — XLM across six fiat currencies plus two crypto anchors.
// Kraken is the widest XLM-fiat source we integrate; the default
// set captures that value without per-operator tuning.
//
// XLM is represented as crypto:XLM so it aligns with Binance's
// XLMUSDT and with Reflector CEX outputs. Fiats use the fiat:
// asset variant.
//
// Kraken wire-format symbols use "/" separators — "XLM/USD", not
// "XLMUSD".
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

	// The fiat allow-list (ADR-0010) covers all six Kraken XLM
	// quote currencies.
	fiats := []string{"USD", "EUR", "GBP", "AUD", "CAD", "CHF"}
	fiatAssets := make(map[string]canonical.Asset, len(fiats))
	for _, code := range fiats {
		a, err := canonical.NewFiatAsset(code)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", code, err)
		}
		fiatAssets[code] = a
	}

	spec := []struct {
		symbol string
		base   canonical.Asset
		quote  canonical.Asset
	}{
		{"XLM/USD", xlm, fiatAssets["USD"]},
		{"XLM/EUR", xlm, fiatAssets["EUR"]},
		{"XLM/GBP", xlm, fiatAssets["GBP"]},
		{"XLM/AUD", xlm, fiatAssets["AUD"]},
		{"XLM/CAD", xlm, fiatAssets["CAD"]},
		{"XLM/CHF", xlm, fiatAssets["CHF"]},
		{"BTC/USD", btc, fiatAssets["USD"]},
		{"ETH/USD", eth, fiatAssets["USD"]},
	}

	out := make(map[string]canonical.Pair, len(spec))
	for _, s := range spec {
		pair, err := canonical.NewPair(s.base, s.quote)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.symbol, err)
		}
		out[s.symbol] = pair
	}
	return out, nil
}

// DefaultPairList returns the same set as DefaultPairs as a slice.
// Feeds Streamer.Start.
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
