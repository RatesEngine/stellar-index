package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/metadata"
)

// MetadataResolver is the narrow dependency the assets handler needs
// from [internal/metadata]. Both [*metadata.Resolver] and
// [*metadata.Cache] satisfy it.
type MetadataResolver interface {
	Resolve(ctx context.Context, domain string) (*metadata.SEP1, error)
}

// sep1OverlayTimeout caps how long a single /v1/assets/{id} request
// will wait on a SEP-1 fetch. Above this budget we return the core
// asset detail with sep1_status="unreachable" rather than blocking
// the caller.
const sep1OverlayTimeout = 500 * time.Millisecond

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
	// Sep1Status is the state of the SEP-1 overlay for this asset:
	//   - "not_applicable" — no home-domain (native, fiat, SAC-only).
	//   - "not_fetched"    — has a home-domain but overlay not configured.
	//   - "verified"       — SEP-1 fetched + matching [[CURRENCIES]] entry found.
	//   - "no_match"       — SEP-1 fetched but no matching issuer+code entry.
	//   - "unreachable"    — fetch / parse failed (see server logs).
	Sep1Status string `json:"sep1_status"`

	// ─── SEP-1 overlay fields (populated when Sep1Status=="verified") ─

	// Name is the currency's human-readable name from [[CURRENCIES]]
	// (e.g. "USD Coin").
	Name *string `json:"name,omitempty"`
	// Description is the currency's `desc` field (short blurb).
	Description *string `json:"description,omitempty"`
	// Image is an absolute URL to the asset logo (from `image`).
	Image *string `json:"image,omitempty"`
	// OrgName is the issuer organisation's name
	// (DOCUMENTATION.ORG_NAME in stellar.toml).
	OrgName *string `json:"org_name,omitempty"`
	// AnchorAsset is the off-chain asset this token anchors to (e.g.
	// "USD"). Empty for non-anchored tokens.
	AnchorAsset *string `json:"anchor_asset,omitempty"`
	// AnchorAssetType classifies the anchor (fiat, crypto, stock, …).
	AnchorAssetType *string `json:"anchor_asset_type,omitempty"`
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
		if clientAborted(r, err) {
			return
		}
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
			if clientAborted(r, err) {
				return
			}
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

	// SEP-1 overlay — only for assets with a home-domain and only if
	// the operator has wired a metadata resolver. Budgeted with a
	// short timeout so a slow issuer domain doesn't stall the API.
	if s.meta != nil && detail.HomeDomain != nil && *detail.HomeDomain != "" {
		s.applySep1Overlay(r.Context(), &detail, parsed)
	} else if detail.HomeDomain != nil && *detail.HomeDomain != "" && detail.Sep1Status == "" {
		detail.Sep1Status = "not_fetched"
	}

	writeJSON(w, detail, Flags{})
}

// applySep1Overlay resolves the issuer's stellar.toml and attaches
// the matching [[CURRENCIES]] entry's fields to detail. On any
// failure it sets sep1_status="unreachable" and leaves the core
// fields untouched.
func (s *Server) applySep1Overlay(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	ctx, cancel := context.WithTimeout(ctx, sep1OverlayTimeout)
	defer cancel()

	sep, err := s.meta.Resolve(ctx, *detail.HomeDomain)
	if err != nil {
		s.logger.Debug("sep1 overlay failed", "asset_id", asset.String(),
			"home_domain", *detail.HomeDomain, "err", err)
		detail.Sep1Status = "unreachable"
		return
	}

	// Find matching currency: classic asset matches on (code, issuer);
	// Soroban asset matches on (code) alone since SEP-1 doesn't
	// currently specify contract_id per-currency.
	match := findMatchingCurrency(sep, asset)
	if match == nil {
		detail.Sep1Status = "no_match"
		if name := strings.TrimSpace(sep.OrgName); name != "" {
			detail.OrgName = &name
		}
		return
	}

	detail.Sep1Status = "verified"
	if name := strings.TrimSpace(sep.OrgName); name != "" {
		detail.OrgName = &name
	}
	if v := strings.TrimSpace(match.Name); v != "" {
		detail.Name = &v
	}
	if v := strings.TrimSpace(match.Description); v != "" {
		detail.Description = &v
	}
	if v := strings.TrimSpace(match.Image); v != "" {
		detail.Image = &v
	}
	if v := strings.TrimSpace(match.AnchorAsset); v != "" {
		detail.AnchorAsset = &v
	}
	if v := strings.TrimSpace(match.AnchorAssetType); v != "" {
		detail.AnchorAssetType = &v
	}
	// Prefer issuer-declared display_decimals over our canonical
	// default (7) — it's the value Freighter + wallets will surface
	// to users. Fall back to decimals if display_decimals is zero.
	if match.DisplayDecimals > 0 {
		detail.Decimals = match.DisplayDecimals
	} else if match.Decimals > 0 {
		detail.Decimals = match.Decimals
	}
}

// findMatchingCurrency finds the [[CURRENCIES]] entry that matches
// asset. Returns nil if no entry matches.
//
// Matching is strict — we refuse to guess. Specifically:
//
//   - Classic assets match on (code, issuer) exactly. SEP-1 entries
//     with empty issuers are malformed and skipped.
//   - Soroban assets can't be matched today: our Currency struct
//     doesn't carry contract_id (SEP-1 added it in a later revision
//     we haven't caught up to). Return nil so the caller surfaces
//     sep1_status="no_match" rather than attaching random metadata
//     from the first entry in the TOML.
//   - Fiat and native assets never have a home-domain to overlay
//     from; callers shouldn't be calling this for them. Return nil
//     defensively.
func findMatchingCurrency(sep *metadata.SEP1, asset canonical.Asset) *metadata.Currency {
	// Only classic assets have enough identity (code + issuer) to
	// match a SEP-1 currency entry safely. Everything else — Soroban,
	// native, fiat — can't be matched without contract_id support.
	if asset.Type != canonical.AssetClassic {
		return nil
	}
	if asset.Code == "" || asset.Issuer == "" {
		return nil
	}
	for i := range sep.Currencies {
		c := &sep.Currencies[i]
		if !strings.EqualFold(c.Code, asset.Code) {
			continue
		}
		// SEP-1 entry MUST have a non-empty issuer that matches —
		// otherwise we can't confidently attribute metadata.
		if c.Issuer == "" || c.Issuer != asset.Issuer {
			continue
		}
		return c
	}
	return nil
}
