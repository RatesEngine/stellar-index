package external

import "sort"

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
// Operators toggle individual venues via config
// (see `ExternalConfig` in internal/config/config.go — each venue
// has an `enabled` flag that disables it when false). Class and
// Paid are venue facts, not per-deployment — they aren't exposed
// as config. Per-venue DefaultWeight / IncludeInVWAP overrides are
// not wired today; if an operator needs them, that's a future
// follow-up rather than a missing surface.
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
	"aquarius": {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; 313 pools, 3 unique WASMs, shared-emitter topology. See docs/operations/wasm-audits/aquarius.md */},
	"phoenix":  {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; 11 pools, 2 unique WASM hashes, both contain 8 expected swap-field strings. See docs/operations/wasm-audits/phoenix.md */},
	"comet":    {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; only known mainnet pool is Blend backstop CAS3FL6T..., WASM 8abc2891... verified. See docs/operations/wasm-audits/comet.md */},
	"sdex":     {Class: ClassExchange, Subclass: SubclassDEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},

	// ─── On-chain oracles ────────────────────────────────────────
	// Excluded from VWAP by default — they publish already-aggregated
	// derived prices with their own governance and methodology. Reported
	// alongside for transparency. Operator opts one in per-source via
	// config if they want oracle-inclusive aggregation.
	"reflector-dex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; v2 disassembly confirms compat. See docs/operations/wasm-audits/reflector.md */},
	"reflector-cex": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; v2 disassembly confirms compat. See docs/operations/wasm-audits/reflector.md */},
	"reflector-fx":  {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/reflector.md */},
	"redstone":      {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/redstone.md */},
	"band":          {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-04-29; see docs/operations/wasm-audits/band.md */},

	// ─── On-chain lending protocols ─────────────────────────────
	// Auction events surface stress-prices during liquidations; we
	// report them alongside as a secondary validation surface but
	// they DO NOT contribute to VWAP. See
	// docs/discovery/dexes-amms/blend.md and the blend source
	// package README for the full extraction scope.
	"blend": {Class: ClassLending, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-05-02; 11 contracts (9 pools + backstop + factory), 3 unique WASMs, no mid-life upgrades observed in 5h4m walk over [50457424, 62249727]. See docs/operations/wasm-audits/blend.md §"Phase 2 results". */},

	// ─── On-chain routers + aggregator vaults ────────────────────
	// Excluded from VWAP — these don't emit independent trades; they
	// invoke other contracts (DEX pairs / lending pools) which do.
	// Captured for per-tx attribution + user-intent visibility (path
	// requested vs path realised; aggregator vault → underlying
	// protocol exposures). See docs/architecture/explorer-data-
	// inventory.md §7.9 + migration 0025 (routers + aggregator_exposures).
	"soroswap-router": {Class: ClassRouter, DefaultWeight: 0, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-05-19; r1 wasm-history walk: single hash 4c3db3eb...07 over the contract's entire life [50746272→tip], zero mid-life upgrades; both swap_exact_tokens_for_tokens + swap_tokens_for_exact_tokens exports verified present; ContractCallDecoder (router emits no events). See docs/operations/wasm-audits/soroswap-router.md */},
	"defindex":        {Class: ClassRouter, DefaultWeight: 0, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-05-19; decoder re-derived to the real on-chain schema ("BlendStrategy",deposit|withdraw){from,amount}, topic-dispatched across all emitters (the tag-1.0.0 "DeFindexVault" schema was fiction; deployed WASM 11329c24...988 is Blend strategy code). Live-verified post-rc.58 deploy: indexer emitting `defindex strategy flow` log lines against real traffic (9 in 90min sample). wasm2wat data-section scan of the deployed bytes confirmed all required symbols present (BlendStrategy/deposit/withdraw/from/amount). See docs/operations/wasm-audits/defindex.md */},

	// ─── Cross-chain bridges (flow coverage; excluded from VWAP) ─
	// Bridges move tokens across chains — they publish no prices and
	// emit no trades, so they never contribute to VWAP. Captured for
	// the granular-coverage mission: CCTP's deposit_for_burn /
	// mint_and_withdraw are USDC supply exits / entries beyond the
	// classic trustline mint/burn channel. BackfillSafe stays false
	// until a WASM-history audit lands at
	// docs/operations/wasm-audits/cctp.md — the contracts are brand
	// new (a single WASM hash is expected) but the audit is required
	// program work per CLAUDE.md "Soroban DeFi contracts upgrade in
	// place". See docs/architecture/cctp-stellar-coverage.md.
	"cctp": {Class: ClassBridge, DefaultWeight: 0, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-05-26 via wasm-history walk [60M, 62.64M] across all 3 mainnet contracts (TokenMessengerMinter, MessageTransmitter, CctpForwarder): zero WASM upgrades observed, ranges=null per /tmp/wasm-history-bridges.json. Single-deploy 2026-04-16 confirmed via stellar.expert. See docs/operations/wasm-audits/cctp.md. */},
	// Rozo v1 intent-bridge — same bridge semantics. payment / flush
	// events from the three live v1 Payment contracts. Audited
	// 2026-05-26 alongside CCTP — same walk.
	"rozo": {Class: ClassBridge, DefaultWeight: 0, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true /* audited 2026-05-26 via wasm-history walk [60M, 62.64M] across all 3 mainnet payment contracts: zero WASM upgrades observed, ranges=null. Single WASM hash b56aedeaf80c3d4b... shared across all three contracts per stellar.expert (2026-01-18 + 2026-03-24 deploys). See docs/operations/wasm-audits/rozo.md. */},

	// ─── Off-chain centralised exchanges (this package's scope) ─
	"binance":  {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"kraken":   {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true /* implemented, but 720-interval cap: ~30d at 1h */, BackfillSafe: true},
	"bitstamp": {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"coinbase": {Class: ClassExchange, Subclass: SubclassCEX, DefaultWeight: 100, IncludeInVWAP: true, Paid: false, BackfillAvailable: true, BackfillSafe: true},

	// ─── Institutional FX feeds ──────────────────────────────────
	"polygon-forex":    {Class: ClassExchange, Subclass: SubclassFX, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true, BackfillSafe: true},
	"exchangeratesapi": {Class: ClassExchange, Subclass: SubclassFX, DefaultWeight: 100, IncludeInVWAP: true, Paid: true, BackfillAvailable: true, BackfillSafe: true},

	// ─── Aggregators (divergence signal; excluded from VWAP) ─────
	"coingecko":     {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true},
	"coinmarketcap": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true, BackfillSafe: true},
	"cryptocompare": {Class: ClassAggregator, DefaultWeight: 100, IncludeInVWAP: false, Paid: true, BackfillAvailable: true, BackfillSafe: true},

	// ─── Sovereign daily anchors (sanity check only) ─────────────
	"ecb": {Class: ClassAuthoritySanity, DefaultWeight: 100, IncludeInVWAP: false, Paid: false, BackfillAvailable: true, BackfillSafe: true},

	// ─── Off-chain oracles (Chainlink via EVM RPC) ───────────────
	// Chainlink is on Ethereum mainnet, not Stellar; we read it via
	// JSON-RPC against AggregatorV3 contracts. Class=ClassOracle
	// because it's a price publisher (not raw trades). BackfillSafe
	// is true — off-chain HTTPS source, no on-chain Soroban WASM
	// dependency to audit. Backfill via eth_getLogs walks
	// AnswerUpdated events. See internal/sources/external/chainlink/.
	"chainlink": {Class: ClassOracle, DefaultWeight: 100, IncludeInVWAP: false, Paid: false /* Alchemy free tier covers 516-feed scale */, BackfillAvailable: true, BackfillSafe: true},
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

// FXSources returns the registered source names whose Subclass is
// SubclassFX, in deterministic lexicographic order. Used by the
// X2.5 forex-snap rule to query the trades hypertable for the most
// recent FX-source quote at-or-before a bucket-end timestamp; the
// stable order makes the across-region tiebreak deterministic when
// two FX sources publish the same observed_at.
func FXSources() []string {
	out := make([]string, 0, 2)
	for name, m := range Registry {
		if m.Subclass == SubclassFX {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// IsFXSource reports whether the named source has Subclass=SubclassFX.
// Convenience wrapper for the snap-rule's per-leg classification.
func IsFXSource(source string) bool {
	return Lookup(source).Subclass == SubclassFX
}

// AggregatorSources returns every registered source whose Class is
// ClassAggregator, in deterministic lexicographic order. Powers the
// `aggregator_avg` tier of the global-price fallback chain (R-018
// Phase 1.3): handlers pass this list to
// `Store.LatestAggregatorPricesForPair` to scope queries to the
// aggregator class without leaking the registry's class-filter
// policy into the storage layer.
//
// Operator-disabled aggregators (e.g. CMC when no API key is
// configured) still appear in this list — the storage query
// degrades naturally to "no observations" for disabled sources
// because the indexer never wrote any. Returning the full registry
// set keeps this helper deployment-agnostic.
func AggregatorSources() []string {
	out := make([]string, 0, 4)
	for name, m := range Registry {
		if m.Class == ClassAggregator {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
