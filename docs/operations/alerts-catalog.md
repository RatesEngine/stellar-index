---
title: Alerts Catalogue
last_verified: 2026-04-23
status: ratified — incremental growth
---

# Alerts Catalogue

**Ratified:** 2026-04-22 (table shape); entries grow with each
feature PR per repo-hygiene-plan.md §16 ("no alert without a
runbook").

Every row is a Prometheus / AlertManager rule. The `Runbook` column
links to `docs/operations/runbooks/<name>.md`; a missing runbook
fails `scripts/ci/lint-docs.sh` section 9 (runbook-url check,
enforced 2026-04-23 onward).

Severity maps to [sev-playbook.md §1](sev-playbook.md#1-severity-definitions).

**Shape of each alert:**

- **Name** — `ratesengine_<area>_<specific>`. Stable — referenced
  from AlertManager routing + the runbook filename.
- **Metric** — the Prometheus expression that triggers.
- **Condition** — the threshold + duration.
- **Severity** — P1/P2/P3 = SEV-1/2/3 from the playbook.
- **Runbook** — what the responder does (link).
- **Escalation** — which route in AlertManager.

---

## Ingestion alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_ingestion_source_stopped` | `rate(ratesengine_source_events_total[5m])` per source | == 0 for > 5 min on an enabled source | P2 | [source-stopped](runbooks/source-stopped.md) |
| `ratesengine_ingestion_all_sources_stopped` | `sum(rate(ratesengine_source_events_total[5m]))` | == 0 for > 3 min | **P1** | [all-ingestion-down](runbooks/all-ingestion-down.md) |
| `ratesengine_ingestion_lag_high` | `ratesengine_source_lag_ledgers` per source | > 1000 for > 10 min | P2 | [ingestion-lag](runbooks/ingestion-lag.md) |
| `ratesengine_ingestion_cursor_stuck` | `increase(ratesengine_cursor_last_ledger[5m])` per source | == 0 while source is live | P2 | [cursor-stuck](runbooks/cursor-stuck.md) |
| `ratesengine_ingestion_orphan_events` | `rate(ratesengine_source_orphan_events_total[10m])` | > 10/min per source | P3 | [orphan-events](runbooks/orphan-events.md) |
| `ratesengine_ingestion_decode_error` | `rate(ratesengine_source_decode_errors_total[5m])` | > 1/s sustained 5 min | P3 | [decode-errors](runbooks/decode-errors.md) |
| `ratesengine_ingestion_insert_errors` | `rate(ratesengine_source_insert_errors_total[5m])` per (source, kind) | > 0.1/s (≈6/min) sustained 5 min | P2 | [insert-errors](runbooks/insert-errors.md) |

## Storage alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_timescale_primary_down` | `up{job="postgres",role="primary"}` | == 0 for > 30 s | **P1** | [timescale-primary-down](runbooks/timescale-primary-down.md) |
| `ratesengine_timescale_replica_lag` | `pg_replication_lag_seconds` on sync replica | > 5 s for > 2 min | P2 | [replica-lag](runbooks/replica-lag.md) |
| `ratesengine_timescale_disk_full` | `(node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100` on DB vol | < 10 % | **P1** | [db-disk-full](runbooks/db-disk-full.md) |
| `ratesengine_timescale_disk_warning` | same | < 20 % | P2 | [db-disk-full](runbooks/db-disk-full.md) |
| `ratesengine_timescale_connections_saturated` | `pg_stat_activity_count / pg_settings_max_connections * 100` | > 80 % for > 5 min | P2 | [pg-conns-saturated](runbooks/pg-conns-saturated.md) |
| `ratesengine_timescale_cagg_stale` | `time() - ratesengine_cagg_last_refresh_unix` per CAGG | > 5× its refresh interval | P2 | [cagg-stale](runbooks/cagg-stale.md) |
| `ratesengine_timescale_compression_lag` | `ratesengine_uncompressed_chunks_older_than_7d` | > 0 for > 24 h | P3 | [compression-lag](runbooks/compression-lag.md) |
| `ratesengine_timescale_backup_failed` | `ratesengine_pgbackrest_last_success_unix` | > 2× expected interval | P2 | [backup-failed](runbooks/backup-failed.md) |
| `ratesengine_timescale_backup_none_24h` | same | > 24 h | **P1** | [backup-failed](runbooks/backup-failed.md) |

## Cache / serving alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_redis_master_down` | `redis_up` per master | == 0 for > 30 s | **P1** | [redis-master-down](runbooks/redis-master-down.md) |
| `ratesengine_redis_memory_saturated` | `redis_memory_used_bytes / redis_memory_max_bytes * 100` | > 90 % for > 5 min | P2 | [redis-memory](runbooks/redis-memory.md) |
| `ratesengine_redis_evictions_high` | `rate(redis_evicted_keys_total[5m])` | > 100/s | P2 | [redis-memory](runbooks/redis-memory.md) |
| `ratesengine_redis_replication_broken` | `redis_connected_slaves` per master | < expected for > 2 min | P2 | [redis-replication](runbooks/redis-replication.md) |

## API plane alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_api_down` | `up{job="api"}` across regions | == 0 for > 60 s | **P1** | [api-down](runbooks/api-down.md) |
| `ratesengine_api_latency_p95_high` | `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))` | > 500 ms for > 2 min | P2 | [api-latency](runbooks/api-latency.md) |
| `ratesengine_api_latency_p99_high` | `histogram_quantile(0.99, ...)` | > 2 s for > 2 min | P2 | [api-latency](runbooks/api-latency.md) |
| `ratesengine_api_error_rate_high` | `rate(http_requests_total{status=~"5.."}[5m]) / rate(http_requests_total[5m])` | > 1 % for > 2 min | P2 | [api-5xx](runbooks/api-5xx.md) |
| `ratesengine_api_error_rate_critical` | same | > 5 % for > 2 min | **P1** | [api-5xx](runbooks/api-5xx.md) |
| `ratesengine_api_price_stale` | `ratesengine_price_staleness_seconds` per asset | > 120 s sustained 5 min | P2 | [price-stale](runbooks/price-stale.md) |

## Stellar / node alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_stellar_core_ledger_age` | `time() - ratesengine_stellar_core_last_ledger_time_unix` | > 60 s for > 2 min | **P1** | [core-lag](runbooks/core-lag.md) |
| `ratesengine_stellar_core_peers_low` | `ratesengine_stellar_core_peer_count` | < 5 for > 5 min | P2 | [core-peers](runbooks/core-peers.md) |
| `ratesengine_stellar_rpc_lag` | `ratesengine_stellar_rpc_latest_ledger_age_seconds` | > 300 s for > 5 min | P2 | [rpc-lag](runbooks/rpc-lag.md) |
| `ratesengine_stellar_archive_publish_fail` | `increase(ratesengine_stellar_archive_publish_errors_total[1h])` | > 0 | P3 | [archive-publish](runbooks/archive-publish.md) |
| `ratesengine_stellar_archive_divergence` | `ratesengine_archive_divergence_total` (cross-region hash check) | > 0 ever | **P1** | [archive-divergence](runbooks/archive-divergence.md) |

## Divergence / quality alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_price_divergence_warning` | `abs(our_price - ref_price) / ref_price` per pair | > 5 % for > 2 min | P3 | [price-divergence](runbooks/price-divergence.md) |
| `ratesengine_price_divergence_critical` | same | > 10 % for > 2 min | P2 | [price-divergence](runbooks/price-divergence.md) |
| `ratesengine_oracle_stale` | `time() - ratesengine_oracle_last_update_unix` per source | > 10× its resolution | P2 | [oracle-stale](runbooks/oracle-stale.md) |

## Aggregator alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_aggregator_silent` | `rate(ratesengine_aggregator_vwap_writes_total[5m])` | == 0 for > 5 min | **P1** | [aggregator-silent](runbooks/aggregator-silent.md) |
| `ratesengine_aggregator_outlier_storm` | `rate(ratesengine_aggregator_dropped_trades_total{reason="outlier"}[10m])` | > 5× baseline (offset 1h) for > 15 min | P3 | [aggregator-outlier-storm](runbooks/aggregator-outlier-storm.md) |
| `ratesengine_aggregator_class_drop_spike` | `rate(ratesengine_aggregator_dropped_trades_total{reason="class"}[10m])` | > 10× baseline (offset 1h) for > 15 min | P3 | [aggregator-class-drop-spike](runbooks/aggregator-class-drop-spike.md) |

## Infra / host alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_host_down` | `up` for any host | == 0 for > 2 min | P2 | [host-down](runbooks/host-down.md) |
| `ratesengine_host_cpu_high` | `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)` | > 90 % for > 10 min | P3 | [host-cpu-high](runbooks/host-cpu-high.md) |
| `ratesengine_host_memory_high` | `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes * 100` | > 90 % for > 10 min | P3 | [host-memory-high](runbooks/host-memory-high.md) |
| `ratesengine_zfs_pool_degraded` | `node_zfs_pool_state{state=~"DEGRADED|FAULTED|UNAVAIL"}` | any, for > 60 s | **P1** | [zfs-degraded](runbooks/zfs-degraded.md) |
| `ratesengine_nvme_smart_warn` | `node_disk_io_errors_total` or SMART attributes | > 0 increase in 1 h | P2 | [nvme-smart](runbooks/nvme-smart.md) |
| `ratesengine_nvme_thermal_throttle` | NVMe `composite_temperature` | > 70 °C for > 5 min | P2 | [nvme-thermal](runbooks/nvme-thermal.md) |

## Observability / meta alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `ratesengine_prometheus_scrape_failing` | `up{job=~"api\|indexer\|aggregator"}` | == 0 for any target > 2 min | P3 | [scrape-failing](runbooks/scrape-failing.md) |
| `ratesengine_alertmanager_config_bad` | `alertmanager_config_last_reload_successful` | == 0 | P2 | [alertmanager-bad-config](runbooks/alertmanager-bad-config.md) |
| `ratesengine_deadmansswitch` | `vector(1)` constant | MUST fire every minute | **P1** if receiver stops seeing it | [deadmansswitch](runbooks/deadmansswitch.md) |

The `deadmansswitch` alert is inverse-logic: AlertManager routes it
to a receiver that expects it every minute. If the receiver stops
seeing it, that's the alarm (catches AlertManager-down and
Prometheus-down scenarios).

---

## Rules of thumb

- **Every alert has a runbook.** No exceptions. CI check enforces.
- **Alerts that page oncall must be actionable.** If the runbook
  is "wake up, check the dashboard, probably go back to bed", the
  alert belongs in P3 / tickets, not P1.
- **Alerts fire on meaningful windows.** A 5-second blip that
  self-resolves should not page someone; the `for:` clause is
  mandatory on every rule.
- **Duplicate alerts are a smell.** If two rules fire on the same
  root cause, consolidate. Oncall shouldn't be paged twice for the
  same incident.
- **Every alert has a test.** Synthetic fixture → AlertManager →
  stub receiver → assert the right page fires. CI target
  `make test-alerts` (TBD) exercises this.

---

## Adding an alert

1. Define the metric in the code that exposes it
   (`internal/obs/*.go`); add to
   `docs/reference/metrics/README.md` (generated).
2. Write the Prometheus rule in `deploy/monitoring/rules/<area>.yml`.
3. Write the runbook at `docs/operations/runbooks/<name>.md` —
   copy `_template.md`.
4. Add a row to this catalogue.
5. Write an alert-firing test at `test/monitoring/<name>_test.yml`.

All five in one PR. The lint enforces the most-load-bearing
piece (`scripts/ci/lint-docs.sh` §9 — every rule's
`runbook_url` must point at an existing runbook file); the
metric-doc and catalogue-row checks catch the two next-most
common drifts. The alert-firing test at
`test/monitoring/<name>_test.yml` is not yet machine-checked
(`test/monitoring/` doesn't exist as a directory today) — write
it anyway as part of the same PR; the convention precedes the
enforcement.

---

## References

- [sev-playbook.md](sev-playbook.md) — response timelines each
  severity binds to.
- [runbooks/](runbooks/) — per-alert response steps.
- [repo-hygiene-plan.md §16](../architecture/repo-hygiene-plan.md#16-observability-discipline) —
  "no alert without a runbook" rule.
- External:
  - Prometheus best practices — <https://prometheus.io/docs/practices/alerting/>
  - The "USE method" — utilisation / saturation / errors.
