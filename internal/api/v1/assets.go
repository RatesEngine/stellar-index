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

	// ─── F2 fields (ADR-0011 supply derivation) ─────────────────

	// CirculatingSupply / TotalSupply / MaxSupply are decimal
	// strings in the asset's smallest integer unit (stroops for
	// XLM / classic; contract-defined for SEP-41). Consumers divide
	// by 10^decimals for display. Null when no supply snapshot
	// exists for this asset (untracked, or supply orchestrator
	// hasn't run yet).
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	TotalSupply       *string `json:"total_supply,omitempty"`
	MaxSupply         *string `json:"max_supply,omitempty"`

	// MarketCapUSD = circulating × USD price / 10^decimals,
	// formatted to two fractional digits. Null when supply or USD
	// price is unavailable.
	MarketCapUSD *string `json:"market_cap_usd,omitempty"`

	// FDVUSD = max_supply × USD price / 10^decimals. Null when
	// max_supply is null (uncapped issuer + no override + no SEP-1
	// declaration) or when USD price is unavailable.
	FDVUSD *string `json:"fdv_usd,omitempty"`

	// SupplyBasis identifies which ADR-0011 policy produced the
	// supply numbers; null when no snapshot exists. Lets consumers
	// decide how much to trust the absolute value (e.g. `override`
	// indicates an operator curated the locked-set or SEP-1
	// declared a max_supply).
	SupplyBasis *string `json:"supply_basis,omitempty"`

	// VolumeUSD24h is the trailing-24h USD-denominated trade
	// volume across every pair this asset participates in (as base
	// OR quote). Sourced from the prices_1m CAGG. Per Freighter V2
	// scope ("24h Trading Volume aggregate across indexed
	// markets").
	//
	// String-typed for the same reason as the supply / market-cap
	// fields: NUMERIC sums don't fit a fixed-width Go type cleanly.
	// "0" is a valid value (asset tracked, no trades in 24h);
	// null means "volume reader not wired" or "lookup failed" —
	// callers presenting the field should distinguish these.
	VolumeUSD24h *string `json:"volume_24h_usd,omitempty"`

	// Change24hPct is the trailing-24h price change as a signed
	// percentage with two fractional digits (e.g. "+1.27", "-0.05",
	// "0.00"). Computed as `(now_usd - then_usd) / then_usd * 100`
	// where `then_usd` anchors to the latest closed prices_1m bucket
	// whose end is at or before now-24h.
	//
	// Null when (a) no current USD price exists for the asset (no
	// indexed pair, or pair is fiat:USD itself), or (b) no closed
	// bucket exists in the 24h-ago window (asset first traded < 24h
	// ago, or trade history was retention-pruned). Surfacing the
	// distinction is intentional — clients render "—" rather than
	// fabricating "0%".
	Change24hPct *string `json:"change_24h_pct,omitempty"`

	// ─── SEP-1 issuance declarations ───────────────────────────
	//
	// Drawn directly from the issuer's stellar.toml [[CURRENCIES]]
	// entry; populated only when Sep1Status == "verified". These
	// are issuer-declared, not derived from the ledger — distinct
	// from the F2 fields above which observe live ledger state.

	// Conditions is the issuer's declared terms / conditions for
	// the asset (SEP-1 `conditions`). Free-form text.
	Conditions *string `json:"conditions,omitempty"`

	// FixedNumber is the SEP-1-declared fixed total supply, if the
	// issuer has committed to one. Decimal string (NUMERIC-safe per
	// ADR-0003); the asset's smallest integer unit. Distinct from
	// `total_supply` above: that's the live-ledger sum, this is
	// what the issuer publicly committed to.
	FixedNumber *string `json:"fixed_number,omitempty"`

	// MaxNumber is the SEP-1-declared maximum supply, if the
	// issuer has set a cap. Decimal string. Distinct from
	// `max_supply` above: that's the operator/policy-derived cap
	// (XLM hard cap, operator override, future-PR overlay), this
	// is the issuer's own self-declared ceiling.
	MaxNumber *string `json:"max_number,omitempty"`

	// IsUnlimited is the issuer's SEP-1 declaration that they reserve
	// the right to issue an unbounded amount. When true, FixedNumber
	// and MaxNumber are typically both empty. Pointer so the
	// "issuer didn't say" case is distinguishable from "issuer said
	// false".
	IsUnlimited *bool `json:"is_unlimited,omitempty"`
}

// detailFromAsset populates an AssetDetail from the canonical shape.
// Nullable fields are nil-pointered when empty so JSON omits them
// cleanly.
//
// This is the SCAFFOLDING path used when no AssetReader is wired
// (tests, the dev binary before the storage layer is up). The
// production cmd/ratesengine-api path uses its own assetToDetail
// that consults the operator's curated home-domain map; this stub
// version doesn't have access to that map, so HomeDomain stays nil
// and the overlay handler stamps sep1_status="not_applicable" via
// the existing default.
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
//
// Filter params reserved in the OpenAPI spec — `type=classic,soroban`,
// `code=USDC`, `issuer=G…` — are accepted by the parser without
// rejection but **the handler does not apply them**: every request
// returns the unfiltered paginated catalogue. Operators who need
// filtering today should walk the cursor and filter client-side.
// Tracked under the day-1 contract in `docs/reference/api-design.md
// §5.0 Resource catalogue` so SDK consumers don't expect filtering.
//
// Returns an empty list when no AssetReader is wired (operator did
// not configure the asset-catalog reader). The Envelope shape is
// otherwise correct so clients can integrate against the wire
// contract regardless of whether the catalogue is populated.
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
	// Defensive: a reader that returns a nil slice on empty would
	// marshal as "data": null, which violates the OpenAPI
	// required-array contract. The production adapter already
	// returns non-nil empty, but we don't want correctness to depend
	// on that for every future reader.
	if rows == nil {
		rows = []AssetDetail{}
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

// normaliseAssetIDInput rescues the most common case-typo on
// /v1/assets/{asset_id} — uppercase "NATIVE". The canonical
// asset_id format demands lowercase `native` (per ADR-0010 +
// asset_fiat_test.go's "case-significance is intentional" pin),
// so callers passing `NATIVE` got 400 invalid-asset-id pre-fix.
//
// Scope is deliberately narrow: only the bare-`native` string
// (case-insensitive) collapses. Other compound forms
// (`USDC-Gxxxx`, `CDLZF…`, `fiat:USD`) preserve case-significance
// because:
//   - Stellar protocol allows issuers to mint case-different
//     classic codes; uppercasing `usdc-Gxxxx` would silently
//     merge two distinct assets.
//   - Soroban contract IDs (C-strkeys) are uppercase-only by
//     SEP-23 — the parser already accepts uppercase, no rescue
//     needed.
//   - `fiat:USD` is case-significant per asset_fiat.go (NewFiatAsset
//     rejects lowercase ISO codes).
func normaliseAssetIDInput(raw string) string {
	if strings.EqualFold(raw, "native") {
		return "native"
	}
	return raw
}

// handleAssetGet serves GET /v1/assets/{asset_id}.
func (s *Server) handleAssetGet(w http.ResponseWriter, r *http.Request) {
	rawID := normaliseAssetIDInput(r.PathValue("asset_id"))

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

	// F2 overlay — supply / market-cap / FDV. Best-effort; runs
	// AFTER the SEP-1 overlay because applySep1Overlay may set
	// detail.Decimals from the issuer's display_decimals
	// declaration, which the market-cap math reads.
	s.applyF2Fields(r.Context(), &detail, parsed)

	writeJSON(w, detail, Flags{})
}

// AssetMetadata is the wire shape of /v1/assets/{id}/metadata —
// the SEP-1 overlay slice of AssetDetail without the core
// canonical fields. Distinct endpoint for clients (Freighter,
// wallets) that just want the metadata and not the per-asset
// canonical info they already have. Reuses the same pointer-
// elision-on-empty pattern as AssetDetail.
type AssetMetadata struct {
	AssetID         string  `json:"asset_id"`
	HomeDomain      *string `json:"home_domain,omitempty"`
	Sep1Status      string  `json:"sep1_status"`
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	Image           *string `json:"image,omitempty"`
	OrgName         *string `json:"org_name,omitempty"`
	AnchorAsset     *string `json:"anchor_asset,omitempty"`
	AnchorAssetType *string `json:"anchor_asset_type,omitempty"`
	// SEP-1 issuance declarations — projected from the same overlay
	// as AssetDetail. Distinct from any live-ledger F2 numbers; this
	// surface is metadata-only.
	Conditions  *string `json:"conditions,omitempty"`
	FixedNumber *string `json:"fixed_number,omitempty"`
	MaxNumber   *string `json:"max_number,omitempty"`
	IsUnlimited *bool   `json:"is_unlimited,omitempty"`
}

// handleAssetMetadata serves GET /v1/assets/{asset_id}/metadata.
// Same overlay-resolution path as handleAssetGet but returns ONLY
// the SEP-1 fields. Useful for clients that need refresh of the
// metadata slice without re-fetching the canonical asset core.
//
// Returns 404 when the asset isn't indexed; 200 with sep1_status =
// "not_applicable" / "not_fetched" / "unreachable" / "no_match" /
// "verified" otherwise. Never 503 (the resolver fail mode is
// reflected in sep1_status, not HTTP status — same behaviour as
// /v1/assets/{id}).
func (s *Server) handleAssetMetadata(w http.ResponseWriter, r *http.Request) {
	rawID := normaliseAssetIDInput(r.PathValue("asset_id"))

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
			s.logger.Error("GetAsset failed (metadata)", "err", err, "asset_id", parsed.String())
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/internal",
				"Internal error", http.StatusInternalServerError, "")
			return
		}
		detail = d
	} else {
		detail = detailFromAsset(parsed)
	}

	if s.meta != nil && detail.HomeDomain != nil && *detail.HomeDomain != "" {
		s.applySep1Overlay(r.Context(), &detail, parsed)
	} else if detail.HomeDomain != nil && *detail.HomeDomain != "" && detail.Sep1Status == "" {
		detail.Sep1Status = "not_fetched"
	}

	out := AssetMetadata{
		AssetID:         detail.AssetID,
		HomeDomain:      detail.HomeDomain,
		Sep1Status:      detail.Sep1Status,
		Name:            detail.Name,
		Description:     detail.Description,
		Image:           detail.Image,
		OrgName:         detail.OrgName,
		AnchorAsset:     detail.AnchorAsset,
		AnchorAssetType: detail.AnchorAssetType,
		Conditions:      detail.Conditions,
		FixedNumber:     detail.FixedNumber,
		MaxNumber:       detail.MaxNumber,
		IsUnlimited:     detail.IsUnlimited,
	}
	writeJSON(w, out, Flags{})
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
	if v := strings.TrimSpace(match.Image); isSafeImageURL(v) {
		detail.Image = &v
	}
	if v := strings.TrimSpace(match.AnchorAsset); v != "" {
		detail.AnchorAsset = &v
	}
	if v := strings.TrimSpace(match.AnchorAssetType); v != "" {
		detail.AnchorAssetType = &v
	}
	// SEP-1 issuance declarations (issuer's own stated commitments,
	// distinct from live-ledger / operator-policy F2 fields above).
	if v := strings.TrimSpace(match.Conditions); v != "" {
		detail.Conditions = &v
	}
	if v := strings.TrimSpace(match.FixedNumber); v != "" {
		detail.FixedNumber = &v
	}
	if v := strings.TrimSpace(match.MaxNumber); v != "" {
		detail.MaxNumber = &v
	}
	// IsUnlimited: TOML doesn't distinguish "absent" from
	// "explicitly false" (parser zero-value is false either way),
	// so stamping `false` whenever the issuer omitted the field
	// would over-claim. Project the bool only when the issuer
	// addressed supply at all — i.e. set it to true OR declared
	// at least one of fixed_number / max_number alongside.
	if match.IsUnlimited || match.FixedNumber != "" || match.MaxNumber != "" {
		unlim := match.IsUnlimited
		detail.IsUnlimited = &unlim
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

// isSafeImageURL reports whether s is a plausible http(s) image
// URL. Refuses the tricky schemes (javascript:, data:, file:,
// blob:) that an issuer could embed in a hostile stellar.toml's
// [[CURRENCIES]] image field. An API consumer rendering
// `<img src={data.image}>` without further validation must be
// safe on today's browsers; this makes it so.
//
// We don't try to validate the host or fetch the image — that
// would leak outbound requests. Scheme-only check is enough to
// rule out script-execution vectors.
func isSafeImageURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
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
