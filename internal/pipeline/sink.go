package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/accounts"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/cctp"
	claimable_balances "github.com/RatesEngine/rates-engine/internal/sources/claimable_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/defindex"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/sources/liquidity_pools"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/rozo"
	sac_balances "github.com/RatesEngine/rates-engine/internal/sources/sac_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	sep41_supply "github.com/RatesEngine/rates-engine/internal/sources/sep41_supply"
	sep41_transfers "github.com/RatesEngine/rates-engine/internal/sources/sep41_transfers"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	soroswap_router "github.com/RatesEngine/rates-engine/internal/sources/soroswap_router"
	"github.com/RatesEngine/rates-engine/internal/sources/trustlines"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// SinkMode controls which event classes [PersistEvents] writes when
// draining the dispatcher's events channel. Introduced for ADR-0032
// Phase 4: once the projector becomes sole writer for Soroban-derived
// events, the dispatcher's events-goroutine must stop writing them
// (otherwise duplicate-PK errors flood + the writer-of-record is
// ambiguous). Soroban-derived sinks (trades, blend_*, phoenix_*,
// comet_*, soroswap_skim, sep41_*, cctp_events, rozo_events,
// reflector/redstone oracle_updates) ride the projector path;
// everything else (sdex trades, external CEX/FX, band oracle_updates,
// supply observers writing LedgerEntry observations) still rides
// the dispatcher path because those sources don't flow through
// soroban_events.
type SinkMode int

const (
	// SinkModeAll writes every consumer.Event the dispatcher emits.
	// The projector ALWAYS uses this mode (it's the sole writer for
	// the Soroban-derived subset, so it must persist them).
	// Pre-Phase-4 the dispatcher's events-goroutine also uses this
	// mode (parallel write with projector, ON CONFLICT DO NOTHING
	// absorbs duplicates).
	SinkModeAll SinkMode = iota

	// SinkModeSkipProjected skips Soroban-derived events the
	// projector handles (see [IsProjectedEvent]). Phase 4+ the
	// dispatcher's events-goroutine uses this mode so the projector
	// owns Soroban-derived writes outright; the events-goroutine
	// continues handling sdex / external / band / supply observers.
	SinkModeSkipProjected
)

// PersistEvents drains `in` and writes each event to its hypertable
// via the supplied store. Returns when ctx is canceled and the
// channel has been drained, or when the channel is closed.
//
// One goroutine drains; per-event work is sequential. Throughput is
// bounded by InsertTrade / InsertOracleUpdate latency. If that ever
// becomes the bottleneck, the right fix is per-pair sharding inside
// the store, not parallel sinks here — sequential ordering keeps the
// trades hypertable's per-(source, pair, ts) uniqueness sane.
//
// Cursor-vs-channel safety: callers (the indexer's pipeline + the
// backfill subcommand) advance their per-source cursor AFTER
// ProcessLedger enqueues events to `in`, but BEFORE this sink
// writes them to postgres. If we returned on ctx cancellation
// without draining, up to cap(in) buffered events would be silently
// dropped while the cursor's "I processed up to ledger N" claim
// stayed advanced — the trades for those ledgers would be missing
// even though `-resume` would skip them on restart. The drain
// below uses a fresh context (parent ctx is already canceled, so
// postgres calls would fail) bounded to 30s so a stuck shutdown
// can't hang the binary forever; if the deadline trips, the
// remaining buffered events are dropped and logged.
//
// The `mode` parameter is new with ADR-0032 Phase 4: see the
// [SinkMode] godoc for why the dispatcher's events-goroutine
// skips Soroban-derived events once the projector is sole writer.
//
//nolint:gocognit // batched-drain loop has natural fan-out: ctx.Done, ticker, channel — splitting hurts readability of the flush invariants.
func PersistEvents(ctx context.Context, logger *slog.Logger, store *timescale.Store, in <-chan consumer.Event, mode SinkMode) {
	// Trade events buffer for BatchInsertTrades. Live-r1 incident
	// 2026-06-01: per-INSERT roundtrip cost capped sustained
	// throughput at ~5 trades/sec even though postgres-side can do
	// 9000+/sec. Batching collapses N INSERTs into 1 statement and
	// lifts throughput by roughly the batch factor. The buffer
	// flushes on size cap, time tick, or context cancellation.
	tradeBuf := make([]canonical.Trade, 0, tradeBatchSize)
	flushTicker := time.NewTicker(tradeBatchFlushInterval)
	defer flushTicker.Stop()

	flush := func(fctx context.Context) {
		if len(tradeBuf) == 0 {
			return
		}
		batch := tradeBuf
		tradeBuf = make([]canonical.Trade, 0, tradeBatchSize)
		if err := store.BatchInsertTrades(fctx, batch); err != nil {
			// On batch failure log + fall back per-row so we don't
			// drop ledger data on a single transient PG hiccup. The
			// per-row path is the slow one but it's still correct.
			logger.Warn("batch trade insert failed; falling back per-row",
				"batch_size", len(batch), "err", err)
			for _, t := range batch {
				persistTrade(fctx, logger, store, t)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush(ctx)
			drainBufferedEvents(in, logger, store, mode)
			return
		case <-flushTicker.C:
			flush(ctx)
		case ev, ok := <-in:
			if !ok {
				flush(ctx)
				return
			}
			if mode == SinkModeSkipProjected && IsProjectedEvent(ev) {
				continue
			}
			// Trade events (Soroban-derived OR sdex OR external) all
			// land in the same hypertable — batch them. Everything
			// else (oracle updates, supply observations, log-only
			// events) takes the per-event path which is fine because
			// volumes are low.
			if t, ok := tradeFromEvent(ev); ok {
				// Per-source events_total + last_event_unix metrics
				// still increment per event so freshness signals
				// work the same in batched mode.
				obs.SourceEventsTotal.WithLabelValues(t.Source).Inc()
				obs.SourceLastEventUnix.WithLabelValues(t.Source).Set(float64(time.Now().Unix()))
				tradeBuf = append(tradeBuf, t)
				if len(tradeBuf) >= tradeBatchSize {
					flush(ctx)
				}
				continue
			}
			HandleEvent(ctx, logger, store, ev)
		}
	}
}

// tradeBatchSize caps the trades-per-batch in BatchInsertTrades.
// Sized to fit comfortably under PostgreSQL's max-bind-params limit
// (32767 parameters); each row has 12 placeholders, so 200 rows =
// 2400 placeholders — well under. Production throughput is roughly
// linear in this until either PG's planning cost or the events
// channel runs dry; 200 is the operating point validated post the
// r1 2026-06-01 incident.
const tradeBatchSize = 200

// tradeBatchFlushInterval is the upper bound on staleness for events
// stuck below the size threshold. With low-volume periods the buffer
// fills slowly; this caps how long a trade waits before landing
// regardless. 200ms keeps per-batch latency well under the SLA
// freshness threshold while still amortising the per-roundtrip cost.
const tradeBatchFlushInterval = 200 * time.Millisecond

// tradeFromEvent returns the underlying canonical.Trade for any
// event that targets the trades hypertable. Used by the PersistEvents
// batcher to route trade-shaped events down the batch path while
// leaving everything else (oracle updates, supply observations,
// log-only events) on the single-row HandleEvent path.
//
// MUST stay in lockstep with HandleEvent — every event type whose
// HandleEvent case calls persistTrade(...) must return its trade
// here, otherwise the event silently falls through to HandleEvent's
// per-event slow path (correctness-equivalent, performance bug).
func tradeFromEvent(ev consumer.Event) (canonical.Trade, bool) {
	switch e := ev.(type) {
	case soroswap.TradeEvent:
		return e.Trade, true
	case aquarius.TradeEvent:
		return e.Trade, true
	case phoenix.TradeEvent:
		return e.Trade, true
	case comet.TradeEvent:
		return e.Trade, true
	case sdex.TradeEvent:
		return e.Trade, true
	case external.TradeEvent:
		return e.Trade, true
	default:
		return canonical.Trade{}, false
	}
}

// IsProjectedEvent reports whether the ADR-0032 projector handles
// `ev`. Phase 4+ the dispatcher's events-goroutine skips these so
// the projector owns the write outright. Non-projected events
// (sdex, external CEX/FX, band, supply observers) continue through
// the events-goroutine because they don't flow through
// soroban_events.
//
// MUST stay in lockstep with `internal/projector/registry.go`
// `buildSource` — every consumer.Event a registered source can emit
// must return true here. ADR-0030 lint guard catches drift.
func IsProjectedEvent(ev consumer.Event) bool {
	switch ev.(type) {
	case soroswap.TradeEvent, soroswap.SkimEvent,
		aquarius.TradeEvent,
		phoenix.TradeEvent, phoenix.LiquidityEvent, phoenix.StakeEvent,
		comet.TradeEvent, comet.LiquidityEvent,
		reflector.UpdateEvent, redstone.UpdateEvent,
		blend.NewAuctionEvent, blend.FillAuctionEvent, blend.DeleteAuctionEvent,
		blend.PositionEvent, blend.EmissionEvent, blend.AdminEvent,
		cctp.Event, rozo.Event,
		defindex.Event, defindex.VaultEvent,
		sep41_supply.Event, sep41_transfers.Event:
		return true
	default:
		// sdex.TradeEvent, external.TradeEvent, external.UpdateEvent,
		// band.UpdateEvent, soroswap_router.Event (log-only), supply
		// observers (accounts / trustlines / claimable_balances /
		// liquidity_pools / sac_balances) — all out of scope for the
		// projector per ADR-0032.
		return false
	}
}

// drainBufferedEvents writes any remaining buffered events using a
// fresh shutdown context so postgres calls succeed past the parent
// context's cancellation. Bounded by [drainTimeout] so a hung
// shutdown can't keep the binary alive indefinitely; on deadline,
// remaining buffered events are dropped and the loss is logged.
//
// Deliberately does not take a context parameter — the whole reason
// this exists is to keep writing past the parent's cancellation.
//
//nolint:contextcheck,gocognit // intentional fresh context + batched-drain fan-out; see godoc above.
func drainBufferedEvents(in <-chan consumer.Event, logger *slog.Logger, store *timescale.Store, mode SinkMode) {
	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	tradeBuf := make([]canonical.Trade, 0, tradeBatchSize)
	flushTrades := func() {
		if len(tradeBuf) == 0 {
			return
		}
		batch := tradeBuf
		tradeBuf = make([]canonical.Trade, 0, tradeBatchSize)
		if err := store.BatchInsertTrades(drainCtx, batch); err != nil {
			logger.Warn("drain batch trade insert failed; falling back per-row",
				"batch_size", len(batch), "err", err)
			for _, t := range batch {
				persistTrade(drainCtx, logger, store, t)
			}
		}
	}
	for {
		select {
		case ev, ok := <-in:
			if !ok {
				flushTrades()
				return
			}
			if mode == SinkModeSkipProjected && IsProjectedEvent(ev) {
				continue
			}
			if t, ok := tradeFromEvent(ev); ok {
				tradeBuf = append(tradeBuf, t)
				if len(tradeBuf) >= tradeBatchSize {
					flushTrades()
				}
				continue
			}
			HandleEvent(drainCtx, logger, store, ev)
		case <-drainCtx.Done():
			flushTrades()
			logger.Warn("PersistEvents drain deadline exceeded — buffered events dropped",
				"buffered", len(in))
			return
		}
	}
}

// drainTimeout caps how long PersistEvents will spend writing
// already-buffered events on shutdown. 30s is comfortable headroom:
// a 256-deep buffer at typical 1ms-per-insert latency drains in
// ~250 ms; 30s tolerates 100x slowdown (e.g. postgres saturated by
// a concurrent VACUUM) before giving up.
const drainTimeout = 30 * time.Second

// HandleEvent dispatches one event to its hypertable insert.
// Panic-recovers so a single malformed Amount can't take the whole
// sink down — the source-level decoder error metric has already
// counted the upstream event by the time we get here.
//
// Exported for use by the ADR-0032 projector (`internal/projector`),
// which invokes this function per decoded event as its sink during
// Phase 3 parallel mode. The drain-from-channel pattern in
// [PersistEvents] still uses this as its per-event handler.
func HandleEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, ev consumer.Event) { //nolint:gocyclo,funlen // dispatch table; one case per consumer.Event implementation. Splitting would reduce clarity.
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
	case soroswap.SkimEvent:
		persistSoroswapSkim(ctx, logger, store, e)
	case aquarius.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case phoenix.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case phoenix.LiquidityEvent:
		persistPhoenixLiquidity(ctx, logger, store, e)
	case phoenix.StakeEvent:
		persistPhoenixStake(ctx, logger, store, e)
	case comet.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case comet.LiquidityEvent:
		persistCometLiquidity(ctx, logger, store, e)
	case sdex.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case reflector.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case redstone.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case band.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case soroswap_router.Event:
		// Phase A: log-only sink. Phase B will tag matching same-tx
		// trades.routed_via and (TBD) write to a dedicated
		// router_swaps table. Until then this just records the
		// dispatcher correctly routed the call so operators can
		// validate via journal. Counter bump surfaces decoded
		// activity on /v1/diagnostics/ingestion's `entries` even
		// without a per-protocol storage table.
		bumpEntryCount(ctx, logger, store, soroswap_router.SourceName)
		logger.Info("soroswap-router swap routed",
			"source", soroswap_router.SourceName,
			"tx_hash", e.Swap.TxHash,
			"ledger", e.Swap.Ledger,
			"function", e.Swap.Function,
			"path_len", len(e.Swap.Path),
			"recipient", e.Swap.Recipient,
			"amount_in", e.Swap.AmountIn.String(),
			"amount_out", e.Swap.AmountOut.String(),
		)
	case defindex.Event:
		// Strategy-layer flow (vault → strategy capital movement).
		// `from` is always the vault contract C-strkey; end-user
		// attribution lives at the vault layer (case defindex.VaultEvent
		// below). Log-only sink; a future revision will tag matching
		// same-tx Blend / Soroswap legs as `routed_via` and write to
		// the aggregator_exposures hypertable from a separate periodic
		// ticker. Until then we emit one INFO line per strategy flow
		// so operators can verify the dispatcher routes BlendStrategy
		// events correctly via the journal. Counter bump as for
		// soroswap-router above — surfaces decoded activity on
		// /v1/diagnostics/ingestion's `entries`.
		bumpEntryCount(ctx, logger, store, defindex.SourceName)
		logger.Info("defindex strategy flow",
			"source", defindex.SourceName,
			"tx_hash", e.Flow.TxHash,
			"ledger", e.Flow.Ledger,
			"contract_id", e.Flow.ContractID,
			"direction", string(e.Flow.Direction),
			"from", e.Flow.From,
			"amount", e.Flow.Amount.String(),
		)
	case defindex.VaultEvent:
		// Vault-wrapper layer (user-facing deposit/withdraw). `user`
		// is the end-user G-strkey for direct interactions, a router
		// C-strkey for aggregator-routed flows. Phase B (#49) added
		// this branch; the pre-Phase-B decoder only emitted strategy
		// events and missed ~86% of defindex activity historically.
		bumpEntryCount(ctx, logger, store, defindex.SourceName)
		amounts := make([]string, 0, len(e.Flow.Amounts))
		for _, a := range e.Flow.Amounts {
			amounts = append(amounts, a.String())
		}
		logger.Info("defindex vault flow",
			"source", defindex.SourceName,
			"tx_hash", e.Flow.TxHash,
			"ledger", e.Flow.Ledger,
			"contract_id", e.Flow.ContractID,
			"direction", string(e.Flow.Direction),
			"user", e.Flow.User,
			"amounts", strings.Join(amounts, ","),
			"df_tokens", e.Flow.DfTokens.String(),
		)
	case external.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case external.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	case blend.NewAuctionEvent:
		persistBlendNewAuction(ctx, logger, store, e)
	case blend.FillAuctionEvent:
		persistBlendFillAuction(ctx, logger, store, e)
	case blend.DeleteAuctionEvent:
		persistBlendDeleteAuction(ctx, logger, store, e)
	case blend.PositionEvent:
		persistBlendPositionEvent(ctx, logger, store, e)
	case blend.EmissionEvent:
		persistBlendEmissionEvent(ctx, logger, store, e)
	case blend.AdminEvent:
		persistBlendAdminEvent(ctx, logger, store, e)
	case cctp.Event:
		persistCCTPEvent(ctx, logger, store, e)
	case rozo.Event:
		persistRozoEvent(ctx, logger, store, e)
	case accounts.Observation:
		persistAccountObservation(ctx, logger, store, e)
	case trustlines.Observation:
		persistTrustlineObservation(ctx, logger, store, e)
	case claimable_balances.Observation:
		persistClaimableObservation(ctx, logger, store, e)
	case liquidity_pools.Observation:
		persistLPReserveObservation(ctx, logger, store, e)
	case sac_balances.Observation:
		persistSACBalanceObservation(ctx, logger, store, e)
	case sep41_supply.Event:
		persistSEP41SupplyEvent(ctx, logger, store, e)
	case sep41_transfers.Event:
		persistSEP41TransferEvent(ctx, logger, store, e)
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
	// Check populated-ness BEFORE InsertTrade so the metric counts
	// every attempt — including the ON CONFLICT DO NOTHING dedupe
	// case, which from this layer's POV is still "we tried, with
	// this populate state".
	//
	// Phase 2 fallback (USDVolumeFXResolver, when wired) makes this
	// predicate consult the resolver synchronously. Production
	// resolvers are in-memory cache lookups; a slow resolver here
	// would slow the trade-insert hot path by 2× (predicate +
	// InsertTrade both call it). Treat resolver latency as a
	// constraint when wiring one.
	populated := store.WouldPopulateUSDVolume(ctx, t)

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
	obs.TradeInsertsTotal.WithLabelValues(t.Source, usdPopulatedLabel(populated)).Inc()
	logger.Debug("trade ingested",
		"source", t.Source,
		"ledger", t.Ledger,
		"pair", t.Pair.String(),
	)
}

// usdPopulatedLabel maps the WouldPopulateUSDVolume bool to the
// stable Prometheus label values the dashboards filter on.
func usdPopulatedLabel(populated bool) string {
	if populated {
		return "yes"
	}
	return "no"
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

func persistBlendNewAuction(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.NewAuctionEvent) {
	if err := store.InsertBlendNewAuction(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_auction").Inc()
		logger.Error("insert blend new_auction failed",
			"pool", e.Pool, "user", e.User, "auction_type", e.AuctionType,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Info("blend new_auction ingested",
		"pool", e.Pool, "user", e.User, "auction_type", e.AuctionType,
		"percent", e.Percent, "ledger", e.Ledger)
}

func persistBlendFillAuction(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.FillAuctionEvent) {
	if err := store.InsertBlendFillAuction(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_auction").Inc()
		logger.Error("insert blend fill_auction failed",
			"pool", e.Pool, "user", e.User, "filler", e.Filler,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Info("blend fill_auction ingested",
		"pool", e.Pool, "user", e.User, "filler", e.Filler,
		"fill_percent", e.FillPercent, "ledger", e.Ledger)
}

func persistBlendDeleteAuction(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.DeleteAuctionEvent) {
	if err := store.InsertBlendDeleteAuction(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_auction").Inc()
		logger.Error("insert blend delete_auction failed",
			"pool", e.Pool, "user", e.User, "auction_type", e.AuctionType,
			"ledger", e.Ledger, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Info("blend delete_auction ingested",
		"pool", e.Pool, "user", e.User, "auction_type", e.AuctionType,
		"ledger", e.Ledger)
}

func persistCometLiquidity(ctx context.Context, logger *slog.Logger, store *timescale.Store, e comet.LiquidityEvent) {
	if err := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
		ContractID:      e.ContractID,
		Ledger:          e.Ledger,
		LedgerCloseTime: e.ObservedAt,
		TxHash:          e.TxHash,
		OpIndex:         e.OpIndex,
		Kind:            timescale.CometLiquidityKind(e.Kind),
		Caller:          e.Caller,
		Token:           e.Token,
		Amount:          e.Amount,
		PoolAmountIn:    e.PoolAmountIn,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(comet.SourceName, "comet_liquidity").Inc()
		logger.Error("insert Comet liquidity event failed",
			"contract_id", e.ContractID, "kind", e.Kind,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, comet.SourceName)
	logger.Debug("Comet liquidity event ingested",
		"source", comet.SourceName, "kind", e.Kind,
		"contract_id", e.ContractID, "ledger", e.Ledger,
		"token", e.Token, "amount", e.Amount.String())
}

// persistBlendPositionEvent routes one money-market position event
// (supply / withdraw / supply_collateral / withdraw_collateral /
// borrow / repay / flash_loan) to the blend_positions hypertable.
func persistBlendPositionEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.PositionEvent) {
	if err := store.InsertBlendPositionEvent(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_position").Inc()
		logger.Error("insert blend position event failed",
			"pool", e.Pool, "kind", e.Kind, "user", e.User, "asset", e.Asset,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Debug("blend position event ingested",
		"pool", e.Pool, "kind", e.Kind, "user", e.User, "asset", e.Asset,
		"token_amount", e.TokenAmount.String(), "ledger", e.Ledger)
}

// persistBlendEmissionEvent routes one emission / credit-risk event
// (gulp / claim / reserve_emission_update / gulp_emissions /
// bad_debt / defaulted_debt) to the blend_emissions hypertable.
func persistBlendEmissionEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.EmissionEvent) {
	if err := store.InsertBlendEmissionEvent(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_emission").Inc()
		logger.Error("insert blend emission event failed",
			"pool", e.Pool, "kind", e.Kind,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Debug("blend emission event ingested",
		"pool", e.Pool, "kind", e.Kind, "ledger", e.Ledger)
}

// persistBlendAdminEvent routes one admin / pool-config / pool-
// factory lifecycle event (set_admin / update_pool /
// queue_set_reserve / cancel_set_reserve / set_reserve / set_status
// / deploy) to the blend_admin hypertable. The deploy event from
// the pool-factory drives runtime pool enumeration.
func persistBlendAdminEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e blend.AdminEvent) {
	if err := store.InsertBlendAdminEvent(ctx, e); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(blend.SourceName, "blend_admin").Inc()
		logger.Error("insert blend admin event failed",
			"contract_id", e.ContractID, "kind", e.Kind,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, blend.SourceName)
	logger.Info("blend admin event ingested",
		"contract_id", e.ContractID, "kind", e.Kind,
		"admin", e.Admin, "target", e.Target, "asset", e.Asset,
		"ledger", e.Ledger)
}

func persistCCTPEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e cctp.Event) {
	if err := store.InsertCCTPEvent(ctx, timescale.CCTPEvent{
		ContractID:         e.ContractID,
		Ledger:             e.Ledger,
		TxHash:             e.TxHash,
		OpIndex:            uint32(e.OpIndex),
		ObservedAt:         e.ObservedAt,
		EventType:          timescale.CCTPEventType(e.EventType),
		Amount:             e.Amount,
		Fee:                e.Fee,
		Token:              e.Token,
		CounterpartyDomain: e.CounterpartyDomain,
		Attributes:         e.Attributes,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(cctp.SourceName, "cctp_event").Inc()
		logger.Error("insert CCTP event failed",
			"contract_id", e.ContractID, "event_type", e.EventType,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, cctp.SourceName)
	logger.Debug("CCTP event ingested",
		"source", cctp.SourceName, "event_type", e.EventType,
		"contract_id", e.ContractID, "ledger", e.Ledger, "tx_hash", e.TxHash)
}

func persistRozoEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e rozo.Event) {
	if err := store.InsertRozoEvent(ctx, timescale.RozoEvent{
		ContractID:  e.ContractID,
		Ledger:      e.Ledger,
		TxHash:      e.TxHash,
		OpIndex:     uint32(e.OpIndex),
		ObservedAt:  e.ObservedAt,
		EventType:   timescale.RozoEventType(e.EventType),
		Amount:      e.Amount,
		Destination: e.Destination,
		From:        e.From,
		Memo:        e.Memo,
		Token:       e.Token,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(rozo.SourceName, "rozo_event").Inc()
		logger.Error("insert Rozo event failed",
			"contract_id", e.ContractID, "event_type", e.EventType,
			"ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, rozo.SourceName)
	logger.Debug("Rozo event ingested",
		"source", rozo.SourceName, "event_type", e.EventType,
		"contract_id", e.ContractID, "ledger", e.Ledger, "tx_hash", e.TxHash)
}

// bumpEntryCount is the shared 'entries' counter increment used by
// every sink whose decoded events don't ride the trades + oracle_updates
// per-insert bump path (those tables have their counter bump inlined
// in the INSERT). Surfaces source-attributed protocol activity on
// /v1/diagnostics/ingestion's `entries` column for the broader set
// of sources (blend lending, soroswap-router + defindex log-only
// sinks). Errors are logged at Warn — a failed bump doesn't fail
// the underlying decode/persist; the operator's periodic
// `ratesengine-ops seed-entry-counts` reconciles drift.
func bumpEntryCount(ctx context.Context, logger *slog.Logger, store *timescale.Store, source string) {
	if err := store.BumpSourceEntryCount(ctx, source, 1); err != nil {
		logger.Warn("bump source entry count failed",
			"source", source, "err", err)
	}
}

func persistAccountObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o accounts.Observation) {
	if err := store.InsertAccountObservation(ctx, o); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(accounts.SourceName, "account_observation").Inc()
		logger.Error("insert account observation failed",
			"account_id", o.AccountID, "ledger", o.Ledger,
			"is_removal", o.IsRemoval, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, accounts.SourceName)
	logger.Debug("account observation ingested",
		"account_id", o.AccountID, "ledger", o.Ledger,
		"balance_stroops", o.Balance.String(),
		"home_domain", o.HomeDomain, "is_removal", o.IsRemoval)
}

func persistTrustlineObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o trustlines.Observation) {
	if err := store.InsertTrustlineObservation(ctx, timescale.TrustlineObservation{
		AccountID:  o.AccountID,
		AssetKey:   o.AssetKey,
		Ledger:     o.Ledger,
		ObservedAt: o.ObservedAt,
		Balance:    o.Balance,
		IsRemoval:  o.IsRemoval,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(trustlines.SourceName, "trustline_observation").Inc()
		logger.Error("insert trustline observation failed",
			"account_id", o.AccountID, "asset_key", o.AssetKey, "ledger", o.Ledger,
			"is_removal", o.IsRemoval, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, trustlines.SourceName)
	logger.Debug("trustline observation ingested",
		"account_id", o.AccountID, "asset_key", o.AssetKey, "ledger", o.Ledger,
		"balance_stroops", o.Balance.String(), "is_removal", o.IsRemoval)
}

func persistClaimableObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o claimable_balances.Observation) {
	if err := store.InsertClaimableObservation(ctx, timescale.ClaimableObservation{
		ClaimableID: o.ClaimableID,
		AssetKey:    o.AssetKey,
		Ledger:      o.Ledger,
		ObservedAt:  o.ObservedAt,
		Balance:     o.Balance,
		IsRemoval:   o.IsRemoval,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(claimable_balances.SourceName, "claimable_observation").Inc()
		logger.Error("insert claimable observation failed",
			"claimable_id", o.ClaimableID, "asset_key", o.AssetKey, "ledger", o.Ledger,
			"err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, claimable_balances.SourceName)
	logger.Debug("claimable observation ingested",
		"claimable_id", o.ClaimableID, "asset_key", o.AssetKey, "ledger", o.Ledger,
		"balance_stroops", o.Balance.String())
}

func persistLPReserveObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o liquidity_pools.Observation) {
	if err := store.InsertLPReserveObservation(ctx, timescale.LPReserveObservation{
		PoolID:     o.PoolID,
		AssetKey:   o.AssetKey,
		Ledger:     o.Ledger,
		ObservedAt: o.ObservedAt,
		Balance:    o.Balance,
		IsRemoval:  o.IsRemoval,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(liquidity_pools.SourceName, "lp_reserve_observation").Inc()
		logger.Error("insert LP-reserve observation failed",
			"pool_id", o.PoolID, "asset_key", o.AssetKey, "ledger", o.Ledger,
			"err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, liquidity_pools.SourceName)
	logger.Debug("LP-reserve observation ingested",
		"pool_id", o.PoolID, "asset_key", o.AssetKey, "ledger", o.Ledger,
		"balance_stroops", o.Balance.String())
}

func persistSACBalanceObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o sac_balances.Observation) {
	if err := store.InsertSACBalanceObservation(ctx, timescale.SACBalanceObservation{
		ContractID: o.ContractID,
		AssetKey:   o.AssetKey,
		Holder:     o.Holder,
		Ledger:     o.Ledger,
		ObservedAt: o.ObservedAt,
		Balance:    o.Balance,
		IsRemoval:  o.IsRemoval,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(sac_balances.SourceName, "sac_balance_observation").Inc()
		logger.Error("insert SAC balance observation failed",
			"contract_id", o.ContractID, "holder", o.Holder, "asset_key", o.AssetKey,
			"ledger", o.Ledger, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, sac_balances.SourceName)
	logger.Debug("SAC balance observation ingested",
		"contract_id", o.ContractID, "holder", o.Holder, "asset_key", o.AssetKey,
		"ledger", o.Ledger, "balance_stroops", o.Balance.String(),
		"is_removal", o.IsRemoval)
}

func persistSoroswapSkim(ctx context.Context, logger *slog.Logger, store *timescale.Store, e soroswap.SkimEvent) {
	txHash, err := timescale.DecodeSoroswapTxHash(e.TxHash)
	if err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(soroswap.SourceName, "soroswap_skim_event").Inc()
		logger.Error("decode soroswap skim tx_hash failed",
			"contract_id", e.ContractID, "ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	if err := store.InsertSoroswapSkimEvent(ctx, timescale.SoroswapSkimEvent{
		ContractID:      e.ContractID,
		Ledger:          e.Ledger,
		LedgerCloseTime: e.ObservedAt,
		TxHash:          txHash,
		OpIndex:         int16(e.OpIndex),
		EventIndex:      int16(e.EventIndex),
		To:              e.To,
		Amount0:         e.Amount0.String(),
		Amount1:         e.Amount1.String(),
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(soroswap.SourceName, "soroswap_skim_event").Inc()
		logger.Error("insert soroswap skim event failed",
			"contract_id", e.ContractID, "ledger", e.Ledger, "tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, soroswap.SourceName)
	logger.Debug("soroswap skim event ingested",
		"contract_id", e.ContractID, "ledger", e.Ledger,
		"amount_0", e.Amount0.String(), "amount_1", e.Amount1.String(),
		"to", e.To)
}

func persistPhoenixLiquidity(ctx context.Context, logger *slog.Logger, store *timescale.Store, e phoenix.LiquidityEvent) {
	c := e.Change
	sharesStr := ""
	// Withdraw rows have shares_amount populated; provide rows don't.
	if c.Action == phoenix.EventActionWithdrawLiquidity {
		sharesStr = c.SharesAmount.String()
	}
	if err := store.InsertPhoenixLiquidityChange(ctx, timescale.PhoenixLiquidityChange{
		Pool:         c.Pool,
		Ledger:       c.Ledger,
		ObservedAt:   c.ClosedAt,
		TxHash:       c.TxHash,
		OpIndex:      uint32(c.OpIndex),
		Action:       timescale.PhoenixLiquidityAction(c.Action),
		Sender:       c.Sender,
		TokenA:       c.TokenA,
		TokenB:       c.TokenB,
		AmountA:      c.AmountA.String(),
		AmountB:      c.AmountB.String(),
		SharesAmount: sharesStr,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(phoenix.SourceName, "phoenix_liquidity").Inc()
		logger.Error("insert phoenix liquidity failed",
			"pool", c.Pool, "action", c.Action,
			"ledger", c.Ledger, "tx_hash", c.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, phoenix.SourceName)
	logger.Debug("phoenix liquidity ingested",
		"pool", c.Pool, "action", c.Action,
		"sender", c.Sender, "ledger", c.Ledger)
}

func persistPhoenixStake(ctx context.Context, logger *slog.Logger, store *timescale.Store, e phoenix.StakeEvent) {
	c := e.Change
	if err := store.InsertPhoenixStakeEvent(ctx, timescale.PhoenixStakeEvent{
		StakeContract: c.Contract,
		Ledger:        c.Ledger,
		ObservedAt:    c.ClosedAt,
		TxHash:        c.TxHash,
		OpIndex:       uint32(c.OpIndex),
		Action:        timescale.PhoenixStakeAction(c.Action),
		User:          c.User,
		LPToken:       c.LPToken,
		Amount:        c.Amount.String(),
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(phoenix.SourceName, "phoenix_stake").Inc()
		logger.Error("insert phoenix stake event failed",
			"stake_contract", c.Contract, "action", c.Action,
			"ledger", c.Ledger, "tx_hash", c.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, phoenix.SourceName)
	logger.Debug("phoenix stake event ingested",
		"stake_contract", c.Contract, "action", c.Action,
		"user", c.User, "ledger", c.Ledger)
}

func persistSEP41SupplyEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e sep41_supply.Event) {
	if err := store.InsertSEP41SupplyEvent(ctx, timescale.SEP41SupplyEvent{
		ContractID:   e.ContractID,
		Ledger:       e.Ledger,
		TxHash:       e.TxHash,
		OpIndex:      e.OpIndex,
		ObservedAt:   e.ObservedAt,
		Kind:         timescale.SEP41EventKind(e.Kind),
		Amount:       e.Amount,
		Counterparty: e.Counterparty,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(sep41_supply.SourceName, "sep41_supply_event").Inc()
		logger.Error("insert SEP-41 supply event failed",
			"contract_id", e.ContractID, "kind", e.Kind, "ledger", e.Ledger,
			"tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, sep41_supply.SourceName)
	logger.Debug("SEP-41 supply event ingested",
		"contract_id", e.ContractID, "kind", e.Kind, "ledger", e.Ledger,
		"amount", e.Amount.String(), "counterparty", e.Counterparty)
}

// persistSEP41TransferEvent routes one sep41_transfers audit-trail
// event (transfer / approve / set_admin / set_authorized) to the
// sep41_transfers hypertable. F-0021 closure (audit-2026-05-26):
// unlocks per-account net-position queries — the Stellar moat
// feature CG/CMC structurally cannot offer.
func persistSEP41TransferEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, e sep41_transfers.Event) {
	if err := store.InsertSEP41Transfer(ctx, timescale.SEP41TransferRow{
		ContractID:      e.ContractID,
		Ledger:          e.Ledger,
		TxHash:          e.TxHash,
		OpIndex:         e.OpIndex,
		EventIndex:      e.EventIndex,
		ObservedAt:      e.ObservedAt,
		Kind:            timescale.SEP41TransferKind(e.Kind),
		FromAddr:        e.FromAddr,
		ToAddr:          e.ToAddr,
		Amount:          e.Amount,
		LiveUntilLedger: e.LiveUntilLedger,
		Authorized:      e.Authorized,
	}); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(sep41_transfers.SourceName, "sep41_transfers_event").Inc()
		logger.Error("insert SEP-41 transfer event failed",
			"contract_id", e.ContractID, "kind", e.Kind, "ledger", e.Ledger,
			"tx_hash", e.TxHash, "err", err)
		return
	}
	bumpEntryCount(ctx, logger, store, sep41_transfers.SourceName)
	logger.Debug("SEP-41 transfer event ingested",
		"contract_id", e.ContractID, "kind", e.Kind, "ledger", e.Ledger,
		"from", e.FromAddr, "to", e.ToAddr)
}
