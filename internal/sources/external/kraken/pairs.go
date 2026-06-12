package kraken

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DefaultPairs returns the built-in Kraken symbol → canonical.Pair
// map — XLM across six fiat currencies, two crypto anchors, plus
// the top-cap globals against USD. Kraken is the widest XLM-fiat
// source we integrate AND lists every major we want for cross-
// venue VWAP coverage.
//
// XLM is represented as crypto:XLM so it aligns with Binance's
// XLMUSDT and with Reflector CEX outputs. Fiats use the fiat:
// asset variant.
//
// Kraken wire-format symbols use "/" separators — "XLM/USD", not
// "XLMUSD".
//
// Top-cap globals all verified live via /0/public/Ticker on
// 2026-05-05 (full set: ADA, ATOM, AVAX, BCH, BNB, DASH, DOGE,
// DOT, LINK, LTC, NEAR, SHIB, SOL, TON, TRX, UNI, XRP).
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
	}
	// BTC + ETH cross-fiat. Pre-2026-05-14 these were USD-only,
	// which left BTC/EUR (and ETH/EUR) with single-source coverage
	// (only Bitstamp publishes them in our integration set). Phase 2
	// freeze fired permanently on those pairs as a result. Adding
	// BTC + ETH × {EUR, GBP} to the cross-venue set so multi-source
	// VWAP works on the most-asked-for fiat conversions.
	for _, base := range []struct {
		code  string
		asset canonical.Asset
	}{{"BTC", btc}, {"ETH", eth}} {
		for _, fiat := range []string{"USD", "EUR", "GBP"} {
			spec = append(spec, struct {
				symbol string
				base   canonical.Asset
				quote  canonical.Asset
			}{base.code + "/" + fiat, base.asset, fiatAssets[fiat]})
		}
	}
	for _, code := range majors {
		spec = append(spec, struct {
			symbol string
			base   canonical.Asset
			quote  canonical.Asset
		}{code + "/USD", majorAssets[code], fiatAssets["USD"]})
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
