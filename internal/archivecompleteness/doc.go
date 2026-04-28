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
// integrity guarantees to hold. This package implements:
//
//   - [CrossAnchorChecker.Check] — read-only scan of the cross-
//     anchor archive's `ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz`
//     positions, returning a list of missing checkpoints.
//
//   - [Report] — the JSON wire shape that bundles results from
//     both archives. Per ADR-0017 PR A is `check` (read-only); the
//     `fix` / `verify` modes that follow consume the same Report.
//
// # Sequencing
//
// PR A (this package as initially shipped) provides:
//   - Cross-anchor structural scan (native Go filesystem walk)
//   - Report struct with one section per archive
//   - Primary structural scan via shell-out to `galexie detect-gaps`
//
// PR B will add native primary scanning + the `fix` mode (multi-
// source fallback fetcher that consumes a Report's missing-files
// list and writes the bytes back). PR C wires the `verify` mode
// (chain-link + checkpoint-anchor) and the systemd timer.
//
// # Concurrency
//
// CrossAnchorChecker is safe for concurrent Check calls on
// different ranges. The underlying os.Stat doesn't mutate state.
//
// [ADR-0017]: ../../docs/adr/0017-archive-completeness-invariants.md
package archivecompleteness
