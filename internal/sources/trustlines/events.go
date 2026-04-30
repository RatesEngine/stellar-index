package trustlines

import (
	"math/big"
	"time"
)

// SourceName is the canonical identifier for the trustlines
// observer. Stamped on metric labels and on every
// [Observation.Source]. Stable.
const SourceName = "trustlines"

// ObservationKind is the consumer.Event.EventKind value emitted
// by the observer. The indexer's sink type-switches on this
// string to route observations to `trustline_observations`.
const ObservationKind = "trustlines.observation"

// Observation is one TrustLineEntry-delta record. Captures the
// post-change trustline state at the ledger that produced the
// change.
//
// One Observation per (account, asset, ledger) — when an account
// is touched multiple times in a single ledger, the writer's
// last-writer-wins ON CONFLICT path keeps the final state.
//
// Removed-variant changes set IsRemoval=true with Balance zeroed —
// the reader interprets this as "holder no longer has trustline
// at this ledger."
type Observation struct {
	// AccountID is the G-strkey of the trustline's holder.
	AccountID string

	// AssetKey is the supply.AssetKey form of the asset
	// (CODE:ISSUER for classic credits). Stamped at decode time
	// so the storage row carries it directly.
	AssetKey string

	// Ledger is the ledger sequence at which this delta landed.
	Ledger uint32

	// ObservedAt is the ledger close time, UTC.
	ObservedAt time.Time

	// Balance is the post-change trustline balance in stroops.
	// big.Int per ADR-0003.
	Balance *big.Int

	// IsRemoval is true when the change removed the trustline.
	// Balance is zero on these rows.
	IsRemoval bool
}
