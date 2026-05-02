---
title: Supply-snapshot writer — daily cron + operator-managed reserve balances
last_verified: 2026-05-02
status: living procedure
---

# Supply-snapshot writer — daily cron + operator-managed reserve balances

Operational companion to [ADR-0011](../adr/0011-supply-algorithm.md)
(the policy decision). This doc covers:

- What the snapshot writer is + why it runs
- The `[supply]` config block + manual reserve-balance updates
- Daily cron via `deploy/systemd/supply-snapshot.{service,timer}`
- Asset-class scope at v1 (XLM only) + the follow-up plan

The implementation lives in `cmd/ratesengine-ops/supply.go` (the
`supply snapshot` subcommand) and `internal/supply/config_reader.go`
(the operator-managed `ReserveBalanceReader`).

## Purpose

`/v1/assets/{id}` exposes Freighter V2's market-data fields —
`total_supply`, `circulating_supply`, `max_supply`, `market_cap_usd`,
`fdv_usd`, `supply_basis` — by reading from the
`asset_supply_history` hypertable. Without a producer, the table
stays empty and those fields ship as JSON null.

The writer is the producer. Each run computes the current Supply
per ADR-0011 Algorithm 1 (native XLM at v1; classic + SEP-41 follow
once their respective computers ship) and inserts a row into
`asset_supply_history`. The handler reads back the latest row.

## Why operator-managed reserve balances

Per ADR-0011 Algorithm 1:

```
total_supply       = 50,001,806,812 × 10^7 stroops      (frozen 2019)
max_supply         = total_supply                        (XLM is hard-capped)
circulating_supply = total_supply − Σ(SDF reserve balances)
```

Total + max are constants. The only moving piece is the SDF reserve
balance sum. The writer reads it from the LCM AccountEntry observer
(Task #54, wired into the indexer dispatcher in PR #411) when
`account_observations` has rows for every account in the watched
set; until that backfill completes, the writer falls back to the
operator-static `[supply].reserve_balances_stroops` map per the
chained-fallback reader pattern in
[`docs/architecture/supply-pipeline.md`](../architecture/supply-pipeline.md#the-chained-fallback-reader-pattern).
Empty `[supply].reserve_balances_stroops` is fine on a deployment
where the observer has caught up; the static config is just a
bring-up cushion.

```toml
[supply]
sdf_reserve_accounts = [
  "GA5XIGA5C7QTPTWXQHY6MCJRMTRZDOSHR6EFIBNDQTCQHG262N4GGKTM",
  "GBLDBN3QQAA2QAH7ZQI6LQ5TXGMVCOATJYBSXQYDQB7ZUR3OVF5JEHO5",
  # … one entry per active SDF reserve account, per latest SDF announcement
]

[supply.reserve_balances_stroops]
GA5XIGA5C7QTPTWXQHY6MCJRMTRZDOSHR6EFIBNDQTCQHG262N4GGKTM = "12345678900000000"
GBLDBN3QQAA2QAH7ZQI6LQ5TXGMVCOATJYBSXQYDQB7ZUR3OVF5JEHO5 = "98765432100000000"
```

The writer-start path validates that **every** account in
`sdf_reserve_accounts` has a corresponding entry in
`reserve_balances_stroops`. A missing entry is a hard fail —
silently treating an unknown account as zero would publish an
over-stated circulating supply, the exact failure mode ADR-0011
prohibits.

### When SDF announces a reserve move

1. Wait for SDF's public announcement (typically a forum post
   referencing the destination account + the stroop amount).
2. Edit the operator's `[supply.reserve_balances_stroops]` table.
   Update the moving accounts; add new accounts to
   `sdf_reserve_accounts` if SDF created a new reserve.
3. Reload the config (next timer fire picks it up — no service
   restart needed).
4. (Optional) Force an out-of-cadence run:
   ```
   sudo systemctl start supply-snapshot.service
   ```

### Live LCM-derived reserve balances (shipped)

As of PR #300 (Task #54 closed), the supply-snapshot subcommand
chains the live `LCMReserveBalanceReader` (backed by the
`account_observations` hypertable populated by the AccountEntry
observer in #298) with the operator-static `ConfigReserveBalanceReader`
as fallback. On every run:

1. The live reader queries the most-recent observation
   at-or-before the ledger being attributed.
2. If every reserve account has an observation, the live sum
   wins and the static config is not consulted.
3. If any account has no observation (the observer hasn't
   backfilled to a deep enough range yet) OR a transient storage
   error fires, the chain drops to the static reader for the
   whole call.

The `[supply.reserve_balances_stroops]` block stays valid as a
bootstrap fallback. Operators that have backfilled the observer
across the watched accounts can leave it stale (or empty) without
behavioral impact; once observations exist, the static map is
never consulted.

The manual-update procedure above is now bootstrap-only; once
the observer covers the live operator set, balance changes show
up in the next snapshot automatically.

## Daily cron

### Files

- `deploy/systemd/supply-snapshot.timer` — `OnCalendar=04:42 UTC`
  daily, with up to 5 min jitter. Spaced after the
  archive-completeness verify (02:17) and verify-archive-tier-a
  (03:23) so the three operator timers don't all fire at once.
- `deploy/systemd/supply-snapshot.service` — calls
  `ratesengine-ops supply snapshot -config $CONFIG_PATH -asset $ASSET`.

### Operator wiring

```sh
sudo cp deploy/systemd/supply-snapshot.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now supply-snapshot.timer
```

Override defaults via `/etc/default/supply-snapshot`:

```sh
CONFIG_PATH=/etc/ratesengine.toml      # default
ASSET=native                            # default; only `native` at v1
EXTRA_FLAGS="-ledger 50000000"          # pin to a specific ledger
                                        # (default: max from ingestion_cursors)
```

### Pre-flight: dry-run

Before enabling the timer, validate the config + reserve balances
with a dry-run:

```sh
sudo -u ratesengine /usr/local/bin/ratesengine-ops supply snapshot \
  -config /etc/ratesengine.toml -dry-run
```

The output lists `total_supply` / `circulating_supply` /
`max_supply` / `basis` / `ledger_sequence`. Sanity-check
`circulating_supply` against the latest SDF announcement (the
delta from `total_supply` should match the announced reserve
balance sum). If it doesn't, fix the
`reserve_balances_stroops` block before enabling the cron.

## Asset-class scope

| Asset class       | Algorithm                               | Status   | Reference |
| ----------------- | --------------------------------------- | -------- | --------- |
| Native XLM        | 1 — `total − Σ(SDF reserve balances)`   | Shipped  | ADR-0011  |
| Classic credit    | 2 — `Σ trustline+claimable+LP+SAC`      | Shipped  | ADR-0022  |
| SEP-41 Soroban    | 3 — `Σ mint − Σ burn − Σ clawback`      | Shipped  | ADR-0023  |

The aggregator-resident refresher (per `[supply]
aggregator_refresh_enabled`) iterates all watched assets across
the three algorithms — XLM (always), classic
(`watched_classic_assets`), and SEP-41
(`watched_sep41_contracts`). One `Refresher.Tick` goroutine per
watched asset; the per-tick outcome counter
(`ratesengine_aggregator_supply_refresh_total`) labels by
outcome.

The CLI subcommand (`ratesengine-ops supply snapshot`) supports
`-asset native` only at present; multi-asset CLI support will
follow if operators need ad-hoc snapshots for non-XLM assets
outside the aggregator goroutine cadence.

## Textfile-collector integration

`-textfile-output PATH` writes a Prometheus textfile after each run
so node_exporter can scrape per-asset supply values, run duration,
and a pass/fail gauge. Operator wiring:

```sh
# /etc/default/supply-snapshot
TEXTFILE_OUTPUT=/var/lib/node_exporter/textfile_collector/supply_snapshot.prom
```

The systemd service writes via the `<path>.tmp`-then-rename atomic
protocol; node_exporter skips files whose name ends in `.tmp` so a
partial write never appears in a scrape.

### Metric set

```
ratesengine_supply_snapshot_total_xlm{asset_key=}             gauge   XLM
ratesengine_supply_snapshot_circulating_xlm{asset_key=}       gauge   XLM
ratesengine_supply_snapshot_max_xlm{asset_key=}               gauge   XLM (only when set)
ratesengine_supply_snapshot_ledger{asset_key=}                gauge   ledger seq
ratesengine_supply_snapshot_observed_at_seconds{asset_key=}   gauge   unix
ratesengine_supply_snapshot_run_duration_seconds              gauge   seconds
ratesengine_supply_snapshot_unit_failed{asset_key=}           gauge   1 on fail, 0 on pass
ratesengine_supply_snapshot_last_success_timestamp{asset_key=} gauge  unix; only on pass
```

Values are emitted in **XLM** units (not stroops) for human-
readable Grafana panels. The `asset_supply_history` hypertable
retains full NUMERIC precision; the textfile loses sub-stroop
precision in the float64 conversion, which is fine for monitoring.

### Alerts

Four alerts in `deploy/monitoring/rules/supply-snapshot.yml`:

| Alert | Condition | Severity |
|-------|-----------|----------|
| `ratesengine_supply_snapshot_unit_failed_alert` | unit_failed=1 sustained 30 min | P3 ticket |
| `ratesengine_supply_snapshot_stale` | last_success > 36 h | P3 ticket |
| `ratesengine_supply_snapshot_critical_stale` | last_success > 72 h | **P2** page |
| `ratesengine_supply_snapshot_circulating_zero` | circulating ≤ 0 (XLM only) | **P2** page |

Each has a runbook under `docs/operations/runbooks/supply-snapshot-*.md`.

## Verifying it ran

After the first cron fire, check the snapshot landed:

```sh
ratesengine-ops supply audit native -config /etc/ratesengine.toml
```

The output prints `total_supply` / `circulating` / `max_supply` /
`basis` / `ledger_sequence` / `observed_at` for the latest snapshot.

A second daily run should produce a row with the same
`circulating_supply` (same operator config) and a fresher
`ledger_sequence` (cursors advance). If `circulating_supply`
suddenly diverges with no operator-config edit, that's a signal
worth investigating — see
[supply-cross-check-divergence runbook](runbooks/supply-cross-check-divergence.md).

## Why daily, not hourly

The values change only when operator config changes (rare —
multiple-times-per-year cadence). Re-publishing at higher cadence
buys nothing on the data side. Daily is enough to keep
`observed_at` fresh on the asset-detail surface; the bookkeeping
overhead is negligible.

## Aggregator-resident goroutine path (preferred once observer is backfilled)

As of PR #301 (Task #57 closed), an alternative to the systemd
timer is the in-aggregator goroutine:

```toml
[supply]
aggregator_refresh_enabled = true
aggregator_refresh_cadence = "5m"
```

When enabled, the aggregator runs the supply-snapshot refresher
on the configured cadence inside its own goroutine — the same
chained-fallback reader as the systemd-timer path (live LCM →
operator-static fallback). Per-tick outcomes are emitted as
`ratesengine_aggregator_supply_refresh_total{outcome=…}` counters.

The two paths are mutually exclusive — operators that flip
`aggregator_refresh_enabled = true` should disable the systemd
timer (`sudo systemctl disable --now supply-snapshot.timer`) to
avoid double-writes (the writes are idempotent on conflict so
this is correctness-safe; it just doubles the bookkeeping cost).

Once the LCM observer has backfilled the watched accounts, the
goroutine path is preferred — `observed_at` tracks current ledger
within `aggregator_refresh_cadence` rather than within a day.
