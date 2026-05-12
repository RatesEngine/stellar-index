package supply

import (
	"math/big"
	"time"
)

// Basis identifies which policy produced a [Supply] value. Stable
// string values appear on the API response and in metric labels;
// renaming is a wire break.
type Basis string

const (
	// BasisXLMSDFReserveExclusion — Algorithm 1: native XLM with
	// SDF reserve accounts subtracted from total to yield
	// circulating.
	BasisXLMSDFReserveExclusion Basis = "xlm_sdf_reserve_exclusion"

	// BasisIssuerExclusion — Algorithm 2 default: classic credit
	// assets where circulating excludes the issuer's own balance.
	BasisIssuerExclusion Basis = "issuer_exclusion"

	// BasisAdminExclusion — Algorithm 3 default: SEP-41 tokens
	// where circulating excludes the admin account/contract balance.
	BasisAdminExclusion Basis = "admin_exclusion"

	// BasisOverride — operator-configured override beat the
	// algorithm-default policy. Used for both circulating
	// (extended locked-set) and max_supply.
	BasisOverride Basis = "override"

	// BasisNoMetadata — we don't have a defensible value for
	// at least one of total / circulating / max. Per ADR-0011
	// "we don't fabricate" — the corresponding field is nil.
	BasisNoMetadata Basis = "no_metadata"
)

// String returns the basis as a string for log lines + metric
// labels. Equivalent to a direct cast; provided for fluency.
func (b Basis) String() string { return string(b) }

// Supply is the wire-shape result of a supply derivation. Every
// per-algorithm computer in this package returns one of these.
//
// Field semantics:
//
//   - AssetKey is "XLM" for native, "CODE:G..." for classic, or the
//     bare contract id ("C...") for SEP-41 Soroban tokens. Matches
//     the asset_supply_history primary-key column.
//   - TotalSupply / CirculatingSupply are never nil; the algorithms
//     always have a defensible value (zero is a valid answer for an
//     asset that has been fully burned).
//   - MaxSupply is nil when no defensible value exists — uncapped
//     classic issuers with no SEP-1 declaration and no operator
//     override produce nil here. Per ADR-0011 we don't fabricate;
//     consumers handle nil explicitly (the API layer marshals it as
//     JSON null).
//   - Basis identifies which policy produced this Supply. Surfaced
//     on API responses so consumers know whether to trust the
//     absolute number.
//   - LedgerSequence + ObservedAt mark the ledger this snapshot
//     reflects. UTC; ledger close time, not write time.
type Supply struct {
	AssetKey          string
	TotalSupply       *big.Int
	CirculatingSupply *big.Int
	MaxSupply         *big.Int
	Basis             Basis
	LedgerSequence    uint32
	ObservedAt        time.Time

	// MinComponentLedger is the oldest ledger any per-component
	// observation contributing to this snapshot was last updated
	// at. F-1236 (codex audit-2026-05-12): the refresher uses
	// this to detect "snapshot stamped at fresh ledger N but
	// constructed from per-component observations as old as M"
	// and reject snapshots where (N - M) exceeds the operator-
	// configured stale-component threshold.
	//
	// Zero = "computer didn't populate" (legacy / non-storage-
	// backed computers like the static-config XLM reader). The
	// refresher treats zero as "no freshness signal" and falls
	// through to the legacy max-ledger semantics, matching the
	// pre-F-1236 posture for deployments that haven't wired
	// storage-backed readers yet.
	MinComponentLedger uint32
}
