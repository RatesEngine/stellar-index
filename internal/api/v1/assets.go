package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// AssetReader is the storage-side interface for asset reads.
// Implementations:
//   - *timescale.Store (queries trades hypertable's distinct assets).
//   - in-memory stubs for tests.
type AssetReader interface {
	// GetAsset returns the canonical representation for the given
	// asset-id. Returns ErrAssetNotFound when the asset isn't yet
	// indexed (i.e. no trades observed for it).
	GetAsset(ctx context.Context, id canonical.Asset) (AssetDetail, error)

	// ListAssets returns a page of indexed assets. cursor = ""
	// starts at the beginning; limit clamped to [1, 500].
	ListAssets(ctx context.Context, cursor string, limit int) ([]AssetDetail, string, error)
}

// ErrAssetNotFound is what AssetReader.GetAsset returns for an
// unknown asset. Handlers translate it to HTTP 404 + problem+json.
var ErrAssetNotFound = errors.New("api: asset not found")

// AssetDetail is the payload for /v1/assets responses. Matches the
// shape in docs/reference/api-design.md §5.2.
type AssetDetail struct {
	AssetID    string  `json:"asset_id"`
	Type       string  `json:"type"`
	Code       string  `json:"code,omitempty"`
	Issuer     *string `json:"issuer,omitempty"`
	ContractID *string `json:"contract_id,omitempty"`
	HomeDomain *string `json:"home_domain,omitempty"`
	Decimals   int     `json:"decimals"`
	Sep1Status string  `json:"sep1_status"`
}

// detailFromAsset populates an AssetDetail from the canonical shape.
// Nullable fields are nil-pointered when empty so JSON omits them
// cleanly.
func detailFromAsset(a canonical.Asset) AssetDetail {
	d := AssetDetail{
		AssetID:  a.String(),
		Type:     string(a.Type),
		Code:     a.Code,
		Decimals: 7, // default for classic + native; SAC metadata
		// overlay in a follow-up PR.
		Sep1Status: "not_applicable",
	}
	if a.Issuer != "" {
		v := a.Issuer
		d.Issuer = &v
	}
	if a.ContractID != "" {
		v := a.ContractID
		d.ContractID = &v
	}
	return d
}

// ─── Asset reader on the Server ──────────────────────────────────

// assets is the AssetReader registered at server construction.
// May be nil during the /v1/assets scaffolding phase — handlers
// degrade gracefully to "feature unavailable" 503 when unset.
func (s *Server) assetReaderOrNil() AssetReader { return s.assets }

// ─── Handlers ─────────────────────────────────────────────────────

// handleAssetList serves GET /v1/assets.
//
// Query params:
//   - cursor (optional): opaque, from a prior response's pagination.next.
//   - limit  (optional): integer 1-500, default 100.
//   - type   (optional): "classic" | "soroban" | "fiat" | "any" (default any).
//
// This first implementation returns an empty list if no AssetReader
// is wired (ratesengine-api passes nil until the asset-catalog
// migration lands). The Envelope shape is otherwise correct + the
// endpoint is callable so clients can integrate against the wire
// contract now.
func (s *Server) handleAssetList(w http.ResponseWriter, r *http.Request) {
	// Parse + validate query params FIRST — bad input is 400
	// regardless of whether the backing reader is wired.
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 500]")
			return
		}
		limit = parsed
	}

	reader := s.assetReaderOrNil()
	if reader == nil {
		// Feature not wired yet — empty list is consistent with
		// the contract and doesn't force a 503.
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}

	rows, next, err := reader.ListAssets(r.Context(), cursor, limit)
	if err != nil {
		s.logger.Error("ListAssets failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError,
			"")
		return
	}

	env := Envelope{
		Data:  rows,
		Flags: Flags{},
	}
	if next != "" {
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

// handleAssetGet serves GET /v1/assets/{asset_id}.
func (s *Server) handleAssetGet(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("asset_id")

	parsed, err := canonical.ParseAsset(rawID)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest,
			"asset_id must match: native | <code>-<G-issuer> | <C-contract> | fiat:<CODE>")
		return
	}
	if err := parsed.Validate(); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	reader := s.assetReaderOrNil()
	var detail AssetDetail
	if reader != nil {
		d, err := reader.GetAsset(r.Context(), parsed)
		if errors.Is(err, ErrAssetNotFound) {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/asset-not-found",
				"Asset not found", http.StatusNotFound,
				"no trades or oracle observations for "+parsed.String())
			return
		}
		if err != nil {
			s.logger.Error("GetAsset failed", "err", err, "asset_id", parsed.String())
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/internal",
				"Internal error", http.StatusInternalServerError, "")
			return
		}
		detail = d
	} else {
		// No reader wired — echo back a pure-canonical representation.
		// Useful for clients integrating against the wire contract
		// before we have an asset catalogue populated.
		detail = detailFromAsset(parsed)
	}

	writeJSON(w, detail, Flags{})
}
