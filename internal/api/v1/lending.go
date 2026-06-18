package v1

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// LendingReader is the storage-side seam for /v1/lending/pools.
// timescale.Store implements via ListBlendPools / BlendPoolAssets.
type LendingReader interface {
	ListBlendPools(ctx context.Context) ([]timescale.BlendPoolSummary, error)
	BlendPoolAssets(ctx context.Context, pool string) ([]string, error)
}

// LendingPool is the wire shape for /v1/lending/pools entries.
//
// Today the listing is Blend-only — every row is one Blend pool
// contract observed in the event stream (auctions and/or position
// events). net_supplied_30d / net_borrowed_30d are a 30-day NET-FLOW
// proxy (token base-units, summed across the pool's assets), NOT
// all-time TVL or current reserve balances. utilization_30d_pct is
// the window borrow/supply ratio when net supply is positive (a
// coarse proxy), else null. Real current-state TVL + supply/borrow
// APYs (reserve b_rate/d_rate) need the Soroban pool-storage reader;
// these fields stand in until it ships and the wire shape is designed
// to grow rather than version-bump.
type LendingPool struct {
	Protocol          string    `json:"protocol"`
	Pool              string    `json:"pool"`
	Auctions24h       int64     `json:"auctions_24h"`
	AuctionsTotal     int64     `json:"auctions_total"`
	UniqueUsers30d    int64     `json:"unique_users_30d"`
	LastSeen          time.Time `json:"last_seen"`
	NetSupplied30d    string    `json:"net_supplied_30d"`              // token base-units, window net-flow proxy
	NetBorrowed30d    string    `json:"net_borrowed_30d"`              // token base-units, window net-flow proxy
	Utilization30dPct *float64  `json:"utilization_30d_pct,omitempty"` // borrow/supply window ratio; null when net supply ≤ 0
}

// handleLendingPools serves GET /v1/lending/pools.
//
// Returns one row per distinct Blend pool contract observed in
// the trailing-7d auction stream, with auction counts and last-
// seen timestamp. Sorted by total auction count desc.
//
// 200 + empty array when no LendingReader is wired or no pools
// have been observed — consistent with the rest of the
// "feature-gated reader" handlers.
func (s *Server) handleLendingPools(w http.ResponseWriter, r *http.Request) {
	reader := s.lending
	if reader == nil {
		writeJSON(w, []LendingPool{}, Flags{})
		return
	}
	// 8s ceiling — same pattern as #1082 / #1099–#1104.
	// ListBlendPools fans out per-pool auction-count + user-count
	// queries against the trades hypertable; cold cache can take 5+s.
	lpCtx, lpCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer lpCancel()
	rows, err := reader.ListBlendPools(lpCtx)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(lpCtx, err) {
			s.logger.Warn("ListBlendPools deadline exceeded")
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/lending-timeout",
				"Lending pools query timed out", http.StatusServiceUnavailable,
				"the per-pool auction + user aggregates didn't return in 8s; retry shortly.")
			return
		}
		if IsCacheUnavailable(err) {
			s.logger.Warn("ListBlendPools cache unavailable", "err", err)
			writeCacheUnavailableProblem(w, r)
			return
		}
		s.logger.Error("ListBlendPools failed", "err", err)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	out := make([]LendingPool, len(rows))
	for i, p := range rows {
		out[i] = LendingPool{
			Protocol:          "blend",
			Pool:              p.Pool,
			Auctions24h:       p.Auctions24h,
			AuctionsTotal:     p.AuctionsTotal,
			UniqueUsers30d:    p.UniqueUsers30d,
			LastSeen:          p.LastSeen,
			NetSupplied30d:    p.NetSupplied30d,
			NetBorrowed30d:    p.NetBorrowed30d,
			Utilization30dPct: utilizationPct(p.NetSupplied30d, p.NetBorrowed30d),
		}
	}
	writeJSON(w, out, Flags{})
}

// LendingPoolReservesView is the wire response for
// GET /v1/lending/pools/{pool}/reserves — real per-reserve current
// state (ADR-0039), read from the lake's Soroban contract storage.
type LendingPoolReservesView struct {
	Pool     string        `json:"pool"`
	TVLUSD   *string       `json:"tvl_usd"` // sum of priced reserves; null when none priced
	Reserves []ReserveView `json:"reserves"`
}

// ReserveView is one reserve's decoded state + derived metrics.
// Amounts are token-base-unit decimal strings (ADR-0003); USD values
// are decimal strings or null when the asset has no resolved price.
type ReserveView struct {
	Asset          string  `json:"asset"`
	Decimals       uint32  `json:"decimals"`
	Supplied       string  `json:"supplied"` // underlying token base units
	Borrowed       string  `json:"borrowed"`
	SuppliedUSD    *string `json:"supplied_usd"`
	BorrowedUSD    *string `json:"borrowed_usd"`
	UtilizationPct float64 `json:"utilization_pct"`
	BorrowAPR      float64 `json:"borrow_apr"`
	SupplyAPR      float64 `json:"supply_apr"`
}

// handleLendingPoolReserves serves GET /v1/lending/pools/{pool}/reserves
// — REAL current-state TVL / utilization / supply+borrow APY per
// reserve (ADR-0039), decoded from the pool contract's Soroban storage
// in the lake. USD values are best-effort: priced when we have a USD
// price for the reserve's underlying token, else null (the token-unit
// amounts + util + APY are always exact). Distinct from the
// /v1/lending/pools window net-flow proxy.
func (s *Server) handleLendingPoolReserves(w http.ResponseWriter, r *http.Request) {
	if s.explorer == nil {
		s.explorerUnavailable(w, r)
		return
	}
	if s.lending == nil {
		writeJSON(w, LendingPoolReservesView{Reserves: []ReserveView{}}, Flags{})
		return
	}
	pool := r.PathValue("pool")
	if !canonical.IsContractID(pool) {
		writeProblem(w, r, "https://api.stellarindex.io/errors/invalid-pool",
			"Invalid pool", http.StatusBadRequest, "pool must be a C-strkey contract id")
		return
	}

	// 15s — the reserve lookup is a ledger-windowed scan over the lake's
	// contract_data (key_xdr has no skip-index yet); ~6-7s on r1, so a
	// generous ceiling with margin. See BlendPoolReserves.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	assets, err := s.lending.BlendPoolAssets(ctx, pool)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("BlendPoolAssets failed", "err", err, "pool", pool)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}
	states, err := s.explorer.BlendPoolReserves(ctx, pool, assets)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("BlendPoolReserves failed", "err", err, "pool", pool)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}

	out := LendingPoolReservesView{Pool: pool, Reserves: make([]ReserveView, 0, len(states))}
	tvl := new(big.Rat)
	anyPriced := false
	for _, st := range states {
		rv, suppliedUSD := s.buildReserveView(ctx, st)
		if suppliedUSD != nil {
			tvl.Add(tvl, suppliedUSD)
			anyPriced = true
		}
		out.Reserves = append(out.Reserves, rv)
	}
	if anyPriced {
		tvlStr := tvl.FloatString(2)
		out.TVLUSD = &tvlStr
	}
	writeJSON(w, out, Flags{})
}

// buildReserveView maps one decoded reserve state to its wire shape,
// pricing it in USD best-effort. Returns the supplied-USD contribution
// (nil when unpriced) so the caller can sum the pool TVL.
func (s *Server) buildReserveView(ctx context.Context, st clickhouse.BlendReserveState) (ReserveView, *big.Rat) {
	rv := ReserveView{
		Asset:          st.Asset,
		Decimals:       st.Config.Decimals,
		Supplied:       st.Metrics.SuppliedUnderlying.String(),
		Borrowed:       st.Metrics.BorrowedUnderlying.String(),
		UtilizationPct: round2(st.Metrics.UtilizationPct),
		BorrowAPR:      round4(st.Metrics.BorrowAPR),
		SupplyAPR:      round4(st.Metrics.SupplyAPR),
	}
	price, ok := s.reservePriceUSD(ctx, st.Asset)
	if !ok {
		return rv, nil
	}
	dec := int(st.Config.Decimals)
	var suppliedContrib *big.Rat
	if usd, err := usdMarketValue(st.Metrics.SuppliedUnderlying, price, dec); err == nil {
		rv.SuppliedUSD = &usd
		if v, ok := new(big.Rat).SetString(usd); ok {
			suppliedContrib = v
		}
	}
	if usd, err := usdMarketValue(st.Metrics.BorrowedUnderlying, price, dec); err == nil {
		rv.BorrowedUSD = &usd
	}
	return rv, suppliedContrib
}

// reservePriceUSD resolves a USD price for a Blend reserve's underlying
// token (a Soroban SAC contract id). Best-effort: returns ok=false when
// no price is available (TVL is then reported in token units only).
func (s *Server) reservePriceUSD(ctx context.Context, assetC string) (string, bool) {
	asset, err := canonical.ParseAsset(assetC)
	if err != nil {
		return "", false
	}
	return s.lookupUSDPrice(ctx, asset)
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

// utilizationPct returns the window borrow/supply ratio as a
// percentage (2dp), or nil when net supply is ≤ 0 (a utilisation
// figure has no meaning then). Both inputs are decimal big-int
// strings in token base-units; the ratio is dimensionless so the
// per-asset decimal scale cancels for a single-asset pool and is a
// coarse proxy for a multi-asset one (documented on the wire shape).
func utilizationPct(netSuppliedStr, netBorrowedStr string) *float64 {
	supplied, ok := new(big.Rat).SetString(netSuppliedStr)
	if !ok || supplied.Sign() <= 0 {
		return nil
	}
	borrowed, ok := new(big.Rat).SetString(netBorrowedStr)
	if !ok || borrowed.Sign() < 0 {
		return nil
	}
	ratio := new(big.Rat).Quo(borrowed, supplied)
	pct, _ := new(big.Rat).Mul(ratio, big.NewRat(100, 1)).Float64()
	pct = float64(int64(pct*100+0.5)) / 100 // round to 2dp
	return &pct
}
