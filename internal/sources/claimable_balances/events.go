package claimable_balances

import (
	"math/big"
	"time"
)

const (
	SourceName      = "claimable_balances"
	ObservationKind = "claimable_balances.observation"
)

// Observation is one ClaimableBalanceEntry-delta record.
// Identity is per-claimable-balance-id (not per-account) — a
// claimable balance has its own ledger entry that exists from
// creation until claim.
//
// Created / Updated / Restored variants populate ClaimableID +
// AssetKey + Balance. Removed variants are not emitted by this
// observer at v1 (see package doc).
type Observation struct {
	// ClaimableID is the BalanceID's hex form. Unique across the
	// network — a single claimable balance occupies one ledger
	// entry from creation until claim.
	ClaimableID string

	// AssetKey is the supply.AssetKey form (CODE:ISSUER for
	// classic credits) of the asset the claimable balance pays.
	AssetKey string

	Ledger     uint32
	ObservedAt time.Time

	// Balance is the post-change Amount in stroops. big.Int per
	// ADR-0003.
	Balance *big.Int

	// IsRemoval is reserved for future use when the writer-side
	// lookup path lands (per package doc); v1 always emits
	// IsRemoval=false.
	IsRemoval bool
}
