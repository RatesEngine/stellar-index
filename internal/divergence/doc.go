// Package divergence cross-checks our computed VWAP against
// external reference oracles + aggregators. Per ADR-0019 §"Layer 5
// — Cross-reference divergence monitoring", this is the last
// line of defense against the case where every layer above
// (multi-source consensus, source-class exclusion, outlier filter)
// somehow agrees on a wrong price — divergence catches it by
// comparing against an independent universe of references.
//
// # Scope
//
// PR A (this package as initially shipped):
//
//   - [Reference] interface — every external source plugs in here
//   - [Compare] — gather references in parallel, compute the
//     divergence percentage from the median
//   - [Result] — the wire shape consumed by the aggregator's
//     bucket-close path to set [api.v1.Flags].DivergenceWarning
//   - [CoinGecko] — reference implementation against CoinGecko's
//     /simple/price endpoint, demonstrating the contract
//
// Subsequent PRs add more references:
//
//   - CoinMarketCap (HTTP)
//   - Reflector (Stellar contract — DEX/CEX/FX variants)
//   - Band (Stellar contract)
//   - Redstone (Stellar contract — WritePrices event subscription)
//   - Chainlink (HTTP — no live Stellar Data Feed at audit time)
//
// # Algorithm
//
// At bucket close the aggregator calls:
//
//	res := divergence.Compare(ctx, refs, pair, ourPrice, observedAt)
//
// Compare fetches each reference's price in parallel (one
// goroutine per reference, bounded timeout). It collects the
// successful responses, computes the median, then the percentage
// deviation between our price and the median. The aggregator
// reads `res.DivergencePct` and gates `flags.divergence_warning`
// on a configurable threshold.
//
// # Tolerance for partial failures
//
// References can fail (HTTP timeout, asset unsupported, vendor
// outage). [Compare] is designed to surface partial results: any
// successful reference contributes to the median; failures are
// recorded in [Result.Failures] for operator visibility.
//
// The threshold on "do we trust the median?" is implicit in the
// caller's logic — if N of M references succeeded and N is too
// small to constitute a meaningful consensus, the aggregator
// SHOULD NOT fire divergence_warning purely on its own. PR A
// exposes the count via [Result.SuccessCount]; PR B/C will add
// confidence-weighted aggregation.
//
// # Concurrency
//
// All exported types are safe for concurrent use after
// construction. [Reference] implementations MUST be safe for
// concurrent LookupPrice calls (Compare invokes them in
// parallel goroutines).
package divergence
