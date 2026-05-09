package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// LendingReader is the storage-side seam for /v1/lending/pools.
// timescale.Store implements via ListBlendPools.
type LendingReader interface {
	ListBlendPools(ctx context.Context) ([]timescale.BlendPoolSummary, error)
}

// LendingPool is the wire shape for /v1/lending/pools entries.
//
// Today the listing is Blend-only — every row is one Blend pool
// contract observed in the auction stream. Per-pool TVL,
// utilisation, supply/borrow APYs land via additional fields
// once the pool-storage reader worker ships; the wire shape is
// designed to grow rather than version-bump.
type LendingPool struct {
	Protocol       string    `json:"protocol"`
	Pool           string    `json:"pool"`
	Auctions24h    int64     `json:"auctions_24h"`
	AuctionsTotal  int64     `json:"auctions_total"`
	UniqueUsers30d int64     `json:"unique_users_30d"`
	LastSeen       time.Time `json:"last_seen"`
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
		if errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("ListBlendPools deadline exceeded")
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/lending-timeout",
				"Lending pools query timed out", http.StatusServiceUnavailable,
				"the per-pool auction + user aggregates didn't return in 8s; retry shortly.")
			return
		}
		s.logger.Error("ListBlendPools failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	out := make([]LendingPool, len(rows))
	for i, p := range rows {
		out[i] = LendingPool{
			Protocol:       "blend",
			Pool:           p.Pool,
			Auctions24h:    p.Auctions24h,
			AuctionsTotal:  p.AuctionsTotal,
			UniqueUsers30d: p.UniqueUsers30d,
			LastSeen:       p.LastSeen,
		}
	}
	writeJSON(w, out, Flags{})
}
