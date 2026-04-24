package external

// Registry is the source-of-truth metadata table for every source the
// aggregator knows about — both external (this package's responsibility)
// AND on-chain (internal/sources/*). Centralising here lets the
// aggregator do `Registry[trade.Source].Class` without importing
// every source package to ask.
//
// Lookups that miss the registry fall back to ClassExchange-with-
// IncludeInVWAP=false, which makes unknown sources visible in
// /v1/sources but not contributing to VWAP — fail-closed on
// misconfiguration.
//
// Operators override DefaultWeight and IncludeInVWAP via config
// (see internal/config/external.go once it lands). Class and Paid
// are venue facts, not per-deployment — don't expose them as config.
var Registry = map[string]Metadata{
	// ─── On-chain exchanges (dispatcher-path; listed here so the
	// aggregator has a single lookup table) ──────────────────────
	"soroswap": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"aquarius": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"phoenix":  {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"comet":    {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"sdex":     {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},

	// ─── On-chain oracles ────────────────────────────────────────
	// Excluded from VWAP by default — they publish already-aggregated
	// derived prices with their own governance and methodology. Reported
	// alongside for transparency. Operator opts one in per-source via
	// config if they want oracle-inclusive aggregation.
	"reflector-dex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"reflector-cex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"reflector-fx":  {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"redstone":      {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"band":          {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},

	// ─── Off-chain centralised exchanges (this package's scope) ─
	"binance":  {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"kraken":   {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true /* implemented, but 720-interval cap: ~30d at 1h */},
	"bitstamp": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"coinbase": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},
	"bitfinex": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true},

	// ─── Institutional FX feeds ──────────────────────────────────
	"polygon-forex":    {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true},
	"exchangeratesapi": {Class: ClassExchange, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true},

	// ─── Aggregators (divergence signal; excluded from VWAP) ─────
	"coingecko":     {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"coinmarketcap": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true},
	"cryptocompare": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true},

	// ─── Sovereign daily anchors (sanity check only) ─────────────
	"ecb":     {Class: ClassAuthoritySanity, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
	"fed-h10": {Class: ClassAuthoritySanity, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true},
}

// Lookup returns metadata for a source, with a safe fallback for
// unknown names. The fallback intentionally excludes-from-VWAP so a
// typo or renamed source can't quietly inject unauthorised data into
// aggregation — it shows up in /v1/sources as `class=exchange,
// included_in_vwap=false` and ops fixes the registry entry.
func Lookup(source string) Metadata {
	if m, ok := Registry[source]; ok {
		return m
	}
	return Metadata{
		Class:         ClassExchange,
		DefaultWeight: 100,
		IncludeInVWAP: false, // fail-closed — see doc above
	}
}

// IncludeInVWAP is a convenience wrapper for the most-common
// aggregator-side question. Returns true only when the source is
// registered AND its IncludeInVWAP flag is true.
func IncludeInVWAP(source string) bool {
	return Lookup(source).IncludeInVWAP
}
