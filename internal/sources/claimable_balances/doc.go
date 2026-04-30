// Package claimable_balances is the canonical
// ClaimableBalanceEntry observer per ADR-0022. Plugs into the
// dispatcher's LedgerEntryChange hook (#297) and emits one
// Observation per change touching a claimable balance whose
// asset matches an operator-watched classic credit asset.
//
// Operator usage: the same `[supply] watched_classic_assets`
// list used by the trustlines observer (#304) drives this one
// too. Match fast-path is type discriminator
// (LedgerEntryTypeClaimableBalance) + asset variant + asset_key
// map lookup.
//
// # Removed-variant scope
//
// The XDR LedgerKey for claimable balances carries only the
// BalanceId — not the asset. So a Removed change can't be
// asset-key-filtered at the observer level (we'd need the prior
// Created/Updated row's asset to know which watched asset this
// one belonged to). Two possible handlings:
//
//  1. Match returns false for all Removed changes. The Sum
//     query will overcount by the claimed-but-not-recorded
//     amount per watched asset. For circulating-supply this is
//     a CONSERVATIVE error (we under-report circulating, treating
//     claimed-and-gone-from-claimable as "still in claimable") —
//     better than over-reporting.
//  2. Match returns true for all Removed; the writer looks up
//     the prior asset_key from claimable_observations to
//     populate the removal row.
//
// This PR ships option 1. If/when the Sum overcount becomes
// measurable in production, option 2 lands as a writer-side
// follow-up — the observer interface stays unchanged.
//
// # Why classic-only
//
// Native (XLM) claimable balances exist but contribute to
// Algorithm 1, not Algorithm 2. The reader path for XLM doesn't
// consume claimable_observations.
package claimable_balances
