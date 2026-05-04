package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// IssuersReader is the seam the issuer handlers read through.
// timescale.Store satisfies it via GetIssuer + ListIssuerAssets +
// ListIssuers.
type IssuersReader interface {
	GetIssuer(ctx context.Context, gStrkey string) (timescale.IssuerRow, error)
	ListIssuerAssets(ctx context.Context, gStrkey string) ([]timescale.IssuerAsset, error)
	ListIssuers(ctx context.Context, limit int) ([]timescale.IssuerSummary, error)
}

// IssuerListEntry is the wire shape of one row in /v1/issuers.
// Compact summary suitable for the issuer-directory page.
type IssuerListEntry struct {
	GStrkey               string `json:"g_strkey"`
	HomeDomain            string `json:"home_domain,omitempty"`
	AssetCount            int64  `json:"asset_count"`
	TotalObservationCount int64  `json:"total_observation_count"`
}

// Issuer is the wire shape returned by /v1/issuers/{g_strkey}.
type Issuer struct {
	GStrkey        string          `json:"g_strkey"`
	HomeDomain     string          `json:"home_domain,omitempty"`
	AuthRequired   *bool           `json:"auth_required,omitempty"`
	AuthRevocable  *bool           `json:"auth_revocable,omitempty"`
	AuthImmutable  *bool           `json:"auth_immutable,omitempty"`
	AuthClawback   *bool           `json:"auth_clawback,omitempty"`
	SEP1ResolvedAt *string         `json:"sep1_resolved_at,omitempty"`
	SEP1Payload    json.RawMessage `json:"sep1_payload,omitempty"`
	CreationLedger *uint32         `json:"creation_ledger,omitempty"`
	Assets         []IssuedAsset   `json:"assets,omitempty"`
}

// IssuedAsset is one entry in the issuer's `assets` list.
type IssuedAsset struct {
	AssetID          string `json:"asset_id"`
	Code             string `json:"code"`
	Slug             string `json:"slug"`
	FirstSeenLedger  uint32 `json:"first_seen_ledger"`
	LastSeenLedger   uint32 `json:"last_seen_ledger"`
	ObservationCount int64  `json:"observation_count"`
}

// handleIssuersList serves GET /v1/issuers.
//
// Returns the issuer directory ordered by total observation count
// across the issuer's classic assets — the proxy-for-activity
// ranking the showcase /issuers page exposes. Returns 503 when
// no IssuersReader is wired and 400 on out-of-range limit.
func (s *Server) handleIssuersList(w http.ResponseWriter, r *http.Request) {
	if s.issuers == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/issuers-unavailable",
			"Issuers unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the issuer reader yet.")
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
	rows, err := s.issuers.ListIssuers(r.Context(), limit)
	if err != nil {
		s.logger.Warn("issuers list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/issuers-error",
			"Issuers list failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}
	out := make([]IssuerListEntry, len(rows))
	for i, r := range rows {
		out[i] = IssuerListEntry{
			GStrkey:               r.GStrkey,
			HomeDomain:            r.HomeDomain,
			AssetCount:            r.AssetCount,
			TotalObservationCount: r.TotalObservationCount,
		}
	}
	writeJSON(w, out, Flags{})
}

// handleIssuer serves GET /v1/issuers/{g_strkey}.
//
// Returns 404 (problem+json) when the issuer has never been observed.
// Always includes the assets array so the showcase issuer card has
// the per-issuer drill-down data without a second request.
func (s *Server) handleIssuer(w http.ResponseWriter, r *http.Request) {
	if s.issuers == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/issuers-unavailable",
			"Issuers unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the issuer reader yet.")
		return
	}

	gStrkey := r.PathValue("g_strkey")
	if gStrkey == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-g-strkey",
			"Invalid G-strkey", http.StatusBadRequest,
			"g_strkey path segment is required")
		return
	}

	row, err := s.issuers.GetIssuer(r.Context(), gStrkey)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/issuer-not-found",
			"Issuer not found", http.StatusNotFound,
			"This G-strkey hasn't been observed as an issuer.")
		return
	}
	if err != nil {
		s.logger.Warn("issuer read", "g_strkey", gStrkey, "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/issuer-error",
			"Issuer read failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	assets, err := s.issuers.ListIssuerAssets(r.Context(), gStrkey)
	if err != nil {
		// Soft-fail on the asset list — the issuer card still
		// renders without it.
		s.logger.Warn("issuer assets", "g_strkey", gStrkey, "err", err)
		assets = nil
	}

	out := Issuer{
		GStrkey:        row.GStrkey,
		HomeDomain:     row.HomeDomain,
		AuthRequired:   row.AuthRequired,
		AuthRevocable:  row.AuthRevocable,
		AuthImmutable:  row.AuthImmutable,
		AuthClawback:   row.AuthClawback,
		SEP1ResolvedAt: row.SEP1ResolvedAt,
		SEP1Payload:    row.SEP1Payload,
		CreationLedger: row.CreationLedger,
	}
	for _, a := range assets {
		out.Assets = append(out.Assets, IssuedAsset{
			AssetID:          a.AssetID,
			Code:             a.Code,
			Slug:             a.Slug,
			FirstSeenLedger:  a.FirstSeenLedger,
			LastSeenLedger:   a.LastSeenLedger,
			ObservationCount: a.ObservationCount,
		})
	}
	writeJSON(w, out, Flags{})
}
