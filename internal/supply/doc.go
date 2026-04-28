// Package supply derives total / circulating / max supply for every
// asset class the engine indexes, per ADR-0011.
//
// Stellar's asset model has three structurally-different domains
// that need three different algorithms:
//
//  1. Native XLM — fixed: 50_001_806_812 * 10^7 stroops, frozen by
//     network vote in October 2019. Only the SDF-reserve exclusion
//     changes circulating. Implemented by [XLMComputer] in xlm.go.
//
//  2. Classic credit assets (CODE:ISSUER) — issuer-authoritative;
//     reconstructed from Galexie ledger meta. Total = Σ trustline +
//     Σ claimable + Σ LP-reserve + Σ SAC-wrapped. Future PR.
//
//  3. SEP-41 Soroban tokens — event-defined: Σ mint − Σ burn −
//     Σ clawback over the contract's lifetime. Future PR.
//
// All amounts are *big.Int end-to-end (i128 safety per ADR-0003).
// Wire-form serialisation is decimal-string; JSON encoding lives at
// the API boundary, not here.
//
// The operator-configurable [Policy] supplies the SDF reserve
// account list, per-asset locked-set overrides for circulating-
// supply derivation, and operator overrides for max_supply.
//
// Package surface (current):
//
//   - [Supply] — the result type every Computer returns.
//   - [Basis] — string identifier for which policy produced the
//     numbers, exposed on the API response.
//   - [Policy] / [LockedSet] — operator configuration.
//   - [XLMComputer] — Algorithm 1, native XLM.
//
// Future PRs add: ClassicComputer, SEP41Computer, Postgres-backed
// Store + asset_supply_history hypertable migration, SAC-wrapped
// cross-check.
package supply
