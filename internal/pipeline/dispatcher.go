// Package pipeline holds the shared ingest-pipeline glue used by both
// the long-running indexer (`cmd/ratesengine-indexer`) and the
// bounded-replay backfill subcommand (`cmd/ratesengine-ops backfill`).
//
// What's here vs what isn't:
//
//   - BuildDispatcher: registers the right per-source decoders given
//     the operator's enabled-sources list + the oracle contract IDs.
//   - ProcessLedger: runs the dispatcher over one LCM and forwards
//     emitted events to a sink channel. Does NOT touch cursors or
//     emit cursor metrics — that's a long-running-indexer concern,
//     not a pipeline concern.
//   - PersistEvents: drains a sink channel and writes each event to
//     its hypertable. Type-switch covers every event kind any
//     registered source can emit.
//   - LedgerstreamConfig: builds a ledgerstream.Config from a global
//     config + bucket name. Trivial but kept here so both binaries
//     share the same datastore wiring.
//
// What stays in the binaries: cursor management, signal handling,
// flag parsing, metrics-server lifecycle. Those differ between live
// tail and bounded replay; trying to share them produces the wrong
// abstraction.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/sources/accounts"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/claimable_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/liquidity_pools"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/sac_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	sep41supply "github.com/RatesEngine/rates-engine/internal/sources/sep41_supply"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/sources/trustlines"
)

// BuildDispatcher constructs a dispatcher with decoders registered
// for every name in `names`. Returns an error on unknown names or
// when an oracle source is requested without its required contract
// ID populated in `oracle`.
//
// Source name → decoder kind mapping:
//
//   - soroswap / aquarius / phoenix / comet — event Decoder
//   - reflector-{dex,cex,fx} / redstone     — event Decoder (oracle)
//   - band                                  — ContractCallDecoder
//     (Band's Soroban contract emits zero events; we observe the
//     relay() InvokeContract call instead — see CLAUDE.md "Band's
//     Soroban contract emits zero events")
//   - sdex                                  — OpDecoder (classic
//     pre-Soroban; we read ManageOffer ops, not events)
func BuildDispatcher(names []string, oracle config.OracleConfig) (*dispatcher.Dispatcher, error) { //nolint:gocognit,gocyclo // linear case-table, splitting hurts readability
	var decoders []dispatcher.Decoder
	var opDecoders []dispatcher.OpDecoder
	var callDecoders []dispatcher.ContractCallDecoder
	for _, name := range names {
		switch strings.ToLower(name) {
		case soroswap.SourceName:
			decoders = append(decoders, soroswap.NewDecoder())
		case aquarius.SourceName:
			decoders = append(decoders, aquarius.NewDecoder())
		case phoenix.SourceName:
			decoders = append(decoders, phoenix.NewDecoder())
		case comet.SourceName:
			decoders = append(decoders, comet.NewDecoder())
		case reflector.SourceDEX:
			if oracle.Reflector.DEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.dex_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantDEX, oracle.Reflector.DEXContract))
		case reflector.SourceCEX:
			if oracle.Reflector.CEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.cex_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantCEX, oracle.Reflector.CEXContract))
		case reflector.SourceFX:
			if oracle.Reflector.FXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.fx_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantFX, oracle.Reflector.FXContract))
		case redstone.SourceName:
			if oracle.Redstone.AdapterContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.redstone.adapter_contract is empty",
					name)
			}
			decoders = append(decoders,
				redstone.NewDecoder(oracle.Redstone.AdapterContract))
		case band.SourceName:
			if oracle.Band.StandardReferenceContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.band.standard_reference_contract is empty",
					name)
			}
			callDecoders = append(callDecoders,
				band.NewDecoder(oracle.Band.StandardReferenceContract))
		case sdex.SourceName:
			opDecoders = append(opDecoders, sdex.NewDecoder())
		case blend.SourceName:
			// Blend matches by topic[0] across every Blend pool
			// contract — the per-pool address is stamped into the
			// decoded event but no contract-list filter is needed
			// at dispatch time. See internal/sources/blend/README.md.
			decoders = append(decoders, blend.NewDecoder())
		default:
			return nil, fmt.Errorf("unknown source %q in ingestion.enabled_sources — check internal/sources/", name)
		}
	}
	disp := dispatcher.New(decoders...)
	for _, od := range opDecoders {
		disp.AddOpDecoder(od)
	}
	for _, ccd := range callDecoders {
		disp.AddContractCallDecoder(ccd)
	}
	return disp, nil
}

// RegisterSupplyEntryDecoders attaches the LCM-based supply observers
// to disp based on the supply config. Each observer is opt-in: an
// empty watched-set leaves the corresponding observer unregistered
// (no decoder, no work per ledger). Returns the slice of registered
// observer names so the caller can log which observers are live —
// operators reading boot logs see the wired set without consulting
// config.
//
// Currently wired:
//
//   - accounts.Observer — backed by [supply.SDFReserveAccounts].
//     Powers Algorithm 1 (XLM circulating supply) by recording
//     AccountEntry balance changes for the operator-curated reserve
//     account set into `account_observations`.
//   - trustlines.Observer — backed by [supply.WatchedClassicAssets].
//     Records TrustLineEntry balance changes for the watched
//     classic assets into `classic_supply_trustline_observations`.
//   - claimable_balances.Observer — same watched-set. Records
//     ClaimableBalanceEntry create/remove deltas into
//     `classic_supply_claimable_observations`.
//   - liquidity_pools.Observer — same watched-set. Records
//     LiquidityPoolEntry reserve changes into
//     `classic_supply_lp_reserve_observations`.
//   - sac_balances.Observer — backed by [supply.SACWrappers] (the
//     SAC contract C-strkey → asset_key map). Records ContractData
//     balance changes for SAC-wrapped classics + pure SEP-41
//     contracts into `classic_supply_sac_balance_observations`.
//
// The persistence side (internal/pipeline/sink.go) already type-
// switches on every observer's Observation type and calls the right
// store.Insert*; this function only fills the registration gap.
//
// Design rule: the watched-set itself is the on/off switch. Empty
// list → observer skipped. This avoids a separate `[supply] enabled`
// boolean an operator could forget to flip.
//
// The Algorithm 3 sep41_supply observer is event-stream (regular
// Decoder, not LedgerEntryChangeDecoder); it ships in
// [RegisterSupplyEventDecoders] alongside the LedgerEntry registration
// here so an indexer that wants the full supply pipeline calls both.
func RegisterSupplyEntryDecoders(disp *dispatcher.Dispatcher, sup config.SupplyConfig) ([]string, error) {
	var registered []string
	if len(sup.SDFReserveAccounts) > 0 {
		obs, err := accounts.NewObserver(sup.SDFReserveAccounts)
		if err != nil {
			return nil, fmt.Errorf("accounts observer: %w", err)
		}
		disp.AddEntryDecoder(obs)
		registered = append(registered, accounts.SourceName)
	}
	if len(sup.WatchedClassicAssets) > 0 {
		tl, err := trustlines.NewObserver(sup.WatchedClassicAssets)
		if err != nil {
			return nil, fmt.Errorf("trustlines observer: %w", err)
		}
		disp.AddEntryDecoder(tl)
		registered = append(registered, trustlines.SourceName)

		cb, err := claimable_balances.NewObserver(sup.WatchedClassicAssets)
		if err != nil {
			return nil, fmt.Errorf("claimable_balances observer: %w", err)
		}
		disp.AddEntryDecoder(cb)
		registered = append(registered, claimable_balances.SourceName)

		lp, err := liquidity_pools.NewObserver(sup.WatchedClassicAssets)
		if err != nil {
			return nil, fmt.Errorf("liquidity_pools observer: %w", err)
		}
		disp.AddEntryDecoder(lp)
		registered = append(registered, liquidity_pools.SourceName)
	}
	if len(sup.SACWrappers) > 0 {
		sac, err := sac_balances.NewObserver(sup.SACWrappers)
		if err != nil {
			return nil, fmt.Errorf("sac_balances observer: %w", err)
		}
		disp.AddEntryDecoder(sac)
		registered = append(registered, sac_balances.SourceName)
	}
	return registered, nil
}

// RegisterSupplyEventDecoders attaches event-stream Soroban decoders
// for the supply pipeline. Currently registers exactly one — the
// sep41_supply Algorithm 3 mint/burn/clawback decoder — but the
// shape mirrors [RegisterSupplyEntryDecoders] so a future per-asset
// event observer slots in cleanly.
//
// Closes the L2.12a six-observer wiring sweep alongside
// [RegisterSupplyEntryDecoders]. Without this call, the
// `sep41_supply_events` hypertable stays empty and Algorithm 3
// returns zero for every SEP-41 contract regardless of whether
// `[supply] watched_sep41_contracts` is set.
//
// Design rule: same as the entry-decoder helper — empty watched-set
// → observer skipped, no behaviour change for non-opted-in
// deployments.
func RegisterSupplyEventDecoders(disp *dispatcher.Dispatcher, sup config.SupplyConfig) ([]string, error) {
	var registered []string
	if len(sup.WatchedSEP41Contracts) > 0 {
		dec, err := sep41supply.NewDecoder(sup.WatchedSEP41Contracts)
		if err != nil {
			return nil, fmt.Errorf("sep41_supply decoder: %w", err)
		}
		disp.AddDecoder(dec)
		registered = append(registered, sep41supply.SourceName)
	}
	return registered, nil
}
