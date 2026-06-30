---
title: Runbook — data source stale
last_verified: 2026-06-30
status: ratified
severity: P3
---

# Runbook — `stellarindex_data_source_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_data_source_stale` |
| Severity | **P3** (ticket) |
| Detected by | `stellarindex_data_freshness_stale{domain,source} == 1` for > 1h |
| Emitted by | `data-freshness.sh` → node_exporter textfile (`data_freshness.prom`), every 15 min |
| Typical MTTR | 10–60 min (often an external key/quota or a wedged poller) |
| Impact | The named domain/source is no longer ingesting fresh rows. Reference/oracle feeds degrade cross-checks; a CEX/DEX trade source going stale degrades VWAP for its pairs. |

## Symptoms

`stellarindex_data_freshness_stale{domain="X",source="Y"} = 1`. The age is on
`stellarindex_data_freshness_age_seconds{domain="X",source="Y"}`. The gap
detector (`stellarindex_ingest_gap_detected`) covers on-chain trade/event source
*gaps*; this alert covers everything else going *stale*: reference oracles, FX,
supply, the issuer-metadata cron, and the completeness verdict itself.

## Quick diagnosis (≤ 5 min)

```sh
# Which domain/source + how stale (seconds):
curl -s localhost:9100/metrics | grep 'stellarindex_data_freshness_age_seconds' | sort -t' ' -k2 -n | tail
# The writer's recent logs (oracle/fx pollers run in the indexer or api):
journalctl -u stellarindex-indexer -u stellarindex-api --since '2 hours ago' | grep -iE "<source>|poller error|429|401|quota"
```

## Mitigation (≤ 15 min)

- **External API quota / auth (oracle `coingecko`, FX `massive`, `chainlink`):**
  a `429`/`401` in the poller logs means the key is exhausted/expired. Restore
  the paid key in `/etc/default/stellarindex` and restart the owning binary.
  (CoinGecko Pro purchase is tracked as launch-todo P0-3.)
- **`domain="verdict"` stale:** the `compute-completeness.timer` isn't running —
  see `systemctl status compute-completeness.service`.
- **`domain="sep1"` stale:** the `sep1-refresh.timer` isn't running.
- **`domain="trades"` (CEX/DEX) stale:** the venue connector/dispatcher stopped;
  check the indexer.

## Root cause analysis

The freshness threshold per domain lives in `data-freshness.sh` (a generous
multiple of each domain's natural cadence). A sustained breach means the writer
behind that domain stopped: an external quota, a wedged poller goroutine, a
crashed timer, or a connector outage.

## Known false-positive patterns

- **FX `massive`** is daily-grain; freshness is measured off `bucket` (today's
  bucket written), threshold 48h — a same-day late publish does not fire.
- A brand-new source with no rows yet won't emit a gauge (no false fire).
- `coingecko` will legitimately read stale until the CoinGecko Pro key lands
  (P0-3) — expected, not a regression.

## Related

- `stellarindex_completeness_incomplete` — a source that ingests but no longer reconciles to the lake.
- `stellarindex_ingest_gap_detected` — contiguous on-chain ingest gaps (data-derived gap detector).
- `docs/operations/launch-todo.md` — P0-3 (CoinGecko), the freshness-watchdog design.

## Changelog

- 2026-06-30: created with the data-freshness watchdog.
