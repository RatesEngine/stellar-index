package v1

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/supply"
)

// SupplyLooker is the read-side interface the v1 server uses to
// populate the F2 fields on /v1/assets/{id}. Production
// implementation: a thin adapter around
// timescale.Store.LatestSupply, returning ErrSupplyNotFound when
// the asset has no recorded snapshot.
//
// Returns the most-recent persisted [supply.Supply] for the asset
// or [ErrSupplyNotFound] when no snapshot exists. Real errors
// (Postgres unreachable, parse failures) propagate as-is so the
// handler can log them at WARN — the F2 fields then stay null on
// the response (the asset-detail itself still serves; F2 is
// best-effort overlay).
type SupplyLooker interface {
	LatestSupply(ctx context.Context, assetKey string) (supply.Supply, error)
}

// ErrSupplyNotFound is what [SupplyLooker.LatestSupply] returns when
// the asset has no recorded supply snapshot. The handler treats this
// as "feature unavailable for this asset" — F2 fields stay null,
// no error logged, the asset-detail body still serves.
var ErrSupplyNotFound = errors.New("api: supply snapshot not found")

// VolumeReader is the read-side interface the v1 server uses to
// populate the volume_24h_usd field on /v1/assets/{id}. Production
// implementation: a thin adapter around
// timescale.Store.Volume24hUSDForAsset.
//
// Returns the trailing-24h USD-denominated trade volume for the
// asset (summed across every pair where it appears as base or
// quote) as a decimal string. "0" is a valid value (asset tracked,
// no trades). Errors propagate so the handler can log them at
// WARN — the volume field stays null on any failure, the asset-
// detail body still serves cleanly.
type VolumeReader interface {
	Volume24hUSDForAsset(ctx context.Context, assetKey string) (string, error)
}

// applyF2Fields populates the F2 supply / market-cap / FDV fields
// on detail by consulting the [SupplyLooker] (for supply numbers)
// and the [PriceReader] (for USD price). Best-effort — all fields
// remain null on any failure, the asset-detail body still serves.
//
// Per ADR-0011 "we don't fabricate": every field is only set when
// a defensible value exists. max_supply nil → fdv_usd nil; no USD
// price → market_cap_usd + fdv_usd nil; no supply snapshot → all
// six F2 fields nil.
func (s *Server) applyF2Fields(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	key, keyErr := supply.AssetKey(asset)

	// Volume path is independent of supply — even an asset without
	// a supply snapshot has a meaningful 24h volume if it's been
	// trading. Run it first so a missing snapshot doesn't shadow
	// the volume field.
	if keyErr == nil {
		s.populateVolume24h(ctx, detail, key)
	}

	if s.supply == nil {
		return
	}
	if keyErr != nil {
		// Off-chain assets (fiat / crypto-pure) — supply path is a
		// silent no-op (matches the existing scope; volume path
		// above already returned for the same reason).
		return
	}
	snap, ok := s.fetchSupplySnapshot(ctx, key)
	if !ok {
		return
	}
	populateSupplyFields(detail, snap)
	s.populateMarketCap(ctx, detail, asset, snap, key)
}

// populateVolume24h fills detail.VolumeUSD24h via the [VolumeReader].
// Best-effort — failure logs WARN, the field stays null, the rest
// of the body still serves cleanly.
func (s *Server) populateVolume24h(ctx context.Context, detail *AssetDetail, assetKey string) {
	if s.volume == nil {
		return
	}
	v, err := s.volume.Volume24hUSDForAsset(ctx, assetKey)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("volume_24h_usd lookup failed", "err", err, "asset_key", assetKey)
		}
		return
	}
	detail.VolumeUSD24h = &v
}

// fetchSupplySnapshot wraps the SupplyLooker call with the
// best-effort error policy: ErrSupplyNotFound is silent, real
// errors are logged at WARN, client-cancel doesn't log.
func (s *Server) fetchSupplySnapshot(ctx context.Context, key string) (supply.Supply, bool) {
	snap, err := s.supply.LatestSupply(ctx, key)
	if errors.Is(err, ErrSupplyNotFound) {
		return supply.Supply{}, false
	}
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("supply lookup failed", "err", err, "asset_key", key)
		}
		return supply.Supply{}, false
	}
	return snap, true
}

// populateSupplyFields sets the raw supply integers + basis on
// detail. Each field skipped when its source is nil/zero — per
// ADR-0011 we don't fabricate.
func populateSupplyFields(detail *AssetDetail, snap supply.Supply) {
	if snap.TotalSupply != nil {
		v := snap.TotalSupply.String()
		detail.TotalSupply = &v
	}
	if snap.CirculatingSupply != nil {
		v := snap.CirculatingSupply.String()
		detail.CirculatingSupply = &v
	}
	if snap.MaxSupply != nil {
		v := snap.MaxSupply.String()
		detail.MaxSupply = &v
	}
	if snap.Basis != "" {
		v := string(snap.Basis)
		detail.SupplyBasis = &v
	}
}

// populateMarketCap fills market_cap_usd + fdv_usd when a USD
// price is available. Compute failures log at WARN; the field
// stays nil so the rest of the body still serves cleanly.
func (s *Server) populateMarketCap(ctx context.Context, detail *AssetDetail, asset canonical.Asset, snap supply.Supply, key string) {
	if s.prices == nil {
		return
	}
	usdPrice, ok := s.lookupUSDPrice(ctx, asset)
	if !ok {
		return
	}
	if snap.CirculatingSupply != nil {
		if mc, err := usdMarketValue(snap.CirculatingSupply, usdPrice, detail.Decimals); err != nil {
			s.logger.Warn("market_cap_usd compute failed",
				"err", err, "asset_key", key, "price", usdPrice)
		} else {
			detail.MarketCapUSD = &mc
		}
	}
	if snap.MaxSupply != nil {
		if fdv, err := usdMarketValue(snap.MaxSupply, usdPrice, detail.Decimals); err != nil {
			s.logger.Warn("fdv_usd compute failed",
				"err", err, "asset_key", key, "price", usdPrice)
		} else {
			detail.FDVUSD = &fdv
		}
	}
}

// lookupUSDPrice consults the PriceReader for the asset's USD price.
// Returns ("", false) on any failure (no data, asset is fiat:USD
// itself, etc.) — caller treats this as "market_cap unavailable".
func (s *Server) lookupUSDPrice(ctx context.Context, asset canonical.Asset) (string, bool) {
	if asset.Equal(defaultPriceQuote) {
		// fiat:USD priced against fiat:USD is meaningless;
		// short-circuit before the reader rejects it.
		return "", false
	}
	snap, _, _, err := s.prices.LatestPrice(ctx, asset, defaultPriceQuote)
	if err != nil {
		// Errors here include ErrPriceNotFound (asset has no USD
		// pair indexed). Either way, market_cap is unavailable.
		return "", false
	}
	if snap.Price == "" {
		return "", false
	}
	return snap.Price, true
}

// usdMarketValue computes amountStroops × usdPriceStr / 10^decimals,
// formatted as a decimal string with two fractional digits (USD
// cents). Pure big.Rat math — no float — so very large supplies +
// very small prices stay precise.
//
// Returns an error when usdPriceStr isn't a parseable decimal or
// decimals is negative. amountStroops==0 produces "0.00" (legitimate
// "asset has no circulating supply" reading, not an error).
func usdMarketValue(amountStroops *big.Int, usdPriceStr string, decimals int) (string, error) {
	if amountStroops == nil {
		return "", errors.New("usdMarketValue: amountStroops is nil")
	}
	if decimals < 0 {
		return "", fmt.Errorf("usdMarketValue: negative decimals (%d)", decimals)
	}
	price, ok := new(big.Rat).SetString(usdPriceStr)
	if !ok {
		return "", fmt.Errorf("usdMarketValue: parse price %q", usdPriceStr)
	}

	// amount × price
	valueRat := new(big.Rat).Mul(new(big.Rat).SetInt(amountStroops), price)

	// divide by 10^decimals
	if decimals > 0 {
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
		valueRat.Quo(valueRat, new(big.Rat).SetInt(divisor))
	}

	return valueRat.FloatString(2), nil
}
