package supply

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// AssetKey produces the supply-package canonical key for a
// [canonical.Asset]. Output shape per ADR-0011:
//
//   - native XLM         → "XLM"
//   - classic (CODE:G…)  → "CODE:ISSUER"   (colon, matches the ADR
//     schema; canonical.Asset
//     uses dash for the API
//     surface, supply uses
//     colon for storage)
//   - SEP-41 Soroban     → "<contract_id>" (bare C-strkey)
//
// Off-chain assets (fiat, crypto-pure) have no on-chain supply we
// publish; AssetKey returns ("", error) for those — the supply
// package never derives values for them, so the key is meaningless.
func AssetKey(a canonical.Asset) (string, error) {
	switch a.Type {
	case canonical.AssetNative:
		return "XLM", nil
	case canonical.AssetClassic:
		return a.Code + ":" + a.Issuer, nil
	case canonical.AssetSoroban:
		return a.ContractID, nil
	case canonical.AssetFiat, canonical.AssetCrypto:
		return "", fmt.Errorf("supply: off-chain asset %q has no on-chain supply key", a.String())
	default:
		return "", fmt.Errorf("supply: unknown asset type %q", a.Type)
	}
}
