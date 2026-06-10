---
adr: 0030
title: Per-source coverage invariant
status: Accepted
date: 2026-05-28
supersedes: []
superseded_by: null
---

# ADR-0030 — Per-source coverage invariant

**Status:** Accepted (2026-05-28)
**Context wave:** rc.87
**Supersedes:** —
**Superseded by:** —

## Context

The F-0020 cascade-window incident (Postgres back-pressure halted the soroban_events writer across 103,396 contiguous ledgers, ~14 h of network time) went undetected for the entire outage because every existing coverage signal measured **process state**, not **data state**:

- The cursor-derived density projection in `/v1/diagnostics/ingestion` read 100% because the live cursor's `last_ledger` advanced past the gap window once back-pressure cleared.
- The per-source `*_total{outcome="ok"}` counters kept incrementing once the writer recovered; no metric captured the "what was missed during the halt" semantic.
- The smoke-script + per-binary heartbeat signals are alive/dead bits, not coverage measurements.

The rc.85 / rc.86 series shipped the response: an honest cursor-derived density, a periodic data-derived gap detector against `soroban_events`, and a `find-data-gaps` operator CLI. Then rc.87 generalised the gap detector to every per-source hypertable (PR #2), added the orchestrator for cascade remediation (PR #1), and registered SDEX as the 14th target so the classic-DEX path has symmetric coverage (PR #3).

But the response itself created a new failure mode: a future engineer adding a per-source hypertable could forget to register the target. The system would silently regress to the pre-F-0020 state for that new source — no signal, no alert, no honest density.

## Decision

**Every per-source hypertable MUST be registered as a `GapDetectorTarget` in `internal/storage/timescale/per_source_gaps.go` within the same PR that creates it.** The Go test `TestGapDetectorTargetsCoverAllPerSourceHypertables` makes this binding by introspecting `migrations/*.up.sql` for `CREATE TABLE <name>` statements matching the per-source naming pattern (`*_events|*_liquidity|*_positions|*_emissions|*_admin|*_transfers|*_swaps|*_stake_events|*_supply_events|*_auctions`) and failing CI if any unregistered table is found.

A table can be exempted only by adding it to `excludedFromGapDetector` with a documented prose reason — "leftover from refactor" is not a valid reason; delete the entry or the table instead. Current exemptions: `freeze_events`, `mev_events`, `api_usage_events` (each a system-state table, not per-source ingest).

**Three additional binding sub-decisions:**

1. **Headline density MUST be data-derived, not cursor-derived.** The `/v1/diagnostics/ingestion` handler's per-source `density_pct` already came from cursor coverage; rc.85 cleaned up the NULL→genesis fallback but the underlying source is still cursors. The next step (separate PR, scoped for rc.88+) is to surface the data-derived gap gauge values in the same response, so consumers (status page, dashboards) read the honest signal directly.

2. **Coverage labels are `{source, table}`, not just `{source}`.** A single source may span multiple tables (Blend has three: `blend_positions` + `blend_emissions` + `blend_admin`). Per-table granularity is necessary for diagnostic depth; alerts aggregate via `max by (source)` to preserve paging-dedup behaviour.

3. **No table-name interpolation from user input.** Identifier values fed to `FindPerSourceLedgerGaps` (`Table`, `LedgerColumn`, `WhereFilter`) MUST come from `DefaultGapDetectorTargets` (a compile-time const). The query string-interpolates them because Postgres doesn't support `$N` binding for identifiers; the safety comes from upstream provenance, not from the SQL builder. This is the same pattern as the existing schema migration runner.

## Consequences

**Positive:**

- A future cascade in any per-source table fires the same paging alert within ~45-60 min of formation. No new alerts to add per source.
- The lint guard makes adding a new Soroban DeFi source a one-PR operation: migration + decoder + target registration + projector source registration, all in one mental unit. CI enforces the discipline.
- The exclusion list documents *why* a table is exempt; future engineers don't have to guess whether `freeze_events` was forgotten or deliberate.

**Negative:**

- Adding a per-source hypertable now requires touching `per_source_gaps.go` even if the engineer doesn't otherwise care about gap detection. This is the intended friction.
- The per-cycle scan time grows linearly with target count; 13 targets × 30s = ~7 min today, fits inside the 30-min cadence with headroom. Beyond ~30 targets (or if the per-target scans grow), revisit the `soroban_event_ledgers` materialised-view optimisation noted in `gap_detector.go`.
- The `WhereFilter` field on `GapDetectorTarget` is an attack surface if a future engineer puts user-controlled data into it. The godoc + ADR call this out, but the linter doesn't enforce it. Mitigation: keep the field's use to the existing single instance (`sdex` filtering by `source = 'sdex'`) unless a new use case justifies the audit.

## Alternatives considered

- **Per-source data-derived density in the diagnostic handler instead of a separate gap-detector worker.** Would consolidate the signal but couples the API handler to the (slow) LAG scan. Rejected: the worker pattern lets us bound the scan to a 30-min cadence, while a handler call would either block requests or require its own cache layer.

- **Single `{source}` label, sum across multiple tables in the worker.** Simpler metric set, but loses the per-table diagnostic detail. Rejected: when the F-0020-class incident hits *one of Blend's three tables* but not the others, the source-aggregate gauge would still light up — but the operator would have no signal for *which* table to drain. Per-table labels remove a layer of guessing.

- **Manual target list, no lint guard.** Easier to ship, but exactly the failure mode F-0020 inflicted: a new table can drift into existence without coverage. Rejected: the whole point of this ADR is to make the discipline binding, not optional.

## Reference

- `internal/storage/timescale/per_source_gaps.go` — target registry + scan function
- `internal/storage/timescale/gap_detector.go` — periodic worker
- `internal/storage/timescale/gap_targets_test.go` — lint guard
- `docs/operations/runbooks/ingest-gap-detected.md` — operator response
- `docs/operations/runbooks/cascade-window-drain.md` — orchestrator runbook
- `docs/operations/runbooks/sdex-gap-detected.md` — SDEX-specific surface
- F-0020 (audit-2026-05-26) — motivating incident
