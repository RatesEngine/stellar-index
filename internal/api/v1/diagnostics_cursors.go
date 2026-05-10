package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CursorsReader is the seam the /v1/diagnostics/cursors handler reads
// through. timescale.Store satisfies it via ListCursors.
type CursorsReader interface {
	ListCursors(ctx context.Context) ([]timescale.Cursor, error)
}

// Cursor is the wire shape of one row in the
// /v1/diagnostics/cursors response. last_updated is RFC 3339; lag
// is reported as seconds-since-update so operators can spot stuck
// sources without wall-clock math.
type Cursor struct {
	Source      string `json:"source"`
	SubSource   string `json:"sub_source,omitempty"`
	LastLedger  uint32 `json:"last_ledger"`
	LastUpdated string `json:"last_updated"`
	LagSeconds  int64  `json:"lag_seconds"`
}

// handleCursors serves GET /v1/diagnostics/cursors — every row of
// `ingestion_cursors` so operators (and the explorer /diagnostics
// page) can see per-source ingest progress at a glance. Not a hot
// path; the table is small (one row per (source, sub_source)).
//
// Optional query param:
//
//   - max_age — Go-duration string (e.g. "1h", "30m", "5m"). When
//     present, rows with lag_seconds greater than this value are
//     excluded. Useful for direct API users who only care about
//     live cursors (the explorer's /diagnostics page does the
//     same filter client-side, defaulting to 1h — see
//     CursorsTable). Empty / omitted = return everything.
//     Invalid duration → 400 invalid-max-age.
func (s *Server) handleCursors(w http.ResponseWriter, r *http.Request) {
	if s.cursors == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/cursors-unavailable",
			"Cursors unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the cursors reader yet.")
		return
	}

	var maxAge time.Duration
	if raw := r.URL.Query().Get("max_age"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-max-age",
				"Invalid max_age", http.StatusBadRequest,
				"max_age must be a positive Go-duration string (e.g. \"1h\", \"30m\", \"5m\")")
			return
		}
		maxAge = d
	}

	rows, err := s.cursors.ListCursors(r.Context())
	if err != nil {
		s.logger.Warn("cursors list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/cursors-error",
			"Cursors listing failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	now := time.Now().UTC()
	out := make([]Cursor, 0, len(rows))
	for _, c := range rows {
		lag := now.Sub(c.UpdatedAt)
		if maxAge > 0 && lag > maxAge {
			continue
		}
		out = append(out, Cursor{
			Source:      c.Source,
			SubSource:   c.Sub,
			LastLedger:  c.LastLedger,
			LastUpdated: c.UpdatedAt.UTC().Format(time.RFC3339),
			LagSeconds:  int64(lag.Seconds()),
		})
	}
	writeJSON(w, out, Flags{})
}
