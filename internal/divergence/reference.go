package divergence

import (
	"context"
	"errors"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Reference is a pluggable external price reference. Each
// implementation maps to one external oracle / aggregator
// (CoinGecko, CMC, Reflector, Band, etc.) and exposes one method
// to fetch the price the source reports for a given pair.
//
// Implementations:
//
//   - MUST be safe for concurrent LookupPrice calls
//   - SHOULD set a per-call timeout via the supplied ctx (caller
//     also bounds it but the implementation is the closest layer
//     to the network)
//   - SHOULD return [ErrAssetUnsupported] when the source doesn't
//     list the asset (so [Compare] can record the gap without
//     treating it as a transport failure)
type Reference interface {
	// Name returns a stable, lowercase, hyphenated label suitable
	// for Prometheus labels and JSON keys (e.g. "coingecko",
	// "coinmarketcap", "reflector-cex"). Stable across versions —
	// renaming is a wire break against the divergence-warning alerts.
	Name() string

	// LookupPrice returns the source's reported price for the
	// pair, denominated as `1 base = N quote`. observedAt is
	// the bucket-end timestamp the aggregator wants the price
	// for; sources that only support "current" prices ignore it
	// (acceptable when the bucket is recent — within minutes —
	// and the operator accepts the staleness on the divergence
	// signal).
	LookupPrice(ctx context.Context, pair canonical.Pair, observedAt time.Time) (float64, error)
}

// Sentinel errors returned by [Reference] implementations.
//
// Compare distinguishes these from generic transport errors so
// "asset not listed on this source" doesn't pollute the
// degradation signal. An asset that genuinely isn't listed on
// CoinGecko is information for the operator (consider adding the
// pair to the source's supported list, or accept a smaller
// reference universe for that pair); a transport failure is a
// transient outage signal.
var (
	// ErrAssetUnsupported — the source does not list the asset
	// (or the pair). Caller treats this as "no reference for this
	// pair on this source", not as a degradation.
	ErrAssetUnsupported = errors.New("divergence: asset unsupported by reference")

	// ErrPriceUnavailable — the source supports the asset but
	// has no recent price (vendor outage, stale feed). Treated
	// as a transient failure; surfaces in [Result.Failures].
	ErrPriceUnavailable = errors.New("divergence: price unavailable")
)
