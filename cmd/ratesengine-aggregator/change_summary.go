package main

import (
	"context"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/changesummary"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// changeSummaryPriceSource adapts the timescale Store to the
// changesummary.PriceSource interface. Lives in the binary to avoid
// the worker-package importing the storage package directly.
type changeSummaryPriceSource struct{ store *timescale.Store }

func (a changeSummaryPriceSource) TimedVWAPs1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]changesummary.TimedValue, error) {
	rows, err := a.store.TimedVWAPs1mForChangeSummary(ctx, pair, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]changesummary.TimedValue, len(rows))
	for i, r := range rows {
		out[i] = changesummary.TimedValue{At: r.At, Value: r.Value}
	}
	return out, nil
}

// changeSummarySink translates the worker's storage-neutral Row into
// the timescale row shape and forwards the upsert. Same boundary
// pattern as contributionSink.
type changeSummarySink struct{ store *timescale.Store }

func (a changeSummarySink) UpsertChangeSummary(ctx context.Context, row changesummary.Row) error {
	return a.store.UpsertChangeSummary(ctx, timescale.ChangeSummaryRow{
		EntityType:      row.EntityType,
		EntityID:        row.EntityID,
		RefreshedAt:     row.RefreshedAt,
		CurrentValue:    row.CurrentValue,
		H1Value:         row.H1Value,
		H1DeltaPct:      row.H1DeltaPct,
		H24Value:        row.H24Value,
		H24DeltaPct:     row.H24DeltaPct,
		D7Value:         row.D7Value,
		D7DeltaPct:      row.D7DeltaPct,
		D30Value:        row.D30Value,
		D30DeltaPct:     row.D30DeltaPct,
		ATHValue:        row.ATHValue,
		ATHAt:           row.ATHAt,
		ATLValue:        row.ATLValue,
		ATLAt:           row.ATLAt,
		StreakDirection: row.StreakDirection,
		StreakDays:      row.StreakDays,
		Acceleration:    row.Acceleration,
	})
}

// buildChangeSummaryEntities maps the aggregator's configured pairs
// into changesummary.Entity rows. We emit two entities per pair: a
// "coin" keyed on the base asset's canonical id (the explorer
// /coins/{slug} page reads this), and a "pair" keyed on the full
// "base/quote" form (the explorer /pairs/{base}/{quote} page reads
// this). The same source pair drives both — the rollup math is
// identical.
func buildChangeSummaryEntities(pairs []canonical.Pair) []changesummary.Entity {
	out := make([]changesummary.Entity, 0, 2*len(pairs))
	seenCoins := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		baseID := p.Base.String()
		if _, ok := seenCoins[baseID]; !ok {
			out = append(out, changesummary.Entity{
				Type: "coin",
				ID:   baseID,
				Pair: p, // first pair we see for this base wins as canonical
			})
			seenCoins[baseID] = struct{}{}
		}
		out = append(out, changesummary.Entity{
			Type: "pair",
			ID:   p.String(),
			Pair: p,
		})
	}
	return out
}
