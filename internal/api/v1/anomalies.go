package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// AnomalyReader backs /v1/anomalies — the durable freeze-event mirror
// (ADR-0019). timescale.Store implements it.
type AnomalyReader interface {
	ListFreezeEvents(ctx context.Context, firingOnly bool, limit int) ([]timescale.FreezeEventRow, error)
	FreezeReasonCounts(ctx context.Context, sinceDays int) ([]timescale.FreezeReasonCount, error)
}

// DivergenceReader backs /v1/divergence — the per-reference
// divergence-observation history. timescale.Store implements it.
type DivergenceReader interface {
	ListDivergenceLatest(ctx context.Context, sinceDays int, firingOnly bool, limit int) ([]timescale.DivergenceRow, error)
}

// ── /v1/anomalies ────────────────────────────────────────────────

// AnomaliesView is the wire response for GET /v1/anomalies.
type AnomaliesView struct {
	FiringCount int               `json:"firing_count"`
	ReasonTally []ReasonCountV    `json:"reason_tally"`
	Events      []FreezeEventView `json:"events"`
}

type ReasonCountV struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// FreezeEventView mirrors a freeze_events row. recovered_at is null
// while the freeze is currently firing. frozen_value is a decimal
// string (ADR-0003).
type FreezeEventView struct {
	AssetID           string          `json:"asset_id"`
	QuoteID           string          `json:"quote_id"`
	FrozenAt          string          `json:"frozen_at"`
	FrozenAtLedger    int64           `json:"frozen_at_ledger"`
	Reason            string          `json:"reason"`
	FrozenValue       string          `json:"frozen_value"`
	RecoveredAt       *string         `json:"recovered_at"`
	RecoveredAtLedger *int64          `json:"recovered_at_ledger"`
	Firing            bool            `json:"firing"`
	Detail            json.RawMessage `json:"detail,omitempty"`
}

// handleAnomalies serves GET /v1/anomalies — the freeze timeline
// (ADR-0019). `?firing=true` restricts to currently-firing pairs;
// `?limit=` (default 100, max 500); `?window_days=` scopes the reason
// tally (default 30). Always reports the live firing count + per-reason
// breakdown alongside the event list.
//
// 200 + empty payload when no reader is wired — feature-gated like
// /v1/lending/pools.
func (s *Server) handleAnomalies(w http.ResponseWriter, r *http.Request) {
	if s.anomalies == nil {
		writeJSON(w, AnomaliesView{ReasonTally: []ReasonCountV{}, Events: []FreezeEventView{}}, Flags{})
		return
	}
	limit, ok := parseExplorerLimit(w, r, 100, 500)
	if !ok {
		return
	}
	firingOnly := r.URL.Query().Get("firing") == "true"
	windowDays := parseWindowDays(r, 30)

	events, err := s.anomalies.ListFreezeEvents(r.Context(), firingOnly, limit)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ListFreezeEvents failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}
	// Always compute the live firing count (independent of the firing
	// filter) so the UI can show "N firing now" on the full timeline.
	firing, err := s.anomalies.ListFreezeEvents(r.Context(), true, 500)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ListFreezeEvents(firing) failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}
	tally, err := s.anomalies.FreezeReasonCounts(r.Context(), windowDays)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("FreezeReasonCounts failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}

	out := AnomaliesView{
		FiringCount: len(firing),
		ReasonTally: make([]ReasonCountV, len(tally)),
		Events:      make([]FreezeEventView, len(events)),
	}
	for i, t := range tally {
		out.ReasonTally[i] = ReasonCountV{Reason: t.Reason, Count: t.Count}
	}
	for i, e := range events {
		out.Events[i] = freezeEventView(e)
	}
	writeJSON(w, out, Flags{})
}

func freezeEventView(e timescale.FreezeEventRow) FreezeEventView {
	v := FreezeEventView{
		AssetID:           e.AssetID,
		QuoteID:           e.QuoteID,
		FrozenAt:          e.FrozenAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		FrozenAtLedger:    e.FrozenAtLedger,
		Reason:            e.Reason,
		FrozenValue:       e.FrozenValue,
		RecoveredAtLedger: e.RecoveredAtLedger,
		Firing:            e.RecoveredAt == nil,
		Detail:            rawOrNull(e.Detail),
	}
	if e.RecoveredAt != nil {
		s := e.RecoveredAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		v.RecoveredAt = &s
	}
	if e.Detail == "" {
		v.Detail = nil // omit rather than emit "null"
	}
	return v
}

// ── /v1/divergence ───────────────────────────────────────────────

// DivergenceView is the wire response for GET /v1/divergence.
type DivergenceView struct {
	Observations []DivergenceObsV `json:"observations"`
}

// DivergenceObsV mirrors the latest divergence_observations row per
// (asset, quote, reference). Prices + delta are decimal strings.
type DivergenceObsV struct {
	AssetID          string `json:"asset_id"`
	QuoteID          string `json:"quote_id"`
	Reference        string `json:"reference"`
	ObservedAt       string `json:"observed_at"`
	ObservedAtLedger int64  `json:"observed_at_ledger"`
	OurPrice         string `json:"our_price"`
	RefPrice         string `json:"ref_price"`
	DeltaPct         string `json:"delta_pct"`
	Status           string `json:"status"`
}

// handleDivergence serves GET /v1/divergence — the current
// cross-reference divergence board: the latest observation per
// (asset, quote, reference) within `?window_days=` (default 7),
// widest |delta_pct| first. `?firing=true` restricts to references
// whose latest comparison breached threshold; `?limit=` (default 100,
// max 500).
//
// 200 + empty payload when no reader is wired.
func (s *Server) handleDivergence(w http.ResponseWriter, r *http.Request) {
	if s.divergences == nil {
		writeJSON(w, DivergenceView{Observations: []DivergenceObsV{}}, Flags{})
		return
	}
	limit, ok := parseExplorerLimit(w, r, 100, 500)
	if !ok {
		return
	}
	firingOnly := r.URL.Query().Get("firing") == "true"
	windowDays := parseWindowDays(r, 7)

	rows, err := s.divergences.ListDivergenceLatest(r.Context(), windowDays, firingOnly, limit)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ListDivergenceLatest failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal", "Internal error", http.StatusInternalServerError, "")
		return
	}
	out := DivergenceView{Observations: make([]DivergenceObsV, len(rows))}
	for i, d := range rows {
		out.Observations[i] = DivergenceObsV{
			AssetID:          d.AssetID,
			QuoteID:          d.QuoteID,
			Reference:        d.Reference,
			ObservedAt:       d.ObservedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			ObservedAtLedger: d.ObservedAtLedger,
			OurPrice:         d.OurPrice,
			RefPrice:         d.RefPrice,
			DeltaPct:         d.DeltaPct,
			Status:           d.Status,
		}
	}
	writeJSON(w, out, Flags{})
}

// parseWindowDays reads an optional ?window_days= positive int,
// clamped to [1, 365]; def when absent or malformed.
func parseWindowDays(r *http.Request, def int) int {
	raw := r.URL.Query().Get("window_days")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 365 {
		return def
	}
	return n
}
