package main

import (
	"context"

	"github.com/RatesEngine/rates-engine/internal/aggregate/orchestrator"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// rewrittenSnapshotSink adapts the timescale Store to the
// orchestrator's RewrittenSnapshotSink interface (ADR-0025 phase 2).
// Lives in the binary so the orchestrator package doesn't take a
// storage import — same boundary pattern as contributionSink and
// changeSummarySink elsewhere in this binary.
//
// The orchestrator hands one snapshot per (pair, window) at every
// successful VWAP compute whose target is fiat-quoted; this adapter
// translates the snapshot's call-site shape into a postgres row and
// forwards the INSERT.
type rewrittenSnapshotSink struct{ store *timescale.Store }

func (a rewrittenSnapshotSink) InsertRewrittenVWAPSnapshot(ctx context.Context, snap orchestrator.RewrittenSnapshot) error {
	return a.store.InsertRewrittenVWAPSnapshot(ctx, timescale.VwapSnapshotRow{
		ObservedAt:    snap.ObservedAt,
		BaseAsset:     snap.Pair.Base.String(),
		QuoteAsset:    snap.Pair.Quote.String(),
		WindowSeconds: snap.WindowSeconds,
		VWAP:          snap.VWAP,
		Volume:        snap.Volume,
		VolumeUSD:     snap.VolumeUSD,
		TradeCount:    snap.TradeCount,
		Sources:       snap.Sources,
	})
}
