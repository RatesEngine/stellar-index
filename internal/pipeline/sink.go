package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/accounts"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	claimable_balances "github.com/RatesEngine/rates-engine/internal/sources/claimable_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/sources/liquidity_pools"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	sac_balances "github.com/RatesEngine/rates-engine/internal/sources/sac_balances"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	sep41_supply "github.com/RatesEngine/rates-engine/internal/sources/sep41_supply"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/sources/trustlines"
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
func handleOneEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, ev consumer.Event) { //nolint:gocyclo // dispatch table; one case per consumer.Event implementation. Splitting would reduce clarity.
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
	case blend.NewAuctionEvent:
		persistBlendNewAuction(ctx, logger, store, e)
	case blend.FillAuctionEvent:
		persistBlendFillAuction(ctx, logger, store, e)
	case blend.DeleteAuctionEvent:
		persistBlendDeleteAuction(ctx, logger, store, e)
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
	logger.Info("blend delete_auction ingested",
		"pool", e.Pool, "user", e.User, "auction_type", e.AuctionType,
		"ledger", e.Ledger)
}

func persistAccountObservation(ctx context.Context, logger *slog.Logger, store *timescale.Store, o accounts.Observation) {
	if err := store.InsertAccountObservation(ctx, o); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(accounts.SourceName, "account_observation").Inc()
		logger.Error("insert account observation failed",
			"account_id", o.AccountID, "ledger", o.Ledger,
			"is_removal", o.IsRemoval, "err", err)
		return
	}
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
	logger.Debug("SAC balance observation ingested",
		"contract_id", o.ContractID, "holder", o.Holder, "asset_key", o.AssetKey,
		"ledger", o.Ledger, "balance_stroops", o.Balance.String(),
		"is_removal", o.IsRemoval)
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
	logger.Debug("SEP-41 supply event ingested",
		"contract_id", e.ContractID, "kind", e.Kind, "ledger", e.Ledger,
		"amount", e.Amount.String(), "counterparty", e.Counterparty)
}
