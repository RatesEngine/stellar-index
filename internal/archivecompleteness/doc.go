// Package archivecompleteness implements the daemon side of the
// dual-archive completeness contract specified in [ADR-0017].
//
// # Scope
//
// Two archives are checked:
//
//   - The PRIMARY archive — galexie-archive MinIO bucket, holding
//     per-ledger XDR meta files. Source of rate data.
//   - The CROSS-ANCHOR archive — `/srv/history-archive/`, a
//     traditional Stellar history archive. Used by verify-archive
//     to anchor each checkpoint against SDF's signed view.
//
// Both must be structurally complete for the API's downstream
// integrity guarantees to hold. The package implements all three
// ADR-0017 modes (check / fix / verify), driven by the
// `ratesengine-ops archive-completeness <mode>` subcommand and
// the `archive-completeness.{service,timer}` systemd units.
//
//   - [CrossAnchorChecker.Check] — read-only scan of the cross-
//     anchor archive's `ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz`
//     positions, returning a list of missing checkpoints.
//
//   - [Report] — the JSON wire shape that bundles results from
//     both archives; consumed by `fix` (multi-source fallback
//     fetcher that downloads missing bytes back into place) and
//     `verify` (chain-link + checkpoint-anchor integrity check).
//
// # Modes (all shipped)
//
//  1. `check` — read-only scan (cross-anchor + primary).
//     Cross-anchor is a native Go filesystem walk; primary is via
//     shell-out to `galexie detect-gaps`.
//  2. `fix` — fetches missing files via the multi-source fallback
//     chain (SDF mainnet → AWS public-blockchain → peers).
//  3. `verify` — chain-link + checkpoint-anchor verification of
//     the repaired archive.
//
// The `archive-completeness.timer` runs the daily steady-state
// guardrail (see `docs/operations/archive-completeness.md`).
//
// # Concurrency
//
// CrossAnchorChecker is safe for concurrent Check calls on
// different ranges. The underlying os.Stat doesn't mutate state.
//
// [ADR-0017]: ../../docs/adr/0017-archive-completeness-invariants.md
package archivecompleteness
