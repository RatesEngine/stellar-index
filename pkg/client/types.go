package client

import "time"

// Envelope is the shape of every 2xx JSON response from the server.
// Mirrors `internal/api/v1.Envelope` but parameterised on the data
// type for type safety in client code.
type Envelope[T any] struct {
	Data       T          `json:"data"`
	AsOf       time.Time  `json:"as_of"`
	Sources    []string   `json:"sources,omitempty"`
	Flags      Flags      `json:"flags"`
	Pagination Pagination `json:"pagination,omitempty"`
}

// Flags are the advisory quality markers per the server's
// envelope.go. New flags may be added in minor server releases —
// the JSON decoder ignores unknown fields, so adding a flag is
// non-breaking for SDK consumers.
type Flags struct {
	Stale             bool `json:"stale"`
	ReducedRedundancy bool `json:"reduced_redundancy"`
	Triangulated      bool `json:"triangulated"`
	DivergenceWarning bool `json:"divergence_warning"`
	Frozen            bool `json:"frozen,omitempty"`
	SingleSource      bool `json:"single_source,omitempty"`
}

// Pagination is present on list-returning endpoints when there are
// more results than the requested limit.
type Pagination struct {
	Next string `json:"next,omitempty"`
}

// PriceSnapshot is the data shape returned by [Client.Price].
type PriceSnapshot struct {
	AssetID       string    `json:"asset_id"`
	Quote         string    `json:"quote"`
	Price         string    `json:"price"`
	PriceType     string    `json:"price_type"`
	ObservedAt    time.Time `json:"observed_at"`
	WindowSeconds int       `json:"window_seconds,omitempty"`
}

// HistorySeries is the data shape returned by
// [Client.HistorySinceInception].
type HistorySeries struct {
	AssetID     string         `json:"asset_id"`
	Quote       string         `json:"quote"`
	Granularity string         `json:"granularity"`
	Points      []HistoryPoint `json:"points"`
}

// HistoryPoint is one row of a [HistorySeries].
type HistoryPoint struct {
	T         time.Time `json:"t"`
	P         string    `json:"p"`
	VolumeUSD *string   `json:"v_usd,omitempty"`
}

// AssetDetail is the data shape returned by [Client.Asset] +
// [Client.Assets].
type AssetDetail struct {
	// Identity
	AssetID    string  `json:"asset_id"`
	Type       string  `json:"type"` // "native" / "classic" / "soroban" / "fiat" / "crypto"
	Code       string  `json:"code,omitempty"`
	Issuer     string  `json:"issuer,omitempty"`
	ContractID string  `json:"contract_id,omitempty"`
	HomeDomain *string `json:"home_domain,omitempty"`
	// Decimals is the asset's smallest-unit-to-display divisor power
	// (always present on the wire). Stellar classic + native = 7;
	// SEP-41 contracts publish their own value via `decimals()`.
	Decimals int `json:"decimals"`

	// Sep1Status is one of "not_applicable" / "not_fetched" /
	// "verified" / "no_match" / "unreachable" — see the API design
	// reference for the full state machine.
	Sep1Status string `json:"sep1_status"`

	// IsExperimental flags assets the operator has marked as
	// pre-production.
	IsExperimental bool `json:"is_experimental,omitempty"`

	// ─── SEP-1 overlay (populated when Sep1Status == "verified") ──

	// Name is the human-readable currency name (e.g. "USD Coin")
	// from the issuer's stellar.toml [[CURRENCIES]] entry.
	Name *string `json:"name,omitempty"`
	// Description is the currency's `desc` field.
	Description *string `json:"description,omitempty"`
	// Image is an absolute URL to the asset logo.
	Image *string `json:"image,omitempty"`
	// OrgName is the issuer organisation's name (DOCUMENTATION.ORG_NAME).
	OrgName *string `json:"org_name,omitempty"`
	// AnchorAsset is the off-chain asset this token anchors to
	// (e.g. "USD"). Empty for non-anchored tokens.
	AnchorAsset *string `json:"anchor_asset,omitempty"`
	// AnchorAssetType classifies the anchor (fiat, crypto, stock, …).
	AnchorAssetType *string `json:"anchor_asset_type,omitempty"`

	// ─── F2 fields (Freighter V2 / ADR-0011 supply derivation) ────

	// CirculatingSupply / TotalSupply / MaxSupply are decimal
	// strings in the asset's smallest integer unit (stroops for
	// XLM / classic; contract-defined for SEP-41). Consumers divide
	// by 10^Decimals for display. Null when no supply snapshot
	// exists for this asset.
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	TotalSupply       *string `json:"total_supply,omitempty"`
	MaxSupply         *string `json:"max_supply,omitempty"`

	// MarketCapUSD = circulating × USD price / 10^Decimals,
	// formatted to two fractional digits. Null when supply or USD
	// price is unavailable.
	MarketCapUSD *string `json:"market_cap_usd,omitempty"`

	// FDVUSD = max_supply × USD price / 10^Decimals. Null when
	// max_supply is null (uncapped issuer + no override + no SEP-1
	// declaration) or when USD price is unavailable.
	FDVUSD *string `json:"fdv_usd,omitempty"`

	// SupplyBasis identifies which ADR-0011 policy produced the
	// supply numbers (e.g. "issuer_exclusion", "admin_exclusion",
	// "override"); null when no snapshot exists.
	SupplyBasis *string `json:"supply_basis,omitempty"`

	// VolumeUSD24h is the trailing-24h USD-denominated trade volume
	// across every pair this asset participates in. "0" is a valid
	// value (asset tracked, no trades in the window); null means
	// the volume reader isn't wired or the lookup failed.
	//
	// Scope caveat (launch-readiness L2.2): off-chain CEX/FX trades
	// always populate this; on-chain DEX trades populate it when
	// the operator has configured `[trades].usd_pegged_classic_assets`
	// in their server config. See the API description for the full
	// caveat.
	VolumeUSD24h *string `json:"volume_24h_usd,omitempty"`

	// Change24hPct is the trailing-24h price change as a signed
	// percentage with two fractional digits (e.g. "+1.27", "-0.05",
	// "0.00"). Null when no current USD price exists for the asset,
	// or when the 24h-ago comparison bucket is unavailable (asset
	// first traded < 24h ago, or pruned by retention). Clients
	// should render "—" on null rather than fabricating "0%".
	Change24hPct *string `json:"change_24h_pct,omitempty"`

	// ─── SEP-1 issuance declarations ─────────────────────────────
	//
	// The issuer's own commitments from their stellar.toml
	// `[[CURRENCIES]]` entry; populated only when Sep1Status ==
	// "verified". Distinct from the F2 fields above which observe
	// live ledger state — these say what the issuer pledged, the F2
	// fields say what's actually on-chain.

	// Conditions is the issuer's free-form terms / conditions text
	// (SEP-1 `conditions`).
	Conditions *string `json:"conditions,omitempty"`

	// FixedNumber is the SEP-1-declared fixed total supply, if the
	// issuer committed to one. Decimal string in the asset's
	// smallest integer unit. Distinct from `TotalSupply` which is
	// the live-ledger sum.
	FixedNumber *string `json:"fixed_number,omitempty"`

	// MaxNumber is the SEP-1-declared maximum supply ceiling, if the
	// issuer set a cap. Decimal string. Distinct from `MaxSupply`
	// which is operator/policy-derived.
	MaxNumber *string `json:"max_number,omitempty"`

	// IsUnlimited signals the issuer asserts unbounded issuance.
	// Null when the issuer didn't address supply at all (no
	// fixed_number / max_number / is_unlimited declaration); false
	// when they did and committed to a bounded supply.
	IsUnlimited *bool `json:"is_unlimited,omitempty"`
}

// AssetMetadata is the data shape returned by [Client.AssetMetadata]
// (the SEP-1 overlay endpoint, /v1/assets/{id}/metadata). Mirrors
// the AssetMetadata schema in openapi/rates-engine.v1.yaml.
//
// All overlay fields populate only when Sep1Status == "verified".
// Other states ("not_applicable" / "not_fetched" / "unreachable" /
// "no_match") leave the overlay fields nil.
type AssetMetadata struct {
	AssetID    string  `json:"asset_id"`
	HomeDomain *string `json:"home_domain,omitempty"`
	Sep1Status string  `json:"sep1_status"`

	// SEP-1 [[CURRENCIES]] overlay — populated only on Sep1Status=="verified".
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	Image           *string `json:"image,omitempty"`
	OrgName         *string `json:"org_name,omitempty"`
	AnchorAsset     *string `json:"anchor_asset,omitempty"`
	AnchorAssetType *string `json:"anchor_asset_type,omitempty"`

	// SEP-1 issuance declarations — issuer-declared, distinct from
	// the F2 fields on AssetDetail which observe live ledger state.
	Conditions  *string `json:"conditions,omitempty"`
	FixedNumber *string `json:"fixed_number,omitempty"`
	MaxNumber   *string `json:"max_number,omitempty"`
	IsUnlimited *bool   `json:"is_unlimited,omitempty"`
}

// Account is the data shape returned by [Client.Me].
type Account struct {
	KeyID           string    `json:"key_id"`
	Label           string    `json:"label,omitempty"`
	Tier            string    `json:"tier"`
	RateLimitPerMin int       `json:"rate_limit_per_min,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// UsageRow is one entry in the array returned by [Client.Usage].
type UsageRow struct {
	Date      string `json:"date"`
	Requests  int    `json:"requests"`
	Errors    int    `json:"errors"`
	Throttled int    `json:"throttled"`
}

// KeyCreated is the data shape returned by [Client.CreateKey].
// The Plaintext field is the only place the new key's secret bytes
// appear; the server returns it once and never again.
type KeyCreated struct {
	KeyID     string `json:"key_id"`
	Plaintext string `json:"plaintext"`
	Label     string `json:"label,omitempty"`
}
