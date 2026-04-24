package aggregate

import "github.com/RatesEngine/rates-engine/internal/canonical"

// Stablecoin → fiat proxy mapping — **aggregator policy, not decoder
// policy**.
//
// Why this belongs here and not at ingest: pegs can break. USDT
// trading at $0.9680 during a stress event IS news — folding
// USDT→USD unconditionally at decode time would hide a depeg from
// every downstream consumer. We store the raw pair (`XLM/USDT`,
// `XLM/USDC`) on the way in, and the aggregator rewrites
// quote-side stablecoins to their target fiat at VWAP compute
// time only. See CLAUDE.md § "stablecoins-as-fiat is aggregator
// policy" and ADR-0014's stablecoin-code notes.
//
// Extension is a one-line amendment — add the new ticker here and
// ensure it's already on the `knownCryptoCodes` allow-list in
// internal/canonical/asset_crypto.go. If you catch yourself mapping
// a token whose stable peg is disputed (e.g. an algo-stable that
// has failed before), the aggregator is the wrong layer — that
// belongs behind a per-deployment feature flag or exclusion.

var stablecoinFiatProxy = map[string]string{
	// USD-pegged stablecoins.
	"USDT":  "USD",
	"USDC":  "USD",
	"DAI":   "USD",
	"PYUSD": "USD",
	"USDP":  "USD",
	// EUR-pegged stablecoins.
	"EURC":  "EUR",
	"EUROC": "EUR",
	"EUROB": "EUR",
	// MXN-pegged stablecoin.
	"MXNe": "MXN",
}

// FiatProxy returns the fiat asset a stablecoin tracks, and ok=true,
// when the given asset is a `crypto:<STABLE>` ticker with a known
// peg. Returns ok=false for everything else (real crypto, fiat
// already, native/classic/soroban on-chain assets).
//
// Classic-issued stablecoins (`USDC-GA5ZSEJY…` = Circle's
// Stellar-classic USDC) are intentionally NOT mapped here: classic
// assets already carry full issuer identity, and an operator who
// wants classic-USDC→USD substitution configures that downstream
// alongside per-issuer trust. The crypto-prefixed form is the
// abstract global ticker — unambiguous to proxy.
func FiatProxy(a canonical.Asset) (canonical.Asset, bool) {
	if a.Type != canonical.AssetCrypto {
		return canonical.Asset{}, false
	}
	fiat, ok := stablecoinFiatProxy[a.Code]
	if !ok {
		return canonical.Asset{}, false
	}
	// All targets ("USD", "EUR", "MXN") are on the ADR-0010 fiat
	// allow-list — NewFiatAsset cannot fail here. Construct via the
	// typed ctor so future additions catch mis-spellings at startup.
	proxy, err := canonical.NewFiatAsset(fiat)
	if err != nil {
		// Unreachable unless the allow-list regresses; preserve the
		// "no proxy available" semantic rather than panic, so a
		// misconfiguration degrades to "stablecoin stays crypto" at
		// the aggregator.
		return canonical.Asset{}, false
	}
	return proxy, true
}

// ProxyPair rewrites the quote side of a pair through the
// stablecoin→fiat map. Returns ok=false when the quote isn't a
// known stablecoin, i.e. the pair is already fiat-denominated,
// crypto/crypto, or any form not matching.
//
// Only the QUOTE is rewritten. The semantic question a VWAP
// answers is "what is the price of BASE expressed in quote?" —
// rewriting XLM/USDT → XLM/USD preserves that axis. Rewriting the
// base side would re-pose the question on the wrong axis
// (USDC/XLM → USD/XLM recasts a stablecoin-denominated market as
// a fiat-denominated one with the assets in the wrong roles).
func ProxyPair(p canonical.Pair) (canonical.Pair, bool) {
	proxy, ok := FiatProxy(p.Quote)
	if !ok {
		return canonical.Pair{}, false
	}
	rewritten, err := canonical.NewPair(p.Base, proxy)
	if err != nil {
		// Base and proxy quote collide (theoretical: base is
		// fiat:USD and quote is crypto:USDT). Skip rather than
		// fail — the aggregator treats a non-proxiable pair as
		// "leave it alone" so real-world edge cases don't crash
		// the tick.
		return canonical.Pair{}, false
	}
	return rewritten, true
}

// ProxyTrade returns a copy of the trade with its Pair rewritten
// through ProxyPair. Returns the original trade and ok=false when
// no proxy applies — callers decide whether to pass through the
// unrewritten row (useful when the target pair set already
// includes the original) or drop it.
func ProxyTrade(t canonical.Trade) (canonical.Trade, bool) {
	p, ok := ProxyPair(t.Pair)
	if !ok {
		return t, false
	}
	t.Pair = p
	return t, true
}
