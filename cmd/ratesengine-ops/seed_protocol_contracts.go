package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/pipeline"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/childgate"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/sorobanevents"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// seedProtocolContracts is the genesis bootstrap for a factory-anchored
// gated decoder's pool/vault registry (ADR-0035). It walks the source's
// factory creation events (e.g. Blend pool-factory `deploy`) from the
// factory genesis ledger forward in the Postgres soroban_events lake and
// upserts every announced child contract into protocol_contracts.
//
// Run once per gated source as a DEPLOY PRECONDITION before relying on the
// gate — like the migration 0057-0060 re-derive. Until it runs, the
// decoder's registry is empty and (correctly, per ADR-0035) drops every
// child event; after it runs, the indexer keeps the table current live and
// every consumer warms a complete registry from it.
//
// Idempotent: the factory creation events are immutable history and
// UpsertProtocolContract is ON CONFLICT DO UPDATE, so re-running re-walks
// the same set harmlessly. Cheap: creation events are rare and the
// (contract_id, topic_0_sym) index on soroban_events serves the filter.
//
// Flags:
//
//	-config PATH   TOML config (required) — postgres DSN.
//	-source NAME   gated source to seed (required): blend, …
//	               (`-source all` seeds every gated source).
//	-to LEDGER     last ledger to walk (inclusive); 0 = the soroban_events
//	               max ledger.
//	-timeout DUR   wall-clock budget. Default 15m.
func seedProtocolContracts(args []string) error {
	fs := flag.NewFlagSet("seed-protocol-contracts", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	source := fs.String("source", "", "gated source to seed (blend, … or 'all') (required)")
	to := fs.Uint("to", 0, "last ledger (inclusive); 0 = soroban_events max ledger")
	timeout := fs.Duration("timeout", 15*time.Minute, "wall-clock budget")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config required")
	}
	if *source == "" {
		return fmt.Errorf("-source required (one of: %s, or 'all')", strings.Join(sortedGatedNames(), ", "))
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	hi := uint32(*to)
	if hi == 0 {
		maxL, ok, merr := store.MaxSorobanEventLedger(ctx)
		if merr != nil {
			return fmt.Errorf("resolve soroban_events max ledger: %w", merr)
		}
		if !ok {
			return errors.New("soroban_events is empty — nothing to walk")
		}
		hi = maxL
	}

	var sources []string
	if strings.EqualFold(*source, "all") {
		sources = sortedGatedNames()
	} else {
		sources = []string{strings.ToLower(*source)}
	}

	for _, src := range sources {
		n, serr := seedOneGatedSource(ctx, store, src, hi)
		if serr != nil {
			return serr
		}
		fmt.Fprintf(os.Stderr, "seed-protocol-contracts: %s — upserted %d child contract(s) into protocol_contracts (walked [%d, %d])\n",
			src, n, 0, hi)
	}
	return nil
}

// seedOneGatedSource walks one source's factory creation events and
// upserts each announced child. Returns the number of children seeded.
func seedOneGatedSource(ctx context.Context, store *timescale.Store, source string, hi uint32) (int, error) {
	meta, ok := pipeline.GatedMetaFor(source)
	if !ok {
		return 0, fmt.Errorf("%q is not a factory-anchored gated source (one of: %s)", source, strings.Join(sortedGatedNames(), ", "))
	}

	// Build the source's decoder with a hook that upserts each newly
	// observed child into protocol_contracts — the SAME persistence path
	// the live indexer uses, so the genesis walk and live ingest converge
	// on identical rows. The factory that deployed each child is supplied by
	// the decoder (a protocol can have several factories).
	seeded := 0
	hook := func(childID, factoryID string, firstLedger uint32) {
		if err := store.UpsertProtocolContract(ctx, source, childID, factoryID, firstLedger); err != nil {
			fmt.Fprintf(os.Stderr, "seed-protocol-contracts: %s upsert %s failed: %v\n", source, childID, err)
			return
		}
		seeded++
	}
	dec := meta.NewDecoder(childgate.WithHook(hook))

	// Walk every factory's creation events in one lake scan (filter on the
	// factory SET — Blend has more than one factory).
	err := store.StreamSorobanEvents(ctx, meta.Genesis, hi,
		meta.Factories, []string{meta.CreationSym}, nil,
		func(row sorobanevents.Row) error {
			ev, rerr := sorobanevents.Reconstruct(row)
			if rerr != nil {
				return nil //nolint:nilerr // skip a broken creation row
			}
			if dec.Matches(ev) {
				if _, derr := dec.Decode(ev); derr != nil {
					fmt.Fprintf(os.Stderr, "seed-protocol-contracts: %s decode at ledger %d: %v\n", source, ev.Ledger, derr)
				}
			}
			return nil
		})
	if err != nil {
		return seeded, fmt.Errorf("%s: walk factory creation events: %w", source, err)
	}
	return seeded, nil
}

func sortedGatedNames() []string {
	names := pipeline.GatedSourceNames()
	sort.Strings(names)
	return names
}
