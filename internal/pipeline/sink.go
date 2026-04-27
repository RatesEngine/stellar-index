package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// PersistEvents drains `in` and writes each event to its hypertable
// via the supplied store. Returns when ctx is canceled or the
// channel is closed.
//
// One goroutine drains; per-event work is sequential. Throughput is
// bounded by InsertTrade / InsertOracleUpdate latency. If that ever
// becomes the bottleneck, the right fix is per-pair sharding inside
// the store, not parallel sinks here — sequential ordering keeps the
// trades hypertable's per-(source, pair, ts) uniqueness sane.
func PersistEvents(ctx context.Context, logger *slog.Logger, store *timescale.Store, in <-chan consumer.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			handleOneEvent(ctx, logger, store, ev)
		}
	}
}

// handleOneEvent dispatches one event to its hypertable insert.
// Panic-recovers so a single malformed Amount can't take the whole
// sink down — the source-level decoder error metric has already
// counted the upstream event by the time we get here.
func handleOneEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, ev consumer.Event) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in event sink — recovered",
				"panic", fmt.Sprintf("%v", r),
				"kind", ev.EventKind(),
				"source", ev.Source())
			obs.SourceInsertErrorsTotal.WithLabelValues(ev.Source(), "panic").Inc()
		}
	}()

	source := ev.Source()
	if source == "" {
		logger.Warn("event with empty source", "kind", ev.EventKind())
		source = "_unknown"
	}
	obs.SourceEventsTotal.WithLabelValues(source).Inc()
	obs.SourceLastEventUnix.WithLabelValues(source).Set(float64(time.Now().Unix()))

	switch e := ev.(type) {
	case soroswap.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case aquarius.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case phoenix.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case comet.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case sdex.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case reflector.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case redstone.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case band.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case external.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case external.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	default:
		// A source emitted an event type the sink doesn't know how
		// to persist. Usually means a new source was registered in
		// BuildDispatcher but the type-switch wasn't updated in
		// lock-step. Count + log — silent drops would otherwise
		// look like "metrics say we're ingesting but the tables
		// stay empty" from the operator's POV.
		obs.SourceInsertErrorsTotal.WithLabelValues(source, "unhandled").Inc()
		logger.Warn("unhandled event kind",
			"kind", ev.EventKind(),
			"source", source)
	}
}

func persistTrade(ctx context.Context, logger *slog.Logger, store *timescale.Store, t canonical.Trade) {
	if err := store.InsertTrade(ctx, t); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(t.Source, "trade").Inc()
		logger.Error("insert trade failed",
			"source", t.Source,
			"ledger", t.Ledger,
			"tx_hash", t.TxHash,
			"op_index", t.OpIndex,
			"err", err,
		)
		return
	}
	logger.Debug("trade ingested",
		"source", t.Source,
		"ledger", t.Ledger,
		"pair", t.Pair.String(),
	)
}

func persistOracle(ctx context.Context, logger *slog.Logger, store *timescale.Store, u canonical.OracleUpdate) {
	if err := store.InsertOracleUpdate(ctx, u); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(u.Source, "oracle").Inc()
		logger.Error("insert oracle update failed",
			"source", u.Source,
			"ledger", u.Ledger,
			"tx_hash", u.TxHash,
			"op_index", u.OpIndex,
			"asset", u.Asset.String(),
			"err", err,
		)
		return
	}
	obs.OracleLastUpdateUnix.WithLabelValues(u.Source, u.Asset.String()).
		Set(float64(u.Timestamp.Unix()))
	logger.Debug("oracle update ingested",
		"source", u.Source,
		"ledger", u.Ledger,
		"asset", u.Asset.String(),
		"price", u.Price.String(),
		"decimals", u.Decimals,
	)
}
