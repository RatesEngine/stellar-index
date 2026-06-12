package main

import (
	"context"
	"fmt"
	"os"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/sorobanevents"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// preseedFactoryChildren seeds a factory-anchored reconcile source's
// childgate.Registry (ADR-0035) by walking the factory's creation events
// from the source genesis up to `to` and running each through the
// decoder — dec.Decode registers the announced child as a side effect.
//
// No-op for non-gated sources (factory == ""). Idempotent.
//
// Why it's needed: the projection re-derive gates Matches() on the
// registry, so a child's business events are only counted once its
// creation event has been seen. A re-derive that starts at the source
// genesis self-seeds in-stream (the factory's creation events precede
// every child's events). But a re-derive over a CUSTOM sub-range
// (verify-reconciliation -from N, with N after some pool deploys) starts
// past those creation events, so without this pre-walk it would silently
// drop every pre-N child's events and report a false "missing rows"
// delta — the exact false-coverage signal the gate must not introduce.
//
// The walk is cheap: factory creation events are rare and the
// (contract_id, topic_0_sym) index on soroban_events serves the filter.
func preseedFactoryChildren(ctx context.Context, store *timescale.Store, src reconSource, to uint32) error {
	if len(src.factories) == 0 || src.dec == nil {
		return nil
	}
	seeded := 0
	err := store.StreamSorobanEvents(ctx, src.genesis, to,
		src.factories, []string{src.creationSym}, nil,
		func(row sorobanevents.Row) error {
			ev, rerr := sorobanevents.Reconstruct(row)
			if rerr != nil {
				return nil //nolint:nilerr // skip a broken row like the projector does
			}
			if src.dec.Matches(ev) {
				if _, derr := src.dec.Decode(ev); derr == nil {
					seeded++
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("preseed %s factory children: %w", src.name, err)
	}
	if seeded > 0 {
		fmt.Fprintf(os.Stderr, "verify-reconciliation: pre-seeded %d %s factory children (gate registry)\n", seeded, src.name)
	}
	return nil
}
