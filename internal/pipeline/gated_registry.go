package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/childgate"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// GatedMeta is the per-source description a factory-anchored decoder
// (ADR-0035) needs to seed its childgate.Registry: the trust-root factory,
// the topic_0_sym of the factory's creation event (which announces a new
// child), the factory's genesis ledger (lower bound for the deploy walk),
// and a constructor that builds the source's decoder with childgate
// options applied.
type GatedMeta struct {
	Factory     string // canonical factory C-strkey (gate trust root)
	CreationSym string // topic_0_sym of the creation event (e.g. "deploy")
	Genesis     uint32 // factory deploy ledger; lower bound for the fan-out walk
	// NewDecoder builds the source's decoder with the given childgate
	// options (WithSeed / WithHook). Returned as a dispatcher.Decoder so
	// the genesis-seed CLI can drive it generically.
	NewDecoder func(opts ...childgate.Option) dispatcher.Decoder
}

// gatedSources is the registry of factory-anchored sources. Adding one is
// four edits: an entry here, the decoder's Matches() gate, the NewDecoder
// call in BuildDispatcher + BuildRegistry (forwarding gated[source]), and
// the reconcile-catalogue factory/creationSym fields.
//
// Soroswap is NOT here — it keeps its richer soroswap_pairs registry (it
// carries token identities, not just a contract set); see
// SoroswapPersistenceOptions. Comet is the open case (shared Balancer-v1
// WASM, no factory namespace — ADR-0035 "Open: Comet").
var gatedSources = map[string]GatedMeta{
	blend.SourceName: {
		Factory:     blend.MainnetPoolFactory,
		CreationSym: blend.EventDeploy,
		Genesis:     blend.FactoryGenesisLedger,
		NewDecoder:  func(opts ...childgate.Option) dispatcher.Decoder { return blend.NewDecoder(opts...) },
	},
}

// GatedMetaFor returns the metadata for a factory-anchored source and
// whether it is gated. Used by the genesis-seed CLI.
func GatedMetaFor(source string) (GatedMeta, bool) {
	m, ok := gatedSources[source]
	return m, ok
}

// GatedSourceNames returns the factory-anchored source names (those that
// warm a childgate.Registry from protocol_contracts). Stable order is not
// guaranteed; callers that need determinism should sort.
func GatedSourceNames() []string {
	out := make([]string, 0, len(gatedSources))
	for name := range gatedSources {
		out = append(out, name)
	}
	return out
}

// GatedFactory returns the canonical factory C-strkey for a gated source,
// or "" if the source is not factory-anchored.
func GatedFactory(source string) string { return gatedSources[source].Factory }

// GatedRegistryOptions warms the childgate.Registry for every
// factory-anchored source from the protocol_contracts table and returns a
// map keyed by source name. BuildDispatcher / BuildRegistry forward
// out[source] to each gated decoder's NewDecoder so the in-memory gate
// resumes with a COMPLETE registry across restarts (the projector cursor
// advances past the creation events, so live-only seeding would miss every
// pool deployed before boot — ADR-0035 coverage note).
//
// withHook installs the live-upsert persistence callback (the indexer
// path): when a decoder observes a NEW factory creation event it upserts
// the child into protocol_contracts so the next restart inherits it.
// Read-only consumers (the recognition / completeness audits) pass
// withHook=false — they must not mutate the registry while auditing it.
//
// hookCtx scopes the live upserts' lifetime (typically the process root
// context) and is unused when withHook is false.
func GatedRegistryOptions(
	ctx context.Context,
	store *timescale.Store,
	logger *slog.Logger,
	hookCtx context.Context,
	withHook bool,
) (map[string][]childgate.Option, error) {
	out := make(map[string][]childgate.Option, len(gatedSources))
	for source, meta := range gatedSources {
		ids, err := store.LoadProtocolContracts(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("gated registry warm %s: %w", source, err)
		}
		opts := []childgate.Option{childgate.WithSeed(ids)}
		if withHook {
			src, fac := source, meta.Factory // capture per iteration
			opts = append(opts, childgate.WithHook(func(childID string, firstLedger uint32) {
				hookTimeout, cancel := context.WithTimeout(hookCtx, upsertHookTimeout)
				defer cancel()
				if err := store.UpsertProtocolContract(hookTimeout, src, childID, fac, firstLedger); err != nil {
					logger.Warn("protocol_contracts upsert (live factory creation)",
						"source", src, "child", childID, "ledger", firstLedger, "err", err)
				}
			}))
		}
		logger.Info("gated registry warmed", "source", source, "factory", meta.Factory, "children", len(ids))
		out[source] = opts
	}
	return out, nil
}
