package timescale

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// USDVolumeQuoteSpec lets the trade-insert path recognise on-chain
// quote assets the operator has declared as USD-pegged stablecoins,
// and supplies the per-asset decimals needed to compute usd_volume.
//
// The off-chain CEX/FX path doesn't need this — every external source
// stamps amounts at the uniform 10^8 scale and recognises peg via
// `aggregate.FiatProxy`'s crypto-ticker map. On-chain trades, however,
// stamp amounts at per-asset decimals and the quote can be:
//
//   - `AssetClassic{Code, Issuer}`, e.g. SDEX's USDC-GA5...
//   - `AssetSoroban{ContractID}`, e.g. Soroswap's USDC SAC contract
//     CCW6...
//
// Neither form maps to a global ticker; the operator declares which
// (CODE, ISSUER) pairs they trust as USD-pegged, and this spec
// resolves both the classic form and its SAC-wrapped counterpart
// transitively via the same `[supply.sac_wrappers]` map the supply
// pipeline already consumes.
//
// Phase 1 scope (launch-readiness L2.2 phase 1): USD-pegged
// stablecoins only, classic-decimal (7) only. This struct stays
// USD-only — the orthogonal Phase 2 path (F-1268
// audit-2026-05-12) lives in [VWAPUSDFXResolver] and covers
// non-USD pegs (EUR / MXN / etc.) by looking up
// `<quote>/<USD-peg>` in prices_1m at the trade's timestamp.
// SEP-41 tokens with non-classic decimals (rare on Stellar today)
// remain unsupported on either path — they'd need per-asset
// decimals plumbed through the trade insert path; not a current
// blocker.
type USDVolumeQuoteSpec struct {
	// classicUSDPegs is the set of classic asset_keys (in the
	// canonical "CODE-ISSUER" wire form) the operator has declared
	// as USD-pegged stablecoins.
	classicUSDPegs map[string]struct{}

	// sacToClassic is the SAC contract id → classic asset_key map
	// (in "CODE-ISSUER" form, normalised from the supply package's
	// "CODE:ISSUER" form). Used to resolve a Soroban-form quote
	// asset (Soroswap/Phoenix/Aquarius) back to its underlying
	// classic so the peg + decimals lookup can succeed.
	sacToClassic map[string]string
}

// NewUSDVolumeQuoteSpec composes the lookup. classicUSDPegs is the
// operator's `[trades].usd_pegged_classic_assets` list (each entry is
// the canonical "CODE-ISSUER" form for a classic credit). sacWrappers
// is the same map used by the supply pipeline:
// SAC contract id → "CODE:ISSUER" (the supply.AssetKey colon form).
//
// Returns an error when a classicUSDPegs entry doesn't parse as a
// classic asset — operator config validation; we want to fail at
// startup rather than silently ignore a typo'd asset_key that
// matches no trade.
func NewUSDVolumeQuoteSpec(classicUSDPegs []string, sacWrappers map[string]string) (*USDVolumeQuoteSpec, error) {
	out := &USDVolumeQuoteSpec{
		classicUSDPegs: make(map[string]struct{}, len(classicUSDPegs)),
		sacToClassic:   make(map[string]string, len(sacWrappers)),
	}
	for _, raw := range classicUSDPegs {
		asset, err := canonical.ParseAsset(raw)
		if err != nil {
			return nil, fmt.Errorf("usd-volume spec: classic peg %q: %w", raw, err)
		}
		if asset.Type != canonical.AssetClassic {
			return nil, fmt.Errorf("usd-volume spec: classic peg %q must be a classic asset (got %s)", raw, asset.Type)
		}
		out.classicUSDPegs[classicKey(asset)] = struct{}{}
	}
	for sacID, supplyKey := range sacWrappers {
		// supplyKey is the supply.AssetKey "CODE:ISSUER" colon form;
		// normalise to the dash form used in classicUSDPegs.
		asset, err := canonical.ParseAsset(supplyKey)
		if err != nil {
			return nil, fmt.Errorf("usd-volume spec: sac_wrapper for %s (%q): %w", sacID, supplyKey, err)
		}
		if asset.Type != canonical.AssetClassic {
			return nil, fmt.Errorf("usd-volume spec: sac_wrapper for %s (%q) must point at a classic asset (got %s)", sacID, supplyKey, asset.Type)
		}
		out.sacToClassic[sacID] = classicKey(asset)
	}
	return out, nil
}

// QuoteUSDPegInfo, when ok=true, says: this quote asset is on the
// operator's USD-pegged list and the trade's quote_amount should be
// divided by 10^decimals to obtain its USD value (using the trusted
// 1.0 peg). When ok=false, the quote isn't recognised — the caller
// stores usd_volume as NULL.
//
// Returns ok=false for nil spec (no operator config supplied) and
// for SEP-41 contracts that aren't in sacWrappers (pure SEP-41
// stablecoins are phase-2 work).
func (s *USDVolumeQuoteSpec) QuoteUSDPegInfo(asset canonical.Asset) (decimals int, ok bool) {
	if s == nil {
		return 0, false
	}
	switch asset.Type {
	case canonical.AssetClassic:
		if _, pegged := s.classicUSDPegs[classicKey(asset)]; pegged {
			// Stellar classic credits are uniformly 7-decimal.
			return 7, true
		}
	case canonical.AssetSoroban:
		// Resolve the SAC contract id back to its underlying classic.
		// If the resolved classic is on the operator's USD-pegged
		// list, the SAC inherits the peg + the 7-decimal scale.
		if classicKeyStr, ok := s.sacToClassic[asset.ContractID]; ok {
			if _, pegged := s.classicUSDPegs[classicKeyStr]; pegged {
				return 7, true
			}
		}
	}
	return 0, false
}

// classicKey renders a classic Asset as "CODE-ISSUER" (the canonical
// wire form, matching how operators write asset keys in TOML and
// consistent with `[supply].watched_classic_assets`).
func classicKey(a canonical.Asset) string {
	return a.Code + "-" + a.Issuer
}
