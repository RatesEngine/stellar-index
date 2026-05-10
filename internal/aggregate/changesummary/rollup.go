// Package changesummary computes the multi-window delta strip
// every entity on the explorer renders.
//
// Periodic worker reads the source table for each (entity_type,
// entity_id), computes h1/h24/d7/d30 deltas + ATH/ATL + streak +
// acceleration, and writes one row to the change_summary_5m
// hypertable. Every list view + every detail page on the explorer
// reads from this in O(1) — without it, every render would do
// N+1 queries against prices_1m.
//
// See migrations/0022_create_change_summary_5m.up.sql for the
// table shape and docs/architecture/explorer-data-inventory.md
// §6.1 + §9.6 for the design.
package changesummary

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Row is the storage-neutral shape the worker hands to the Sink.
// The aggregator binary translates this to timescale.ChangeSummaryRow
// at the boundary; changesummary stays free of a storage import to
// avoid the cycle (storage owns the read-side Point type via the
// PriceSource adapter).
type Row struct {
	EntityType   string
	EntityID     string
	RefreshedAt  time.Time
	CurrentValue float64

	H1Value     *float64
	H1DeltaPct  *float64
	H24Value    *float64
	H24DeltaPct *float64
	D7Value     *float64
	D7DeltaPct  *float64
	D30Value    *float64
	D30DeltaPct *float64

	ATHValue *float64
	ATHAt    *time.Time
	ATLValue *float64
	ATLAt    *time.Time

	StreakDirection string
	StreakDays      *int
	Acceleration    string
}

// PriceSource is the read seam — what the worker queries to compute
// deltas. Implemented by timescale.Store via TimedVWAPsForPair1m.
//
// The worker passes a window of [from, to] and expects the source
// to return (timestamp, value) tuples ordered oldest-first.
type PriceSource interface {
	TimedVWAPs1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]TimedValue, error)
}

// TimedValue is one (timestamp, value) data point. value is decimal
// as a string to dodge float precision issues during delta math —
// the worker parses to float64 only at the moment of computing %
// change, accepting the rounding because deltas are display-grade.
type TimedValue struct {
	At    time.Time
	Value string
}

// Sink is the write seam — where computed summaries land. The
// aggregator binary wires an adapter that translates Row to the
// timescale row type and calls UpsertChangeSummary.
type Sink interface {
	UpsertChangeSummary(ctx context.Context, row Row) error
}

// Worker periodically refreshes change_summary_5m for a configured
// set of (entity_type, entity_id, pair) tuples.
//
// Run via [Worker.Run]; caller cancels via context.
type Worker struct {
	source   PriceSource
	sink     Sink
	logger   *slog.Logger
	interval time.Duration
	clock    func() time.Time

	// entities is the working set. Each one maps an
	// (entity_type, entity_id) coordinate to the canonical pair
	// whose VWAP drives the entity's deltas. Entities that don't
	// have a single canonical pair (e.g. protocols, which sum
	// across pools) live in their own workers.
	entities []Entity
}

// Entity binds an (entity_type, entity_id) tuple to the source pair
// whose 1-minute VWAP series drives the deltas.
type Entity struct {
	Type string         // 'coin' | 'pair'
	ID   string         // canonical id, e.g. "stellar" or "stellar/fiat:USD"
	Pair canonical.Pair // source of truth for prices_1m lookups
}

// Options tunes a Worker.
type Options struct {
	// Interval between refreshes. Default 5 min — matches the
	// change_summary_5m table name.
	Interval time.Duration

	// Clock injection for tests. Default time.Now.
	Clock func() time.Time
}

// New constructs a Worker. Returns an error if required fields are
// missing.
func New(source PriceSource, sink Sink, entities []Entity, logger *slog.Logger, opts Options) (*Worker, error) {
	if source == nil {
		return nil, errors.New("changesummary: PriceSource is required")
	}
	if sink == nil {
		return nil, errors.New("changesummary: Sink is required")
	}
	if logger == nil {
		return nil, errors.New("changesummary: logger is required")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	// Defensive copy so the caller's slice can't be mutated under us.
	cp := make([]Entity, len(entities))
	copy(cp, entities)
	return &Worker{
		source:   source,
		sink:     sink,
		logger:   logger,
		interval: interval,
		clock:    clock,
		entities: cp,
	}, nil
}

// Run blocks until ctx is cancelled, refreshing every interval.
// Returns nil on context cancellation; never returns an error
// (per-entity failures log + continue so one bad pair doesn't kill
// the whole worker).
func (w *Worker) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()

	// Refresh once immediately so a fresh boot doesn't wait a full
	// interval before the explorer has data.
	w.refresh(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.refresh(ctx)
		}
	}
}

// refresh runs one pass over every configured entity. Per-entity
// failures log + continue — a single broken pair must not block
// the rest of the working set.
func (w *Worker) refresh(ctx context.Context) {
	now := w.clock().UTC()
	// Look back 30 days + a small buffer to capture the current
	// observation that anchors d30 deltas.
	from := now.Add(-30 * 24 * time.Hour).Add(-1 * time.Hour)

	for _, ent := range w.entities {
		if err := w.refreshOne(ctx, ent, from, now); err != nil {
			w.logger.Debug("change-summary refresh",
				"type", ent.Type, "id", ent.ID, "err", err)
		}
	}
}

func (w *Worker) refreshOne(ctx context.Context, ent Entity, from, now time.Time) error {
	series, err := w.source.TimedVWAPs1m(ctx, ent.Pair, from, now)
	if err != nil {
		return err
	}
	if len(series) == 0 {
		return errors.New("no observations in window")
	}
	row := computeSummary(ent, series, now)
	return w.sink.UpsertChangeSummary(ctx, row)
}

// computeSummary derives the full Row from a sorted (oldest-first)
// series of TimedValues. Pulled out of refreshOne so it's directly
// testable without a fake PriceSource.
//
// Empty series is rejected upstream — by the time we land here,
// len(series) >= 1.
func computeSummary(ent Entity, series []TimedValue, now time.Time) Row {
	current := series[len(series)-1]
	currentVal, _ := strconv.ParseFloat(current.Value, 64)

	row := Row{
		EntityType:   ent.Type,
		EntityID:     ent.ID,
		RefreshedAt:  now,
		CurrentValue: currentVal,
	}

	// Multi-window deltas. valueAt returns the most-recent observation
	// at-or-before the target time; nil pointers if no data spans
	// that far back (the row's *Value / *DeltaPct fields stay zero
	// → upstream serializes as NULL).
	if v, ok := valueAt(series, now.Add(-1*time.Hour)); ok {
		row.H1Value = ptr(v)
		row.H1DeltaPct = ptr(deltaPct(v, currentVal))
	}
	if v, ok := valueAt(series, now.Add(-24*time.Hour)); ok {
		row.H24Value = ptr(v)
		row.H24DeltaPct = ptr(deltaPct(v, currentVal))
	}
	if v, ok := valueAt(series, now.Add(-7*24*time.Hour)); ok {
		row.D7Value = ptr(v)
		row.D7DeltaPct = ptr(deltaPct(v, currentVal))
	}
	if v, ok := valueAt(series, now.Add(-30*24*time.Hour)); ok {
		row.D30Value = ptr(v)
		row.D30DeltaPct = ptr(deltaPct(v, currentVal))
	}

	// ATH / ATL across the full series. Note this is "30d ATH" not
	// all-time — the worker only fetches 30d of history. A future
	// pass that wants true all-time can switch the query to a
	// 1-day-bucketed CAGG covering the full hypertable.
	athValue, athAt := currentVal, current.At
	atlValue, atlAt := currentVal, current.At
	for _, p := range series {
		v, _ := strconv.ParseFloat(p.Value, 64)
		if v > athValue {
			athValue, athAt = v, p.At
		}
		if v < atlValue || atlValue == 0 {
			atlValue, atlAt = v, p.At
		}
	}
	row.ATHValue = ptr(athValue)
	row.ATHAt = ptrTime(athAt)
	row.ATLValue = ptr(atlValue)
	row.ATLAt = ptrTime(atlAt)

	row.StreakDirection, row.StreakDays = computeStreak(series)
	row.Acceleration = computeAcceleration(series)

	return row
}

// valueAt returns the most-recent observation whose timestamp is
// at-or-before target. ok=false when no observation reaches that far
// back. series is assumed sorted oldest-first.
func valueAt(series []TimedValue, target time.Time) (float64, bool) {
	// Binary-search the largest index whose At <= target.
	idx := sort.Search(len(series), func(i int) bool {
		return series[i].At.After(target)
	}) - 1
	if idx < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(series[idx].Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// deltaPct = (current - past) / past * 100. Returns 0 on past=0
// (avoids divide-by-zero; a delta against a zero baseline is
// undefined and we'd rather leak a fresh entity's NULL than
// stamp Inf%).
func deltaPct(past, current float64) float64 {
	if past == 0 {
		return 0
	}
	return (current - past) / past * 100.0
}

// dailyPoint is one daily summary used by computeStreak. The
// streak walks days, not minutes, so an intraday wobble doesn't
// reset the count.
type dailyPoint struct {
	Day   time.Time
	Value float64
}

// dailyize buckets a minute-level series into daily-last-value
// observations. Returns nil if every value parses as invalid.
func dailyize(series []TimedValue) []dailyPoint {
	days := make([]dailyPoint, 0, 32)
	var current dailyPoint
	started := false
	for _, p := range series {
		v, err := strconv.ParseFloat(p.Value, 64)
		if err != nil {
			continue
		}
		dayStart := p.At.Truncate(24 * time.Hour)
		if !started {
			current = dailyPoint{Day: dayStart, Value: v}
			started = true
			continue
		}
		if !dayStart.Equal(current.Day) {
			days = append(days, current)
			current = dailyPoint{Day: dayStart, Value: v}
		} else {
			current.Value = v
		}
	}
	if started {
		days = append(days, current)
	}
	return days
}

// directionOf returns "up" / "down" / "flat" for one diff.
func directionOf(diff float64) string {
	switch {
	case diff > 0:
		return "up"
	case diff < 0:
		return "down"
	default:
		return "flat"
	}
}

// computeStreak walks the daily-summarised series back-to-front
// looking for the longest run of consecutive same-direction moves
// ending at the latest day. Per-day granularity dodges intraday
// noise that would yield ~0 streaks at minute level.
func computeStreak(series []TimedValue) (string, *int) {
	days := dailyize(series)
	if len(days) < 2 {
		return "", nil
	}
	streakDir := ""
	count := 0
	for i := len(days) - 1; i > 0; i-- {
		dir := directionOf(days[i].Value - days[i-1].Value)
		if streakDir == "" {
			streakDir = dir
			count = 1
			continue
		}
		if dir != streakDir {
			break
		}
		count++
	}
	return streakDir, ptrInt(count)
}

// computeAcceleration returns 'increasing' / 'flat' / 'decreasing'
// based on the sign of the second derivative across the most-recent
// 24h of the series. Increasing means recent moves are bigger than
// older moves in the same direction (momentum building).
//
// Defensive: returns empty string if series too short for the
// comparison. Caller treats empty as NULL.
func computeAcceleration(series []TimedValue) string {
	if len(series) < 4 {
		return ""
	}
	// Compare the last quarter of the series against the previous
	// quarter. Cheap proxy for "is the move accelerating?"
	q := len(series) / 4
	if q < 1 {
		return ""
	}
	last := avgDelta(series[len(series)-q:])
	prev := avgDelta(series[len(series)-2*q : len(series)-q])
	switch {
	case last > prev*1.05:
		return "increasing"
	case last < prev*0.95:
		return "decreasing"
	default:
		return "flat"
	}
}

// avgDelta returns the mean per-step change across a slice. Used by
// computeAcceleration; returns 0 on input < 2.
func avgDelta(slice []TimedValue) float64 {
	if len(slice) < 2 {
		return 0
	}
	var sum float64
	count := 0
	for i := 1; i < len(slice); i++ {
		a, errA := strconv.ParseFloat(slice[i-1].Value, 64)
		b, errB := strconv.ParseFloat(slice[i].Value, 64)
		if errA != nil || errB != nil {
			continue
		}
		sum += b - a
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func ptr(f float64) *float64         { return &f }
func ptrInt(i int) *int              { return &i }
func ptrTime(t time.Time) *time.Time { return &t }
