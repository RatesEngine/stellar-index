package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// LedgerTipView is the wire shape of /v1/ledger/tip and the data
// field of each /v1/ledger/stream event. It reports the live-ingest
// frontier: the highest ledger the indexer has committed.
//
// LatestLedger is read from the `ledgerstream` row of
// `ingestion_cursors` — the indexer upserts it once per ledger, so
// it is the freshest "latest ledger we hold" signal available
// (fresher than /v1/diagnostics/ingestion's `latest_ledger`, which
// is derived from prices_1m's MAX(ledger_sequence) and only advances
// on ledgers that produced a trade row). The two agree within a few
// ledgers in steady state.
//
// IngestedAt is the cursor's last_updated (server-side now() at
// upsert time); LagSeconds is its wall-clock age — the same lag
// definition /v1/diagnostics/ingestion reports.
type LedgerTipView struct {
	LatestLedger uint32    `json:"latest_ledger"`
	IngestedAt   time.Time `json:"ingested_at"`
	LagSeconds   int64     `json:"lag_seconds"`
}

// handleLedgerTip serves GET /v1/ledger/tip — a deliberately
// lightweight endpoint returning only the live-ingest frontier
// (latest ingested ledger + its age). It exists so a status page or
// monitor can poll "what ledger are we on" without pulling the full
// /v1/diagnostics/ingestion snapshot (which also computes 24h
// volume, backfill state, supply, the source registry, …).
//
// Cache-Control is a short max-age=2: the cursor advances every
// ~5s, so a 2s edge cache absorbs a refreshing status page without
// hiding a stall. Clients that want push semantics use
// /v1/ledger/stream instead.
func (s *Server) handleLedgerTip(w http.ResponseWriter, r *http.Request) {
	if s.cursors == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/ledger-tip-unavailable",
			"Ledger tip not available", http.StatusServiceUnavailable,
			"this deployment has no CursorsReader wired — check binary configuration")
		return
	}

	view, ok, err := s.ledgerTip(r.Context())
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ledgerTip failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	if !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/ledger-tip-unavailable",
			"Ledger tip not available", http.StatusServiceUnavailable,
			"the live-ingest cursor has not been established yet — the indexer "+
				"has not committed its first ledger on this deployment")
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=2")
	writeJSON(w, view, Flags{})
}

// ledgerTip is the shared core of [Server.handleLedgerTip] and the
// /v1/ledger/stream producer. It reads the cursors table, finds the
// live `ledgerstream` row, and projects it into a LedgerTipView.
//
// Returns ok=false (no error) when the ledgerstream cursor row does
// not exist yet — that is a legitimate cold-start state, not a
// failure. A non-nil error means the cursors read itself failed.
func (s *Server) ledgerTip(ctx context.Context) (LedgerTipView, bool, error) {
	rows, err := s.cursors.ListCursors(ctx)
	if err != nil {
		return LedgerTipView{}, false, err
	}
	c, ok := findLedgerstreamCursor(rows)
	if !ok {
		return LedgerTipView{}, false, nil
	}
	lag := int64(time.Since(c.UpdatedAt).Seconds())
	if lag < 0 {
		// Clock skew between the API host and the DB server: never
		// report a negative lag.
		lag = 0
	}
	return LedgerTipView{
		LatestLedger: c.LastLedger,
		IngestedAt:   c.UpdatedAt.UTC(),
		LagSeconds:   lag,
	}, true, nil
}

// findLedgerstreamCursor picks the live-ingest cursor out of a
// ListCursors result. The live indexer's row is (source =
// "ledgerstream", sub_source = ""); backfill cursors share the table
// but carry a non-empty sub_source, and other sources never use the
// "ledgerstream" name. Mirrors the selector in ledgerStreamLagSeconds.
func findLedgerstreamCursor(rows []timescale.Cursor) (timescale.Cursor, bool) {
	for _, c := range rows {
		if c.Source == cursorSourceLedgerstream && c.Sub == "" {
			return c, true
		}
	}
	return timescale.Cursor{}, false
}

// cursorSourceLedgerstream is the ingestion_cursors.source value the
// live indexer writes (cmd/ratesengine-indexer's cursorSource const).
const cursorSourceLedgerstream = "ledgerstream"
