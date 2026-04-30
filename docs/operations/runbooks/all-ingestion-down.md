---
title: Runbook — all ingestion sources down
last_verified: 2026-04-30
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
- Indexer logs show no activity, repeated MinIO read errors, or
  Galexie producing no fresh objects in `galexie-live`.

> The legacy `ratesengine_ingestion_lag_high` companion alert was
> retired with the move off the orchestrator topology
> ([alerts-catalog.md](../alerts-catalog.md) §Ingestion historical
> note); don't expect to see it fire today.

## Quick diagnosis (≤ 5 min)

> **Architecture reminder.** Production ingest reads Galexie's MinIO
> output directly via `go-stellar-sdk/ingest.ApplyLedgerMetadata`
> ([architecture/ingest-pipeline.md](../../architecture/ingest-pipeline.md));
> stellar-rpc was removed from r1 on 2026-04-23 and is no longer in
> the data path. The "shared upstream" is now Galexie + MinIO, not
> stellar-rpc. Sections C / D of the legacy "stellar-rpc is the
> problem" branch below are retained as future-tense for any
> deployment that still routes through RPC; on r1 today, jump to
> Galexie / MinIO checks first.

The "all sources down" shape usually means one of three common roots: Galexie's MinIO output (the shared upstream), the shared storage (Timescale), or the indexer process itself.

```sh
# 1. Is the indexer running?
systemctl status ratesengine-indexer      # baremetal
# or
kubectl -n ratesengine get pod -l app=ratesengine-indexer

# 2. What do its logs say?
journalctl -u ratesengine-indexer -n 200 --no-pager | tail -40
# Look for: "source stream ended with error", "insert trade failed",
# "minio: object not found", "ledger metadata read".

# 3. Is Galexie producing fresh ledger objects?
sudo journalctl -u galexie -n 50 --no-pager
mc ls minio/galexie-live | tail -5      # newest objects within ~1 min
# OR if you suspect a network-state issue, query upstream directly
# (r1 has no local stellar-rpc; point at a public endpoint):
ratesengine-ops rpc-probe https://mainnet.sorobanrpc.com
# Expect: version info + latest ledger close time within 60s.

# 4. Is Timescale reachable + writable?
PGCONNECT_TIMEOUT=3 psql -h db-primary.internal -U ratesengine \
  -d ratesengine -c "INSERT INTO ingestion_cursors (source, sub_source, last_ledger) VALUES ('probe', 'healthcheck', 0) ON CONFLICT DO NOTHING;"
```

Route by the result:

- Galexie isn't producing fresh objects in `galexie-live` → galexie's captive-core stalled or upstream network issue. Check `journalctl -u galexie`; if galexie itself is healthy, fall back to the public-rpc probe to confirm the network is closing ledgers.
- Galexie healthy + fresh objects in MinIO but indexer not reading → networking issue between indexer and MinIO, or indexer's MinIO credentials / endpoint config wrong. Check firewall, DNS, and `[ledgerstream]` config.
- psql INSERT fails → Timescale issue. Jump to [timescale-primary-down](timescale-primary-down.md).
- All probes pass but indexer produces no events → the indexer is alive but wedged. Likely deadlock or internal bug.

## Mitigation (≤ 15 min)

### A. Galexie / MinIO is the upstream problem

- Confirm galexie itself is healthy: `systemctl status galexie`,
  `journalctl -u galexie --since="10 min ago"`. galexie embeds its
  own captive-core; recoverable hangs typically clear with a
  service restart. Check disk pressure on `data/galexie`.
- Confirm fresh objects are landing in MinIO:
  `mc ls minio/galexie-live | tail -5`. The newest object should
  be within ~1 minute. If MinIO itself is unhealthy, follow
  whichever `redis-master-down`-style runbook applies (no
  dedicated MinIO runbook today).
- Wider network problem? `ratesengine-ops rpc-probe https://mainnet.sorobanrpc.com`
  confirms ledgers are still closing on the network — if the
  network is fine but galexie has stalled, capture logs and
  restart galexie.
- *Future-tense:* if a deployment routes through stellar-rpc
  rather than direct-MinIO ingest, [rpc-lag](rpc-lag.md) covers
  that path. r1 does not run stellar-rpc as of 2026-04-23.

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
- 2026-04-30 — quick-diagnosis + Mitigation A rewritten around
  Galexie + MinIO (the actual r1 upstream); rpc-probe URL points
  at a public stellar-rpc since r1 doesn't run its own
  (removed 2026-04-23). Symptoms drop the retired
  `ratesengine_ingestion_lag_high` reference.
