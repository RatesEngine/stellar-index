package main

import (
	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/cctp"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/defindex"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/rozo"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	soroswap_router "github.com/RatesEngine/rates-engine/internal/sources/soroswap_router"
)

// reconTarget is one protocol table a source writes, plus the
// EventKinds that route to it. Re-derive counts ONLY these kinds for
// this table (a multi-table source like soroswap/phoenix/comet/blend
// routes different kinds to different tables; counting all outputs
// would overcount any single table).
type reconTarget struct {
	table       string
	whereFilter string   // "" = whole table belongs to this source
	kinds       []string // EventKind() values routing here; nil for census (sdex)
}

// reconSource is one source's reconciliation spec (ADR-0033 Claim 2b).
type reconSource struct {
	name        string
	dec         completeness.Decoder // nil for census-only sources (sdex)
	contractIDs []string             // SQL prefilter (oracles); empty = match-by-topic
	topic0Syms  []string
	targets     []reconTarget
	census      bool   // sdex: expected = decoder re-derive over the lake's SDEX ops
	genesis     uint32 // first-possible-data ledger; mirrors DefaultGapDetectorTargets (WASM-audit sourced)

	// Factory-anchored gating (ADR-0035): when factories is non-empty, dec
	// gates Matches() on a registry of factory-deployed children, so the
	// re-derive must seed that registry before counting. A re-derive that
	// starts at `genesis` self-seeds in-stream (the factories' creation
	// events precede every child's events and dec.Decode registers them);
	// a re-derive over a custom sub-range does NOT, so the caller pre-walks
	// every factory's creation events via preseedFactoryChildren. creationSym
	// is the topic_0_sym of the creation event (e.g. blend "deploy"). A
	// protocol can have several factories (Blend was redeployed).
	factories   []string
	creationSym string

	// Event-less ContractCall sources (band, soroswap-router): no
	// soroban_events landing zone, so the projection census is re-derived by
	// streaming InvokeContract ops from the lake (filtered on callContract's
	// bytes in body_xdr) and running callDec over each. callDec != nil selects
	// the ContractCall census path. callContract is the C-strkey of the
	// invoked contract (strkey-decoded to the body_xdr substring filter).
	callDec      dispatcher.ContractCallDecoder
	callContract string
}

// buildReconciliationCatalogue assembles the per-source reconciliation
// set and returns the soroswap decoder separately so the caller can
// seed its pair registry (its swap event omits token identities).
//
// Scope: every source whose decoder matches by TOPIC (so a soroban_events
// re-derive reproduces it) or by a REAL contract address (oracles); sdex via
// the LCM op census; and the event-less ContractCall sources (band,
// soroswap-router) via the InvokeContract-op census (callDec path) — their
// calls are re-derived from the lake by filtering body_xdr on the contract
// bytes (stellar.operations has no contract_id column). Deliberately EXCLUDED:
//   - sep41-transfers / sep41-supply: now eligible in principle — the
//     re-derive (ReDeriveOutputCountsByKindFromEvents) calls dec.Matches()
//     before Decode, and the sep41 decoders gate Matches() on the watched
//     set, so building them here with cfg.Supply.WatchedSEP41Contracts +
//     contractIDs prefilter would count exactly the watched-contract rows
//     the dispatcher wrote (kinds: "sep41_transfers.event" /
//     "sep41_supply.event"; genesis 50_457_424). They are NOT added yet for
//     a CONCRETE correctness reason: the historical table rows predate the
//     event_index PK discriminator (migrations 0057), so multiple same-op
//     events are still COLLAPSED on disk — a re-derive (which counts each
//     event) would flag every such historical ledger as "missing rows", a
//     false delta. Add them only AFTER migration 0057 is applied AND the
//     tables are re-derived from the lake. Until then they're covered by the
//     data-derived gap detector, not the ADR-0033 projection reconcile.
func buildReconciliationCatalogue(cfg config.Config) ([]reconSource, *soroswap.Decoder) {
	soroswapDec := soroswap.NewDecoder()

	// genesis values mirror DefaultGapDetectorTargets (WASM-audit sourced).
	cat := []reconSource{
		{name: "soroswap", genesis: 50_746_266, dec: soroswapDec, targets: []reconTarget{
			{"trades", "source = 'soroswap'", []string{"soroswap.trade"}},
			{"soroswap_skim_events", "", []string{"soroswap.skim"}},
		}},
		{name: "aquarius", genesis: 52_728_375, dec: aquarius.NewDecoder(), targets: []reconTarget{
			{"trades", "source = 'aquarius'", []string{"aquarius.trade"}},
		}},
		{name: "phoenix", genesis: 51_572_016, dec: phoenix.NewDecoder(), targets: []reconTarget{
			{"trades", "source = 'phoenix'", []string{"phoenix.trade"}},
			{"phoenix_liquidity", "", []string{"phoenix.liquidity"}},
			{"phoenix_stake_events", "", []string{"phoenix.stake"}},
		}},
		{name: "comet", genesis: 51_499_546, dec: comet.NewDecoder(), targets: []reconTarget{
			{"trades", "source = 'comet'", []string{"comet.trade"}},
			{"comet_liquidity", "", []string{"comet.liquidity"}},
		}},
		{name: "cctp", genesis: 62_403_000, dec: cctp.NewDecoder(), targets: []reconTarget{
			{"cctp_events", "", []string{"cctp.event"}},
		}},
		{name: "rozo", genesis: 62_403_000, dec: rozo.NewDecoder(), targets: []reconTarget{
			{"rozo_events", "", []string{"rozo.event"}},
		}},
		{name: "defindex", genesis: 57_056_338, dec: defindex.NewDecoder(), targets: []reconTarget{
			// Computed kinds: "defindex.{strategy,vault}.{deposit,withdraw}"
			// (defindex.Event / VaultEvent EventKind()). Both layers land
			// in defindex_flows (layer discriminator column).
			{"defindex_flows", "", []string{
				"defindex.strategy.deposit", "defindex.strategy.withdraw",
				"defindex.vault.deposit", "defindex.vault.withdraw",
			}},
		}},
		{
			name: "blend", genesis: blend.FactoryGenesisLedger, dec: blend.NewDecoder(),
			factories: blend.MainnetPoolFactories, creationSym: blend.EventDeploy,
			targets: []reconTarget{
				{"blend_auctions", "", []string{blend.NewAuctionEventKind, blend.FillAuctionEventKind, blend.DeleteAuctionEventKind}},
				{"blend_positions", "", []string{blend.PositionEventKind}},
				{"blend_emissions", "", []string{blend.EmissionEventKind}},
				{"blend_admin", "", []string{blend.AdminEventKind}},
			},
		},
		{name: "sdex", genesis: 2, census: true, targets: []reconTarget{
			{"trades", "source = 'sdex'", nil},
		}},
	}

	// Oracle sources: decoder needs a real contract address; include only
	// when configured. The contract prefilter also makes the re-derive
	// fast (uses the soroban_events contract index).
	if a := cfg.Oracle.Reflector.DEXContract; a != "" {
		cat = append(cat, reconSource{
			name: "reflector-dex", genesis: 50_644_229, dec: reflector.NewDecoder(reflector.VariantDEX, a), contractIDs: []string{a},
			targets: []reconTarget{{"oracle_updates", "source = 'reflector-dex'", []string{"reflector.update"}}},
		})
	}
	if a := cfg.Oracle.Reflector.CEXContract; a != "" {
		cat = append(cat, reconSource{
			name: "reflector-cex", genesis: 50_644_239, dec: reflector.NewDecoder(reflector.VariantCEX, a), contractIDs: []string{a},
			targets: []reconTarget{{"oracle_updates", "source = 'reflector-cex'", []string{"reflector.update"}}},
		})
	}
	if a := cfg.Oracle.Reflector.FXContract; a != "" {
		cat = append(cat, reconSource{
			name: "reflector-fx", genesis: 56_733_481, dec: reflector.NewDecoder(reflector.VariantFX, a), contractIDs: []string{a},
			targets: []reconTarget{{"oracle_updates", "source = 'reflector-fx'", []string{"reflector.update"}}},
		})
	}
	if a := cfg.Oracle.Redstone.AdapterContract; a != "" {
		cat = append(cat, reconSource{
			name: "redstone", genesis: 58_758_722, dec: redstone.NewDecoder(a), contractIDs: []string{a},
			targets: []reconTarget{{"oracle_updates", "source = 'redstone'", []string{"redstone.update"}}},
		})
	}

	// Event-less ContractCall sources — census re-derived from the lake's
	// InvokeContract ops (callDec path). band is gated on its configured
	// StandardReference contract; soroswap-router uses the mainnet router
	// const (matching how the indexer wires both decoders). genesis bounds the
	// verify range; the empty pre-first-call prefix reconciles to zero.
	if a := cfg.Oracle.Band.StandardReferenceContract; a != "" {
		cat = append(cat, reconSource{
			name: "band", genesis: 60_000_000, callContract: a, callDec: band.NewDecoder(a),
			targets: []reconTarget{{"oracle_updates", "source = 'band'", nil}},
		})
	}
	cat = append(cat, reconSource{
		name: "soroswap-router", genesis: 50_746_272,
		callContract: soroswap_router.MainnetRouter,
		callDec:      soroswap_router.NewDecoder(soroswap_router.MainnetRouter),
		targets:      []reconTarget{{"soroswap_router_swaps", "", nil}},
	})
	return cat, soroswapDec
}
