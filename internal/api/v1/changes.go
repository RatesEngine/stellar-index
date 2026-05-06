package v1

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// ChangeSummaryReader is the seam the change-summary handler reads
// through. timescale.Store satisfies it via GetChangeSummary; tests
// substitute a fake.
type ChangeSummaryReader interface {
	GetChangeSummary(ctx context.Context, entityType, entityID string) (timescale.ChangeSummaryRow, error)
}

// ChangeSummaryResponse is the wire shape returned by
// GET /v1/changes/{entity_type}/{id}.
//
// Mirrors the change_summary_5m hypertable but with JSON-friendly
// types: pointers stay pointers (omitempty + null on the wire),
// timestamps are RFC 3339, and the entity-keying tuple is echoed
// in the response so a single payload is self-describing.
//
// Powers every multi-window delta strip on the explorer per
// data-inventory §6.1.
type ChangeSummaryResponse struct {
	EntityType   string  `json:"entity_type"`
	EntityID     string  `json:"entity_id"`
	RefreshedAt  string  `json:"refreshed_at"`
	CurrentValue float64 `json:"current_value"`

	H1Value     *float64 `json:"h1_value,omitempty"`
	H1DeltaPct  *float64 `json:"h1_delta_pct,omitempty"`
	H24Value    *float64 `json:"h24_value,omitempty"`
	H24DeltaPct *float64 `json:"h24_delta_pct,omitempty"`
	D7Value     *float64 `json:"d7_value,omitempty"`
	D7DeltaPct  *float64 `json:"d7_delta_pct,omitempty"`
	D30Value    *float64 `json:"d30_value,omitempty"`
	D30DeltaPct *float64 `json:"d30_delta_pct,omitempty"`

	ATHValue *float64 `json:"ath_value,omitempty"`
	ATHAt    string   `json:"ath_at,omitempty"`
	ATLValue *float64 `json:"atl_value,omitempty"`
	ATLAt    string   `json:"atl_at,omitempty"`

	StreakDirection string `json:"streak_direction,omitempty"`
	StreakDays      *int   `json:"streak_days,omitempty"`
	Acceleration    string `json:"acceleration,omitempty"`
}

// allowedChangeSummaryEntityTypes pins the set of entity_type values
// the API accepts. Mirrors the CHECK constraint on change_summary_5m
// — having both means an operator hitting a fresh deployment with a
// new type sees a clean 400 rather than an ambiguous 404.
var allowedChangeSummaryEntityTypes = map[string]struct{}{
	"coin":     {},
	"protocol": {},
	"pair":     {},
	"source":   {},
}

// handleChangeSummary serves GET /v1/changes/{entity_type}/{id}.
//
// Returns 503 when no ChangeSummary reader is wired (operator
// hasn't run the rollup worker yet). Returns 400 for bad
// entity_type. Returns 404 (problem+json) when the entity has no
// row yet — the worker's first refresh hasn't run, or the entity
// was added since the last refresh.
//
// Cache header: short-lived, since the worker refreshes on a
// 5-minute cadence.
func (s *Server) handleChangeSummary(w http.ResponseWriter, r *http.Request) {
	if s.changesum == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/change-summary-unavailable",
			"Change summary unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the change-summary reader yet.")
		return
	}

	entityType := r.PathValue("entity_type")
	if _, ok := allowedChangeSummaryEntityTypes[entityType]; !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-entity-type",
			"Invalid entity_type", http.StatusBadRequest,
			"entity_type must be one of: coin, protocol, pair, source")
		return
	}
	entityID := r.PathValue("id")
	if entityID == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-entity-id",
			"Invalid entity_id", http.StatusBadRequest,
			"id path segment is required")
		return
	}

	row, err := s.changesum.GetChangeSummary(r.Context(), entityType, entityID)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/change-summary-not-found",
			"Change summary not found", http.StatusNotFound,
			"The change-summary worker hasn't computed a row for this entity yet.")
		return
	}
	if err != nil {
		s.logger.Warn("change-summary read",
			"entity_type", entityType, "entity_id", entityID, "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/change-summary-error",
			"Change summary read failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	resp := ChangeSummaryResponse{
		EntityType:      row.EntityType,
		EntityID:        row.EntityID,
		RefreshedAt:     row.RefreshedAt.UTC().Format(time.RFC3339),
		CurrentValue:    row.CurrentValue,
		H1Value:         row.H1Value,
		H1DeltaPct:      row.H1DeltaPct,
		H24Value:        row.H24Value,
		H24DeltaPct:     row.H24DeltaPct,
		D7Value:         row.D7Value,
		D7DeltaPct:      row.D7DeltaPct,
		D30Value:        row.D30Value,
		D30DeltaPct:     row.D30DeltaPct,
		ATHValue:        row.ATHValue,
		ATLValue:        row.ATLValue,
		StreakDirection: row.StreakDirection,
		StreakDays:      row.StreakDays,
		Acceleration:    row.Acceleration,
	}
	if row.ATHAt != nil {
		resp.ATHAt = row.ATHAt.UTC().Format(time.RFC3339)
	}
	if row.ATLAt != nil {
		resp.ATLAt = row.ATLAt.UTC().Format(time.RFC3339)
	}

	writeJSON(w, resp, Flags{})
}
