---
title: Supply-snapshot writer — daily cron + operator-managed reserve balances
last_verified: 2026-04-30
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
balance sum. Until the LCM-AccountEntry observer ships (Task #54 +
the home-domain overlay it pairs with), the writer reads the
balance sum from operator config:

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

### Future: live LCM-derived reserve balances

Tracked as Task #54. When that ships, the
`ConfigReserveBalanceReader` is replaced by an
`LCMReserveBalanceReader` that watches AccountEntry deltas in the
existing dispatcher hook and persists per-(account, ledger) balances
to a new hypertable. The `[supply.reserve_balances_stroops]` block
becomes optional (overrides the live reader); the manual-update
procedure above goes away.

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

## Asset-class scope at v1

| Asset class       | Algorithm                               | Status            | Tracked as |
| ----------------- | --------------------------------------- | ----------------- | ---------- |
| Native XLM        | 1 — `total − Σ(SDF reserve balances)`   | Shipped (#285)    | —          |
| Classic credit    | 2 — `Σ trustline+claimable+LP+SAC`      | Pending computer  | Task #55   |
| SEP-41 Soroban    | 3 — `Σ mint − Σ burn − Σ clawback`      | Pending computer  | Task #56   |

The writer rejects `-asset CODE-G…` and `-asset C…` with a clear
"not yet supported at v1" message until the corresponding computer
ships.

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

When Task #54's LCM-derived reader ships and the writer becomes
goroutine-resident in the aggregator, the cadence flips to per-tick
(every aggregator window) and this systemd unit becomes redundant.
