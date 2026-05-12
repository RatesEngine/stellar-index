package main

import (
	"context"

	"github.com/RatesEngine/rates-engine/internal/aggregate/orchestrator"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// contributionSink adapts the timescale Store to the
// orchestrator.ContributionSink interface. Lives in the binary
// rather than the storage package to avoid an import cycle
// (storage already imports nothing from aggregate; the orchestrator
// imports storage for triangulate, so a storage→orchestrator import
// would close the loop).
//
// Translates each [orchestrator.ContributionRecord] into a batch
// of [timescale.PriceSourceContribution] rows and forwards to the
// store.
type contributionSink struct {
	store *timescale.Store
}

func newContributionSink(s *timescale.Store) *contributionSink {
	return &contributionSink{store: s}
}

func (s *contributionSink) RecordContributions(ctx context.Context, rec orchestrator.ContributionRecord) error {
	if len(rec.Contributions) == 0 {
		return nil
	}
	// F-1242 (codex audit-2026-05-12): read per-source USD volume
	// directly from the post-filter breakdown the orchestrator
	// supplies. SourceUSDVolume sums per-trade USD over the same
	// surviving trade slice that computed Contributions[].Weight,
	// so the persisted `volume_usd` matches the published
	// contribution set even when outliers/class-filter dropped
	// rows. Sources with no SourceUSDVolume entry get NULL —
	// matches the prior all-NULL posture for non-USD windows
	// rather than fabricating a value.
	rows := make([]timescale.PriceSourceContribution, 0, len(rec.Contributions))
	for _, c := range rec.Contributions {
		row := timescale.PriceSourceContribution{
			AssetID:    rec.Pair.Base.String(),
			QuoteID:    rec.Pair.Quote.String(),
			Bucket:     rec.ComputedAt,
			Source:     c.Source,
			Weight:     c.Weight,
			TradeCount: c.TradeCount,
		}
		if v, ok := rec.SourceUSDVolume[c.Source]; ok && v > 0 {
			vol := v
			row.VolumeUSD = &vol
		}
		rows = append(rows, row)
	}
	return s.store.InsertPriceSourceContributions(ctx, rows)
}
