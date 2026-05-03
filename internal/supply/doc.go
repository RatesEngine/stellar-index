// Package supply derives total / circulating / max supply for every
// asset class the engine indexes, per ADR-0011.
//
// Stellar's asset model has three structurally-different domains
// that need three different algorithms — all three ship today:
//
//  1. Native XLM — fixed: 50_001_806_812 * 10^7 stroops, frozen by
//     network vote in October 2019. Only the SDF-reserve exclusion
//     changes circulating. Implemented by [XLMComputer] in xlm.go.
//
//  2. Classic credit assets (CODE:ISSUER) — issuer-authoritative;
//     reconstructed from Galexie ledger meta. Total = Σ trustline +
//     Σ claimable + Σ LP-reserve + Σ SAC-wrapped. Implemented by
//     [ClassicComputer] in classic.go (per ADR-0022). Reads from
//     the per-class observers shipped at
//     [internal/sources/{trustlines,claimable_balances,liquidity_pools,sac_balances}].
//
//  3. SEP-41 Soroban tokens — event-defined: Σ mint − Σ burn −
//     Σ clawback over the contract's lifetime. Implemented by
//     [SEP41Computer] in sep41.go (per ADR-0023). Reads from the
//     SEP-41 supply-event observer at
//     [internal/sources/sep41_supply].
//
// All amounts are *big.Int end-to-end (i128 safety per ADR-0003).
// Wire-form serialisation is decimal-string; JSON encoding lives at
// the API boundary, not here.
//
// The operator-configurable [Policy] supplies the SDF reserve
// account list, per-asset locked-set overrides for circulating-
// supply derivation, and operator overrides for max_supply.
//
// Package surface:
//
//   - [Supply] — the result type every Computer returns.
//   - [Basis] — string identifier for which policy produced the
//     numbers, exposed on the API response.
//   - [Policy] / [LockedSet] — operator configuration.
//   - [XLMComputer] / [ClassicComputer] / [SEP41Computer] — the
//     three algorithms.
//   - [Refresher] — the periodic snapshot worker the aggregator
//     binary runs to update `asset_supply_history`.
//   - [StorageClassicSupplyReader] / [StorageSEP41SupplyReader]
//     — Postgres-backed sums over the per-class observer
//     hypertables (migrations 0011–0014, 0017).
//   - [CrosscheckRefresher] — SAC-wrapped classic ↔ SEP-41
//     cross-check (operator monitoring hook); textfile-format
//     snapshot writer at [WriteSnapshotTextfile] for the
//     ratesengine-ops `supply-snapshot` timer.
package supply
