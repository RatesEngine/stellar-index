package bitstamp

import (
	"fmt"
	"strings"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DefaultPairs returns the Bitstamp symbol → canonical.Pair map for
// the default set of markets the indexer subscribes to when Bitstamp
// is enabled without operator pair overrides.
//
// Bitstamp symbols are lowercase concatenated ("xlmusd" not "XLM/USD"
// or "XLMUSD"). We normalise to that format and the channel prefix
// "live_trades_" is added by the streamer when subscribing.
//
// Coverage rationale:
//   - XLM/USD, XLM/EUR, XLM/GBP — adds EUR/GBP native quotes Kraken
//     already covers too, so redundancy across the two XLM fiat
//     venues strengthens the aggregator's confidence.
//   - XLM/BTC — deep European-retail BTC-pair XLM liquidity.
//   - BTC/USD, ETH/USD — reference anchors.
//   - BTC/EUR — European fiat BTC depth, used for triangulation.
//   - {ADA,ATOM,AVAX,BCH,BNB,DASH,DOGE,DOT,LINK,LTC,NEAR,SHIB,SOL,
//     TON,TRX,UNI,XRP}/USD — top-cap globals against USD for
//     cross-venue VWAP coverage. All verified live via /api/v2/ticker
//     on 2026-05-05.
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

	fiats := map[string]canonical.Asset{}
	for _, code := range []string{"USD", "EUR", "GBP"} {
		a, err := canonical.NewFiatAsset(code)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", code, err)
		}
		fiats[code] = a
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
		{"xlmusd", xlm, fiats["USD"]},
		{"xlmeur", xlm, fiats["EUR"]},
		{"xlmgbp", xlm, fiats["GBP"]},
		{"xlmbtc", xlm, btc},
		// BTC + ETH cross-fiat (2026-05-14): adding btcgbp, etheur,
		// ethgbp so the cross-venue VWAP set has full BTC+ETH × {USD,
		// EUR, GBP} parity across all 4 CEXes (binance, coinbase,
		// kraken, bitstamp).
		{"btcusd", btc, fiats["USD"]},
		{"btceur", btc, fiats["EUR"]},
		{"btcgbp", btc, fiats["GBP"]},
		{"ethusd", eth, fiats["USD"]},
		{"etheur", eth, fiats["EUR"]},
		{"ethgbp", eth, fiats["GBP"]},
	}
	for _, code := range majors {
		// Bitstamp uses lowercase concatenated symbols.
		sym := strings.ToLower(code) + "usd"
		spec = append(spec, struct {
			symbol string
			base   canonical.Asset
			quote  canonical.Asset
		}{sym, majorAssets[code], fiats["USD"]})
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
