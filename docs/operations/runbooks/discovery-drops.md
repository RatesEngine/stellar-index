---
title: Runbook — discovery-drops
last_verified: 2026-05-03
status: draft
severity: P3
---

# Runbook — `ratesengine_ingestion_discovery_drops`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_discovery_drops` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | minutes-to-hours |
| Impact | SEP-41 discovery coverage is degrading. The main ingest path keeps running, but some discovery hits are being dropped before they reach Postgres. |

## Symptoms

- `increase(ratesengine_discovery_dropped_hits_total[10m]) > 0` sustained 10 min.
- Indexer logs include `discovery: hits dropped` warnings.
- Persisted `discovered_assets` rows may lag the underlying event stream during the same window.

## Context

The discovery sink is intentionally non-blocking. When its buffered
channel fills, new discovery hits are dropped instead of stalling the
`ledgerstream -> dispatcher` hot path. This protects price ingestion,
but it means discovery completeness is best-effort under recorder
pressure.

## Quick diagnosis (≤ 10 min)

```sh
# Confirm drops are ongoing, not a one-off blip.
curl -s http://indexer:9464/metrics | grep ratesengine_discovery_dropped_hits_total

# Check whether Postgres/discovery writes are struggling.
ssh root@indexer-01 "journalctl -u ratesengine-indexer -n 200 --no-pager" \
  | grep "discovery:"

# Cross-check for broader storage pressure.
curl -s http://indexer:9464/metrics | grep ratesengine_source_insert_errors_total
```

## Typical root causes

1. Discovery recorder writes are slow because Postgres is degraded.
2. Discovery event volume spiked beyond the configured buffer.
3. A long-running recorder timeout is forcing repeated drop/retry cycles.

## Mitigation

- [ ] Check Timescale health first. If storage is degraded, restore it before tuning discovery.
- [ ] If storage is healthy, inspect whether recent discovery volume increased sharply.
- [ ] Increase the discovery buffer only after confirming this is sustained workload, not a transient outage.
- [ ] Verification: `increase(ratesengine_discovery_dropped_hits_total[10m])` returns to `0`.

## Related

- `insert-errors.md` — storage write failures on the main ingest sink.
- `all-ingestion-down.md` — the severe case where ingestion itself stops.
- `internal/canonical/discovery/sink.go` — best-effort buffer/drop contract.
