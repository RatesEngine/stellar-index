package projector

import (
	"strings"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/cctp"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/defindex"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/rozo"
	"github.com/RatesEngine/rates-engine/internal/sources/sep41_supply"
	"github.com/RatesEngine/rates-engine/internal/sources/sep41_transfers"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
)

// BuildRegistry constructs the projector's source list from the
// operator's enabled-sources config + oracle config. Same shape
// as `internal/pipeline.BuildDispatcher` but produces
// `projector.Source` entries rather than dispatcher.Decoder lists.
//
// Out of scope (per ADR-0032 § "Out of scope"):
//   - sdex (classic-DEX; writes direct to trades)
//   - soroswap_router, band (ContractCallDecoder — bound to
//     InvokeContract args, not Soroban events)
//   - external sources (binance, kraken, …) — off-chain, no
//     soroban_events
//
// Returns the [Registry] + an error listing any source that
// requires oracle config + that config is empty (e.g.
// `reflector-dex` enabled but `oracle.reflector.dex_contract`
// is "").
func BuildRegistry(names []string, oracle config.OracleConfig, soroswapOpts ...soroswap.DecoderOption) (Registry, error) {
	var sources []Source
	for _, name := range names {
		s, ok, err := buildSource(strings.ToLower(name), oracle, soroswapOpts...)
		if err != nil {
			return Registry{}, err
		}
		if !ok {
			// Source is enabled but doesn't have a projector entry
			// (sdex, band, soroswap-router, external sources).
			// Silently skip — `pipeline.BuildDispatcher` handles
			// those via different surfaces.
			continue
		}
		sources = append(sources, s)
	}
	return Registry{Sources: sources}, nil
}

// sep41SymbolSet is the topic-0 prefilter for sep41_transfers
// + sep41_supply. Symbols are listed exhaustively per the
// EVERY-event policy (project memory `project_every_event_principle`).
var sep41TransferSyms = []string{
	sep41_transfers.SymbolTransfer,
	sep41_transfers.SymbolApprove,
	sep41_transfers.SymbolSetAdmin,
	sep41_transfers.SymbolSetAuthorized,
}

// firehoseExcludeSyms is the SQL-layer exclusion the DEX/lending sources apply
// so a far-behind catch-up window doesn't stream the CAP-67 classic-token
// firehose (under the r1 archive's uniform V4 meta, ~99.8% of all
// contract_events / soroban_events — transfer alone is ~88%). It's the
// classic-token topic[0] set MINUS set_admin: every one of the eight sources
// below was audited (events.go + classify) and none consumes any of these six,
// so the exclusion is provably lossless — whereas blend DOES dispatch on
// set_admin, so set_admin is deliberately retained (its volume is negligible —
// not even in the top-20 topic_0_sym). This is an exclude-list rather than a
// per-source include-list because several decoders match dynamic/prefixed
// topic[0] symbols (e.g. phoenix "XYK Pool: …") that an include-list would miss.
var firehoseExcludeSyms = []string{
	"transfer", "mint", "burn", "clawback", "approve", "set_authorized",
}

//nolint:gocognit,gocyclo,funlen // dispatch switch; one case per source. Same shape as pipeline.BuildDispatcher (which carries the same exemption).
func buildSource(name string, oracle config.OracleConfig, soroswapOpts ...soroswap.DecoderOption) (Source, bool, error) {
	switch name {
	case soroswap.SourceName:
		// Soroswap dispatches via topic[0] across all pairs in
		// the registry; no contract-list prefilter needed.
		return Source{
			Name:              soroswap.SourceName,
			Decoder:           soroswap.NewDecoder(soroswapOpts...),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case aquarius.SourceName:
		return Source{
			Name:              aquarius.SourceName,
			Decoder:           aquarius.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case phoenix.SourceName:
		return Source{
			Name:              phoenix.SourceName,
			Decoder:           phoenix.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case comet.SourceName:
		return Source{
			Name:              comet.SourceName,
			Decoder:           comet.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case blend.SourceName:
		return Source{
			Name:              blend.SourceName,
			Decoder:           blend.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case cctp.SourceName:
		return Source{
			Name:              cctp.SourceName,
			Decoder:           cctp.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case rozo.SourceName:
		return Source{
			Name:              rozo.SourceName,
			Decoder:           rozo.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case defindex.SourceName:
		return Source{
			Name:              defindex.SourceName,
			Decoder:           defindex.NewDecoder(),
			ExcludeTopic0Syms: firehoseExcludeSyms,
		}, true, nil
	case sep41_transfers.SourceName:
		// SEP-41 NewDecoder requires a non-nil watched-contracts
		// list; the projector wants all-contracts coverage so we
		// pass a single synthetic identity that no real contract
		// will match. The decoder's `Matches` would normally gate
		// on this list, but the projector uses Topic0Syms prefilter
		// at the SQL layer + classify() at the decode-layer so
		// matching by contract is redundant. Pre-existing pattern
		// from sep41_transfers_backfill.go::buildSEP41DecoderContracts.
		sep41Dec, err := sep41_transfers.NewDecoder([]string{"CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"})
		if err != nil {
			return Source{}, false, err
		}
		return Source{
			Name:       sep41_transfers.SourceName,
			Decoder:    sep41Dec,
			Topic0Syms: sep41TransferSyms,
		}, true, nil
	case sep41_supply.SourceName:
		supplyDec, err := sep41_supply.NewDecoder([]string{"CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"})
		if err != nil {
			return Source{}, false, err
		}
		return Source{
			Name:    sep41_supply.SourceName,
			Decoder: supplyDec,
		}, true, nil
	case reflector.SourceDEX:
		if oracle.Reflector.DEXContract == "" {
			return Source{}, false, missingConfigErr(name)
		}
		return Source{
			Name:        reflector.SourceDEX,
			Decoder:     reflector.NewDecoder(reflector.VariantDEX, oracle.Reflector.DEXContract),
			ContractIDs: []string{oracle.Reflector.DEXContract},
		}, true, nil
	case reflector.SourceCEX:
		if oracle.Reflector.CEXContract == "" {
			return Source{}, false, missingConfigErr(name)
		}
		return Source{
			Name:        reflector.SourceCEX,
			Decoder:     reflector.NewDecoder(reflector.VariantCEX, oracle.Reflector.CEXContract),
			ContractIDs: []string{oracle.Reflector.CEXContract},
		}, true, nil
	case reflector.SourceFX:
		if oracle.Reflector.FXContract == "" {
			return Source{}, false, missingConfigErr(name)
		}
		return Source{
			Name:        reflector.SourceFX,
			Decoder:     reflector.NewDecoder(reflector.VariantFX, oracle.Reflector.FXContract),
			ContractIDs: []string{oracle.Reflector.FXContract},
		}, true, nil
	case redstone.SourceName:
		if oracle.Redstone.AdapterContract == "" {
			return Source{}, false, missingConfigErr(name)
		}
		return Source{
			Name:        redstone.SourceName,
			Decoder:     redstone.NewDecoder(oracle.Redstone.AdapterContract),
			ContractIDs: []string{oracle.Redstone.AdapterContract},
		}, true, nil
	default:
		// Out of scope per ADR-0032 (sdex, band, soroswap-router,
		// external CEX/FX).
		return Source{}, false, nil
	}
}

func missingConfigErr(source string) error {
	return &missingConfigError{Source: source}
}

type missingConfigError struct {
	Source string
}

func (e *missingConfigError) Error() string {
	return "projector: source " + e.Source + " enabled but its oracle config is empty (check oracle.* in /etc/ratesengine.toml)"
}

// Ensure dispatcher.Decoder is the type the projector expects.
var _ dispatcher.Decoder = (*aquarius.Decoder)(nil)
