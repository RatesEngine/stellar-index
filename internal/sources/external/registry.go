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
//
// BackfillSafe is default-false for on-chain Soroban sources: a
// `update_contract` upgrade can change event body schemas, so a
// current-version decoder cannot be trusted on historical ledgers
// without a per-WASM-hash audit. Flip to true per-source as the audit
// (`ratesengine-ops wasm-history`) confirms the decoder handles every
// version that ran for the replay range. Off-chain sources and SDEX
// are BackfillSafe=true unconditionally — no on-chain WASM dependency.
var Registry = map[string]Metadata{
	// ─── On-chain exchanges (dispatcher-path; listed here so the
	// aggregator has a single lookup table) ──────────────────────
	"soroswap": {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/soroswap.md */},
	"aquarius": {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"phoenix":  {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"comet":    {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"sdex":     {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},

	// ─── On-chain oracles ────────────────────────────────────────
	// Excluded from VWAP by default — they publish already-aggregated
	// derived prices with their own governance and methodology. Reported
	// alongside for transparency. Operator opts one in per-source via
	// config if they want oracle-inclusive aggregation.
	"reflector-dex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"reflector-cex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"reflector-fx":  {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: false},
	"redstone":      {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/redstone.md */},
	"band":          {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/band.md */},

	// ─── Off-chain centralised exchanges (this package's scope) ─
	"binance":  {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"kraken":   {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true /* implemented, but 720-interval cap: ~30d at 1h */, BackfillSafe: true},
	"bitstamp": {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"coinbase": {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"bitfinex": {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},

	// ─── Institutional FX feeds ──────────────────────────────────
	"polygon-forex":    {Class: ClassExchange, Subclass: SubclassFX, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true, BackfillSafe: true},
	"exchangeratesapi": {Class: ClassExchange, Subclass: SubclassFX, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true, BackfillSafe: true},

	// ─── Aggregators (divergence signal; excluded from VWAP) ─────
	"coingecko":     {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"coinmarketcap": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true, BackfillSafe: true},
	"cryptocompare": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true, BackfillSafe: true},

	// ─── Sovereign daily anchors (sanity check only) ─────────────
	"ecb":     {Class: ClassAuthoritySanity, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"fed-h10": {Class: ClassAuthoritySanity, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true},
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
		BackfillSafe:  false, // fail-closed — unknown sources cannot backfill
	}
}

// IncludeInVWAP is a convenience wrapper for the most-common
// aggregator-side question. Returns true only when the source is
// registered AND its IncludeInVWAP flag is true.
func IncludeInVWAP(source string) bool {
	return Lookup(source).IncludeInVWAP
}

// BackfillSafe reports whether the source's decoder is currently
// authorised to run against historical ledger ranges. The
// `ratesengine-ops backfill` command refuses to enqueue a source
// against a historical range when this returns false — the audit must
// land first to avoid decoding old WASM-event bodies with a current-
// only decoder. See [Metadata.BackfillSafe] for the policy detail.
func BackfillSafe(source string) bool {
	return Lookup(source).BackfillSafe
}
