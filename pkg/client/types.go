package client

import (
	"encoding/json"
	"time"
)

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

	// PriceUSD is the current per-asset USD price as a fixed-
	// precision decimal string — same value the dedicated
	// `/v1/price?asset=…&quote=fiat:USD` endpoint returns. Inlined
	// here so wallet UIs (Freighter, retail apps) don't pay a
	// second round-trip on every asset-detail render. Null when
	// no USD price can be derived (no on-chain trades, prices_1m
	// has no row, or operator hasn't enabled stablecoin-fiat
	// proxy). F-1271 (audit-2026-05-12).
	PriceUSD *string `json:"price_usd,omitempty"`

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
	// Coverage chain (launch-readiness L2.2):
	//   - Phase 1 — off-chain CEX/FX trades populate this directly
	//     (uniform 10^8 scale).
	//   - Phase 1 — on-chain DEX trades populate it when the quote
	//     asset is on the operator's `[trades].usd_pegged_classic_assets`
	//     list (trusted 1:1 peg).
	//   - Phase 2 (F-1268) — on-chain DEX trades whose quote is NOT
	//     on the peg list still populate this when the resolver
	//     finds a recent `<quote>/<USD-peg>` VWAP in prices_1m at
	//     the trade's timestamp. Wired when `[trades].usd_pegged_classic_assets`
	//     is non-empty.
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

	// UnverifiedWarning points at the verified Stellar-canonical
	// asset when the requested asset uses a verified currency's
	// ticker code but is NOT the verified issuer. The classic
	// "someone issued their own USDC on Stellar" surface. Nil for
	// the verified asset itself and for any code not claimed by a
	// verified currency. Pairs with `Flags.UnverifiedTickerCollision`
	// on the envelope. See R-018 /
	// docs/architecture/multi-network-assets-migration.md.
	UnverifiedWarning *UnverifiedWarning `json:"unverified_warning,omitempty"`
}

// UnverifiedWarning is the warning body attached to AssetDetail
// when an asset code-collides with a verified Stellar currency
// but the issuer doesn't match.
type UnverifiedWarning struct {
	// VerifiedSlug is the canonical slug the consumer can redirect to.
	VerifiedSlug string `json:"verified_slug"`
	// VerifiedAssetID is the verified canonical asset_id.
	VerifiedAssetID string `json:"verified_asset_id"`
	// VerifiedName is the human-readable currency name.
	VerifiedName string `json:"verified_name"`
	// VerifiedIssuer is the short attribution string
	// (e.g. "Circle (centre.io)"). Empty when the catalogue entry
	// didn't include a verified_issuer_label.
	VerifiedIssuer string `json:"verified_issuer,omitempty"`
	// Note is a one-sentence warning rendered verbatim by the client.
	Note string `json:"note"`
}

// TradeRow is the data shape returned by [Client.History] — one
// raw trade row from the trades hypertable. All numeric amounts
// are decimal strings (ADR-0003); `Price` is the pre-computed
// quote/base ratio at 10 fractional digits for consumer
// convenience (the storage layer never persists a derived price,
// so the server computes it at response time).
type TradeRow struct {
	Source      string    `json:"source"`
	Ledger      uint32    `json:"ledger"`
	TxHash      string    `json:"tx_hash"`
	OpIndex     uint32    `json:"op_index"`
	Timestamp   time.Time `json:"ts"`
	BaseAsset   string    `json:"base_asset"`
	QuoteAsset  string    `json:"quote_asset"`
	BaseAmount  string    `json:"base_amount"`
	QuoteAmount string    `json:"quote_amount"`
	Price       string    `json:"price"`
}

// OHLCBar is the data shape returned by [Client.OHLC] — a single
// open/high/low/close bar over the requested window. All price
// fields are decimal strings (ADR-0003); volumes are smallest-unit
// integers as strings.
//
// `Truncated` is true when the window's trade count hit the
// server's per-request cap. The bar's High / Low may not reflect
// the actual extreme over the full window — only the
// chronologically-first N trades. Treat truncated bars as a hint
// to narrow the range.
type OHLCBar struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Open        string    `json:"open"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	BaseVolume  string    `json:"base_volume"`
	QuoteVolume string    `json:"quote_volume"`
	TradeCount  int       `json:"trade_count"`
	Truncated   bool      `json:"truncated"`
}

// Source is the data shape returned by [Client.Sources] — one
// row from the operator's source registry (the catalogue of
// venues + oracles + aggregators the deployment can ingest from).
//
// Class is one of: `exchange` / `aggregator` / `oracle` /
// `authority_sanity`. Per the v1 aggregator policy, only
// `exchange` contributes to VWAP — the others are reported
// alongside but excluded (mixing them double-counts upstream
// markets or imposes their methodology on our output).
//
// Subclass refines `class=exchange` into `dex` / `cex` / `fx`.
// Empty for non-exchange classes.
//
// BackfillSafe gates `ratesengine-ops backfill` per CLAUDE.md
// "Soroban DeFi contracts upgrade in place". On-chain Soroban
// sources start `false` and only flip `true` after a per-WASM-
// hash audit (`docs/operations/wasm-audits/`). Off-chain CEX/FX
// sources are always `true`.
type Source struct {
	Name              string `json:"name"`
	Class             string `json:"class"`
	Subclass          string `json:"subclass,omitempty"`
	IncludeInVWAP     bool   `json:"include_in_vwap"`
	Paid              bool   `json:"paid"`
	BackfillAvailable bool   `json:"backfill_available"`
	BackfillSafe      bool   `json:"backfill_safe"`
	DefaultWeight     int    `json:"default_weight"`
}

// Methodology is the data shape returned by [Client.Methodology].
// Machine-readable summary of the active aggregation policy:
// VWAP method, outlier filters, stablecoin → fiat-USD proxy
// allow-list, source classes, registered venues, ADR refs.
//
// Designed for transparency consumers (compliance, auditors,
// integrators verifying RFP §10 "open methodology" claims) who
// want to verify what the deployment is doing without parsing
// the explorer's HTML or chasing ADR cross-refs.
type Methodology struct {
	Version       string                   `json:"version"`
	Aggregation   MethodologyAggregation   `json:"aggregation"`
	SourceClasses []MethodologySourceClass `json:"source_classes"`
	Sources       []Source                 `json:"sources"`
	References    []MethodologyReference   `json:"references"`
}

// MethodologyAggregation captures the price-derivation policy.
type MethodologyAggregation struct {
	PriceMethod               string                     `json:"price_method"`
	OutlierFilter             MethodologyOutlierFilter   `json:"outlier_filter"`
	StablecoinFiatProxy       []MethodologyStablecoinPeg `json:"stablecoin_fiat_proxy"`
	ClosedBucketWindowSeconds int                        `json:"closed_bucket_window_seconds"`
}

// MethodologyOutlierFilter is one σ-trim rule.
type MethodologyOutlierFilter struct {
	Endpoint     string  `json:"endpoint,omitempty"`
	DefaultSigma float64 `json:"default_sigma"`
	Note         string  `json:"note,omitempty"`
}

// MethodologyStablecoinPeg is one (token → fiat) mapping.
type MethodologyStablecoinPeg struct {
	AssetID string `json:"asset_id"`
	PegsTo  string `json:"pegs_to"`
}

// MethodologySourceClass describes one of the four registry classes.
type MethodologySourceClass struct {
	Name              string `json:"name"`
	ContributesToVWAP bool   `json:"contributes_to_vwap"`
	Description       string `json:"description"`
}

// MethodologyReference points at an ADR or other narrative doc.
type MethodologyReference struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Market is the data shape returned by [Client.Markets] and
// [Client.Pair] — one (base, quote) pair the deployment has
// observed at least one trade for, with a 24h activity summary.
type Market struct {
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
	// Volume24hUSD is the trailing-24h USD volume summed from
	// prices_1m's per-bucket volume_usd. Decimal string per
	// ADR-0003. Nil when the pair has no USD-equivalent trades.
	Volume24hUSD *string `json:"volume_24h_usd,omitempty"`
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

// Coin + CoinsPage types removed — `/v1/coins` HTTP surface
// retired (no production consumers). Use the AssetDetail surface
// from `/v1/assets` instead.

// IssuerListEntry is the data shape returned by [Client.Issuers] —
// one row in the issuer directory ranked by total observation
// count across the issuer's classic assets.
//
// HomeDomain is empty until the SEP-1 fetcher worker resolves
// `stellar.toml` for the issuer's account.
type IssuerListEntry struct {
	GStrkey    string `json:"g_strkey"`
	HomeDomain string `json:"home_domain,omitempty"`
	// OrgName is the issuer's organisation name from SEP-1
	// (`[DOCUMENTATION].ORG_NAME`). Empty until the SEP-1
	// fetcher (`ratesengine-ops sep1-refresh`) resolves it.
	OrgName               string `json:"org_name,omitempty"`
	AssetCount            int64  `json:"asset_count"`
	TotalObservationCount int64  `json:"total_observation_count"`
}

// IssuedAsset is one entry in [Issuer.Assets] — a classic asset
// minted by the parent issuer. Mirrors the embedded shape on the
// issuer-detail wire envelope.
type IssuedAsset struct {
	AssetID          string `json:"asset_id"`
	Code             string `json:"code"`
	Slug             string `json:"slug"`
	FirstSeenLedger  uint32 `json:"first_seen_ledger"`
	LastSeenLedger   uint32 `json:"last_seen_ledger"`
	ObservationCount int64  `json:"observation_count"`
}

// Issuer is the data shape returned by [Client.Issuer]
// (`/v1/issuers/{g_strkey}`). Auth flags + SEP-1 fields populate
// as the SEP-1 fetcher worker resolves them; pre-resolution they
// stay nil.
type Issuer struct {
	GStrkey    string `json:"g_strkey"`
	HomeDomain string `json:"home_domain,omitempty"`
	// OrgName is the issuer's organisation name from
	// `[DOCUMENTATION].ORG_NAME` in stellar.toml. Populated by
	// the SEP-1 fetcher; empty until resolved.
	OrgName        string  `json:"org_name,omitempty"`
	AuthRequired   *bool   `json:"auth_required,omitempty"`
	AuthRevocable  *bool   `json:"auth_revocable,omitempty"`
	AuthImmutable  *bool   `json:"auth_immutable,omitempty"`
	AuthClawback   *bool   `json:"auth_clawback,omitempty"`
	SEP1ResolvedAt *string `json:"sep1_resolved_at,omitempty"`
	// SEP1Payload is opaque JSON — schemas drift over time and many
	// issuers add custom fields. Callers who need typed access
	// should unmarshal into their own struct.
	SEP1Payload    json.RawMessage `json:"sep1_payload,omitempty"`
	CreationLedger *uint32         `json:"creation_ledger,omitempty"`
	Assets         []IssuedAsset   `json:"assets,omitempty"`
}

// Cursor is one entry in the array returned by [Client.Cursors] —
// the per-source ingest progress marker exposed at
// `/v1/diagnostics/cursors`. `LagSeconds` is computed server-side
// (now − last_updated) so callers don't need a clock-sync
// agreement with the API.
type Cursor struct {
	Source      string `json:"source"`
	SubSource   string `json:"sub_source,omitempty"`
	LastLedger  uint32 `json:"last_ledger"`
	LastUpdated string `json:"last_updated"`
	LagSeconds  int64  `json:"lag_seconds"`
}

// Status is the data shape returned by [Client.Status]. Mirrors
// the wire shape of /v1/status — a customer-facing system-health
// rollup.
type Status struct {
	Overall   string          `json:"overall"`
	Region    StatusRegion    `json:"region"`
	Services  []StatusService `json:"services"`
	Latency   StatusLatency   `json:"latency"`
	Freshness StatusFreshness `json:"freshness"`
	Incidents StatusIncidents `json:"incidents"`
}

// StatusRegion identifies which region produced the response.
type StatusRegion struct {
	Name       string `json:"name"`
	Deployment string `json:"deployment"`
}

// StatusService is one entry in [Status.Services] — a per-binary
// heartbeat. Status is "ok" when the last scrape was within 60 s,
// "down" when stale, "unknown" when no Prometheus backend is wired.
type StatusService struct {
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen,omitempty"`
}

// StatusLatency reports API histogram-derived percentiles over
// the last [WindowSecs] seconds. Zero values mean no Prometheus
// backend is wired or no samples in the window.
type StatusLatency struct {
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	WindowSecs int     `json:"window_secs"`
}

// StatusFreshness summarises the ingest layer.
type StatusFreshness struct {
	LastAggregatorTick time.Time `json:"last_aggregator_tick,omitempty"`
	ActiveSources      int       `json:"active_sources"`
	TotalSources       int       `json:"total_sources"`
}

// StatusIncidents counts currently-firing alerts grouped by
// severity. Zero values indicate no Alertmanager backend wired or
// no alerts firing.
type StatusIncidents struct {
	ActiveCount        int              `json:"active_count"`
	PageCount          int              `json:"page_count"`
	TicketCount        int              `json:"ticket_count"`
	InformationalCount int              `json:"informational_count"`
	Active             []ActiveIncident `json:"active,omitempty"`
}

// ActiveIncident is one entry in [StatusIncidents.Active] — the
// customer-facing summary of a currently-firing alert. RunbookURL
// links to the public GitHub markdown when the alert rule has it
// set; other internal labels (component, instance) are
// intentionally excluded so the surface stays anonymous-friendly.
type ActiveIncident struct {
	Name       string `json:"name"`
	Severity   string `json:"severity"`
	RunbookURL string `json:"runbook_url,omitempty"`
}

// Health is the data shape returned by [Client.Healthz] and
// [Client.Readyz].
type Health struct {
	Status string `json:"status"`
	Uptime string `json:"uptime,omitempty"`
}

// Version is the data shape returned by [Client.Version] — build
// metadata for the API binary.
type Version struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
	Commit    string `json:"commit"`
	Dirty     string `json:"dirty"`
	GoVersion string `json:"go_version"`
}

// ChartSeries is the data shape returned by [Client.Chart]. The
// per-point time series uses the same shape as
// [HistoryPoint] (`t` / `p` / `v_usd`) but the envelope-level
// metadata differs (Timeframe + bound Granularity).
//
// Truncated reports whether the requested timeframe extends before
// the earliest available data on this deployment (e.g. asking for
// `Timeframe: "1y"` when the deployment only retains 7 days). When
// true, DataStartsAt + RequestedFrom are populated so consumers
// can render "history begins at <ts>" instead of guessing whether
// the deployment is data-thin or the asset is genuinely flat.
// `Timeframe: "all"` always reports Truncated=false because that
// timeframe means "everything you have" by definition.
type ChartSeries struct {
	AssetID       string         `json:"asset_id"`
	Quote         string         `json:"quote"`
	Granularity   string         `json:"granularity"`
	Timeframe     string         `json:"timeframe"`
	PriceType     string         `json:"price_type"`
	Points        []HistoryPoint `json:"points"`
	Truncated     bool           `json:"truncated"`
	DataStartsAt  *time.Time     `json:"data_starts_at,omitempty"`
	RequestedFrom *time.Time     `json:"requested_from,omitempty"`
}

// ChangeSummary is the data shape returned by [Client.ChangeSummary]
// — per-entity multi-window delta rollup.
type ChangeSummary struct {
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

// NetworkStats is the data shape returned by [Client.NetworkStats] —
// the home-page aggregate snapshot the explorer renders in its
// network strip. One round trip replaces fan-out across coins +
// markets + sources + diagnostics. Volume24hUSD is a *string per
// ADR-0003 (raw cents can exceed int64); nil when the rolling 24h
// window has no USD-equivalent trades.
type NetworkStats struct {
	Volume24hUSD    *string `json:"volume_24h_usd,omitempty"`
	MarketsCount24h int64   `json:"markets_count_24h"`
	AssetsIndexed   int64   `json:"assets_indexed"`
	LatestLedger    int64   `json:"latest_ledger"`
	ExchangeSources int     `json:"exchange_sources"`
	TotalSources    int     `json:"total_sources"`
}

// Incident is one customer-facing incident post returned by
// [Client.Incidents]. Mirrors the wire shape served at
// `/v1/incidents`; the SDK can't import the internal incidents
// package directly. Severity is "SEV-1" / "SEV-2" / "SEV-3" /
// "SEV-4"; Status is "investigating" / "identified" / "monitoring"
// / "resolved". BodyMarkdown is the full Markdown post body.
type Incident struct {
	Slug               string     `json:"slug"`
	Title              string     `json:"title"`
	Severity           string     `json:"severity"`
	Status             string     `json:"status"`
	StartedAt          time.Time  `json:"started_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	AffectedComponents []string   `json:"affected_components,omitempty"`
	PostmortemRef      string     `json:"postmortem,omitempty"`
	BodyMarkdown       string     `json:"body_markdown"`
}

// IncidentsList wraps the [Client.Incidents] response. Sorted
// most-recent-first (started_at desc) by the API.
type IncidentsList struct {
	Incidents []Incident `json:"incidents"`
	Count     int        `json:"count"`
}

// Currency / CurrenciesList / CurrencyHistoryPoint / CurrencyDetail
// removed — `/v1/currencies` HTTP surface retired (no production
// consumers). Use AssetDetail from `/v1/assets` instead.

// LendingPool is one row from [Client.LendingPools] — a Blend pool
// contract observed in the trailing 7d auction stream. Auction +
// user counts are derived from the trades hypertable; per-pool
// TVL / utilisation / supply+borrow APYs land via additional
// fields once the pool-storage reader worker ships, so this
// shape is designed to grow rather than version-bump.
type LendingPool struct {
	Protocol       string    `json:"protocol"`
	Pool           string    `json:"pool"`
	Auctions24h    int64     `json:"auctions_24h"`
	AuctionsTotal  int64     `json:"auctions_total"`
	UniqueUsers30d int64     `json:"unique_users_30d"`
	LastSeen       time.Time `json:"last_seen"`
}

// VWAPResult is the data shape returned by [Client.VWAP] —
// volume-weighted average price over the requested [from, to)
// window. Mirrors `internal/api/v1.VWAPResult`.
//
// Truncated is true when the window had MORE than the server's
// max-trades cap (10000 today) — Price then only reflects the
// chronologically-first 10000 trades and is NOT the true window
// VWAP. Clients should narrow the window and retry. For fixed
// cross-region-consistent VWAPs use [Client.Price] (closed-bucket
// per ADR-0015) instead.
type VWAPResult struct {
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	Price            string    `json:"price"`
	BaseVolume       string    `json:"base_volume"`
	QuoteVolume      string    `json:"quote_volume"`
	TradeCount       int       `json:"trade_count"`
	OutliersFiltered int       `json:"outliers_filtered"`
	Truncated        bool      `json:"truncated"`
}

// TWAPResult is the data shape returned by [Client.TWAP] —
// time-weighted average price over the requested [from, to)
// window. Mirrors `internal/api/v1.TWAPResult`. No outlier_sigma
// param on TWAP — time-weighting itself is a form of outlier
// resistance.
type TWAPResult struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Price      string    `json:"price"`
	TradeCount int       `json:"trade_count"`
	Truncated  bool      `json:"truncated"`
}

// Pool is one row from [Client.Pools] — a single (source, base,
// quote) tuple from the DEX/AMM listing. Distinct from [Market]
// (which collapses across sources): the same physical pair traded
// on two DEXes returns ONE Market row but TWO Pool rows.
//
// LastPrice is per-source so two venues trading the same pair
// surface independent prices.
type Pool struct {
	Source        string    `json:"source"`
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
	Volume24hUSD  *string   `json:"volume_24h_usd,omitempty"`
	LastPrice     *string   `json:"last_price,omitempty"`
}

// GlobalAssetView is the wire shape returned by [Client.Asset]
// when called with a verified-currency slug ("usdc", "eurc",
// "aqua"). Distinct from [AssetDetail] which is the per-Stellar-
// network view returned for canonical asset_ids.
//
// See R-018 / docs/architecture/multi-network-assets-migration.md
// Phase 1.4a for the dispatch rationale.
type GlobalAssetView struct {
	Ticker      string `json:"ticker"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Class is one of "crypto" / "stablecoin" / "fiat" — drives
	// listing taxonomies on consumers.
	Class          string `json:"class"`
	VerifiedIssuer string `json:"verified_issuer,omitempty"`
	CoinGeckoID    string `json:"coingecko_id,omitempty"`
	CMCID          string `json:"coinmarketcap_id,omitempty"`

	// Headline price block — all nil/empty together when no tier of
	// the fallback chain produced a price (typically a Stellar-only
	// token whose price isn't aggregated at the global level).
	PriceUSD       *string    `json:"price_usd,omitempty"`
	PriceAuthority string     `json:"price_authority,omitempty"` // "vwap_native" | "aggregator_avg" | "triangulated"
	PriceSources   []string   `json:"price_sources,omitempty"`
	PriceAsOf      *time.Time `json:"price_as_of,omitempty"`

	// Supply + market cap. Populated for fiat (M2 × FX rate).
	// Crypto/stablecoin market cap stays on /v1/assets/{asset_id}'s
	// F2 fields.
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	SupplyDecimals    int     `json:"supply_decimals,omitempty"`
	MarketCapUSD      *string `json:"market_cap_usd,omitempty"`

	Networks []NetworkView `json:"networks"`
}

// NetworkView is one per-network identity entry on GlobalAssetView.
type NetworkView struct {
	Network string `json:"network"`
	// DataQuality is "indexed" (Stellar — we ingest trades; deep_link
	// is set) or "external" (we know the contract address but don't
	// ingest the network's trades).
	DataQuality string `json:"data_quality"`
	// Stellar fields — only present when Network == "stellar".
	AssetID  string `json:"asset_id,omitempty"`
	Code     string `json:"code,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	DeepLink string `json:"deep_link,omitempty"`
	// Non-Stellar fields.
	Contract     string `json:"contract,omitempty"`
	ExternalLink string `json:"external_link,omitempty"`
}

// VerifiedCurrencyListItem is one row in the response to
// [Client.AssetsVerified] (`GET /v1/assets/verified`) — a directory
// entry from the verified-currency catalogue. Identity-only;
// pricing requires a per-row fetch via [Client.Asset] with the
// `Slug` value.
type VerifiedCurrencyListItem struct {
	Ticker string `json:"ticker"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	// Class is one of "crypto" / "stablecoin" / "fiat" — drives the
	// listing taxonomy on consumers (R-018 assets-unification).
	Class             string `json:"class"`
	VerifiedIssuer    string `json:"verified_issuer,omitempty"`
	CoinGeckoID       string `json:"coingecko_id,omitempty"`
	CMCID             string `json:"coinmarketcap_id,omitempty"`
	CirculatingSupply string `json:"circulating_supply,omitempty"`
	SupplyDecimals    int    `json:"supply_decimals,omitempty"`
	// MarketCapUSD is computed for fiat rows only (M2 × current FX
	// rate). Empty for crypto/stablecoin rows. Decimal string with
	// 2 fractional digits.
	MarketCapUSD string        `json:"market_cap_usd,omitempty"`
	NetworkCount int           `json:"network_count"`
	Networks     []NetworkView `json:"networks"`
}

// PerNetworkAssetView is the wire shape returned by
// [Client.AssetByNetwork] (`GET /v1/assets/{slug}/{network}`) when
// the network entry is non-Stellar. Stellar entries redirect to
// the canonical `/v1/assets/{asset_id}` view (which returns the
// full AssetDetail body).
type PerNetworkAssetView struct {
	Ticker       string `json:"ticker"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Class        string `json:"class"`
	Network      string `json:"network"`
	DataQuality  string `json:"data_quality"`
	Contract     string `json:"contract,omitempty"`
	ExternalLink string `json:"external_link,omitempty"`
}
