package supply

import (
	"context"
	"math/big"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// MetadataResolver is the read-side interface the [Overlay]
// function uses to consult SEP-1 `[[CURRENCIES]].max_supply`
// declarations. Production implementation: a thin adapter around
// metadata.Cache wired in the binary.
//
// SEP1MaxSupply returns the raw decimal-string from the issuer's
// stellar.toml when present, ok=false when no declaration exists
// (no stellar.toml, no [[CURRENCIES]] entry, or the entry omits
// max_number / fixed_number). Errors propagate but the caller
// treats them as "no overlay applied" — better to publish nil than
// 5xx because of a TOML resolver blip.
//
// The resolver is responsible for its own caching; Overlay calls
// once per invocation.
type MetadataResolver interface {
	SEP1MaxSupply(ctx context.Context, asset canonical.Asset) (raw string, ok bool, err error)
}

// Overlay applies the SEP-1 max_supply precedence rule on top of a
// computed [Supply]. Per ADR-0011 the max_supply precedence chain is:
//
//  1. Operator override (Policy.MaxSupplyOverrides) — applied by
//     the per-algorithm Computer; surfaces here as snap.MaxSupply
//     already non-nil.
//  2. SEP-1 [[CURRENCIES]].max_supply declaration — applied by this
//     function when the operator override didn't fire.
//  3. nil — preserved.
//
// XLM (Algorithm 1) is hard-capped at total; its MaxSupply is
// always populated by the Computer and Overlay never modifies it.
// Returns applied=false in that case.
//
// When the resolver returns junk (negative value, unparseable
// string, etc.), Overlay does NOT apply — the SEP-1 declaration is
// respected as a *display value*, not a *source of truth*, and a
// junk declaration falling through silently is preferable to 5xx-ing
// the API for the affected asset. Operators surface stellar.toml
// junk through their own monitoring (separate alert path).
//
// Returns:
//   - the (possibly-modified) Supply
//   - applied=true iff the SEP-1 overlay set MaxSupply
//   - error only on resolver returns that are unambiguous bugs (e.g.
//     a non-nil error from SEP1MaxSupply with ok=true — contract
//     violation). Resolver-side errors propagate as
//     (snap-unchanged, applied=false, err) so the caller can decide
//     whether to log + continue or surface.
func Overlay(ctx context.Context, snap Supply, asset canonical.Asset, resolver MetadataResolver) (Supply, bool, error) {
	// XLM and assets with operator-override max already set —
	// nothing to do.
	if snap.AssetKey == xlmAssetKey || snap.MaxSupply != nil {
		return snap, false, nil
	}
	if resolver == nil {
		return snap, false, nil
	}

	raw, ok, err := resolver.SEP1MaxSupply(ctx, asset)
	if err != nil {
		return snap, false, err
	}
	if !ok || raw == "" {
		return snap, false, nil
	}

	val, parsed := new(big.Int).SetString(raw, 10)
	if !parsed {
		return snap, false, nil
	}
	if val.Sign() < 0 {
		// Negative max declared in stellar.toml is nonsensical;
		// don't apply — better to publish nil than a number that
		// asserts "this asset can never have any supply".
		return snap, false, nil
	}

	snap.MaxSupply = val
	// Basis upgrades to Override IF the algorithm-default basis was
	// in play. SEP-1 declaration is a non-derivative external claim,
	// same lane as the operator override per ADR-0011's "respected
	// as a display value" framing.
	if snap.Basis == BasisIssuerExclusion || snap.Basis == BasisAdminExclusion {
		snap.Basis = BasisOverride
	}
	return snap, true, nil
}
