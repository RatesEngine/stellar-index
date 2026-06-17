---
title: Alerts Catalogue
last_verified: 2026-05-02
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

- **Name** — `stellarindex_<area>_<specific>`. Stable — referenced
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
| `stellarindex_ingestion_source_stopped` | `rate(stellarindex_source_events_total[30m])` per high-volume source | == 0 for > 15 min on an enabled source | P2 | [source-stopped](runbooks/source-stopped.md) |
| `stellarindex_ingestion_source_stopped_low_volume_dex` | `rate(stellarindex_source_events_total[6h])` for comet / phoenix / soroswap / blend | == 0 for > 30 min | P2 | [source-stopped](runbooks/source-stopped.md) |
| `stellarindex_ingestion_source_stopped_daily_publisher` | `rate(stellarindex_source_events_total[30h])` for ecb / band | == 0 for > 1 h | P2 | [source-stopped](runbooks/source-stopped.md) |
| `stellarindex_ingestion_all_sources_stopped` | `sum(rate(stellarindex_source_events_total[5m]))` | == 0 for > 3 min | **P1** | [all-ingestion-down](runbooks/all-ingestion-down.md) |
| `stellarindex_ingestion_cursor_stuck` | `increase(stellarindex_cursor_last_ledger[5m])` per source | == 0 while source is live | P2 | [cursor-stuck](runbooks/cursor-stuck.md) |
| `stellarindex_ingestion_orphan_events` | `rate(stellarindex_source_orphan_events_total[10m])` | > 10/min per source | P3 | [orphan-events](runbooks/orphan-events.md) |
| `stellarindex_ingestion_decode_error` | `rate(stellarindex_source_decode_errors_total[5m])` | > 1/s sustained 5 min | P3 | [decode-errors](runbooks/decode-errors.md) |
| `stellarindex_ingestion_discovery_drops` | `increase(stellarindex_discovery_dropped_hits_total[10m])` | > 0 sustained 10 min | P3 | [discovery-drops](runbooks/discovery-drops.md) |
| `stellarindex_ingestion_insert_errors` | `rate(stellarindex_source_insert_errors_total[5m])` per (source, kind) | > 0.1/s (≈6/min) sustained 5 min | P2 | [insert-errors](runbooks/insert-errors.md) |
| `stellarindex_ingestion_duplicate_flood` | `rate(stellarindex_trade_insert_outcome_total{outcome="duplicate"}[10m])` AND `rate(...{outcome="new"}[10m]) == 0` per source | duplicates > 0.5/s with zero new for 10 min | P2 | [ingestion-duplicate-flood](runbooks/ingestion-duplicate-flood.md) |
| `stellarindex_ingestion_source_insert_stale` | `time() - stellarindex_source_last_insert_unix` per source AND `source_enabled=1` | > 3600 s for ≥ 10 min | P2 | [ingestion-duplicate-flood](runbooks/ingestion-duplicate-flood.md) |
| `stellarindex_ingest_gap_detected` | `max by (source) (stellarindex_ingest_gap_max_size_ledgers) > 1000` per (source, table) | sustained 15 min | **P1** | [ingest-gap-detected](runbooks/ingest-gap-detected.md) + per-source [sdex-gap-detected](runbooks/sdex-gap-detected.md) / [projector-replay](runbooks/projector-replay.md) |
| `stellarindex_ingest_gap_detector_silent` | `rate(stellarindex_ingest_gap_detector_runs_total{outcome="ok"}[15m]) == 0` OR series absent for 15 min | for ≥ 10 min | P2 | [ingest-gap-detector-silent](runbooks/ingest-gap-detector-silent.md) |
| `stellarindex_projector_lag_high` | `max by (source) (stellarindex_projector_lag_ledgers)` | > 256 ledgers sustained 10 min | P3 | [projector-lag](runbooks/projector-lag.md) |
| `stellarindex_projector_error_rate_high` | `rate(stellarindex_projector_runs_total{outcome="error"}[15m])` per source | > 0.05/s sustained 15 min | P3 | [projector-lag](runbooks/projector-lag.md) |
| `stellarindex_external_poller_stale` | `time() - stellarindex_external_poller_last_success_unix{source!="ecb"}` | > 1800 s for > 5 min | P2 | [external-poller-stale](runbooks/external-poller-stale.md) |
| `stellarindex_external_poller_stale_ecb` | `time() - stellarindex_external_poller_last_success_unix{source="ecb"}` | > 43200 s (12h) for > 10 min | P3 | [external-poller-stale](runbooks/external-poller-stale.md) |
| `stellarindex_external_poller_error_rate_high` | `rate(stellarindex_external_poller_polls_total{outcome="error"}[15m]) / sum(...) ` | > 0.5 sustained 15 min | P3 | [external-poller-error-rate-high](runbooks/external-poller-error-rate-high.md) |

Historical note: the former `stellarindex_ingestion_lag_high` alert was retired
when the repo moved off the legacy orchestrator topology and the live indexer
stopped emitting a trustworthy per-source lag gauge. Its last runbook remains
archived at [ingestion-lag](runbooks/ingestion-lag.md) until a replacement
signal lands.

## Storage alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_timescale_primary_down` | `up{job="postgres",role="primary"}` | == 0 for > 30 s | **P1** | [timescale-primary-down](runbooks/timescale-primary-down.md) |
| `stellarindex_timescale_replica_lag` | `pg_replication_lag_seconds` on sync replica | > 5 s for > 2 min | P2 | [replica-lag](runbooks/replica-lag.md) |
| `stellarindex_timescale_disk_full` | `(node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100` on DB vol | < 10 % | **P1** | [db-disk-full](runbooks/db-disk-full.md) |
| `stellarindex_timescale_disk_warning` | same | < 20 % | P2 | [db-disk-full](runbooks/db-disk-full.md) |
| `stellarindex_node_root_disk_full` | same expr on `mountpoint="/"` (distinct from DB vol — root FS holds /var/log + /tmp + /var/cache) | < 10 % | **P1** | [node-root-disk-full](runbooks/node-root-disk-full.md) |
| `stellarindex_node_root_disk_warning` | same | < 20 % | P2 | [node-root-disk-warning](runbooks/node-root-disk-warning.md) |
| (no active alert — surfaced via API log) | `forex: fx_quotes persist failed` log line — runtime symptom of an unapplied schema migration | repeating every ~5 min | P3 | [fx-history-missing](runbooks/fx-history-missing.md) |
| `stellarindex_postgres_ping_failing` | `rate(stellarindex_postgres_ping_total{outcome="error"}[5m])` | > 0.5/s for > 2 min — indexer pool wedged (F-0151) | **P1** | [postgres-ping-failing](runbooks/postgres-ping-failing.md) |
| `stellarindex_timescale_connections_saturated` | `pg_stat_activity_count / pg_settings_max_connections * 100` | > 80 % for > 5 min | P2 | [pg-conns-saturated](runbooks/pg-conns-saturated.md) |
| `stellarindex_timescale_lock_table_pressure` | `pg_locks_count / (pg_settings_max_locks_per_transaction * pg_settings_max_connections)` | > 70 % for > 5 min | P3 | [pg-conns-saturated](runbooks/pg-conns-saturated.md) |
| `stellarindex_timescale_cagg_stale` | `time() - stellarindex_cagg_last_refresh_unix` per CAGG | > 5× its refresh interval | P2 | [cagg-stale](runbooks/cagg-stale.md) |
| `stellarindex_timescale_compression_lag` | `stellarindex_uncompressed_chunks_older_than_7d` | > 0 for > 24 h | P3 | [compression-lag](runbooks/compression-lag.md) |
| `stellarindex_timescale_backup_failed` | `stellarindex_pgbackrest_last_success_unix` | > 2× expected interval | P2 | [backup-failed](runbooks/backup-failed.md) |
| `stellarindex_timescale_backup_none_24h` | same | > 24 h | **P1** | [backup-failed](runbooks/backup-failed.md) |

## Cache / serving alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_redis_master_down` | `redis_up` per master | == 0 for > 30 s | **P1** | [redis-master-down](runbooks/redis-master-down.md) |
| `stellarindex_redis_memory_saturated` | `redis_memory_used_bytes / redis_memory_max_bytes * 100` | > 90 % for > 5 min | P2 | [redis-memory](runbooks/redis-memory.md) |
| `stellarindex_redis_evictions_high` | `rate(redis_evicted_keys_total[5m])` | > 100/s | P2 | [redis-memory](runbooks/redis-memory.md) |
| `stellarindex_redis_replication_broken` | `redis_connected_slaves` per master | < expected for > 2 min | P2 | [redis-replication](runbooks/redis-replication.md) |
| `stellarindex_redis_writes_blocked` | `redis_rdb_last_bgsave_status` per master (also surfaces as `MISCONF` errors in client logs) | == 0 for > 60 s | **P1** | [redis-write-blocked-disk-full](runbooks/redis-write-blocked-disk-full.md) |

## API plane alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_api_down` | `up{job=~"stellarindex[_-]api"}` across regions | == 0 for > 60 s | **P1** | [api-down](runbooks/api-down.md) |
| `stellarindex_api_latency_p95_high` | `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))` | > 500 ms for > 2 min | P2 | [api-latency](runbooks/api-latency.md) |
| `stellarindex_api_latency_p99_high` | `histogram_quantile(0.99, ...)` | > 2 s for > 2 min | P2 | [api-latency](runbooks/api-latency.md) |
| `stellarindex_api_error_rate_high` | `rate(http_requests_total{status=~"5.."}[5m]) / rate(http_requests_total[5m])` | > 1 % for > 2 min | P2 | [api-5xx](runbooks/api-5xx.md) |
| `stellarindex_api_error_rate_critical` | same | > 5 % for > 2 min | **P1** | [api-5xx](runbooks/api-5xx.md) |
| `stellarindex_api_price_stale` | `stellarindex_price_staleness_seconds` per asset | > 120 s sustained 5 min | P2 | [price-stale](runbooks/price-stale.md) |
| `stellarindex_api_cache_miss_rate_high` | `rate(stellarindex_api_cache_ops_total{result="miss"}[5m]) / rate(stellarindex_api_cache_ops_total[5m])` per (cache, op) | > 50 % sustained 10 min on a hot op (≥ 0.1 req/s) | P2 | [cache-miss-rate-high](runbooks/cache-miss-rate-high.md) |

## SLA-probe alerts

Source: `cmd/stellarindex-sla-probe` runs every 15 min via the
systemd timer in `configs/healthchecks/stellarindex-sla-probe.timer`; metrics emitted
to node_exporter's textfile_collector via `-textfile-output`.
Per the service SLA targets — these are the synthetic
counterparts to the API-plane alerts above.

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_sla_probe_p95_breach` | `stellarindex_sla_probe_latency_ms{quantile="0.95"}` | > 200 ms for ≥ 30 min | **P2** | [sla-probe-p95-breach](runbooks/sla-probe-p95-breach.md) |
| `stellarindex_sla_probe_freshness_breach` | `stellarindex_sla_probe_freshness_sec` | > 30 s for ≥ 30 min | **P2** | [sla-probe-freshness-breach](runbooks/sla-probe-freshness-breach.md) |
| `stellarindex_sla_probe_unit_failed_alert` | `stellarindex_sla_probe_unit_failed` | > 0 for ≥ 30 min | P3 | [sla-probe-unit-failed](runbooks/sla-probe-unit-failed.md) |
| `stellarindex_sla_probe_stale` | `time() - stellarindex_sla_probe_last_pass_timestamp` | > 90 min for ≥ 5 min | **P2** | [sla-probe-stale](runbooks/sla-probe-stale.md) |

## SLO burn-rate alerts (multi-window)

Per [ADR-0009](../adr/0009-latency-budget.md). Pattern from the
Google SRE workbook: short + long windows must BOTH agree before
firing. Suppresses single-spike noise; catches both fast burns
(near-immediate budget consumption) and slow drifts (sustained
sub-target). Backstop direct-threshold alerts (above) stay live
for incident-time clarity.

| Name | SLO | Burn rate (× monthly budget) | Severity | Runbook |
| ---- | --- | ---------------------------- | -------- | ------- |
| `stellarindex_slo_latency_burn_fast` | 99.9% under 200ms | > 14.4× over 5m AND 1h | **P1** | [slo-latency-burn-fast](runbooks/slo-latency-burn-fast.md) |
| `stellarindex_slo_latency_burn_medium` | same | > 6× over 30m AND 6h | **P1** | [slo-latency-burn-medium](runbooks/slo-latency-burn-medium.md) |
| `stellarindex_slo_latency_burn_slow` | same | > 1× over 6h AND 24h | P3 | [slo-latency-burn-slow](runbooks/slo-latency-burn-slow.md) |
| `stellarindex_slo_availability_burn_fast` | 99.99% non-5xx | > 14.4× over 5m AND 1h | **P1** | [slo-availability-burn-fast](runbooks/slo-availability-burn-fast.md) |
| `stellarindex_slo_availability_burn_medium` | same | > 6× over 30m AND 6h | **P1** | [slo-availability-burn-medium](runbooks/slo-availability-burn-medium.md) |
| `stellarindex_slo_availability_burn_slow` | same | > 1× over 6h AND 24h | P3 | [slo-availability-burn-slow](runbooks/slo-availability-burn-slow.md) |

## Stellar / node alerts

> **Inert on r1 (2026-04-30).** The first four alerts in this table
> reference metrics produced by stellar-core / stellar-rpc / the
> stellar-core-prometheus-exporter. All three were removed from r1
> on 2026-04-23 ([r1-deployment-state.md](r1-deployment-state.md)),
> so these alerts have no producer and cannot fire on the current
> deployment posture. They remain in the rule file for Phase-3
> (Tier-1 validator rollout, ADR-0004); each runbook's *Deployment
> posture* callout explains the revival path. `archive-divergence`
> is **not** affected — it consumes the cross-region hash-check
> metric written by `scripts/ops/archive-cross-check.sh` and remains
> live.

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_stellar_core_ledger_age` | `time() - stellarindex_stellar_core_last_ledger_time_unix` | > 60 s for > 2 min | **P1** | [core-lag](runbooks/core-lag.md) |
| `stellarindex_stellar_core_peers_low` | `stellarindex_stellar_core_peer_count` | < 5 for > 5 min | P2 | [core-peers](runbooks/core-peers.md) |
| `stellarindex_stellar_rpc_lag` | `stellarindex_stellar_rpc_latest_ledger_age_seconds` | > 300 s for > 5 min | P2 | [rpc-lag](runbooks/rpc-lag.md) |
| `stellarindex_stellar_archive_publish_fail` | `increase(stellarindex_stellar_archive_publish_errors_total[1h])` | > 0 | P3 | [archive-publish](runbooks/archive-publish.md) |
| `stellarindex_stellar_archive_divergence` | `stellarindex_archive_divergence_total` (cross-region hash check) | > 0 ever | **P1** | [archive-divergence](runbooks/archive-divergence.md) |

## Archive completeness alerts

Per [ADR-0017](../adr/0017-archive-completeness-invariants.md). Both
the primary archive (`galexie-archive/` MinIO) and the cross-anchor
archive (`/srv/history-archive/`) have hard completeness contracts.
The daily `archive-completeness.timer` enforces them on R1; R2 + R3
delegate to R1 for cross-anchor checks but verify their own
chain-link locally. See [archive-completeness.md](archive-completeness.md).

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_archive_files_missing` | `archive_files_missing` per archive | > 0 for > 4 h | P2 | [archive-files-missing](runbooks/archive-files-missing.md) |
| `stellarindex_archive_completeness_stale` | `time() - archive_completeness_last_success_timestamp` | > 26 h | P2 | [archive-completeness-stale](runbooks/archive-completeness-stale.md) |
| `stellarindex_archive_completeness_critical_stale` | same | > 48 h on R1 (integrity leader) | **P1** | [archive-completeness-stale](runbooks/archive-completeness-stale.md) |
| `stellarindex_archive_repair_source_degraded` | `archive_completeness_repair_failures_total / archive_completeness_repair_attempts_total` per source | > 0.10 over 1 h | P3 | [archive-repair-source-degraded](runbooks/archive-repair-source-degraded.md) |
| `stellarindex_galexie_archive_tip_lag_high` | `galexie_archive_tip_lag_ledgers` (archive newest vs live newest) | > 5,000 for 30 m | P3 | [galexie-archive-tip-lag](runbooks/galexie-archive-tip-lag.md) |
| `stellarindex_galexie_archive_tip_lag_severe` | same | > 50,000 for 30 m | **P1** | [galexie-archive-tip-lag](runbooks/galexie-archive-tip-lag.md) |
| `stellarindex_galexie_archive_tip_lag_metric_stale` | `time() - galexie_archive_tip_lag_updated_seconds` | > 30 m for 15 m | P3 | [galexie-archive-tip-lag](runbooks/galexie-archive-tip-lag.md) |

Defense-in-depth for `#26` — the original 23-day silent stall of
`galexie-archive`. The post-`#26` fix is the hourly
`galexie-archive-fill.timer`; these alerts page within hours if
that timer (or its `mc` aliases / aws-public IAM / MinIO
mtime-poison failure mode) silently breaks. Metric source:
node_exporter textfile_collector reads
`/var/lib/node_exporter/textfile_collector/galexie_archive_tip_lag.prom`,
refreshed every 5 min by `galexie-archive-tip-lag.timer`.

## Ledgerstream tier alerts

Per [ADR-0027](../adr/0027-lcm-cache-tiering.md). R1's
`TieredDataStore` (`internal/ledgerstream/tiered.go`) reads each LCM
from the local `galexie-archive` MinIO bucket (hot) and falls back
on `NoSuchKey` to the AWS public bucket (cold). Pre-§3 of the
rollout (`storage.cold_tier_enabled = false`) the cold path never
runs and these alerts stay silent.

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_ledgerstream_tier_both_missing` | `stellarindex_ledgerstream_tier_read_total{outcome="both_missing"}` | `increase(...[5m]) > 0` for 5 m | **P1** | [ledgerstream-tier-both-missing](runbooks/ledgerstream-tier-both-missing.md) |

`both_missing` is the cold-tier failure mode the rollout sequence
in ADR-0027 was designed to recover from cleanly: the runbook walks
the rehydrate-from-peer + disable-trim-timer steps.

## verify-archive timer alerts

Per the ADR-0016 per-region trust model: R1 runs verify-archive Tier A
(chain-link integrity) nightly via systemd; R2 + R3 trust R1 and run
their own slower cadence. The timer fires once per night at 03:23 UTC
+ jitter; node_exporter's `--collector.systemd` exports the unit
state so failures and stale runs trigger the alerts below. See
[verify-archive-tier-a.timer](https://github.com/StellarIndex/stellar-index/blob/main/deploy/systemd/verify-archive-tier-a.timer).

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_verify_archive_unit_failed` | `node_systemd_unit_state{name="verify-archive-tier-a.service",state="failed"}` | == 1 for > 5 min | P3 | [verify-archive-unit-failed](runbooks/verify-archive-unit-failed.md) |
| `stellarindex_verify_archive_run_stale` | `time() - node_systemd_timer_last_trigger_seconds{name="verify-archive-tier-a.timer"}` | > 36 h for > 10 min | **P2** | [verify-archive-run-stale](runbooks/verify-archive-run-stale.md) |

## Anomaly + freeze alerts

Per [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md).
The freeze policy fires only when `confidence < 0.10 AND z_score >
5σ AND source_count <= 1` — the extreme corner where multi-source
consensus can't help. Operator runbook walks through review +
override.

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_anomaly_freeze_engaged` | `stellarindex_anomaly_freeze_engaged_total` per class | rate > 0 over 5m | P3 | [anomaly-freeze-engaged](runbooks/anomaly-freeze-engaged.md) |
| `stellarindex_anomaly_freeze_sustained` | `stellarindex_anomaly_freeze_engaged_total` per class | rate > 0 sustained 1h+ | **P1** | [anomaly-freeze-sustained](runbooks/anomaly-freeze-sustained.md) |
| `stellarindex_anomaly_freeze_recovery_stalled` | `stellarindex_anomaly_freeze_engaged_total` vs `_recovered_total` + `_recovery_sweeps_total{outcome!="ok"}` | engaged > recovered for 2h+ AND sweep errors in last 15m | P3 | [freeze-recovery-stalled](runbooks/freeze-recovery-stalled.md) |

## Divergence / quality alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_price_divergence_warning` | `abs(our_price - ref_price) / ref_price` per pair | > 5 % for > 2 min | P3 | [price-divergence](runbooks/price-divergence.md) |
| `stellarindex_price_divergence_critical` | same | > 10 % for > 2 min | P2 | [price-divergence](runbooks/price-divergence.md) |
| `stellarindex_oracle_stale` | `time() - stellarindex_oracle_last_update_unix` per source | > 10× its resolution | P2 | [oracle-stale](runbooks/oracle-stale.md) |
| `stellarindex_divergence_refresh_error_dominant` | `rate(divergence_refresh_total{outcome="refresh_error"}[5m]) > rate(...{outcome="ok"}[5m])` | sustained 30 min | P3 | [divergence-refresh-error-dominant](runbooks/divergence-refresh-error-dominant.md) |

## Aggregator alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_aggregator_silent` | `rate(stellarindex_aggregator_vwap_writes_total[5m])` | == 0 for > 5 min | **P1** | [aggregator-silent](runbooks/aggregator-silent.md) |
| `stellarindex_aggregator_outlier_storm` | `rate(stellarindex_aggregator_dropped_trades_total{reason="outlier"}[10m])` | > 5× baseline (offset 1h) for > 15 min | P3 | [aggregator-outlier-storm](runbooks/aggregator-outlier-storm.md) |
| `stellarindex_aggregator_class_drop_spike` | `rate(stellarindex_aggregator_dropped_trades_total{reason="class"}[10m])` | > 10× baseline (offset 1h) for > 15 min | P3 | [aggregator-class-drop-spike](runbooks/aggregator-class-drop-spike.md) |
| `stellarindex_aggregator_fx_snap_fallback_dominant` | `rate(stellarindex_aggregator_fx_snap_fallback_total[15m]) / rate(stellarindex_aggregator_triangulations_total{outcome="ok"}[15m])` | > 0.5 for > 30 min | P3 | [aggregator-fx-snap-fallback-dominant](runbooks/aggregator-fx-snap-fallback-dominant.md) |
| `stellarindex_aggregator_cache_write_errors` | `rate(stellarindex_aggregator_vwap_cache_write_errors_total[5m])` | > 0 for ≥ 2 min | **P1** | [redis-write-blocked-disk-full](runbooks/redis-write-blocked-disk-full.md) |
| `stellarindex_customer_webhook_delivery_failing` | `rate(stellarindex_customer_webhook_delivery_attempts_total{outcome=~"server_error\|network_error"}[5m])` | > 0.1/s for ≥ 15 min | P3 | [customer-webhook-delivery-failing](runbooks/customer-webhook-delivery-failing.md) |
| `stellarindex_customer_webhook_delivery_exhausted` | `rate(stellarindex_customer_webhook_delivery_attempts_total{outcome="exhausted"}[1h])` | > 0 for ≥ 1h | informational | [customer-webhook-delivery-failing](runbooks/customer-webhook-delivery-failing.md) |
| `stellarindex_stripe_platform_sync_errors` | `rate(stellarindex_stripe_platform_sync_errors_total[15m])` | > 0 for ≥ 15 min | P3 | [stripe-platform-sync-errors](runbooks/stripe-platform-sync-errors.md) |
| `stellarindex_tls_cert_expiring_soon` | `stellarindex_tls_cert_not_after_unix - time()` per host | < 14 days for ≥ 1 h | P2 | [tls-cert-expiring-soon](runbooks/tls-cert-expiring-soon.md) |

## Supply alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_supply_cross_check_divergence` | `stellarindex_supply_cross_check_divergence_stroops` per `classic_key` | > 1 stroop for > 5 min | P3 | [supply-cross-check-divergence](runbooks/supply-cross-check-divergence.md) |
| `stellarindex_supply_snapshot_unit_failed_alert` | `stellarindex_supply_snapshot_unit_failed` | > 0 for ≥ 30 min | P3 | [supply-snapshot-unit-failed](runbooks/supply-snapshot-unit-failed.md) |
| `stellarindex_supply_snapshot_stale` | `time() - stellarindex_supply_snapshot_last_success_timestamp` | > 36 h for ≥ 5 min | P3 | [supply-snapshot-stale](runbooks/supply-snapshot-stale.md) |
| `stellarindex_supply_snapshot_critical_stale` | same | > 72 h for ≥ 5 min | **P2** | [supply-snapshot-stale](runbooks/supply-snapshot-stale.md) |
| `stellarindex_supply_snapshot_never_initialized` | `absent_over_time(stellarindex_supply_snapshot_last_success_timestamp[36h])` | == 1 for ≥ 5 min | P3 | [supply-snapshot-never-initialized](runbooks/supply-snapshot-never-initialized.md) |
| `stellarindex_supply_snapshot_circulating_zero` | `stellarindex_supply_snapshot_circulating_xlm{asset_key="XLM"}` | ≤ 0 for ≥ 5 min | **P2** | [supply-snapshot-circulating-zero](runbooks/supply-snapshot-circulating-zero.md) |
| `stellarindex_aggregator_supply_refresh_stalled` | `time() - max(timestamp(stellarindex_aggregator_supply_refresh_total{outcome="ok"}))` | > 30 min for ≥ 5 min | **P2** | [supply-refresh-stalled](runbooks/supply-refresh-stalled.md) |
| `stellarindex_aggregator_supply_refresh_error_dominant` | error-outcome rate / total-rate | > 50% for ≥ 30 min | P3 | [supply-refresh-error-dominant](runbooks/supply-refresh-error-dominant.md) |
| `stellarindex_aggregator_supply_refresh_never_initialized` | `absent_over_time(stellarindex_aggregator_supply_refresh_total{outcome="ok"}[36h])` | == 1 for ≥ 5 min | P3 | [aggregator-supply-refresh-never-initialized](runbooks/aggregator-supply-refresh-never-initialized.md) |

## Infra / host alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_host_down` | `up` for any host | == 0 for > 2 min | P2 | [host-down](runbooks/host-down.md) |
| `stellarindex_host_cpu_high` | `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)` | > 90 % for > 10 min | P3 | [host-cpu-high](runbooks/host-cpu-high.md) |
| `stellarindex_host_memory_high` | `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes * 100` | > 90 % for > 10 min | P3 | [host-memory-high](runbooks/host-memory-high.md) |
| `stellarindex_zfs_pool_degraded` | `node_zfs_pool_state{state=~"DEGRADED|FAULTED|UNAVAIL"}` | any, for > 60 s | **P1** | [zfs-degraded](runbooks/zfs-degraded.md) |
| `stellarindex_nvme_smart_warn` | `node_disk_io_errors_total` or SMART attributes | > 0 increase in 1 h | P2 | [nvme-smart](runbooks/nvme-smart.md) |
| `stellarindex_nvme_thermal_throttle` | NVMe `composite_temperature` | > 70 °C for > 5 min | P2 | [nvme-thermal](runbooks/nvme-thermal.md) |

## Observability / meta alerts

| Name | Metric | Condition | Severity | Runbook |
| ---- | ------ | --------- | -------- | ------- |
| `stellarindex_prometheus_scrape_failing` | `up{job=~"api\|indexer\|aggregator"}` | == 0 for any target > 2 min | P3 | [scrape-failing](runbooks/scrape-failing.md) |
| `stellarindex_alertmanager_config_bad` | `alertmanager_config_last_reload_successful` | == 0 | P2 | [alertmanager-bad-config](runbooks/alertmanager-bad-config.md) |
| `stellarindex_deadmansswitch` | `vector(1)` constant | MUST fire every minute | **P1** if receiver stops seeing it | [deadmansswitch](runbooks/deadmansswitch.md) |
| `prometheus_down` (TSDB corruption) | systemd `prometheus.service` failed | exit-code != 0; runs ad-hoc, not a rule | **P1** | [prometheus-tsdb-corruption](runbooks/prometheus-tsdb-corruption.md) |
| `stellarindex_redis_exporter_down` | `up{job="redis_exporter"}` | == 0 for > 2 min OR series absent for 5 min | **P1** | [exporter-down](runbooks/exporter-down.md) |
| `stellarindex_postgres_exporter_down` | `up{job="postgres_exporter"}` | == 0 for > 2 min OR series absent for 5 min | **P1** | [exporter-down](runbooks/exporter-down.md) |
| `stellarindex_pgbackrest_exporter_down` | `up{job="pgbackrest_exporter"}` | == 0 for > 2 min OR series absent for 5 min | **P1** | [exporter-down](runbooks/exporter-down.md) |
| `stellarindex_minio_exporter_down` | `up{job="minio"}` | == 0 for > 2 min OR series absent for 5 min | **P1** | [exporter-down](runbooks/exporter-down.md) — or [minio-metrics-403](runbooks/minio-metrics-403.md) if the cause is a 403 (bearer-token gap) |

The four `*_exporter_down` rules close the F-0085 cascade-blindness gap surfaced by the 2026-05-26 audit — each exporter feeds an alert family whose detection silently fails if the exporter dies first. Adding the meta-alert ensures any future cascade surfaces immediately even when the metric-producing exporter is the same process tree dying alongside the failure it's meant to detect. The MinIO scrape specifically has a separate 403-shape failure (missing bearer-token file) documented in [minio-metrics-403](runbooks/minio-metrics-403.md) — operators paged by `stellarindex_minio_exporter_down` whose Prometheus `lastError` shows `HTTP status 403` should consult that runbook first.

The `deadmansswitch` alert is inverse-logic: AlertManager routes it
to a receiver that expects it every minute. If the receiver stops
seeing it, that's the alarm (catches AlertManager-down and
Prometheus-down scenarios).

`prometheus_down` is the disk-full / TSDB-corruption family — same
root cause as `redis-write-blocked-disk-full`. Doesn't have its own
Prometheus rule (Prometheus can't alert on its own absence — that's
what `deadmansswitch` is for); the runbook lives under the catalog
because the *recovery* needs documenting and the apt-shipped
systemd unit's `Restart=on-abnormal` doesn't auto-recover from it.

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
