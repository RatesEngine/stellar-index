package v1

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
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
	Class          string `json:"class"` // crypto / stablecoin / fiat
	VerifiedIssuer string `json:"verified_issuer,omitempty"`
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

	// ─── Supply + market cap (catalogue-sourced) ──────────────────
	//
	// CirculatingSupply is the natural-unit amount in circulation.
	// For fiat this is M2 (broad money: cash + checkable deposits
	// + savings + money-market funds), populated from central-bank
	// reporting via `internal/currency/data/seed.yaml`. For crypto /
	// stablecoin classes the catalogue field is unset today; the
	// per-Stellar-asset F2 fields on /v1/assets/{asset_id} are the
	// canonical source.
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	// SupplyDecimals exponent maps the integer in CirculatingSupply
	// to a display value. 0 for fiat (raw dollars/yen/yuan).
	SupplyDecimals int `json:"supply_decimals,omitempty"`
	// MarketCapUSD = CirculatingSupply × PriceUSD when both are
	// available. Decimal string with 2 fractional digits. For fiat
	// at scale: CNY M2 × USD/CNY ≈ $42T, which ranks #1 globally.
	// Null when supply or price is unavailable.
	MarketCapUSD *string `json:"market_cap_usd,omitempty"`

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
		Class:          string(vc.Class),
		VerifiedIssuer: vc.VerifiedIssuerLabel,
		CoinGeckoID:    vc.CoinGeckoID,
		CMCID:          vc.CoinMarketCapID,
		Networks:       networkViewsFromCatalogue(vc),
	}
	// Catalogue-sourced supply (M2 for fiat; usually empty for
	// crypto/stablecoin — the F2 supply pipeline is the source of
	// truth there).
	if vc.CirculatingSupply != "" {
		supply := vc.CirculatingSupply
		view.CirculatingSupply = &supply
		view.SupplyDecimals = vc.SupplyDecimals
	}

	// Populate the price block via the three-tier fallback chain
	// (vwap_native → aggregator_avg → triangulated). Skipped when
	// the binary didn't wire a GlobalPriceReader, leaving the price
	// fields nil — consumers fall back to the Stellar-network deep
	// link via networks[].stellar.deep_link.
	if s.globalPrice == nil {
		return view
	}

	base, ok := assetForCurrency(vc)
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

	// Special-case: USD vs USD. Skip the storage lookup (which would
	// 404 or return weird semantics) and synthesise an identity price.
	if vc.Class == currency.ClassFiat && strings.EqualFold(vc.Ticker, "USD") {
		identity := "1.00000000000000"
		asOf := time.Now().UTC()
		view.PriceUSD = &identity
		view.PriceAuthority = aggregate.AuthorityVWAPNative
		view.PriceSources = []string{"identity"}
		view.PriceAsOf = &asOf
		view.MarketCapUSD = computeFiatMarketCap(vc.CirculatingSupply, identity)
		return view
	}

	opts := s.globalPriceOpts
	if opts.AggregatorSources == nil {
		// Leave aggregator tier disabled if the binary didn't wire a
		// source list — better to skip than fail.
		opts.AggregatorSources = nil
	}
	// FX pairs have very low trade counts (snapshots, not trades).
	// Lower the threshold so non-USD fiat populates a price.
	if vc.Class == currency.ClassFiat {
		opts = fiatGlobalPriceOpts(opts)
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

	// Fiat market cap: CirculatingSupply (M2) × PriceUSD.
	// Crypto / stablecoin market cap stays on the per-asset surface
	// (/v1/assets/{asset_id}); not computed here because the
	// catalogue's CirculatingSupply is typically empty for those
	// classes.
	if vc.Class == currency.ClassFiat {
		view.MarketCapUSD = computeFiatMarketCap(vc.CirculatingSupply, price)
	}
	return view
}

// assetForCurrency builds the canonical asset to query global pricing
// for. Fiat slugs map to a `fiat:CCY` asset (FX feeds populate
// prices_1m for these pairs). Crypto / stablecoin slugs map to
// `crypto:TICKER`. Non-allow-listed tickers return ok=false; the
// caller renders the catalogue identity without a price block.
func assetForCurrency(vc *currency.VerifiedCurrency) (canonical.Asset, bool) {
	if vc.Class == currency.ClassFiat {
		a, err := canonical.NewFiatAsset(vc.Ticker)
		if err != nil {
			return canonical.Asset{}, false
		}
		return a, true
	}
	return globalBaseForTicker(vc.Ticker)
}

// fiatMarketCapUSD computes market_cap_usd for a fiat catalogue
// entry. USD is special-cased to identity (price = 1.00); every
// other fiat goes through ComputeGlobalPrice against fiat:CCY →
// fiat:USD to get the current FX rate, then multiplies by M2.
// Returns nil when the FX rate isn't available (no fiat pair in
// prices_1m yet) or the supply parse fails.
//
// FX-pair VWAP buckets typically have very low trade counts (one
// observation per minute from each FX vendor, often 1-3 total)
// because FX feeds publish snapshots, not order-book trades.
// Override the global default VWAPMinTradeCount (5) down to 1 for
// fiat — any non-empty bucket is authoritative because each
// observation IS the rate, not a sample of trading activity.
func (s *Server) fiatMarketCapUSD(ctx context.Context, vc *currency.VerifiedCurrency) *string {
	if vc.CirculatingSupply == "" {
		return nil
	}
	if strings.EqualFold(vc.Ticker, "USD") {
		return computeFiatMarketCap(vc.CirculatingSupply, "1.00000000000000")
	}
	base, err := canonical.NewFiatAsset(vc.Ticker)
	if err != nil {
		// Ticker isn't on the canonical fiat allow-list. Skip.
		return nil
	}
	quote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		return nil
	}
	opts := fiatGlobalPriceOpts(s.globalPriceOpts)
	res, err := aggregate.ComputeGlobalPrice(ctx, base, quote, s.globalPrice, opts)
	if err != nil {
		return nil
	}
	return computeFiatMarketCap(vc.CirculatingSupply, res.Price)
}

// fiatGlobalPriceOpts lowers VWAPMinTradeCount to 1 so an FX pair
// with a single per-minute snapshot can populate. See the docstring
// on fiatMarketCapUSD for the rationale.
func fiatGlobalPriceOpts(base aggregate.GlobalPriceOptions) aggregate.GlobalPriceOptions {
	out := base
	out.VWAPMinTradeCount = 1
	return out
}

// computeFiatMarketCap returns market_cap_usd = supplyStr × priceStr
// formatted to 2 fractional digits, or nil when either operand can't
// be parsed as a decimal. supplyStr is the natural-unit amount
// (raw yen/yuan/etc.; supply_decimals = 0 for fiat).
func computeFiatMarketCap(supplyStr, priceStr string) *string {
	if supplyStr == "" || priceStr == "" {
		return nil
	}
	supply, ok := new(big.Float).SetPrec(128).SetString(supplyStr)
	if !ok {
		return nil
	}
	price, ok := new(big.Float).SetPrec(128).SetString(priceStr)
	if !ok {
		return nil
	}
	product := new(big.Float).SetPrec(128).Mul(supply, price)
	out := product.Text('f', 2)
	return &out
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

// PerNetworkAssetView is the wire shape returned by
// `/v1/assets/{slug}/{network}` when the per-network entry is on a
// chain we don't ingest trades from. Stellar entries redirect to
// the per-Stellar-asset view instead (which carries the full
// AssetDetail shape).
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

// handleAssetByNetwork serves GET /v1/assets/{slug}/{network} for
// the verified-currency drill-down. Dispatch:
//
//   - {network} == "metadata" — never reached here; the more-
//     specific /v1/assets/{asset_id}/metadata route wins via Go
//     1.22+ mux precedence (literal beats wildcard).
//   - {slug} matches a verified-currency catalogue entry AND
//     the entry has a {network} sub-entry — handle below.
//   - Otherwise — 404 (the slug or the network isn't in the
//     catalogue).
//
// For Stellar entries with a non-empty asset_id, redirect to
// `/v1/assets/{asset_id}` (303 See Other) so consumers get the full
// AssetDetail shape via the canonical URL. For non-Stellar
// entries (Ethereum / Solana / etc.), return a sparse
// PerNetworkAssetView with the contract + external_link metadata
// from the catalogue — we don't index trades on those chains so
// there's no price data to surface.
func (s *Server) handleAssetByNetwork(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("asset_id")
	network := strings.ToLower(r.PathValue("network"))

	if s.verifiedCurrencies == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/not-found",
			"Not found", http.StatusNotFound,
			"Per-network drill-down requires the verified-currency catalogue.")
		return
	}

	vc, ok := s.verifiedCurrencies.LookupBySlug(slug)
	if !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/not-found",
			"Slug not in verified-currency catalogue", http.StatusNotFound,
			"The /assets/{slug}/{network} route requires {slug} to be a verified-currency slug; got "+slug)
		return
	}

	var entry *currency.NetworkEntry
	for i := range vc.Networks {
		if strings.EqualFold(vc.Networks[i].Network, network) {
			entry = &vc.Networks[i]
			break
		}
	}
	if entry == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/not-found",
			"Network not in catalogue", http.StatusNotFound,
			"verified currency "+vc.Slug+" has no entry for network "+network)
		return
	}

	// Stellar entries redirect to the per-Stellar-asset canonical view.
	// Native XLM gets routed to /v1/assets/native; classic + Soroban
	// to the CODE-ISSUER asset_id. Consumers that follow the redirect
	// receive the AssetDetail body with SEP-1 overlay + F2 fields.
	if entry.Network == "stellar" && entry.AssetID != "" {
		http.Redirect(w, r, "/v1/assets/"+entry.AssetID, http.StatusSeeOther)
		return
	}

	// Non-Stellar: return identity + contract + external link.
	out := PerNetworkAssetView{
		Ticker:       vc.Ticker,
		Slug:         vc.Slug,
		Name:         vc.Name,
		Class:        string(vc.Class),
		Network:      entry.Network,
		DataQuality:  "external",
		Contract:     entry.Contract,
		ExternalLink: entry.ExternalLink,
	}
	writeJSON(w, out, Flags{})
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
	Ticker            string `json:"ticker"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Class             string `json:"class"`
	VerifiedIssuer    string `json:"verified_issuer,omitempty"`
	CoinGeckoID       string `json:"coingecko_id,omitempty"`
	CMCID             string `json:"coinmarketcap_id,omitempty"`
	CirculatingSupply string `json:"circulating_supply,omitempty"`
	SupplyDecimals    int    `json:"supply_decimals,omitempty"`
	// MarketCapUSD is computed for fiat rows only — M2 × current
	// FX rate. Empty string for crypto/stablecoin rows (their market
	// cap lives on /v1/assets/{asset_id}'s per-Stellar-asset F2
	// fields; computing it inline here would N-multiply storage
	// round-trips on the listing). Fiat fan-out is parallel; ~19
	// FX lookups happen concurrently per request.
	MarketCapUSD string        `json:"market_cap_usd,omitempty"`
	NetworkCount int           `json:"network_count"`
	Networks     []NetworkView `json:"networks"`
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
	out := make([]VerifiedCurrencyListItem, len(entries))
	for i, vc := range entries {
		out[i] = VerifiedCurrencyListItem{
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
		}
	}

	// Compute market_cap_usd for fiat rows in parallel. Skipped for
	// crypto/stablecoin (no catalogue supply) and when no
	// GlobalPriceReader is wired (no FX rate source).
	if s.globalPrice != nil {
		ctx := r.Context()
		var wg sync.WaitGroup
		for i, vc := range entries {
			if vc.Class != currency.ClassFiat || vc.CirculatingSupply == "" {
				continue
			}
			i, vc := i, vc
			wg.Add(1)
			go func() {
				defer wg.Done()
				if capStr := s.fiatMarketCapUSD(ctx, vc); capStr != nil {
					out[i].MarketCapUSD = *capStr
				}
			}()
		}
		wg.Wait()
	}

	writeJSON(w, out, Flags{})
}
