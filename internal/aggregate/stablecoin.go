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

// FiatBackers returns the stablecoin crypto tickers pegged to the
// given fiat code (e.g. "USD" → ["USDT", "USDC", "DAI", "PYUSD",
// "USDP"]). Returns nil for fiat codes that no stablecoin in the
// proxy map targets — the orchestrator treats that as "nothing to
// fetch beyond the direct pair."
//
// Deterministic ordering is not promised; callers that need stable
// output for assertions should sort. The real consumer (the
// orchestrator) treats the set as a fetch plan and the order of
// parallel TradesInRange calls doesn't affect the VWAP.
func FiatBackers(fiat string) []string {
	var out []string
	for stable, target := range stablecoinFiatProxy {
		if target == fiat {
			out = append(out, stable)
		}
	}
	return out
}

// ExpandTargetPair enumerates the source pairs the aggregator
// should fetch from Timescale to populate a fiat-denominated
// target pair's window.
//
//   - If the target's quote is fiat, the result contains the direct
//     target pair (operators may have real-fiat trades from FX
//     connectors) plus one entry per stablecoin backer (`BASE/USDT`,
//     `BASE/USDC`, …). Trades fetched under backer pairs are then
//     rewritten via ProxyPair before VWAP.
//   - If the target is NOT fiat-denominated (crypto/crypto,
//     crypto/classic, etc.), the result is just the target itself
//     — there is no stablecoin-proxy expansion to do.
//
// Equivalent to [ExpandTargetPairWithClassicPegs] with an empty
// classic-pegs slice — kept as a thin wrapper so the existing
// (target-only) call sites and tests stay short.
//
// An error is returned only if the target is malformed (pair
// validation already happens upstream, so this mostly short-
// circuits) — callers can safely treat err != nil as a
// configuration bug.
func ExpandTargetPair(target canonical.Pair) ([]canonical.Pair, error) {
	return ExpandTargetPairWithClassicPegs(target, nil)
}

// ExpandTargetPairWithClassicPegs is [ExpandTargetPair] augmented
// with operator-declared classic-asset stablecoins. On Stellar
// mainnet today the dominant USD-denominated DEX pairs aren't quoted
// in the abstract `crypto:USDC` ticker but in classic credits like
// `USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`
// (Circle's Stellar-classic USDC). Those quotes carry full issuer
// identity at the canonical layer and aren't in the abstract
// stablecoin map; the operator's allow-list (configured under
// `[trades].usd_pegged_classic_assets` and reused here) names which
// classic credits they trust as fiat-equivalent.
//
// classicUSDPegs is the operator's USD-pegged classic asset list —
// each entry must be `Type=AssetClassic`. Non-classic entries are
// silently skipped (defensive — the parser at config load already
// rejects malformed shapes). Entries are appended to the source
// list when the target's fiat is "USD"; for non-USD fiat targets
// they're ignored. The orchestrator's existing
// `Pair=target`-rewrite step in `fetchForTarget` lifts the fetched
// trades onto the target pair without needing a per-classic
// `ProxyPair` rule.
func ExpandTargetPairWithClassicPegs(target canonical.Pair, classicUSDPegs []canonical.Asset) ([]canonical.Pair, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if target.Quote.Type != canonical.AssetFiat {
		// Not a fiat target — no stablecoin expansion.
		return []canonical.Pair{target}, nil
	}
	backers := FiatBackers(target.Quote.Code)
	out := make([]canonical.Pair, 0, 1+len(backers)+len(classicUSDPegs))
	out = append(out, target)
	for _, ticker := range backers {
		stable, err := canonical.NewCryptoAsset(ticker)
		if err != nil {
			// Already guarded by TestFiatProxy_CodesAreOnCanonicalAllowList
			// — skip defensively so a future registry change can't crash
			// the tick loop.
			continue
		}
		src, err := canonical.NewPair(target.Base, stable)
		if err != nil {
			// target.Base happens to equal the stablecoin (e.g.
			// target is USDC/fiat:USD — base is `crypto:USDC`, and
			// we'd build `crypto:USDC/crypto:USDC`). Skip the
			// collision; the direct target entry still stands.
			continue
		}
		out = append(out, src)
	}
	if target.Quote.Code == "USD" {
		for _, classic := range classicUSDPegs {
			if classic.Type != canonical.AssetClassic {
				continue
			}
			src, err := canonical.NewPair(target.Base, classic)
			if err != nil {
				// Same defensive-skip rationale as above — never
				// abort the tick loop on a single malformed entry.
				continue
			}
			out = append(out, src)
		}
	}
	return out, nil
}
