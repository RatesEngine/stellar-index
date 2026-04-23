---
title: Runbook — all ingestion sources down
last_verified: 2026-04-22
status: ratified
severity: P1
---

# Runbook — `ratesengine_ingestion_all_sources_stopped`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_all_sources_stopped` |
| Severity | **P1** (SEV-1) |
| Detected by | `sum(rate(ratesengine_source_events_total[5m]))` = 0 for > 3 min |
| Typical MTTR | 5–20 min depending on root cause |
| Impact | Price staleness begins at the 60 s cache TTL; API sets `stale_flag=true` globally. If the outage lasts > 30 min we breach the Freighter 30 s freshness SLA. |

## Symptoms

- Alert `ratesengine_ingestion_all_sources_stopped` fires.
- `ratesengine_api_price_stale` follows ~60 s later across every asset.
- `ratesengine_ingestion_lag_high` for every source.
- Indexer logs show connection errors to stellar-rpc OR no activity at all.

## Quick diagnosis (≤ 5 min)

The "all sources down" shape usually means one of three common roots: the shared upstream (stellar-rpc), the shared storage (Timescale), or the indexer process itself.

```sh
# 1. Is the indexer running?
systemctl status ratesengine-indexer      # baremetal
# or
kubectl -n ratesengine get pod -l app=ratesengine-indexer

# 2. What do its logs say?
journalctl -u ratesengine-indexer -n 200 --no-pager | tail -40
# Look for: "source stream ended with error", "insert trade failed",
# "rpc ...: connection refused".

# 3. Is stellar-rpc reachable?
ratesengine-ops rpc-probe http://<our-rpc>:8000
# Expect: version info + latest ledger close time within 60s.

# 4. Is Timescale reachable + writable?
PGCONNECT_TIMEOUT=3 psql -h db-primary.internal -U ratesengine \
  -d ratesengine -c "INSERT INTO ingestion_cursors (source, sub_source, last_ledger) VALUES ('probe', 'healthcheck', 0) ON CONFLICT DO NOTHING;"
```

Route by the result:

- rpc-probe says "no ledger in >30s" → stellar-rpc itself is lagging/down. Jump to [rpc-lag](rpc-lag.md).
- rpc-probe fine but indexer has connection errors → networking issue between indexer and RPC. Check firewall, DNS, recent config changes.
- psql INSERT fails → Timescale issue. Jump to [timescale-primary-down](timescale-primary-down.md).
- All three probes pass but indexer produces no events → the indexer is alive but wedged. Likely deadlock or internal bug.

## Mitigation (≤ 15 min)

### A. stellar-rpc is the problem

- Failover to a secondary RPC endpoint (if one is configured). Edit `ingestion.rpc_endpoints` in config, restart indexer.
- As interim: point indexer at a public RPC (e.g. SDF's `https://mainnet.sorobanrpc.com`) while ours recovers. Event retention + rate-limits apply on public RPC — not a long-term fix.
- Proceed to [rpc-lag](rpc-lag.md) for stellar-rpc side.

### B. Timescale is the problem

- Proceed to [timescale-primary-down](timescale-primary-down.md).

### C. Indexer itself is wedged

- Capture a goroutine dump before restarting:
  ```sh
  kill -QUIT $(pgrep ratesengine-indexer)   # SIGQUIT dumps goroutines to stderr
  journalctl -u ratesengine-indexer -n 200 --no-pager > /tmp/indexer-dump-$(date +%s).log
  ```
- Restart the indexer:
  ```sh
  systemctl restart ratesengine-indexer
  # or
  kubectl -n ratesengine rollout restart deployment/ratesengine-indexer
  ```
- Confirm recovery:
  - `ratesengine_source_events_total` rate > 0 within 60 s.
  - Alert clears within 3 min.

### D. Recent deploy broke the indexer

- Check deploy history: last 4 h.
- Revert via `make rollback INDEXER_VERSION=<previous>` (TODO(#0) make target).
- After revert, re-run diagnostics in step C.

## Root cause analysis

Gather:

- Goroutine dump from step C.
- Indexer logs `journalctl -u ratesengine-indexer --since "30 min ago"`.
- Grafana screenshots of `ratesengine_source_events_total` broken down by source — does it cliff-edge at a specific timestamp, or decay?
- Recent deploys — git log of `cmd/ratesengine-indexer/` in the last 72 h.
- Postgres `pg_stat_activity` during the window — were inserts blocked on locks?
- stellar-rpc's `getHealth` returned values during the window.

Patterns observed:

1. **Shared upstream down** — single dependency (stellar-rpc) dropped. Mitigation: add a second RPC endpoint; indexer round-robins.
2. **Shared storage backpressure** — Timescale insert latency spiked; indexer's output channel filled; all source goroutines blocked on `out <- evt`. Mitigation: indexer needs a buffered channel with drop-oldest policy for slow-consumer safety.
3. **Config-change caused source registry to be empty** — `ingestion.enabled_sources` accidentally set to `[]`. Mitigation: config loader (internal/config/load.go) to reject empty enabled_sources at startup, not just fatal in runtime.
4. **Panic in a source** — bad decoder blows up one goroutine + the watch goroutine waits forever. Mitigation: defer-recover in each source goroutine + orchestrator restart.

## Known false-positive patterns

- **Network event retention window rolled over** — stellar-rpc stopped serving the ledger range our cursor is in. The indexer correctly stops producing trade events while it seeks forward. Alert fires spuriously. Mitigation: tune the alert to look at `time() - ratesengine_source_last_event_unix` (staleness age) instead of raw rate.
- **Midnight UTC continuous-aggregate refresh** — the aggregator's heavy CAGG refresh briefly blocks trade inserts. Indexer queues up, then drains. Alert might fire at the window if duration is short. Tune `for: 3m → for: 5m` if this recurs.

## Related

- [rpc-lag](rpc-lag.md) — next step when stellar-rpc is the root cause.
- [timescale-primary-down](timescale-primary-down.md) — next step when DB is the root cause.
- [ingestion-lag](ingestion-lag.md) — single-source-lag runbook.
- [cursor-stuck](cursor-stuck.md) — cursor-specific diagnosis.
- Internal docs:
  - `internal/consumer/orchestrator.go` — per-source restart logic.
  - `cmd/ratesengine-indexer/main.go` — wiring + shutdown.

## Changelog

- 2026-04-22 — initial draft. @ash.
