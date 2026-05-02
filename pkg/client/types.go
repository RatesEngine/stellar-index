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
	AssetID        string  `json:"asset_id"`
	Type           string  `json:"type"` // "native" / "classic" / "soroban" / "fiat" / "crypto"
	Code           string  `json:"code,omitempty"`
	Issuer         string  `json:"issuer,omitempty"`
	ContractID     string  `json:"contract_id,omitempty"`
	HomeDomain     *string `json:"home_domain,omitempty"`
	IsExperimental bool    `json:"is_experimental,omitempty"`
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
