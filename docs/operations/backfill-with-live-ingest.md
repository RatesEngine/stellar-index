---
title: Running backfill + verify-archive alongside live ingest
last_verified: 2026-05-28
status: ratified
---

# Running backfill + verify-archive alongside live ingest

Audit finding F-0020 (audit-2026-05-26) recorded a 7 h
on-chain-trade ingest freeze caused by Postgres back-pressure
while a 12-way parallel `soroban-events` fill walk + a 12-chunk
verify-archive bootstrap were running concurrently against the
same Postgres cluster. The live indexer's write path back-pressured
to halt because every available connection was busy with the
heavy walks.

This page documents the operating posture that prevents recurrence
and the alerts that page if it happens anyway.

## Posture

**Default operating posture:** the live indexer has Postgres
to itself. Backfill, verify-archive, and any other
write-heavy walks run during dedicated maintenance windows
when the live cursor is allowed to lag.

Concretely:

- **Live indexer always running** at full priority. Its cursor
  must advance every ledger.
- **Backfill walks (`ratesengine-ops backfill`)**: run at
  `-parallel 4` or lower when live ingest is also writing.
  `-parallel 12` is for catch-up windows where live ingest is
  paused or running on a fresh box.
- **verify-archive Tier A**: scheduled by
  `verify-archive-tier-a.timer` for off-peak windows (default:
  Sunday 02:00 UTC). Operators triggering an ad-hoc tier-A run
  should `systemctl stop ratesengine-indexer` first OR
  `-chunks 4` it down from the bootstrap's 12.
- **Galexie + ledgerstream-fill** runs continuously in the
  background but pulls from MinIO, not Postgres — these don't
  contribute to the back-pressure pattern.

## Why this matters

The W28 back-pressure design ensures cursor coherence on the
*fill* walk (no row lands in `soroban_events` after its cursor
has been recorded as advanced past it). That guarantee is held
by blocking the fill walk's producer when its sink is full.
When the live indexer shares Postgres with that sink, the live
indexer's writes also block — which is what F-0020 observed.

The data-correctness invariant is right; the resource-priority
arrangement was wrong (live indexer should have higher priority
than fill walks).

## Alerts that page on recurrence

Even without operator vigilance the recurrence pattern is now
alertable:

| Signal | Alert | Page |
| --- | --- | --- |
| Live cursor stalls | `ratesengine_ingestion_source_insert_stale` | P2 |
| Live indexer keeps inserting duplicates only | `ratesengine_ingestion_duplicate_flood` | P2 |
| Aggregator output stops | `ratesengine_aggregator_silent` | P1 |
| Per-asset staleness > 120 s | `ratesengine_api_price_stale` | P2 |
| Postgres connection pool saturated | `ratesengine_postgres_connections_high` | P2 |

The first two were shipped in this session (tasks #61 / #62 /
#67) specifically to surface the F-0020 pattern at first
observation. Pre-this-session those signals didn't exist and the
freeze was visible only through manual `psql max(ts)` queries
during the audit.

If any of the alerts above fire while a fill walk or
verify-archive run is in progress: assume back-pressure unless
proven otherwise, and stop the heavy walker first.

## Operator commands

### Stop a running fill walk

```sh
# On r1 — find the fill PID. The fill is a manual operator invocation
# (`ratesengine-ops backfill -source soroban-events`), NOT a systemd unit;
# there is no soroban-events-fill.service.
ps -eo pid,args | grep '[r]atesengine-ops backfill'
# kill -INT by the EXPLICIT PID (graceful — drains in-flight rows then exits).
# Do NOT `pkill -f 'backfill'`: the pattern self-matches your own shell over
# ssh and can kill the wrong process.
kill -INT <pid>
```

Either path lets the in-flight batch finish so cursor coherence
is preserved. SIGKILL works too but loses the in-flight batch's
rows; only use if `pkill -INT` doesn't return within 60 s.

### Stop a running verify-archive

```sh
systemctl stop verify-archive-tier-a.service
```

The timer remains armed; the next scheduled fire still happens.

### Resume the live indexer after a freeze

```sh
# The indexer should be running; the freeze symptom is "cursor
# not advancing" not "process not running". Confirm:
systemctl status ratesengine-indexer
# Cursor lag check:
curl -sS http://localhost:3000/v1/diagnostics/cursors \
  | jq '.data[] | select(.source=="ledgerstream") | .lag_seconds'
# Should be under 30 s in steady state.
```

If lag stays high after the back-pressure source is stopped,
restart the indexer:

```sh
systemctl restart ratesengine-indexer
journalctl -u ratesengine-indexer -f
```

## Long-term architecture options

These aren't shipped yet but are the candidate paths the audit
named:

1. **Per-sink prioritisation in AsyncSink.** Live ingest writes
   take Postgres connections ahead of fill walks. Implementation
   cost: moderate (need a priority queue or per-sink connection
   budget); risk: medium (gets into Postgres-pool semantics).
2. **Separate read-replica for fill walks.** The fill walks read
   nothing from Postgres today (they're producers) but a
   replica would let the WAL replay decouple primary writes from
   fill-walk writes via logical replication. Implementation
   cost: high; risk: medium-high (replication-lag handling).
3. **Postgres connection-pool reservation.** Reserve N
   connections for the live indexer; backfill workers can use
   only the remaining (max_connections - N). Cheapest path;
   implementable today via per-binary `DATA_SOURCE_NAME` with
   distinct pool sizes. Doesn't solve write-lock contention
   inside Postgres itself but does prevent connection
   starvation.

The current alert-shipped posture is "operationally well-
behaved AND alertable on recurrence." Architecture path (3) is
the next-cheapest hardening; (1) and (2) are tracked under
W28 / W30 for future work.

## Cross-reference

- F-0020 (audit-2026-05-26) — original finding.
- F-0028 — soroban_events lag (same back-pressure cluster).
- W28 — back-pressure design.
- W30 — cold-tier interaction with backfill.
- `docs/operations/runbooks/ingestion-duplicate-flood.md` — the
  paged-alert runbook for the recurrence detector.

## Changelog

- 2026-05-28 — initial draft (F-0020 closure).
