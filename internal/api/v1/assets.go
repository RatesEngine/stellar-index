package v1

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/currency"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// Sep1CachedReader is the narrow dependency /v1/assets/{id} uses to
// apply the SEP-1 overlay. Returns the parsed payload the
// `ratesengine-ops sep1-refresh` cron persisted in `issuers.sep1_payload`.
//
// Pre-2026-05-29 the assets handler resolved SEP-1 live via HTTPS
// (capped at 500ms via [sep1OverlayTimeout]) on every uncached
// request. That call dominated /v1/assets/{id} p95 (4+s on cold
// issuers) and triggered the slo_latency_burn alerts. The handler
// now uses the cron-populated DB column instead; the live-fetch
// path lives only in `cmd/ratesengine-ops/sep1_refresh.go`.
type Sep1CachedReader interface {
	GetIssuerSep1Cached(ctx context.Context, gStrkey string) (*timescale.IssuerSep1Cached, error)
}

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
	// Decimals is the on-chain smallest-unit scale (7 for classic /
	// native stroops; per-contract for SEP-41). It is the divisor for
	// EVERY unit computation — supply display AND market-cap / FDV math
	// (MarketCapUSD = circulating × price / 10^Decimals). It MUST NOT be
	// set from issuer-declared SEP-1 `display_decimals`, which is a
	// wallet rounding hint, not a unit scale (F-1321: doing so inflated
	// market_cap_usd by up to 10^(7-display_decimals)× on verified
	// classic assets and was an issuer-controlled manipulation vector).
	// Surface the display hint via DisplayDecimals instead.
	Decimals int `json:"decimals"`
	// DisplayDecimals is the issuer's SEP-1 `display_decimals` rounding
	// hint (e.g. USDC declares 2) — a UI preference for how many
	// fractional digits to show, NOT a unit scale. Omitted when the
	// SEP-1 overlay didn't declare one. Never feeds amount math.
	DisplayDecimals *int `json:"display_decimals,omitempty"`

	// Class is the cross-chain asset class for catalogue-backed rows:
	// "fiat" | "stablecoin" | "crypto". Omitted for Stellar-classic
	// rows that have no catalogue entry (the long tail of asset codes
	// observed in trades). Populated from `internal/currency`'s
	// verified-currency catalogue via the asset's slug. R-018
	// assets-unification — lets /v1/assets serve as the CMC/CoinGecko-
	// style global listing with class-filtered views (`?asset_class=fiat`).
	Class string `json:"class,omitempty"`
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

	// ─── Coin-equivalence extension (R-018 final) ────────────────
	//
	// Fields lifted from CoinSummary so /v1/assets/{id} is a
	// superset of /v1/coins/{slug}. Lets the explorer migrate every
	// /v1/coins consumer to /v1/assets without losing data. Populated
	// only when a CoinsReader is wired AND the asset has a row in
	// the coins catalogue (skipped for fiat:* and external:* assets).

	// PriceUSD is the latest VWAP/last-trade USD price as a
	// fixed-precision decimal string. Inlined so wallet UIs
	// (Freighter, retail apps) don't pay a second
	// `/v1/price?asset=…&quote=fiat:USD` round-trip on every
	// asset-detail render (F-1271 audit-2026-05-12). Populated by:
	//
	//   1. Coins overlay when a CoinsReader is wired AND the asset
	//      is in the coins catalogue (richer enrichment path —
	//      delegates to CoinSummary.PriceUSD).
	//   2. Direct USD-price lookup in populateMarketCap when the
	//      overlay didn't populate it (covers fiat:* / external:*
	//      / SEP-41 not in the coins catalogue / any asset whose
	//      market_cap path already paid for the price lookup).
	//
	// Null only when no USD price can be derived at all (no
	// on-chain trades, prices_1m has no row, and stablecoin-fiat
	// proxy is disabled).
	PriceUSD *string `json:"price_usd,omitempty"`

	// Change1hPct / Change7dPct round out the trailing-window set
	// alongside Change24hPct. Same shape — signed percentage with
	// two fractional digits, null when no past-bucket snapshot
	// exists within the window's tolerance.
	Change1hPct *string `json:"change_1h_pct,omitempty"`
	Change7dPct *string `json:"change_7d_pct,omitempty"`

	// TopMarkets is a preview of the asset's top markets by 24h USD
	// volume. Up to 5 entries; null when the coin reader doesn't
	// return any.
	TopMarkets []CoinTopMarket `json:"top_markets,omitempty"`

	// PriceHistory24h / PriceHistory7d are sparkline-grade USD-price
	// timeseries (24 hourly + 7 daily samples respectively).
	PriceHistory24h []CoinPricePoint `json:"price_history_24h,omitempty"`
	PriceHistory7d  []CoinPricePoint `json:"price_history_7d,omitempty"`

	// MarketsCount / TradeCount24h are the trailing-24h activity
	// counters mirrored from CoinSummary. Pointers so 0 ("silent
	// 24h") is distinguishable from "not computed" (no reader / lookup
	// error) in alerting.
	MarketsCount  *int64 `json:"markets_count,omitempty"`
	TradeCount24h *int64 `json:"trade_count_24h,omitempty"`

	// ATH is the asset's all-time-high USD price + the day it was
	// set. Sourced from prices_1d, USD-quotes only.
	ATH *CoinATH `json:"ath,omitempty"`

	// IssuerScamReason is non-empty when this asset's issuer appears
	// in the curated scam directory. Clients should render a
	// prominent warning when present.
	IssuerScamReason string `json:"issuer_scam_reason,omitempty"`

	// Slug is the friendly short identifier for the asset (e.g.
	// "USDC" for the canonical Circle USDC, or the issuer-
	// disambiguated form like "USDC-GA5Z…" for collisions). Mirror
	// of CoinSummary.Slug; lets consumers build canonical /assets/
	// URLs without a parallel /v1/coins lookup.
	Slug string `json:"slug,omitempty"`

	// FirstSeenLedger / LastSeenLedger / ObservationCount are the
	// trades-hypertable activity metadata. Mirrored from CoinRow so
	// the explorer's asset-detail page can drop its parallel
	// /v1/coins/{slug} fetch.
	FirstSeenLedger  *uint32 `json:"first_seen_ledger,omitempty"`
	LastSeenLedger   *uint32 `json:"last_seen_ledger,omitempty"`
	ObservationCount *int64  `json:"observation_count,omitempty"`

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

	// UnverifiedWarning points at the verified Stellar-canonical
	// asset when the requested asset uses a verified currency's
	// ticker code but is NOT the verified issuer (R-018 ticker
	// collision). Populated by handleAssetGet via the verified-
	// currency catalogue; nil for the verified asset itself and
	// for any code not claimed by a verified currency on Stellar.
	// Pairs with Flags.UnverifiedTickerCollision for client-side
	// detection.
	UnverifiedWarning *UnverifiedWarning `json:"unverified_warning,omitempty"`
}

// UnverifiedWarning is the body attached to /v1/assets/{id} when
// the requested asset code-collides with a verified Stellar
// currency but the issuer doesn't match. Designed to be lifted
// directly into UI: every field is human-render-ready, and the
// `note` sentence is verbatim-safe to render in a warning banner.
type UnverifiedWarning struct {
	// VerifiedSlug is the canonical slug ("usdc") consumers can
	// redirect to.
	VerifiedSlug string `json:"verified_slug"`
	// VerifiedAssetID is the verified canonical asset_id ("USDC-G…").
	VerifiedAssetID string `json:"verified_asset_id"`
	// VerifiedName is the human-readable name ("USD Coin").
	VerifiedName string `json:"verified_name"`
	// VerifiedIssuer is the verified-issuer attribution
	// ("Circle (centre.io)"). Empty when the catalogue entry
	// didn't include a verified_issuer_label.
	VerifiedIssuer string `json:"verified_issuer,omitempty"`
	// Note is a one-sentence warning rendered verbatim by clients.
	// Composed server-side from the verified currency's metadata
	// so the wording stays consistent.
	Note string `json:"note"`
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

	// asset_class filter. Drives the CMC/CoinGecko-style class chip
	// group on the explorer's /assets page. Recognised values:
	//   - "fiat"        → catalogue fiat rows only (USD, EUR, …).
	//   - "stablecoin"  → catalogue stablecoin rows only (USDC, USDT, …).
	//   - "crypto" / "blockchain"
	//                   → catalogue crypto rows only (BTC, ETH, XLM, …).
	//                   "blockchain" is the spec's surface alias for
	//                   the same set.
	//   - "all" / ""    → catalogue rows (all 3 classes, market-cap
	//                   ordered) THEN classic_assets via volume-desc.
	//                   See handleAssetListUnified.
	assetClass := normaliseAssetClass(r.URL.Query().Get("asset_class"))
	// Network can compose with the class filter — the redesign-spec
	// explorer surfaces "Network: <chain>" as a sub-dropdown when
	// the class chip is Blockchain or Stablecoin. Empty / "all" /
	// "stellar" returns the catalogue subset for that network +
	// class; explicit non-Stellar narrows to entries with a
	// matching networks[] row (USDC on ethereum, XLM on stellar,
	// etc.). Read here so handleAssetListFromCatalogue can apply
	// it; fiat catalogue entries have no networks so the filter is
	// effectively a no-op for them.
	classNetwork := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("network")))
	if assetClass == "fiat" || assetClass == "stablecoin" || assetClass == "crypto" {
		s.handleAssetListFromCatalogue(w, r, assetClass, classNetwork, limit, cursor)
		return
	}

	// Network filter takes precedence over the unified-all path —
	// the explorer fires /assets?network=ethereum (etc.) from the
	// /blockchains/{network} click-through and that page wants
	// catalogue rows projected to that network, not the merged view.
	network := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("network")))
	if network != "" && network != "stellar" {
		s.handleAssetListExternalNetwork(w, r, network, limit, cursor)
		return
	}
	issuer := strings.TrimSpace(r.URL.Query().Get("issuer"))

	// Unified "All" listing — catalogue rows first (market-cap
	// ordered) then classic_assets via the existing volume-desc
	// path. Cursor encodes the phase so the explorer's paginate-
	// forward UX walks both naturally. OPT-IN via asset_class=all
	// so existing SDK consumers integrated against the legacy
	// classic-only listing don't see a wire-shape change. The
	// explorer's /assets page passes asset_class=all explicitly.
	if assetClass == "all" {
		s.handleAssetListUnified(w, r, limit, cursor)
		return
	}

	// Coins-backed listing — when a CoinsReader is wired, source the
	// listing from the same ListCoinsExt path /v1/coins uses. Gives
	// each row the price / volume / change / sparkline / ATH fields
	// (R-018 finish — assets-unification endgame). Falls through to
	// the lean AssetReader path when no CoinsReader is configured.
	if s.coins != nil {
		s.handleAssetListFromCoins(w, r, issuer, cursor, limit)
		return
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

// handleAssetListFromCoins serves /v1/assets when a CoinsReader is
// wired. Sources rows from ListCoinsExt and projects each CoinRow
// into an AssetDetail with the coin-overlay fields populated — same
// shape as /v1/coins listings, just under the /v1/assets URL.
//
// Honors ?issuer= filter (passed through to ListCoinsExt) and the
// default order (observation_count_desc). cursor passes through
// unchanged.
func (s *Server) handleAssetListFromCoins(
	w http.ResponseWriter,
	r *http.Request,
	issuer, cursor string,
	limit int,
) {
	// Overfetch-by-one: request limit+1 so the (limit+1)th row signals
	// a next page. `limit` is validated to [1,500] by the caller, so the
	// store sees at most 501 (F-1326: previously this passed `limit`
	// itself, so len(rows) > limit was never true and /v1/assets never
	// emitted a next cursor — only the first page of ~440K assets was
	// reachable).
	opts := timescale.ListCoinsOptions{
		Limit:  limit + 1,
		Issuer: issuer,
		Cursor: cursor,
	}
	rows, err := s.coins.ListCoinsExt(r.Context(), opts)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ListCoinsExt (assets listing) failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	// Overfetch-by-one for cursor pagination — same shape as
	// handleCoins. The +1th row determines whether there's a next
	// page; it isn't returned to the caller.
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]AssetDetail, 0, len(rows))
	for _, row := range rows {
		out = append(out, assetDetailFromCoinRow(row))
	}
	env := Envelope{Data: out, Flags: Flags{}}
	if hasMore && len(out) > 0 {
		last := rows[len(rows)-1]
		env.Pagination = &Pagination{
			Next: fmt.Sprintf("%d:%s", last.ObservationCount, last.AssetID),
		}
	}
	writeEnvelope(w, env)
}

// assetDetailFromCoinRow projects a storage CoinRow into the
// AssetDetail wire shape. Mirrors the scalar field population in
// applyCoinRowToDetail but for listing rows (so the listing endpoint
// returns the same per-row shape /v1/coins users were getting).
func assetDetailFromCoinRow(row timescale.CoinRow) AssetDetail {
	asset, err := canonical.ParseAsset(row.AssetID)
	d := AssetDetail{
		AssetID:    row.AssetID,
		Code:       row.Code,
		Decimals:   7,
		Sep1Status: "not_applicable",
	}
	if err == nil {
		d.Type = string(asset.Type)
	}
	if row.IssuerGStrkey != "" {
		v := row.IssuerGStrkey
		d.Issuer = &v
	}
	// Slug + activity metadata.
	if row.Slug != "" {
		d.Slug = row.Slug
	}
	if row.FirstSeenLedger != 0 {
		v := row.FirstSeenLedger
		d.FirstSeenLedger = &v
	}
	if row.LastSeenLedger != 0 {
		v := row.LastSeenLedger
		d.LastSeenLedger = &v
	}
	if row.ObservationCount != 0 {
		v := row.ObservationCount
		d.ObservationCount = &v
	}
	// Coin-overlay scalars (price / volume / change percentages).
	if row.PriceUSD != nil {
		d.PriceUSD = row.PriceUSD
	}
	if row.Volume24hUSD != nil {
		d.VolumeUSD24h = row.Volume24hUSD
	}
	if row.MarketCapUSD != nil {
		d.MarketCapUSD = row.MarketCapUSD
	}
	if row.CirculatingSupply != nil {
		d.CirculatingSupply = row.CirculatingSupply
	}
	if row.Change1hPct != nil {
		d.Change1hPct = row.Change1hPct
	}
	if row.Change24hPct != nil {
		d.Change24hPct = row.Change24hPct
	}
	if row.Change7dPct != nil {
		d.Change7dPct = row.Change7dPct
	}
	if reason := scamReason(row.IssuerGStrkey); reason != "" {
		d.IssuerScamReason = reason
	}
	return d
}

// externalNetworks is the allowlist for /v1/assets?network= values
// that route to the verified-currency catalogue. Stellar is handled
// by the indexer reader; everything in this set is sourced from the
// catalogue's networks[] arrays.
var externalNetworks = map[string]struct{}{
	"ethereum":  {},
	"solana":    {},
	"polygon":   {},
	"base":      {},
	"arbitrum":  {},
	"tron":      {},
	"bitcoin":   {},
	"bsc":       {},
	"avalanche": {},
	"xrpl":      {},
}

// handleAssetListExternalNetwork serves /v1/assets?network=<chain>
// for non-Stellar networks. Source is the verified-currency
// catalogue's networks[] arrays; rows are projected into AssetDetail
// with type="external" and asset_id="<network>:<contract>" so
// downstream consumers can treat the response uniformly with the
// Stellar branch.
//
// Pagination is by simple offset for now — the catalogue is small
// (~45 entries) and the per-network filter narrows it further, so
// cursor pagination would be over-engineering.
func (s *Server) handleAssetListExternalNetwork(w http.ResponseWriter, r *http.Request, network string, limit int, cursor string) {
	if _, ok := externalNetworks[network]; !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-network",
			"Unknown network", http.StatusBadRequest,
			"network must be one of: stellar, ethereum, solana, polygon, base, arbitrum, tron, bitcoin, bsc, avalanche, xrpl")
		return
	}
	if s.verifiedCurrencies == nil {
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}
	out := projectCatalogueForNetwork(s.verifiedCurrencies.All(), network)
	// Offset pagination via cursor=<int>. A malformed cursor 400s
	// rather than silently restarting at page 1 (G3-10).
	offset, ok := parseOffsetCursor(w, r, cursor)
	if !ok {
		return
	}
	if offset >= len(out) {
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	page := out[offset:end]
	env := Envelope{Data: page, Flags: Flags{}}
	if end < len(out) {
		next := strconv.Itoa(end)
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

// normaliseAssetClass folds the spec's surface labels into the
// catalogue's internal class identifiers. "blockchain" is the
// explorer's filter chip name for the catalogue's "crypto" class
// (CMC's / CoinGecko's "Cryptocurrencies" tab equivalent). Empty
// + "all" both fall through unchanged; the handler treats them as
// the legacy classic_assets path.
func normaliseAssetClass(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	switch c {
	case "blockchain", "cryptocurrency", "cryptocurrencies":
		return "crypto"
	}
	return c
}

// handleAssetListFromCatalogue serves /v1/assets?asset_class={fiat,
// stablecoin,crypto} from the verified-currency catalogue (45
// rows total today). The catalogue is the source of cross-chain
// identities (USDC the currency, GBP the fiat) — distinct from
// the classic_assets table which carries per-issuer-on-Stellar
// rows (USDC-GA5Z..., USDT-GCQT...).
//
// Returns rows with:
//   - asset_id = slug ("usdc", "us-dollar", "btc") — the route
//     /v1/assets/{slug} dispatches to GlobalAssetView for these.
//   - type = "global"; consumers distinguishing wire shape can
//     check (a) the type field or (b) the absence of issuer.
//   - market_cap_usd populated for fiat via the same fxHistory-
//     backed path /v1/assets/verified uses (R-018 wave 140 fix).
//     Crypto + stablecoin rows lack supply data in the catalogue
//     today; their market_cap stays null until the crypto-supply
//     pipeline lands.
//   - networks[] reflected on the wire as a count + the array
//     itself in the same shape /v1/assets/verified uses.
//   - issuer/contract_id/home_domain/sep1_status absent — those
//     are Stellar-network-specific and belong on the
//     /v1/assets/{slug}/{network} deep-dive route.
//
// Pagination via offset cursor — the result set is bounded at
// ≤45 catalogue rows per class, so a simple offset is sufficient.
func (s *Server) handleAssetListFromCatalogue(w http.ResponseWriter, r *http.Request, class, network string, limit int, cursor string) {
	if s.verifiedCurrencies == nil {
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}
	matched := filterCatalogueByClass(s.verifiedCurrencies.All(), currency.AssetClass(class))
	// Network sub-filter — when set to a specific chain, narrow to
	// catalogue entries with a matching networks[] row. "all" /
	// "" passes through. Fiat catalogue entries have empty
	// networks so a non-empty network filter would drop them all;
	// that's the spec's intent (fiat class hides the Network
	// dropdown in the UI, so this is an API-direct-callers path).
	if network != "" && network != "all" {
		matched = filterCatalogueByNetwork(matched, network)
	}
	caps := s.computeCatalogueMarketCaps(r.Context(), matched, class)
	rows := projectCatalogueRows(matched, caps)
	sortAssetDetailsByMarketCapDesc(rows)
	writeCataloguePage(w, r, rows, limit, cursor)
}

// filterCatalogueByNetwork narrows the input slice to entries
// whose networks[] contains a row matching `network` (case-
// insensitive). Used by the asset_class + network combo on
// /v1/assets to power the explorer's "Blockchain on Stellar" /
// "Stablecoin on Ethereum" / etc. drilldowns.
func filterCatalogueByNetwork(entries []*currency.VerifiedCurrency, network string) []*currency.VerifiedCurrency {
	target := strings.ToLower(network)
	out := make([]*currency.VerifiedCurrency, 0, len(entries))
	for _, vc := range entries {
		for _, n := range vc.Networks {
			if strings.EqualFold(n.Network, target) {
				out = append(out, vc)
				break
			}
		}
	}
	return out
}

// filterCatalogueByClass returns the catalogue entries matching the
// target class in source order. Helper extracted from
// handleAssetListFromCatalogue to keep that handler under the
// gocognit budget.
func filterCatalogueByClass(entries []*currency.VerifiedCurrency, target currency.AssetClass) []*currency.VerifiedCurrency {
	out := make([]*currency.VerifiedCurrency, 0, len(entries))
	for _, vc := range entries {
		if vc.Class == target {
			out = append(out, vc)
		}
	}
	return out
}

// computeCatalogueMarketCaps fans out market_cap_usd computation
// per row, dispatched by class:
//
//   - "fiat":        fiatMarketCapUSD (M2 × FX rate via fxHistory).
//   - "crypto" /
//     "stablecoin":  read from s.marketCaps (CoinGecko-backed cache
//     populated by the marketcap.Refresher).
//
// Returns a parallel-indexed slice of market_cap strings (empty for
// rows where the source returned nothing). Nil-safe on s.marketCaps
// — crypto/stablecoin rows fall back to empty when the cache hasn't
// been wired.
func (s *Server) computeCatalogueMarketCaps(ctx context.Context, matched []*currency.VerifiedCurrency, class string) []string {
	caps := make([]string, len(matched))
	if class == "fiat" {
		var wg sync.WaitGroup
		for i, vc := range matched {
			if vc.CirculatingSupply == "" {
				continue
			}
			i, vc := i, vc
			wg.Add(1)
			go func() {
				defer wg.Done()
				if capStr := s.fiatMarketCapUSD(ctx, vc); capStr != nil {
					caps[i] = *capStr
				}
			}()
		}
		wg.Wait()
		return caps
	}
	// Crypto + stablecoin path. Read from the CG market_cap cache;
	// empty when the slug isn't cached yet.
	if s.marketCaps == nil {
		return caps
	}
	for i, vc := range matched {
		if snap, ok := s.marketCaps.Lookup(vc.Slug); ok && snap.MarketCapUSD != "" {
			caps[i] = snap.MarketCapUSD
		}
	}
	return caps
}

// projectCatalogueRows applies projectCatalogueRow + the parallel
// market_cap slice to produce the wire shape.
func projectCatalogueRows(matched []*currency.VerifiedCurrency, caps []string) []AssetDetail {
	rows := make([]AssetDetail, len(matched))
	for i, vc := range matched {
		rows[i] = projectCatalogueRow(vc)
		if caps[i] != "" {
			c := caps[i]
			rows[i].MarketCapUSD = &c
		}
	}
	return rows
}

// parseOffsetCursor parses an offset-style pagination cursor. The
// cursor is the integer offset emitted as pagination.next by the
// catalogue listing paths. An empty cursor means "start at page 1".
//
// A non-empty, non-integer (or negative) cursor is a client error:
// we 400 it rather than silently restarting at page 1, matching the
// opaque-cursor markets.go pattern (G3-08/G3-10). Silently swallowing
// it made a typo'd cursor look like a successful first page.
//
// Reports ok=false after writing a problem+json on parse failure.
func parseOffsetCursor(w http.ResponseWriter, r *http.Request, cursor string) (int, bool) {
	if cursor == "" {
		return 0, true
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-cursor",
			"Invalid cursor", http.StatusBadRequest,
			"cursor must be the integer pagination.next value from a prior response, or omitted to start at page 1.")
		return 0, false
	}
	return n, true
}

// writeCataloguePage applies offset-cursor pagination + writes the
// envelope. Catalogue paging is small (≤45 rows per class) so offset
// is sufficient.
func writeCataloguePage(w http.ResponseWriter, r *http.Request, rows []AssetDetail, limit int, cursor string) {
	offset, ok := parseOffsetCursor(w, r, cursor)
	if !ok {
		return
	}
	if offset >= len(rows) {
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	env := Envelope{Data: rows[offset:end], Flags: Flags{}}
	if end < len(rows) {
		next := strconv.Itoa(end)
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

// projectCatalogueRow maps a catalogue entry to the listing's
// AssetDetail wire shape. NO issuer / contract_id / home_domain
// / sep1_status — those are Stellar-network-specific and belong
// on /v1/assets/{slug}/{network}.
func projectCatalogueRow(vc *currency.VerifiedCurrency) AssetDetail {
	name := vc.Name
	d := AssetDetail{
		AssetID:    vc.Slug,
		Type:       "global",
		Code:       vc.Ticker,
		Decimals:   vc.SupplyDecimals,
		Sep1Status: "not_applicable",
		Class:      string(vc.Class),
		Slug:       vc.Slug,
		Name:       &name,
	}
	if vc.CirculatingSupply != "" {
		s := vc.CirculatingSupply
		d.CirculatingSupply = &s
	}
	return d
}

// sortAssetDetailsByMarketCapDesc sorts rows in place by
// market_cap_usd descending. Nil market_cap sinks to the bottom
// (preserves the catalogue's source order among equally-unknown
// rows via stable sort). Used by the catalogue-listing path; the
// classic-assets path orders server-side in SQL.
func sortAssetDetailsByMarketCapDesc(rows []AssetDetail) {
	sort.SliceStable(rows, func(i, j int) bool {
		ai := bigFloatFromOptionalString(rows[i].MarketCapUSD)
		aj := bigFloatFromOptionalString(rows[j].MarketCapUSD)
		switch {
		case ai == nil && aj == nil:
			return false
		case ai == nil:
			return false
		case aj == nil:
			return true
		default:
			return ai.Cmp(aj) > 0
		}
	})
}

func bigFloatFromOptionalString(s *string) *big.Float {
	if s == nil || *s == "" {
		return nil
	}
	f, ok := new(big.Float).SetPrec(128).SetString(*s)
	if !ok {
		return nil
	}
	return f
}

// handleAssetListUnified serves /v1/assets when no asset_class
// filter is set and no issuer filter is in play — the CMC/CoinGecko-
// style "All Assets" view (R-018 assets-unification endgame).
//
// Wire shape: catalogue rows (≤45 across all 3 classes) come first,
// ordered by market_cap_usd desc (fiats top the chart at $44T/$21T/
// $18T/…, then crypto + stablecoin catalogue entries which lack
// supply data today and sort below the fiats). Classic_assets pages
// follow, ordered by trailing-24h volume_usd desc (the existing
// CoinsOrderVolume24hUSDDesc path — most-actively-traded Stellar-
// classic rows surface first within their phase).
//
// Cursor protocol — phase-prefixed:
//   - empty cursor             → catalogue phase, offset 0.
//   - "catalogue:<offset>"     → catalogue phase resumed at offset.
//   - "classic:<inner_cursor>" → classic phase via ListCoinsExt; the
//     inner cursor is whatever
//     CoinsOrderVolume24hUSDDesc emits.
//   - "classic:"               → classic phase fresh start (catalogue
//     just exhausted; inner cursor empty).
//
// Page sequence on a default limit=100 (catalogue has ~45 rows):
//
//	page 1 → 45 catalogue rows + 55 classic rows (single call to
//	         this handler that exhausts catalogue then continues
//	         into classic for the remainder).
//	page 2+ → 100 classic rows each via the classic phase.
//
// Phase transitions within a single page are NOT supported in v1
// of this protocol — each page is either fully catalogue or fully
// classic. (Mixed pages would complicate downstream consumers
// expecting a single ordering predicate per page.) When catalogue
// has fewer remaining than `limit`, this handler returns the
// catalogue tail and signals "classic:" next cursor; the client
// fires the next page to fetch classic rows.
func (s *Server) handleAssetListUnified(w http.ResponseWriter, r *http.Request, limit int, cursor string) {
	phase, inner := parseUnifiedCursor(cursor)

	if phase == "catalogue" {
		s.serveCatalogueUnifiedPage(w, r, limit, inner)
		return
	}
	// phase == "classic"
	s.serveClassicUnifiedPage(w, r, limit, inner)
}

// parseUnifiedCursor decodes the phase-prefixed cursor format. An
// empty cursor maps to catalogue phase offset 0.
func parseUnifiedCursor(cursor string) (phase, inner string) {
	if cursor == "" {
		return "catalogue", "0"
	}
	if rest, ok := strings.CutPrefix(cursor, "catalogue:"); ok {
		return "catalogue", rest
	}
	if rest, ok := strings.CutPrefix(cursor, "classic:"); ok {
		return "classic", rest
	}
	// Legacy cursor format (no phase prefix) — treat as classic-phase
	// to preserve backward compatibility with consumers that round-
	// tripped a pre-unified cursor.
	return "classic", cursor
}

// serveCatalogueUnifiedPage projects the catalogue, computes
// market_cap, sorts, slices to the requested offset/limit, and
// writes the envelope with the appropriate next-cursor.
func (s *Server) serveCatalogueUnifiedPage(w http.ResponseWriter, r *http.Request, limit int, innerCursor string) {
	if s.verifiedCurrencies == nil {
		// No catalogue → skip directly to classic phase.
		s.serveClassicUnifiedPage(w, r, limit, "")
		return
	}
	entries := s.verifiedCurrencies.All()
	caps := s.computeAllCatalogueMarketCaps(r.Context(), entries)
	rows := projectCatalogueRows(entries, caps)
	sortAssetDetailsByMarketCapDesc(rows)

	offset := 0
	if innerCursor != "" {
		if n, err := strconv.Atoi(innerCursor); err == nil && n > 0 {
			offset = n
		}
	}
	if offset >= len(rows) {
		// Catalogue done → transition to classic.
		s.serveClassicUnifiedPage(w, r, limit, "")
		return
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	env := Envelope{Data: rows[offset:end], Flags: Flags{}}
	if end < len(rows) {
		env.Pagination = &Pagination{Next: "catalogue:" + strconv.Itoa(end)}
	} else {
		// Catalogue exhausted on this page → next page picks up classic.
		env.Pagination = &Pagination{Next: "classic:"}
	}
	writeEnvelope(w, env)
}

// serveClassicUnifiedPage delegates to the existing CoinsReader
// path with Volume24hUSDDesc ordering. The inner cursor is what
// that path returned on the prior call. Next-cursor gets phase-
// prefixed before going out the wire.
func (s *Server) serveClassicUnifiedPage(w http.ResponseWriter, r *http.Request, limit int, innerCursor string) {
	if s.coins == nil {
		// No coins reader wired → empty terminator.
		writeJSON(w, []AssetDetail{}, Flags{})
		return
	}
	opts := timescale.ListCoinsOptions{
		Limit:  limit,
		Cursor: innerCursor,
		Order:  timescale.CoinsOrderVolume24hUSDDesc,
	}
	// Overfetch-by-one (same shape as handleAssetListFromCoins) to
	// drive the cursor advance.
	opts.Limit = limit + 1
	rows, err := s.coins.ListCoinsExt(r.Context(), opts)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ListCoinsExt (unified) failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]AssetDetail, 0, len(rows))
	for _, row := range rows {
		out = append(out, assetDetailFromCoinRow(row))
	}
	env := Envelope{Data: out, Flags: Flags{}}
	if hasMore && len(out) > 0 {
		last := rows[len(rows)-1]
		// Volume24hUSDDesc cursor shape: <vol_or_blank>:<asset_id>.
		volStr := ""
		if last.Volume24hUSD != nil {
			volStr = *last.Volume24hUSD
		}
		inner := volStr + ":" + last.AssetID
		env.Pagination = &Pagination{Next: "classic:" + inner}
	}
	writeEnvelope(w, env)
}

// computeAllCatalogueMarketCaps fans out market_cap_usd across the
// full catalogue, mixing both sources:
//
//   - Fiat rows  → fiatMarketCapUSD (M2 × FX rate).
//   - Crypto + stablecoin rows → s.marketCaps cache (CG-sourced).
//
// Parallel-indexed to entries; empty at index i means the row has
// no market_cap available from either source. Nil-safe on
// s.marketCaps.
func (s *Server) computeAllCatalogueMarketCaps(ctx context.Context, entries []*currency.VerifiedCurrency) []string {
	caps := make([]string, len(entries))
	var wg sync.WaitGroup
	for i, vc := range entries {
		switch vc.Class {
		case currency.ClassFiat:
			if vc.CirculatingSupply == "" {
				continue
			}
			i, vc := i, vc
			wg.Add(1)
			go func() {
				defer wg.Done()
				if capStr := s.fiatMarketCapUSD(ctx, vc); capStr != nil {
					caps[i] = *capStr
				}
			}()
		case currency.ClassCrypto, currency.ClassStablecoin:
			if s.marketCaps == nil {
				continue
			}
			if snap, ok := s.marketCaps.Lookup(vc.Slug); ok && snap.MarketCapUSD != "" {
				caps[i] = snap.MarketCapUSD
			}
		}
	}
	wg.Wait()
	return caps
}

// projectCatalogueForNetwork walks the catalogue and returns one
// AssetDetail per (verified-currency, network-entry) pair where the
// network matches. Exported to a free function for unit testing.
func projectCatalogueForNetwork(entries []*currency.VerifiedCurrency, network string) []AssetDetail {
	out := make([]AssetDetail, 0, 8)
	for _, vc := range entries {
		for _, n := range vc.Networks {
			if n.Network != network {
				continue
			}
			contract := n.Contract
			code := vc.Ticker
			name := vc.Name
			detail := AssetDetail{
				AssetID:    network + ":" + contract,
				Type:       "external",
				Code:       code,
				Decimals:   0,
				Sep1Status: "not_applicable",
				Name:       &name,
			}
			if contract != "" {
				detail.ContractID = &contract
			}
			out = append(out, detail)
		}
	}
	return out
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
//
// Dispatch order (R-018 Phase 1.4a):
//  1. If the path parameter matches a verified-currency slug
//     (USDC, EURC, AQUA, …), route to the global view at
//     [handleGlobalAsset] — cross-chain identity with per-network
//     entries.
//  2. Otherwise parse as a canonical asset_id (native | CODE-G… |
//     C… | fiat:CODE) and serve the per-Stellar-asset view.
//
// Slug lookup is case-insensitive (via Catalogue.LookupBySlug) and
// runs before canonical-id parsing — slugs never collide with
// canonical-id shapes because canonical ids have anchored prefixes
// (a single bare ticker like "usdc" doesn't parse as any canonical
// shape, so dispatch order is the only deciding factor).
func (s *Server) handleAssetGet(w http.ResponseWriter, r *http.Request) {
	rawID := normaliseAssetIDInput(r.PathValue("asset_id"))

	if s.tryServeGlobalAsset(w, r, rawID) {
		return
	}

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

	// Response-cache check. Drift-safe by construction — the cached
	// entry was produced by this same handler within the last 30s.
	// Covers the full F2 chain (Volume24hUSDForAsset / supply.LatestSupply
	// / 2× lookupUSDPrice / fetchSupplySnapshot / populateMarketCap)
	// plus applySep1Overlay + applyCoinExtensionFields + the verified-
	// currency overlay. Each of those costs ~50-200ms warm; together
	// they dominate the ~700-900ms warm latency observed pre-cache
	// (rc.63 on r1, 2026-05-21).
	cacheKey := parsed.String()
	if entry, ok := s.assetDetailCache.get(cacheKey); ok {
		writeCachedAssetDetail(w, entry)
		return
	}

	detail, served := s.resolveAssetDetail(w, r, parsed)
	if served {
		return
	}

	// Backfill HomeDomain from the curated known-issuers map when
	// the storage row doesn't carry one (classic assets ingested
	// before `ratesengine-ops sep1-refresh` ran, or issuers whose
	// SEP-1 lookup never persisted because they aren't in the
	// watched set). Mirrors enrichIssuer's policy on /v1/issuers,
	// which is how that endpoint returns home_domain="centre.io"
	// for USDC while /v1/assets was reporting null.
	// R-016 in `docs/review-2026-05-10.md`.
	if (detail.HomeDomain == nil || *detail.HomeDomain == "") && detail.Issuer != nil && *detail.Issuer != "" {
		hd, _ := enrichIssuer(*detail.Issuer, "", "")
		if hd != "" {
			detail.HomeDomain = &hd
		}
	}

	// SEP-1 overlay — reads the cached payload `sep1-refresh` cron
	// persisted in `issuers.sep1_payload`. NO live HTTPS fetch.
	if s.sep1Cache != nil {
		s.applySep1Overlay(r.Context(), &detail, parsed)
	} else if detail.HomeDomain != nil && *detail.HomeDomain != "" && detail.Sep1Status == "" {
		detail.Sep1Status = "not_fetched"
	}

	// F2 overlay — supply / market-cap / FDV. Best-effort. The
	// market-cap math reads detail.Decimals, which is the on-chain
	// scale (the SEP-1 overlay surfaces display_decimals separately and
	// no longer touches Decimals — F-1321), so overlay ordering no
	// longer affects the unit math.
	s.applyF2Fields(r.Context(), &detail, parsed)

	// Coin-equivalence overlay (R-018 final) — lifts price / top_markets
	// / history / changes / ATH / scam_reason from the coins catalogue
	// so /v1/assets/{id} is a superset of /v1/coins/{slug}. Skipped
	// for fiat:* (no coin row); a no-op when no coins reader is wired
	// or the asset has no coin row.
	s.applyCoinExtensionFields(r.Context(), &detail, parsed)

	// Verified-currency overlay (R-018 Phase 1.1) — attaches the
	// `unverified_warning` body + flips flags.unverified_ticker_collision
	// when the asset code matches a verified Stellar ticker but the
	// issuer doesn't. No-op when no catalogue is wired or the asset
	// isn't a classic Stellar asset.
	flags := s.verifiedCurrencyFlags(&detail, parsed)

	// Render to bytes once, cache them, write them. The cache check at
	// the top of this function short-circuits subsequent requests for
	// the same asset_id within the TTL window — see [assetDetailResponseCache].
	body, err := renderAssetDetailEnvelope(detail, flags)
	if err != nil {
		// Marshal failure is exceedingly rare (AssetDetail is plain
		// struct tags); fall back to writeJSON so client still gets a
		// (potentially less-optimal) response rather than a 500.
		s.logger.Debug("asset detail envelope render failed; falling back to direct write", "asset_id", cacheKey, "err", err)
		writeJSON(w, detail, flags)
		return
	}
	s.assetDetailCache.put(cacheKey, body)
	writeCachedAssetDetail(w, &assetDetailEntry{body: body, cachedAt: time.Now()})
}

// resolveAssetDetail fetches the AssetDetail for parsed: from the
// reader when wired, else a canonical-echo. Returns served=true
// when a problem response was already written (404 / 500 / client-
// abort), and the caller must bail out without further work.
// Extracted from handleAssetGet to keep that function's gocognit
// complexity under the 20-line ceiling.
func (s *Server) resolveAssetDetail(w http.ResponseWriter, r *http.Request, parsed canonical.Asset) (AssetDetail, bool) {
	reader := s.assetReaderOrNil()
	if reader == nil {
		// No reader wired — echo back a pure-canonical representation.
		// Useful for clients integrating against the wire contract
		// before we have an asset catalogue populated.
		return detailFromAsset(parsed), false
	}
	d, err := reader.GetAsset(r.Context(), parsed)
	if errors.Is(err, ErrAssetNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/asset-not-found",
			"Asset not found", http.StatusNotFound,
			"no trades or oracle observations for "+parsed.String())
		return AssetDetail{}, true
	}
	if err != nil {
		if clientAborted(r, err) {
			return AssetDetail{}, true
		}
		s.logger.Error("GetAsset failed", "err", err, "asset_id", parsed.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return AssetDetail{}, true
	}
	return d, false
}

// tryServeGlobalAsset returns true when `raw` matched a verified-
// currency catalogue entry (by slug OR by ticker) and the global
// view was served. Caller bails out of the canonical-id path on a
// true return.
//
// Both lookups are case-insensitive. Ticker fallback lets
// /v1/assets/USD dispatch to the same view as /v1/assets/us-dollar
// (and /USDC = /usdc, /EUR = /euro, etc.) — useful for clients
// that have an ISO ticker on hand but not the friendly slug.
// Slug match wins over ticker match if both resolve.
func (s *Server) tryServeGlobalAsset(w http.ResponseWriter, r *http.Request, raw string) bool {
	if s.verifiedCurrencies == nil {
		return false
	}
	if vc, ok := s.verifiedCurrencies.LookupBySlug(raw); ok {
		s.handleGlobalAsset(w, r, vc)
		return true
	}
	if vc, ok := s.verifiedCurrencies.LookupByTicker(raw); ok {
		s.handleGlobalAsset(w, r, vc)
		return true
	}
	return false
}

// verifiedCurrencyFlags applies the unverified-collision warning
// to detail and returns the matching envelope flags. Separated
// from handleAssetGet so the latter stays linear-readable.
func (s *Server) verifiedCurrencyFlags(detail *AssetDetail, asset canonical.Asset) Flags {
	flags := Flags{}
	if applyUnverifiedWarning(detail, asset, s.verifiedCurrencies) {
		flags.UnverifiedTickerCollision = true
	}
	return flags
}

// applyUnverifiedWarning stamps detail.UnverifiedWarning when asset
// is a classic Stellar asset whose code matches a verified-currency
// ticker but whose issuer doesn't. Returns true when the warning
// was attached so the caller can set the matching envelope flag.
//
// Lookups against a nil catalogue are safe and return false — the
// handler can call this unconditionally.
func applyUnverifiedWarning(detail *AssetDetail, asset canonical.Asset, cat *currency.Catalogue) bool {
	if cat == nil {
		return false
	}
	if asset.Type != canonical.AssetClassic {
		return false
	}
	verified, collision := cat.StellarCollision(asset.Code, asset.Issuer)
	if !collision {
		return false
	}
	stellar := verified.StellarEntry()
	if stellar == nil {
		// Defensive — StellarCollision only returns true when the
		// catalogue has a Stellar entry for the code, so this branch
		// is unreachable in practice. Bail safely if the invariant
		// ever changes.
		return false
	}
	var note string
	if verified.VerifiedIssuerLabel != "" {
		note = fmt.Sprintf(
			"Exercise caution — this asset uses the ticker %q but is not the verified %s on Stellar. The verified %s on Stellar is issued by %s: %s.",
			verified.Ticker, verified.Ticker, verified.Ticker,
			verified.VerifiedIssuerLabel, stellar.AssetID,
		)
	} else {
		note = fmt.Sprintf(
			"Exercise caution — this asset uses the ticker %q but is not the verified %s on Stellar. The verified %s on Stellar is %s.",
			verified.Ticker, verified.Ticker, verified.Ticker,
			stellar.AssetID,
		)
	}
	detail.UnverifiedWarning = &UnverifiedWarning{
		VerifiedSlug:    verified.Slug,
		VerifiedAssetID: stellar.AssetID,
		VerifiedName:    verified.Name,
		VerifiedIssuer:  verified.VerifiedIssuerLabel,
		Note:            note,
	}
	return true
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

	// Same known-issuers backfill as handleAssetGet (R-016) — keeps
	// the two surfaces in lockstep on the SEP-1 status they report.
	if (detail.HomeDomain == nil || *detail.HomeDomain == "") && detail.Issuer != nil && *detail.Issuer != "" {
		hd, _ := enrichIssuer(*detail.Issuer, "", "")
		if hd != "" {
			detail.HomeDomain = &hd
		}
	}

	if s.sep1Cache != nil {
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

// applySep1Overlay attaches the issuer's cached SEP-1 metadata to
// detail by reading `issuers.sep1_payload` (populated by the
// `sep1-refresh` cron). Sets `sep1_status` to one of: `verified`
// (cached payload matched a [[CURRENCIES]] entry), `no_match`
// (issuer cached but no per-asset currency entry), `not_fetched`
// (no cached payload yet — cron hasn't visited), `not_applicable`
// (asset has no issuer to look up).
//
// Pre-2026-05-29 this method resolved SEP-1 via live HTTPS on every
// uncached request. That call dominated /v1/assets/{id} p95
// (~4s long tail on cold issuers); it now lives only in the
// `sep1-refresh` cron, which the API reads from.
//
//nolint:gocyclo // linear field-overlay sequence; splitting would scatter the per-field nil checks across helpers.
func (s *Server) applySep1Overlay(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	// Soroban + native assets have no issuer row to look up. Mark
	// not_applicable so the response is shaped consistently.
	if asset.Type != canonical.AssetClassic || asset.Issuer == "" {
		detail.Sep1Status = "not_applicable"
		return
	}
	sep, err := s.sep1Cache.GetIssuerSep1Cached(ctx, asset.Issuer)
	if err != nil {
		s.logger.Debug("sep1 cached lookup failed", "asset_id", asset.String(),
			"issuer", asset.Issuer, "err", err)
		detail.Sep1Status = "not_fetched"
		return
	}
	if sep == nil {
		detail.Sep1Status = "not_fetched"
		return
	}

	match := findMatchingCachedCurrency(sep, asset)
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
	if v := strings.TrimSpace(match.Conditions); v != "" {
		detail.Conditions = &v
	}
	if v := strings.TrimSpace(match.FixedNumber); v != "" {
		detail.FixedNumber = &v
	}
	if v := strings.TrimSpace(match.MaxNumber); v != "" {
		detail.MaxNumber = &v
	}
	// IsUnlimited: TOML doesn't distinguish "absent" from "explicitly
	// false" (parser zero-value is false either way). Project the bool
	// only when the issuer addressed supply at all.
	if match.IsUnlimited || match.FixedNumber != "" || match.MaxNumber != "" {
		unlim := match.IsUnlimited
		detail.IsUnlimited = &unlim
	}

	// display_decimals is a UI rounding hint, NOT a unit scale — surface
	// it on its own field and leave detail.Decimals as the on-chain scale
	// (7 for classic) so supply display and market-cap math stay correct
	// (F-1321). The non-standard SEP-1 `decimals` field is treated the
	// same way: informational, never the amount divisor.
	if match.DisplayDecimals > 0 {
		dd := match.DisplayDecimals
		detail.DisplayDecimals = &dd
	} else if match.Decimals > 0 {
		dd := match.Decimals
		detail.DisplayDecimals = &dd
	}
}

// findMatchingCachedCurrency returns the SEP-1 currency entry in a
// cached issuer payload whose (code, issuer) matches the requested
// classic asset, or nil when there is no match. Code comparison is
// case-insensitive; issuer must match exactly. Walks the
// [timescale.IssuerSep1Cached] currencies slice. (The live-fetched
// twin that this once mirrored was removed.)
func findMatchingCachedCurrency(sep *timescale.IssuerSep1Cached, asset canonical.Asset) *timescale.IssuerSep1Currency {
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
		if c.Issuer == "" || c.Issuer != asset.Issuer {
			continue
		}
		return c
	}
	return nil
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
