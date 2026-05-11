package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/currency"
)

// GlobalAssetView is the wire shape served by `/v1/assets/{slug}`
// when `{slug}` resolves to a verified-currency catalogue entry —
// the cross-chain identity (USDC the currency) rather than one
// specific issuance (USDC-GA5Z... on Stellar).
//
// Stellar-specific data lives on the existing `/v1/assets/{asset_id}`
// surface; the `networks[].stellar.deep_link` field points there.
// Non-Stellar networks surface contract address + external_link.
//
// See `docs/architecture/multi-network-assets-migration.md` Phase
// 1.4 for the design rationale.
type GlobalAssetView struct {
	// ─── Identity (from the verified-currency catalogue) ──────────
	Ticker         string `json:"ticker"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	VerifiedIssuer string `json:"verified_issuer,omitempty"` // e.g. "Circle (centre.io)"
	CoinGeckoID    string `json:"coingecko_id,omitempty"`
	CMCID          string `json:"coinmarketcap_id,omitempty"`

	// ─── Headline price (from ComputeGlobalPrice's three-tier
	// fallback chain, R-018 Phase 1.3a) ─────────────────────────────
	//
	// All four fields are null/empty together when no tier produced
	// a price (typically a Stellar-only token like AQUA where neither
	// CEX nor cross-chain aggregator coverage exists — consumers
	// should drill into the Stellar network entry's deep_link to
	// reach the Stellar-issued price via /v1/assets/{asset_id}).
	PriceUSD       *string                  `json:"price_usd,omitempty"`
	PriceAuthority aggregate.PriceAuthority `json:"price_authority,omitempty"`
	PriceSources   []string                 `json:"price_sources,omitempty"`
	PriceAsOf      *time.Time               `json:"price_as_of,omitempty"`

	// ─── Per-network entries (from catalogue) ─────────────────────
	Networks []NetworkView `json:"networks"`
}

// NetworkView is one per-network identity for a global asset.
type NetworkView struct {
	Network string `json:"network"`
	// DataQuality is "indexed" (we ingest this network's trades) or
	// "external" (we know the asset exists there but don't ingest
	// its trades; the explorer renders contract + an external link
	// instead of an internal deep_link).
	DataQuality string `json:"data_quality"`
	// Stellar fields — only present when network == "stellar".
	AssetID  string `json:"asset_id,omitempty"`
	Code     string `json:"code,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	DeepLink string `json:"deep_link,omitempty"` // e.g. /v1/assets/USDC-GA5Z...
	// Non-Stellar fields.
	Contract     string `json:"contract,omitempty"`
	ExternalLink string `json:"external_link,omitempty"`
}

// buildGlobalAssetView composes a GlobalAssetView from a catalogue
// entry. The price block stays empty when no global-price reader is
// wired or all three tiers come up empty.
func (s *Server) buildGlobalAssetView(ctx context.Context, vc *currency.VerifiedCurrency) GlobalAssetView {
	view := GlobalAssetView{
		Ticker:         vc.Ticker,
		Slug:           vc.Slug,
		Name:           vc.Name,
		Description:    vc.Description,
		VerifiedIssuer: vc.VerifiedIssuerLabel,
		CoinGeckoID:    vc.CoinGeckoID,
		CMCID:          vc.CoinMarketCapID,
		Networks:       networkViewsFromCatalogue(vc),
	}

	// Populate the price block via the three-tier fallback chain
	// (vwap_native → aggregator_avg → triangulated). Skipped when
	// the binary didn't wire a GlobalPriceReader, leaving the price
	// fields nil — consumers fall back to the Stellar-network deep
	// link via networks[].stellar.deep_link.
	if s.globalPrice == nil {
		return view
	}

	base, ok := globalBaseForTicker(vc.Ticker)
	if !ok {
		return view
	}
	quote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		// "USD" must be on the canonical-fiat allow-list; failing here
		// is a code bug rather than a runtime data issue.
		s.logger.Error("global asset view: USD asset construction failed", "err", err)
		return view
	}

	opts := s.globalPriceOpts
	if opts.AggregatorSources == nil {
		// Leave aggregator tier disabled if the binary didn't wire a
		// source list — better to skip than fail.
		opts.AggregatorSources = nil
	}

	res, err := aggregate.ComputeGlobalPrice(ctx, base, quote, s.globalPrice, opts)
	if err != nil {
		if errors.Is(err, aggregate.ErrNoPrice) {
			return view // expected miss; leave price fields nil
		}
		s.logger.Warn("global asset view: ComputeGlobalPrice failed",
			"ticker", vc.Ticker, "err", err)
		return view
	}

	price := res.Price
	view.PriceUSD = &price
	view.PriceAuthority = res.Authority
	view.PriceSources = res.Sources
	asOf := res.AsOf
	view.PriceAsOf = &asOf
	return view
}

// globalBaseForTicker returns the canonical asset to query the
// global price for. We use the `crypto:<TICKER>` form so the
// existing CEX trades (Binance / Coinbase / Kraken / Bitstamp) and
// aggregators (CG / CMC) populate tier 1 + tier 2 against the same
// canonical key. Non-allow-listed tickers (typically Stellar-only
// tokens we haven't added to the crypto allow-list) return ok=false
// — those tokens' headline price isn't available via the global
// view; consumers drill into the Stellar network's deep_link.
func globalBaseForTicker(ticker string) (canonical.Asset, bool) {
	a, err := canonical.NewCryptoAsset(ticker)
	if err != nil {
		return canonical.Asset{}, false
	}
	return a, true
}

// networkViewsFromCatalogue projects the catalogue's per-network
// entries to the wire shape. Stellar entries gain `data_quality:
// "indexed"` + a deep_link; every other network is `data_quality:
// "external"`.
func networkViewsFromCatalogue(vc *currency.VerifiedCurrency) []NetworkView {
	if vc == nil || len(vc.Networks) == 0 {
		return []NetworkView{}
	}
	out := make([]NetworkView, 0, len(vc.Networks))
	for _, n := range vc.Networks {
		entry := NetworkView{
			Network: n.Network,
		}
		if n.Network == "stellar" {
			entry.DataQuality = "indexed"
			entry.AssetID = n.AssetID
			entry.Code = n.Code
			entry.Issuer = n.Issuer
			if n.AssetID != "" {
				entry.DeepLink = "/v1/assets/" + n.AssetID
			}
		} else {
			entry.DataQuality = "external"
			entry.Contract = n.Contract
			entry.ExternalLink = n.ExternalLink
		}
		out = append(out, entry)
	}
	return out
}

// handleGlobalAsset serves the verified-currency global view at
// `/v1/assets/{slug}`. Called by `handleAssetGet` when the path
// parameter matches a slug in the verified-currency catalogue.
//
// Always returns 200 with whatever data the reader chain produced
// — a global view with `price_usd: null` is the legitimate state
// for a Stellar-only token whose price we don't aggregate at the
// global level. Consumers drill into `networks[].stellar.deep_link`
// for the per-Stellar-asset surface.
func (s *Server) handleGlobalAsset(w http.ResponseWriter, r *http.Request, vc *currency.VerifiedCurrency) {
	view := s.buildGlobalAssetView(r.Context(), vc)
	writeJSON(w, view, Flags{})
}

// VerifiedCurrencyListItem is one entry in the response to
// `GET /v1/assets/verified` — the verified-currency catalogue
// directory listing.
//
// Distinct from [GlobalAssetView] in two ways:
//
//  1. No price block. Computing per-currency global prices for
//     every catalogue entry on every request would N-multiply the
//     storage round-trips for a directory listing; the listing
//     surface is identity-only. Consumers fetch
//     `/v1/assets/{slug}` per row when they need pricing.
//  2. Description is omitted to keep payloads small — the detail
//     page already surfaces it.
type VerifiedCurrencyListItem struct {
	Ticker            string        `json:"ticker"`
	Slug              string        `json:"slug"`
	Name              string        `json:"name"`
	Class             string        `json:"class"`
	VerifiedIssuer    string        `json:"verified_issuer,omitempty"`
	CoinGeckoID       string        `json:"coingecko_id,omitempty"`
	CMCID             string        `json:"coinmarketcap_id,omitempty"`
	CirculatingSupply string        `json:"circulating_supply,omitempty"`
	SupplyDecimals    int           `json:"supply_decimals,omitempty"`
	NetworkCount      int           `json:"network_count"`
	Networks          []NetworkView `json:"networks"`
}

// handleAssetsVerified serves GET /v1/assets/verified — the full
// verified-currency catalogue as a directory listing. Drives the
// explorer's "verified currencies" section at the top of the
// /assets page (R-018 Phase 1.5d).
//
// Order matches the seed-file order (deterministic; the catalogue
// loader preserves entry order). 503 when no catalogue is wired.
func (s *Server) handleAssetsVerified(w http.ResponseWriter, r *http.Request) {
	if s.verifiedCurrencies == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/verified-currencies-unavailable",
			"Verified-currency catalogue not wired", http.StatusServiceUnavailable,
			"This deployment hasn't loaded the verified-currency catalogue.")
		return
	}
	entries := s.verifiedCurrencies.All()
	out := make([]VerifiedCurrencyListItem, 0, len(entries))
	for _, vc := range entries {
		out = append(out, VerifiedCurrencyListItem{
			Ticker:            vc.Ticker,
			Slug:              vc.Slug,
			Name:              vc.Name,
			Class:             string(vc.Class),
			VerifiedIssuer:    vc.VerifiedIssuerLabel,
			CoinGeckoID:       vc.CoinGeckoID,
			CMCID:             vc.CoinMarketCapID,
			CirculatingSupply: vc.CirculatingSupply,
			SupplyDecimals:    vc.SupplyDecimals,
			NetworkCount:      len(vc.Networks),
			Networks:          networkViewsFromCatalogue(vc),
		})
	}
	writeJSON(w, out, Flags{})
}
