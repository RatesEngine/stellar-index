package v1

import (
	"net/http"
	"sort"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Methodology is the wire shape of /v1/methodology — a
// machine-readable summary of the active aggregation policy.
//
// Designed for transparency consumers (compliance, auditors, AI
// agents, integrators verifying RFP §10 "open methodology" claims)
// who want to verify what the deployment is doing without parsing
// the explorer's HTML at /methodology or chasing ADR cross-refs.
//
// Every field maps back to either a constant in this package, a
// row in `internal/sources/external.Registry`, or an operator
// config value plumbed through the [Options] struct. Nothing here
// is fabricated for display.
//
// Stability: pkg/client mirrors this shape. Adding new optional
// fields is backwards-compatible; removing or renaming requires a
// SemVer minor bump on the SDK and a CHANGELOG note.
type Methodology struct {
	// Version is the on-disk shape version of the response. Bump
	// on any breaking change.
	Version string `json:"version"`

	// Aggregation describes the VWAP method, outlier filter,
	// stablecoin proxy, and closed-bucket contract that govern
	// the served price. ADR-0007 / ADR-0015 / ADR-0019 are the
	// authoritative narratives.
	Aggregation MethodologyAggregation `json:"aggregation"`

	// SourceClasses enumerates the four class buckets and which
	// contributes to the VWAP. Mirrors `internal/sources/external`.
	// Subdivides ClassExchange into dex/cex/fx subclasses.
	SourceClasses []MethodologySourceClass `json:"source_classes"`

	// Sources is the flat list of registered venues with their
	// class, subclass, default weight, and contribution flag.
	// Same data as `/v1/sources` (without the live trade-count
	// stats); included here so a transparency consumer can verify
	// the policy in a single round trip.
	Sources []MethodologySource `json:"sources"`

	// References point at the long-form ADRs that govern each
	// section. URLs are repo-relative paths; consumers can resolve
	// them against the public source tree.
	References []MethodologyReference `json:"references"`
}

// MethodologyAggregation is the price-derivation policy slice.
type MethodologyAggregation struct {
	// PriceMethod is the formula for the headline served price.
	// Currently only "vwap" — TWAP is reserved per ADR-0020.
	PriceMethod string `json:"price_method"`

	// OutlierFilter describes the σ-trim applied before
	// aggregation. `default_sigma=0` means "no filter by default
	// on this surface"; the field is per-endpoint.
	OutlierFilter MethodologyOutlierFilter `json:"outlier_filter"`

	// StablecoinFiatProxy is the operator's allow-list of classic
	// USD-pegged tokens that the aggregator (and the read-time
	// fallback chain on /v1/price + /v1/ohlc + /v1/chart) treats
	// as proxies for `fiat:USD`. Empty when the operator hasn't
	// declared any pegs (deployment serves only direct quotes).
	StablecoinFiatProxy []MethodologyStablecoinPeg `json:"stablecoin_fiat_proxy"`

	// ClosedBucketWindowSeconds is the boundary granularity that
	// rate endpoints snap "now" to. Per ADR-0015 this is what
	// makes "all 3 regions return the same rate" a real property.
	ClosedBucketWindowSeconds int `json:"closed_bucket_window_seconds"`
}

// MethodologyOutlierFilter captures one filter rule.
type MethodologyOutlierFilter struct {
	// Endpoint is the API surface this rule applies to (e.g.
	// "/v1/ohlc"). Empty = global default across all surfaces.
	Endpoint string `json:"endpoint,omitempty"`

	// DefaultSigma is the σ threshold applied when the caller
	// doesn't override via `?outlier_sigma=`. 0 = filter
	// disabled by default.
	DefaultSigma float64 `json:"default_sigma"`

	// Note is free-form context (e.g. "VWAP volume-weighting
	// already dampens dust trades; OHLC's High/Low needs an
	// explicit filter").
	Note string `json:"note,omitempty"`
}

// MethodologyStablecoinPeg is one (token → fiat) mapping.
type MethodologyStablecoinPeg struct {
	AssetID string `json:"asset_id"`
	PegsTo  string `json:"pegs_to"`
}

// MethodologySourceClass describes one of the four registry
// classes (exchange / aggregator / oracle / authority_sanity).
type MethodologySourceClass struct {
	Name              string `json:"name"`
	ContributesToVWAP bool   `json:"contributes_to_vwap"`
	Description       string `json:"description"`
}

// MethodologySource is one venue, projected from
// `external.Registry`.
type MethodologySource struct {
	Name              string `json:"name"`
	Class             string `json:"class"`
	Subclass          string `json:"subclass,omitempty"`
	DefaultWeight     int    `json:"default_weight"`
	IncludeInVWAP     bool   `json:"include_in_vwap"`
	Paid              bool   `json:"paid"`
	BackfillAvailable bool   `json:"backfill_available"`
	BackfillSafe      bool   `json:"backfill_safe"`
}

// MethodologyReference is a pointer to an ADR or other narrative
// doc that governs one slice of the policy.
type MethodologyReference struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// methodologyVersion is the on-disk shape version. Bump on
// breaking changes to the response. Additive field changes
// (new optional fields) keep the version stable.
const methodologyVersion = "1.0"

// handleMethodology serves GET /v1/methodology.
//
// Static response derived from compile-time constants + the
// in-memory `external.Registry` + the operator's
// [Options.USDPeggedClassics]. No DB call, sub-millisecond.
//
// Cache-Control is set by the cachecontrol middleware; the
// content only changes on binary deploy or operator config
// reload, so it's safe to cache aggressively.
//
// R-023 in `docs/review-2026-05-10.md`.
func (s *Server) handleMethodology(w http.ResponseWriter, r *http.Request) {
	pegs := make([]MethodologyStablecoinPeg, 0, len(s.usdPeggedClassics))
	for _, peg := range s.usdPeggedClassics {
		pegs = append(pegs, MethodologyStablecoinPeg{
			AssetID: peg.String(),
			PegsTo:  canonical.Asset{Type: canonical.AssetFiat, Code: "USD"}.String(),
		})
	}

	classes := []MethodologySourceClass{
		{Name: string(external.ClassExchange), ContributesToVWAP: true, Description: "Real trading venues — DEXes (Soroswap, Phoenix, Aquarius, Comet, sdex), CEXes (Coinbase, Binance, Kraken, Bitstamp), FX vendors. The only sources that contribute to the VWAP."},
		{Name: string(external.ClassAggregator), ContributesToVWAP: false, Description: "Third-party aggregators (CoinGecko, CoinMarketCap) that already aggregate the same upstream venues. Including them in our VWAP would double-count; surfaced separately for divergence checks."},
		{Name: string(external.ClassOracle), ContributesToVWAP: false, Description: "Reflector, Band, Redstone, Chainlink. Each runs its own methodology; adding their output to our VWAP would impose theirs on top of ours. Surfaced as parallel readings + used for cross-checks."},
		{Name: string(external.ClassAuthoritySanity), ContributesToVWAP: false, Description: "Stellar-blessed reference points (anchor home-domains, canonical fiat rates) used as sanity bounds, not price input."},
	}

	names := make([]string, 0, len(external.Registry))
	for name := range external.Registry {
		names = append(names, name)
	}
	sort.Strings(names)

	sources := make([]MethodologySource, 0, len(names))
	for _, name := range names {
		md := external.Registry[name]
		sources = append(sources, MethodologySource{
			Name:              name,
			Class:             string(md.Class),
			Subclass:          string(md.Subclass),
			DefaultWeight:     md.DefaultWeight,
			IncludeInVWAP:     md.IncludeInVWAP,
			Paid:              md.Paid,
			BackfillAvailable: md.BackfillAvailable,
			BackfillSafe:      md.BackfillSafe,
		})
	}

	out := Methodology{
		Version: methodologyVersion,
		Aggregation: MethodologyAggregation{
			PriceMethod: "vwap",
			OutlierFilter: MethodologyOutlierFilter{
				Endpoint:     "/v1/ohlc",
				DefaultSigma: ohlcDefaultOutlierSigma,
				Note:         "OHLC's High/Low have no statistical robustness; a single dust trade can pin them. The default sigma applies to /v1/ohlc only — /v1/vwap and /v1/twap default to 0 (volume-weighting and arithmetic-mean already dampen outliers).",
			},
			StablecoinFiatProxy:       pegs,
			ClosedBucketWindowSeconds: int(closedBucketWindow.Seconds()),
		},
		SourceClasses: classes,
		Sources:       sources,
		References: []MethodologyReference{
			{ID: "ADR-0007", Title: "Aggregation policy + cache-key contract", URL: "/research/adr/0007"},
			{ID: "ADR-0015", Title: "Closed-bucket rate-serving + cross-region answer agreement", URL: "/research/adr/0015"},
			{ID: "ADR-0018", Title: "Three API consistency surfaces (closed-bucket, tip, observations)", URL: "/research/adr/0018"},
			{ID: "ADR-0019", Title: "Anomaly detection + freeze policy", URL: "/research/adr/0019"},
			{ID: "ADR-0020", Title: "/v1/chart timeframe + granularity contract", URL: "/research/adr/0020"},
			{ID: "ADR-0026", Title: "Stablecoin-fiat proxy late binding (aggregator policy, not decoder policy)", URL: "/research/adr/0026"},
		},
	}

	writeJSON(w, out, Flags{})
}

// canonicalFiatUSD is the canonical Asset for the USD fiat side
// of every stablecoin peg. Hoisted for clarity over inlining
// `canonical.Asset{Type: canonical.AssetFiat, Code: "USD"}` at
// each call site.
//
// Currently inlined in handleMethodology; exposed as a package
// var would let other handlers share. Kept as a comment for now
// to flag the future-cleanup target without a behaviour change.
var _ = canonical.Asset{Type: canonical.AssetFiat, Code: "USD"}
