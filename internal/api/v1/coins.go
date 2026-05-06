package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CoinsReader is the seam the /v1/coins handler reads through.
// timescale.Store satisfies it via ListCoins.
type CoinsReader interface {
	ListCoins(ctx context.Context, limit int, issuer string) ([]timescale.CoinRow, error)
}

// Coin is the wire shape of one entry in the /v1/coins response.
//
// v0 omits the price / delta / volume fields that the registry-
// aware super-table per data-inventory §10.1 will ship with — those
// arrive once we join `change_summary_5m` + `classic_asset_stats_5m`.
// Today's response is the bare-minimum identity tuple plus activity
// counters, enough for the explorer /coins directory to render real
// rows instead of a static seed.
type Coin struct {
	Slug             string `json:"slug"`
	AssetID          string `json:"asset_id"`
	Code             string `json:"code"`
	Issuer           string `json:"issuer"`
	FirstSeenLedger  uint32 `json:"first_seen_ledger"`
	LastSeenLedger   uint32 `json:"last_seen_ledger"`
	ObservationCount int64  `json:"observation_count"`
}

// handleCoins serves GET /v1/coins.
//
// Returns 503 when no CoinsReader is wired (deployment hasn't
// connected to a postgres with the classic_assets registry).
// Returns 400 on out-of-range `limit`. Always returns a JSON array
// even when empty so the wire shape stays predictable for the
// explorer frontend.
func (s *Server) handleCoins(w http.ResponseWriter, r *http.Request) {
	if s.coins == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-unavailable",
			"Coins listing unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the coins reader yet.")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be 1-500")
			return
		}
		limit = n
	}

	// Optional ?issuer=G… filter — passed straight through to
	// storage. Empty string means "no filter, return all."
	issuer := r.URL.Query().Get("issuer")

	rows, err := s.coins.ListCoins(r.Context(), limit, issuer)
	if err != nil {
		s.logger.Warn("coins list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coins listing failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	out := make([]Coin, len(rows))
	for i, r := range rows {
		out[i] = Coin{
			Slug:             r.Slug,
			AssetID:          r.AssetID,
			Code:             r.Code,
			Issuer:           r.IssuerGStrkey,
			FirstSeenLedger:  r.FirstSeenLedger,
			LastSeenLedger:   r.LastSeenLedger,
			ObservationCount: r.ObservationCount,
		}
	}
	writeJSON(w, out, Flags{})
}
