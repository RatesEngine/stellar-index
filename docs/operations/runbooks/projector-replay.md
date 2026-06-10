---
title: Runbook — projector-replay
last_verified: 2026-05-29
status: ratified
severity: P3
---

# Runbook — `projector-replay` (operator subcommand)

## At a glance

| Field | Value |
| ----- | ----- |
| Trigger | Per-source projection is stale or missing rows for a known ledger range (e.g. post-decoder-fix re-walk). |
| Tool | `ratesengine-ops projector-replay --source <name> --from <ledger>` |
| Typical wall time | ≤ 5 s SQL + projector catch-up (≈ 1 min per 100k ledgers per source) |
| Impact | None — the projector tails `soroban_events` (ADR-0029); replay just rewinds a cursor. `ON CONFLICT DO NOTHING` makes re-writes idempotent. |

## Why this exists

ADR-0032 Phase 5 (rc.97) **deleted** the family of `*-backfill`
operator subcommands (`cctp-backfill`, `rozo-backfill`,
`soroswap-skim-backfill`, `comet-liquidity-backfill`,
`phoenix-backfill`, `blend-backfill`, `sep41-transfers-backfill`,
`drain-cascade-window`). They no longer exist. `projector-replay`
is the **only** catch-up path for projected (Soroban-derived)
sources — one cursor-rewind:

```sh
ratesengine-ops projector-replay -config /etc/ratesengine.toml \
  -source <name> -from <ledger>
```

The projector goroutine in `ratesengine-indexer` is already
tailing `soroban_events`; rewinding the per-source cursor makes it
re-project the requested window on its next cycle (≤ 5 s
projector interval). Per-source tables use ON CONFLICT DO NOTHING
so re-writes are idempotent.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Where is the projector's per-source cursor right now?
ssh root@136.243.90.96 'psql -U ratesengine -d ratesengine -c \
  "SELECT source, sub_source, last_ledger, updated_at FROM source_cursors \
   WHERE source = '"'"'projector'"'"' ORDER BY sub_source"'

# 2. What rows are actually present in the per-source table for
#    the range you want to backfill?
ssh root@136.243.90.96 'psql -U ratesengine -d ratesengine -c \
  "SELECT MIN(ledger), MAX(ledger), COUNT(*) FROM trades \
   WHERE source = '"'"'aquarius'"'"' AND ledger BETWEEN 62000000 AND 62100000"'

# 3. What rows are present in soroban_events for that range +
#    the per-source's topic? If there are events but no rows in
#    the per-source table, replay will populate. If no events,
#    nothing to do.
ssh root@136.243.90.96 'psql -U ratesengine -d ratesengine -c \
  "SELECT COUNT(*) FROM soroban_events \
   WHERE ledger BETWEEN 62000000 AND 62100000 AND topic_0_sym = '"'"'swap'"'"'"'
```

## Replay procedure

```sh
# Dry-run first to see what would happen.
ratesengine-ops projector-replay -config /etc/ratesengine.toml \
  -source aquarius -from 62000000 -dry-run

# Live.
ratesengine-ops projector-replay -config /etc/ratesengine.toml \
  -source aquarius -from 62000000
```

Source names match the projector registry
(`internal/projector/registry.go`):
`aquarius`, `soroswap`, `phoenix`, `comet`, `blend`, `cctp`, `rozo`,
`defindex`, `soroswap-skim`, `sep41-transfers`, `sep41-supply`,
`reflector-dex`, `reflector-cex`, `reflector-fx`, `redstone`.

## Verification

After replay, the projector cycle log lines (one per minute when
catching up) show progress:

```sh
ssh root@136.243.90.96 'journalctl -u ratesengine-indexer -n 100 -f | grep projector'
```

`projector_lag_ledgers{source="<name>"}` falls to 0 once the
replay is caught up to the live tip.

## Known false-positive patterns

- Asking the projector to replay a range earlier than the source's
  Soroban-genesis is a no-op — there are no events to project.
  Cursor still rewinds but the next cycle scans an empty range
  and advances back to the same toLedger.

## Related

- ADR-0032 — per-source tables as projections.
- ADR-0029 — soroban_events raw-event landing zone.
- `internal/projector/` — the projector implementation.
- [projector-lag](projector-lag.md) — companion runbook for
  the lag alerts.

## Changelog

- 2026-05-29 — initial draft (ADR-0032 Phase 5 rc.97).
