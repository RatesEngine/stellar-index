package v1

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/supply"
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

// Change24hReader returns the asset's USD price as of approximately
// 24 hours ago, used by /v1/assets/{id}.change_24h_pct to compute
// the trailing percentage change against the current USD price.
//
// Production implementation: thin adapter around
// timescale.Store.ClosedVWAP1mAtOrBefore — picks the latest closed
// prices_1m bucket whose bucket-end is ≤ now-24h.
//
// Returns [ErrChange24hUnavailable] when no closed bucket exists in
// the lookback window (e.g. the asset was first traded < 24h ago,
// or trade history was retention-pruned). The handler treats that
// as "feature unavailable for this asset" — change_24h_pct stays
// null, no error logged, the asset-detail body still serves.
//
// Real errors (Postgres unreachable, parse failures) propagate
// as-is so the handler can log them at WARN.
type Change24hReader interface {
	USDPrice24hAgo(ctx context.Context, asset canonical.Asset) (string, error)
}

// ErrChange24hUnavailable is what [Change24hReader.USDPrice24hAgo]
// returns when no comparison price exists in the 24h-ago window.
// Distinct from a real Postgres / parse error — silent fall-through
// for the handler.
var ErrChange24hUnavailable = errors.New("api: change_24h_pct comparison price unavailable")

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
//
// Scope caveat: per launch-readiness L2.2 phase 1, on-chain DEX
// trades populate `usd_volume` when their quote asset is in the
// operator's `[trades].usd_pegged_classic_assets` allow-list (or
// its SAC wrapper, transitive via `[supply.sac_wrappers]`). Other
// on-chain trades — non-USD-pegged quotes, or USD-pegged classics
// not in the allow-list — store NULL and contribute 0 to this
// reader's sum. The OpenAPI surface carries the same caveat.
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

	// The F2 overlay fans out to up to four independent DB-bound
	// reads — 24h volume, 24h change, USD price, supply snapshot.
	// They were historically run serially; each is 50ms–2s, so a
	// cold /v1/assets/{id} paid the SUM. They touch disjoint
	// AssetDetail fields (volume/change/price each write one field;
	// the snapshot writes none), so they run concurrently here and
	// the cold cost collapses to the slowest single read. Every
	// populate* helper is individually best-effort — a failure logs
	// and leaves its field null — so no error plumbing is needed.
	//
	// IMPORTANT: pass asset.String() to populateVolume24h — the
	// canonical wire form trades.base_asset stores — NOT
	// supply.AssetKey(asset). The two diverge for native: AssetKey
	// returns "XLM" (ADR-0011), trades stores "native". A
	// pre-2026-05-04 bug passed the supply key and the volume
	// lookup never matched the trade rows.
	var (
		snap     supply.Supply
		haveSnap bool
		wg       sync.WaitGroup
	)
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These run on child goroutines, which middleware.Recoverer
			// does NOT cover — an unrecovered panic here takes down the
			// whole API process, not just this request. Each populate*
			// is best-effort (failure → field stays null), so a panic
			// degrades to the same: log it and leave the field unset.
			defer func() {
				if p := recover(); p != nil {
					s.logger.Error("panic in asset-detail field populator", "panic", p)
				}
			}()
			fn()
		}()
	}
	run(func() { s.populateVolume24h(ctx, detail, asset.String()) })
	run(func() { s.populateChange24h(ctx, detail, asset) })
	// F-1271: inline price_usd independent of supply availability so
	// wallet UIs that just want the price don't pay a second /v1/price
	// RT. populateMarketCap (phase 2) re-uses detail.PriceUSD.
	run(func() { s.populatePriceUSD(ctx, detail, asset) })
	// Supply snapshot only when a supply reader is wired and the
	// asset has a supply key — off-chain assets (fiat / crypto-pure)
	// have no snapshot, matching the pre-parallelisation early-return.
	if s.supply != nil && keyErr == nil {
		run(func() { snap, haveSnap = s.fetchSupplySnapshot(ctx, key) })
	}
	wg.Wait()

	// Phase 2 — pure compute (no DB), so it runs on this goroutine
	// once the parallel reads have joined. populateMarketCap reads
	// detail.PriceUSD (set by populatePriceUSD) + the snapshot; the
	// wg.Wait barrier makes both safely visible.
	if haveSnap {
		populateSupplyFields(detail, snap)
		s.populateMarketCap(ctx, detail, asset, snap, key)
	}
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

// populatePriceUSD inlines detail.PriceUSD via the lookupUSDPrice
// path. Idempotent: if the coins-overlay or another caller already
// set PriceUSD, this is a no-op (the two paths can't fight). F-1271
// (audit-2026-05-12).
func (s *Server) populatePriceUSD(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	if s.prices == nil || detail.PriceUSD != nil {
		return
	}
	usdPrice, ok := s.lookupUSDPrice(ctx, asset)
	if !ok {
		return
	}
	priceCopy := usdPrice
	detail.PriceUSD = &priceCopy
}

// populateMarketCap fills market_cap_usd + fdv_usd from the supply
// snapshot and the already-populated detail.PriceUSD. Re-uses the
// inlined price (set by populatePriceUSD or the coins-overlay path)
// to avoid a second prices_1m lookup. Compute failures log at WARN;
// the field stays nil so the rest of the body still serves cleanly.
func (s *Server) populateMarketCap(ctx context.Context, detail *AssetDetail, asset canonical.Asset, snap supply.Supply, key string) {
	if detail.PriceUSD == nil {
		// populatePriceUSD ran first and didn't find a price (no
		// prices reader wired OR lookupUSDPrice returned !ok).
		// market_cap / fdv have no value to compute against.
		return
	}
	usdPrice := *detail.PriceUSD
	_ = ctx
	_ = asset
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
//
// On the canonical Stellar deployment NO on-chain trade quotes in
// fiat:USD — every USD-flavoured trade quotes in classic USDC or
// one of the operator's other declared pegs. So a literal
// LatestPrice(asset, fiat:USD) lookup against the prices_1m CAGG
// returns ErrPriceNotFound for nearly every asset on-chain, and the
// downstream F2 fields (market_cap_usd, fdv_usd, change_24h_pct)
// silently stay null on /v1/assets/{id}. We fix this by mirroring
// the handler's tryStablecoinFiatProxy fallback at the reader-call
// level: when the literal lookup misses, walk the operator's
// usd_pegged_classic_assets and rewrite asset/fiat:USD to
// asset/<peg>. Same shape as the handler-side fallback in #1217;
// here it lives at the F2-population layer where the handler's
// priceFallback isn't reachable (the supply / change-24h paths
// bypass the /v1/price handler entirely).
func (s *Server) lookupUSDPrice(ctx context.Context, asset canonical.Asset) (string, bool) {
	if s.prices == nil {
		// Options documents Prices as independently optional ("nil →
		// 503"); populatePriceUSD guards this, but populateChange24h
		// reaches us via a different path. Guard here so a
		// Prices==nil,Change24h!=nil wiring can't nil-panic.
		return "", false
	}
	if asset.Equal(defaultPriceQuote) {
		// fiat:USD priced against fiat:USD is meaningless;
		// short-circuit before the reader rejects it.
		return "", false
	}
	snap, _, _, err := s.prices.LatestPrice(ctx, asset, defaultPriceQuote)
	if err == nil && snap.Price != "" {
		return snap.Price, true
	}
	// Read-time stablecoin-fiat proxy fallback (matches the
	// handler-side fix in #1217 / tryStablecoinFiatProxy).
	if proxy, _, ok := s.tryStablecoinFiatProxy(ctx, asset, defaultPriceQuote); ok && proxy.Price != "" {
		return proxy.Price, true
	}
	return "", false
}

// populateChange24h fills detail.Change24hPct via the
// [Change24hReader] and the existing [PriceReader] used by
// market_cap. Best-effort: no current USD price OR no 24h-ago
// comparison bucket leaves the field null. Real Postgres errors
// log at WARN; all other failure modes are silent.
//
// We deliberately ignore the asset==fiat:USD short-circuit that
// market_cap uses — pricing USD-against-USD over time is
// meaningless, lookupUSDPrice already returns ("", false) for
// that case, and the early-return below kicks in without logging.
func (s *Server) populateChange24h(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	if s.change24h == nil {
		return
	}
	currStr, ok := s.lookupUSDPrice(ctx, asset)
	if !ok {
		return
	}
	thenStr, err := s.change24h.USDPrice24hAgo(ctx, asset)
	if errors.Is(err, ErrChange24hUnavailable) {
		// Asset first traded < 24h ago, or retention pruned the row.
		// Silent — feature unavailable for this asset.
		return
	}
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("change_24h_pct lookup failed", "err", err, "asset", asset.String())
		}
		return
	}
	pct, err := pctChange(currStr, thenStr)
	if err != nil {
		s.logger.Warn("change_24h_pct compute failed",
			"err", err, "asset", asset.String(), "now", currStr, "then", thenStr)
		return
	}
	detail.Change24hPct = &pct
}

// pctChange returns `(now - then) / then * 100` as a signed decimal
// string with two fractional digits — e.g. "+1.27", "-0.05",
// "0.00". Both inputs must parse as decimals; `then` must be > 0
// (a zero anchor would divide-by-zero, and a negative price is
// nonsensical for an asset).
//
// Pure big.Rat math — no float — so very large or very small
// prices stay precise. The leading "+" on positive deltas is
// emitted explicitly so consumers can distinguish "0.00" (no
// change) from a missing field.
func pctChange(nowStr, thenStr string) (string, error) {
	now, ok := new(big.Rat).SetString(nowStr)
	if !ok {
		return "", fmt.Errorf("pctChange: parse now %q", nowStr)
	}
	then, ok := new(big.Rat).SetString(thenStr)
	if !ok {
		return "", fmt.Errorf("pctChange: parse then %q", thenStr)
	}
	if then.Sign() <= 0 {
		return "", fmt.Errorf("pctChange: then %q must be > 0", thenStr)
	}
	delta := new(big.Rat).Sub(now, then)
	pct := new(big.Rat).Quo(delta, then)
	pct.Mul(pct, big.NewRat(100, 1))
	out := pct.FloatString(2)
	// Lead positives with "+" so the wire format distinguishes
	// up-moves from down-moves visually. Suppress the prefix when
	// the rounded output is "0.00" — a sub-cent positive delta
	// reads as flat at two decimals, and showing "+0.00" misleads
	// consumers that render the leading sign.
	if pct.Sign() > 0 && out != "0.00" {
		out = "+" + out
	}
	return out, nil
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
