// Package trustlines is the canonical TrustlineEntry observer per
// ADR-0022. Plugs into the dispatcher's LedgerEntryChange hook
// (#297) and emits one Observation per change touching a
// trustline whose Asset matches an operator-watched classic
// credit asset.
//
// Operator usage: populate `[supply] watched_classic_assets` with
// the asset_keys (CODE:ISSUER form) you want trustline-component
// supply data for. The observer's Matches fast-path is type
// discriminator (LedgerEntryTypeTrustline) + asset_key map
// lookup; non-matching assets are skipped before any decode work.
//
// Output: [Observation] events flow through the dispatcher →
// consumer pipeline. The indexer-side sink writes each
// observation to `trustline_observations` (migration 0011).
// `internal/supply.StorageClassicSupplyReader` consumes
// `Store.SumTrustlineBalancesAtOrBefore` (defined in
// `internal/storage/timescale/classic_supply_observations.go`)
// for the trustline-component sum in Algorithm 2.
//
// Why classic-only:
//
//   - Native (XLM) — Algorithm 1, not Algorithm 2. The
//     AccountEntry observer (#298) already covers XLM holders.
//   - Pool-share trustlines (TrustLineAsset.LiquidityPoolId) —
//     LP shares aren't classic-asset trustlines; their reserves
//     come from the LP-reserve observer (Task #65).
//   - Credit alphanum4 / alphanum12 — Algorithm 2 source. This
//     observer covers them.
package trustlines
