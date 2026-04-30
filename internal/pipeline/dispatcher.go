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
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
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
