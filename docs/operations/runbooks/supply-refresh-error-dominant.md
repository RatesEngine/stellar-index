---
title: Runbook — supply-refresh-error-dominant
last_verified: 2026-06-11
status: ratified
severity: P3
---

# Runbook — `ratesengine_aggregator_supply_refresh_error_dominant`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_supply_refresh_error_dominant` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/supply-refresh.yml` |
| Typical MTTR | 15–60 min |
| Impact | `/v1/assets/{id}` F2 fields are stale OR wrong for affected assets — the previous snapshot stays in `asset_supply_history`, so consumers see correct-but-old data. |

## Symptoms

- `> 50%` of `ratesengine_aggregator_supply_refresh_total` ticks
  have `outcome != "ok"` for ≥ 30 min.
- Aggregator logs show repeated `supply refresh: <outcome>` lines
  with the same outcome label.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Which outcome dominates?
curl -s http://aggregator:9464/metrics | \
  grep ratesengine_aggregator_supply_refresh_total | \
  sort -t' ' -k2 -rn | head

# 2. Per-asset breakdown — does the failure track one asset or all of them?
#    The metric carries an `asset_key` label (added in #314); split by it
#    in PromQL or directly off /metrics:
curl -s http://aggregator:9464/metrics | \
  awk '/^ratesengine_aggregator_supply_refresh_total\{/' | \
  sort
# Equivalent PromQL for dashboards:
#   sum by (asset_key, outcome) (
#     rate(ratesengine_aggregator_supply_refresh_total[15m])
#   )
# If one asset_key dominates the non-ok rate while others are healthy, the
# fault is per-asset (config drift on watched_classic_assets, missing
# locked-set member, SAC wrapper map gap) rather than fleet-wide.
#
# NOTE: outcome="dormant" is NOT an error — it means the asset is quiet and
# its last observation was (correctly) re-stamped as current (F-1320). It
# does NOT count toward the error fraction; do not chase it. A SUSTAINED
# outcome="stale_component" on one asset IS the actionable signal (see the
# per-outcome section below).

# 3. Logs corroborate per-asset failures with the wrapped error text.
sudo journalctl -u ratesengine-aggregator --since "30 min ago" -n 200 | \
  grep "supply refresh: " | sort | uniq -c | sort -rn | head

# 4. Sanity-check the aggregator config.
grep -A 10 "^\[supply" /etc/ratesengine.toml
```

## Typical root causes (split by dominant outcome)

### `outcome="no_ledger"`

The aggregator can't resolve the latest ledger from
`ingestion_cursors`. Either the indexer hasn't produced its
first cursor, or the table is unreachable.

- Signal: `_no_ledger` increments far exceed any other outcome.
- Mitigation: confirm the indexer is running + writing cursors;
  if storage is broken, route to `pg-conns-saturated.md`.

### `outcome="no_observation"`

The chain reader (live LCM) returned no observation for at
least one watched asset, AND the operator-static fallback was
also empty. Most common after a fresh deploy: the
AccountEntry observer hasn't backfilled to a deep enough range
yet.

- Signal: `_no_observation` increments dominate; per-asset logs
  identify which watched accounts are uncovered.
- Mitigation: either (a) wait for the observer's backfill to
  catch up (typical: hours-to-days for the configured
  watched-set), (b) populate the operator-static config blocks
  (`[supply.reserve_balances_stroops]`, `[metadata.issuer_home_domains]`)
  as a bridge until the observer covers the live set.

### `outcome="compute_error"`

The supply algorithm itself failed. For Algorithm 1 (XLM) this
means the reserve reader or the XLMComputer threw. For
Algorithm 2 (classic) it means one of the four component sums
failed. For Algorithm 3 (SEP-41) it means the kind-totals
query failed.

- Signal: `_compute_error` increments in the absence of
  no_observation / no_ledger.
- Mitigation: check the per-asset logs for the wrapped error;
  this is typically a code bug or a config inconsistency
  (asset not parseable, etc.). Roll back the binary if it's a
  recent deploy.

### `outcome="write_error"`

`Store.InsertSupply` failed. Postgres unreachable, NUMERIC
overflow on a malformed amount, etc.

- Signal: `_write_error` increments.
- Mitigation: confirm Postgres is reachable; check the
  asset_supply_history table for CHECK-constraint violations
  in the recent rows.

### `outcome="stale_component"`

The F-1236 freshness gate rejected the snapshot: a per-component
observation lags the snapshot's chain-tip ledger by more than the
configured threshold (`[supply] stale_component_ledgers`, default
`1000` ledgers ≈ 85 min). The previous snapshot stays in
`asset_supply_history`, so consumers see correct-but-old data
until the lag closes.

The gate compares the **chain tip** (always advancing) against
`MinComponentLedger`, which is sourced from the **change-driven**
classic/SEP-41 observers. There are two genuinely different causes
that produce this outcome, and they are NOT distinguished by the
gap alone — you must look at whether `MinComponentLedger` is
*moving*:

1. **Stalled producer (real staleness).** The observer that fills
   the component tables (trustlines / claimable_balances /
   liquidity_pools / sac_balances for classic; sep41_supply for
   SEP-41) is wedged or far behind. `MinComponentLedger` is
   advancing tick-over-tick but can't keep up, OR has regressed.
   This is the case the gate is *supposed* to catch.
   - Signal: the indexer's per-source freshness for the relevant
     observer hypertable is also lagging; the gap in the WARN log
     (`gap=…`) does not stabilise around a fixed
     `min_component_ledger`.
   - Mitigation: treat as an observer-ingest stall — confirm the
     indexer is healthy and the relevant observer is progressing;
     route to the ingest-pipeline runbooks. Do NOT relax the gate
     to mask a genuinely stalled producer.

2. **Dormant asset (NOT staleness — see F-1320).** A low-activity
   asset (governance tokens like **PHO**, niche classic credits)
   simply had no balance change, so `MinComponentLedger` is
   *frozen* — its last observation IS the current supply. Because
   the chain tip keeps advancing, the gap grows monotonically and,
   under the pre-F-1320 gate, **every future tick was permanently
   rejected** and the asset's supply row went silently, permanently
   stale (observed live on PHO: gap grew 1017 → 1324 and kept
   climbing). The refresher now recognises an *unchanged*
   `MinComponentLedger` as dormant and **accepts** the snapshot
   (`outcome="dormant"`, the row is inserted). You will still see a
   **single** `stale_component` on the first tick after an
   aggregator restart for a quiet asset (cold start — dormant vs
   stalled is indistinguishable until we see a second tick), then
   it flips to `dormant`. A *sustained* `stale_component` stream
   for one asset is therefore case (1), not case (2).
   - Signal: in the WARN log the `min_component_ledger` value is
     constant across ticks while `gap` climbs; `first_observation`
     is logged on the cold-start tick.
   - Mitigation (only if you want to suppress the cold-start
     `stale_component` blip entirely, or if you are on a binary
     predating the F-1320 dormancy fix): raise the per-asset
     threshold for that asset (see **Per-asset threshold override**
     below) so the gap never trips.

### `outcome="missing_freshness"`

Strict-freshness mode (`[supply] strict_freshness_required = true`)
rejected a snapshot that arrived with `MinComponentLedger == 0`
(no freshness anchor — the static-XLM fallback, or a transiently
failing freshness producer like a Postgres/Redis blip).

- Signal: `_missing_freshness` increments only when strict mode is
  enabled.
- Mitigation: confirm every freshness producer is wired and not
  transiently failing. If the deployment legitimately runs the
  static fallback for some assets, leave strict mode off (the
  default) until the storage-backed readers cover the watched set.

### Per-asset threshold override (`stale_component` remedy)

The global gate (`[supply] stale_component_ledgers`) is one number
for every asset. A known low-activity asset whose legitimate
observation lag exceeds the global default should get a relaxed
**per-asset** threshold rather than loosening the gate fleet-wide
(which would let a genuinely stalled high-traffic asset like XLM or
USDC through):

```toml
[supply.stale_component_ledgers_by_asset]
# asset_key (CODE-ISSUER for classic, bare contract id for SEP-41)
# = relaxed threshold in ledgers. ≈5000 ≈ 7 h.
"PHO-GDSTRSHXNGB2NW242WXEPSGRDEABYPMKZWNVTHEMSPZ3K4FPSU7XKZE6" = 5000
```

Identify *which* asset to override from the per-asset metric — the
`asset_key` label on `ratesengine_aggregator_supply_refresh_total`
tells you exactly which watched asset is producing the
`stale_component` (or cold-start) outcome (Quick diagnosis #2). The
code option behind the TOML key is
`supply.WithStaleComponentLedgersFor(assetKey, maxLag)`; the
aggregator wires it automatically from
`[supply.stale_component_ledgers_by_asset]`, so operators only
touch the TOML. A value of `0` disables the gate for that one asset
while keeping the global default for all others.

After the F-1320 dormancy fix a per-asset override is no longer
*required* to keep a dormant asset's supply row fresh — the gate
self-recovers via `outcome="dormant"`. The override is still the
right tool when you want to silence the single cold-start
`stale_component` blip, or when you run a binary that predates the
fix.

## Mitigation

- [ ] Step 1 — Identify dominant outcome via Quick diagnosis #1.
- [ ] Step 2 — Apply the matching root-cause fix from above.
- [ ] Step 3 — If `_no_observation` and the observer hasn't
      backfilled: this is expected during bootstrap. Wait
      OR populate the operator-static fallback config blocks.
- [ ] Verification: `outcome="ok"` rate exceeds error-outcome
      rate; alert clears within 30 min as the rolling window
      catches up.

## Known false-positive patterns

- **Bootstrap window after fresh deploy.** The
  `_no_observation` outcome dominates briefly while the
  AccountEntry observer (#298) backfills the watched accounts.
  The 30 min `for` clause typically absorbs this; longer
  bootstraps still trip it. Operators that anticipate this can
  silence the alert during deploy windows.

- **Per-asset bootstrap timing.** A new entry added to
  `[supply] watched_classic_assets` will produce
  `_no_observation` ticks until the observer has rows for the
  asset's locked-set members. The other watched assets continue
  to produce `_ok` ticks; the alert fires only when the error
  fraction exceeds 50%.

## Related

- `supply-refresh-stalled.md` — when no ticks are happening at all.
- ADR-0011, ADR-0021, ADR-0022, ADR-0023 — the algorithms +
  observer designs the refresher consumes.

## Changelog

- 2026-04-30 — initial draft alongside #313 (supply-refresh
  alerts).
- 2026-04-30 — quick-diagnosis #2 corrected: the
  `aggregator_supply_refresh_total` metric DOES carry an
  `asset_key` label (added in #314), so per-asset splitting is
  possible from `/metrics` directly without needing journald.
- 2026-06-11 — F-1320: documented the previously-omitted
  `stale_component` and `missing_freshness` outcomes; added the
  dormant-asset failure mode (the stale-component gate permanently
  rejected dormant assets because it measures the always-advancing
  chain tip against a frozen `MinComponentLedger`). The refresher
  now accepts an unchanged-`MinComponentLedger` snapshot as
  `outcome="dormant"`; documented the per-asset
  `[supply.stale_component_ledgers_by_asset]` /
  `WithStaleComponentLedgersFor` remedy and how to identify the
  failing asset from the `asset_key` label.
