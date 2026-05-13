---
title: Runbook — supply-snapshot-never-initialized
last_verified: 2026-05-08
status: ratified
severity: P3
---

# Runbook — `ratesengine_supply_snapshot_never_initialized`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_supply_snapshot_never_initialized` (P3, ticket) |
| Detected by | `deploy/monitoring/rules/supply-snapshot.yml` |
| Typical MTTR | 10 min (one-shot operator action) |
| Impact | `/v1/assets/{id}` F2 fields (circulating / total / max / market_cap_usd / fdv_usd) render as `null` for every asset. |

## Why this exists separately from `_stale`

`_stale` fires when `ratesengine_supply_snapshot_last_success_timestamp`
is *older than 36 h*. That requires the metric to have existed at
some point — Prometheus's `time() - <missing>` is `no data`, not
infinity, so a deployment that has *never* written a snapshot is
invisible to `_stale`.

This alert closes that blind spot via
`absent_over_time(...[36h]) == 1`. The 36 h window matches `_stale`'s
cushion so a fresh install that hasn't had one daily timer fire
doesn't false-positive.

## Symptoms

- The alert annotation summary: "supply snapshot has never published — pipeline uninitialized."
- `/v1/assets/native` returns no `circulating_supply` /
  `market_cap_usd` fields (they're omitted when null per the
  wire-shape contract).
- `/v1/assets/xlm` (catalogue-slug variant) likewise.
- `psql … -c "SELECT count(*) FROM asset_supply_history"` returns 0.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Is the timer installed at all?
ssh root@<host>
systemctl is-enabled supply-snapshot.timer
# "not-found" / "Unit could not be found" → never installed
# "enabled"   → installed; check status next

# 2. If installed, has it ever fired?
systemctl status supply-snapshot.timer  --no-pager
systemctl status supply-snapshot.service --no-pager  # most recent run

# 3. Is the goroutine path enabled instead?
grep -A 1 'aggregator_refresh_enabled' /etc/ratesengine.toml
# absent or =false → not enabled
# =true            → check aggregator logs for "supply-refresh" lines
journalctl -u ratesengine-aggregator --since '2 days ago' | grep -E 'supply-refresh|supply.*ok|supply.*err'
```

## Resolution

Pick **one** path. Both populate `asset_supply_history`; never run
both simultaneously.

### Path A — systemd timer (daily, simpler)

```sh
sudo cp deploy/systemd/supply-snapshot.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now supply-snapshot.timer

# Verify the unit fires immediately (one-off):
sudo systemctl start supply-snapshot.service
sudo journalctl -u supply-snapshot.service --no-pager -n 50
# Expect: a "wrote snapshot for asset XLM" log line + zero exit.

# Verify the metric appears (once node_exporter rescrapes the .prom):
curl -s http://localhost:9100/metrics | grep ratesengine_supply_snapshot_last_success_timestamp
# Expect a single line with a recent unix timestamp.
```

The alert clears within 5 min of a successful first run.

### Path B — aggregator-resident goroutine (sub-minute cadence)

```sh
# Edit /etc/ratesengine.toml:
[supply]
aggregator_refresh_enabled = true
# Optional: aggregator_refresh_cadence = "5m" (default)

sudo systemctl restart ratesengine-aggregator
sudo journalctl -u ratesengine-aggregator -f | grep -E 'supply-refresh'
# Expect a "supply-refresh" tick line within `aggregator_refresh_cadence`.

# Verify the metric appears in the aggregator's /metrics:
curl -s http://localhost:9091/metrics | grep ratesengine_aggregator_supply_refresh_total
# Expect at least one outcome="ok" line per watched asset_key.
```

This silences `_never_initialized` AND populates the goroutine-path
metrics tracked by
[supply-refresh-stalled.md](supply-refresh-stalled.md). Note that
the textfile-path `_stale` alert WILL stay silent on Path B unless
the timer is also installed — that's expected; per
[supply-snapshot-stale.md §"Two refresh paths exist"](supply-snapshot-stale.md),
silence the `_stale` alert when the deployment uses Path B
exclusively.

## Why neither path is the default

The supply pipeline ships dormant by design — the operator-managed
`reserve_balances_stroops` config is the source of truth for SDF
reserves (the publicly-known accounts whose balances we subtract
from the total to compute circulating). Without those balances,
the writer would emit nonsense; the gate forces operator review
before the first publish.

See [docs/operations/supply-snapshot.md](../supply-snapshot.md)
for the operator-side wiring guide.

## Verifying the fix

After enabling either path, the following should be true within
`max(36 h, aggregator_refresh_cadence)`:

```sh
# Alert clears in Prometheus.

# Database has rows.
sudo -u postgres psql -d ratesengine -c \
  "SELECT asset_key, count(*) AS rows, max(time) AS latest
   FROM asset_supply_history GROUP BY asset_key ORDER BY asset_key"

# API surfaces the data.
curl -s 'https://api.ratesengine.net/v1/assets/native' | jq '.data.circulating_supply'
# Expect a numeric string, not null.
```

## See also

- [supply-snapshot-stale.md](supply-snapshot-stale.md) — sibling alert, fires when a previously-working pipeline goes stale.
- [supply-refresh-stalled.md](supply-refresh-stalled.md) — sibling alert for Path B (goroutine).
- [docs/architecture/supply-pipeline.md](../../architecture/supply-pipeline.md) — the three-algorithm design.
- [docs/adr/0011-supply-algorithm.md](../../adr/0011-supply-algorithm.md) — original ADR.
