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
// Wired today:
//
//   - [Reference] interface — every external source plugs in here.
//   - [Compare] — gather references in parallel, compute the
//     divergence percentage from the median.
//   - [Result] — the wire shape consumed by the aggregator's
//     bucket-close path to set [api.v1.Flags].DivergenceWarning.
//   - [Service] / [CachedResult] — the worker shape the aggregator
//     binary instantiates; per-pair [RefreshPair] is driven by the
//     orchestrator's Tick.
//   - [CoinGeckoReference] — HTTP reference against CoinGecko's
//     /simple/price endpoint. Always-on by default (free tier,
//     no auth).
//   - [ChainlinkReference] — HTTP reference against Chainlink's
//     EVM AggregatorV3 `latestAnswer()` selector. Off by default;
//     operator opts in via FeedMap of mainnet feed addresses.
//
// On-chain oracles (Reflector, Band, Redstone) are NOT plugged in
// here — they ingest as on-chain *sources* (`internal/sources/{
// reflector, band, redstone}`) and contribute to the underlying
// VWAP itself, not to the divergence cross-check. CoinMarketCap is
// a candidate future HTTP reference; deferred until an operator
// asks for a second aggregator behind CoinGecko.
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
// SHOULD NOT fire divergence_warning purely on its own.
// [Result.SuccessCount] exposes the count for that decision;
// [ServiceOptions.MinSourcesForWarning] (config:
// `[divergence].min_sources_for_warning`, default 2) is the
// operator-tunable floor.
//
// # Concurrency
//
// All exported types are safe for concurrent use after
// construction. [Reference] implementations MUST be safe for
// concurrent LookupPrice calls (Compare invokes them in
// parallel goroutines).
package divergence
