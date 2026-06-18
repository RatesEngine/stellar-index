package v1

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// LendingReader is the storage-side seam for /v1/lending/pools.
// timescale.Store implements via ListBlendPools.
type LendingReader interface {
	ListBlendPools(ctx context.Context) ([]timescale.BlendPoolSummary, error)
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
