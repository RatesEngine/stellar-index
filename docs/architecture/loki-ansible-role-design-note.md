---
title: Loki + Promtail ansible role — design note
last_verified: 2026-05-01
status: design ratified (Task #72 — Loki sub-role; closes Task #72)
related:
  - docs/architecture/ha-plan.md §7 (observability)
  - docs/architecture/{patroni,redis-sentinel,haproxy,prometheus}-ansible-role-design-note.md (sister roles)
---

# Loki + Promtail ansible role — design note

> Closes Task #72's five-sub-role sweep (Patroni #344, Redis
> Sentinel #350, HAProxy #362, Prometheus #363, Loki this PR).
> Per ha-plan §7 the metrics + logs + tracing trio is "Prometheus
> + AlertManager + Grafana + Loki + Tempo" — this role lands the
> Loki + Promtail piece. Grafana + Tempo are separate roles
> (post-launch).

## Scope (decided)

- **In scope**:
  - Single-host Loki single-binary deployment per ha-plan §7
    ("Logs: Loki + Tempo" — singular, not paired).
  - Chunks stored in MinIO via S3 backend (already-deployed
    bucket; trivial extra config).
  - BoltDB index (file-based, single-host).
  - 30d retention matching Prometheus.
  - Promtail agent on every host that ships logs, scraping
    systemd journal + selected log files.
  - Single role file with two task surfaces — server tasks run
    on hosts in `log_aggregator` inventory group, agent tasks on
    every host in `log_shippers`.

- **Out of scope** (later):
  - **HA Loki pair.** Logs are less critical than metrics for
    on-call decisions; 1 host is acceptable for v1. Scale path
    documented in §"Future: scaling to HA" below.
  - **Grafana** — separate role.
  - **Tempo / distributed tracing** — separate role.
  - **Long-term log archival** — 30d retention with S3 chunks
    means logs older than that get pruned. Operator who wants
    longer retention bumps `loki_retention_days`.
  - **TLS** between Promtail and Loki — currently HTTP on
    internal CIDR only. Operator wraps in WireGuard if needed.

## Topology

```
 ┌──────────────────────────────────────────────────┐
 │  every host that runs a service or daemon:       │
 │   ratesengine-{api,aggregator,indexer},          │
 │   patroni, redis, haproxy, prometheus, alertmgr  │
 │                                                  │
 │   Promtail :9080  ◀── scrape systemd journal     │
 └────────────────────┬─────────────────────────────┘
                      │  push (HTTP loki/api/v1/push)
                      ▼
       ┌──────────────────────────────┐
       │  log-01                       │
       │  Loki single-binary           │
       │   - HTTP :3100  (loopback)    │
       │   - BoltDB index (local file) │
       └──────────────┬────────────────┘
                      │  s3 chunk writes
                      ▼
              ┌─────────────────┐
              │  MinIO          │
              │  loki-chunks/   │
              └─────────────────┘
```

## Why single-host (not paired) Loki

Per ha-plan §7, Loki is treated as observability-tier (logs are
forensic, not real-time-decision). A 1-host Loki:

- **Simpler to operate**: no memberlist clustering, no
  ingester ring, no consistent-hashing for stream IDs.
- **Acceptable failure mode**: a Loki outage means no NEW logs
  are queryable for the duration; existing logs in S3 are still
  there. Promtail buffers up to 10k entries in-memory during
  the outage; on restore, it flushes.
- **Trivial scale-up path**: the same Loki binary runs in
  microservices mode; switching is a config change + 2 more
  hosts when we outgrow 1.

## Storage backend: MinIO via S3

Already-deployed (`s3_endpoint = http://127.0.0.1:9000` per the
existing storage config). Add a `loki-chunks` bucket at deploy
time. Chunks are immutable post-write; ideal for object storage.

Index uses BoltDB (file-based) — single-host so a shared index
backend (DynamoDB-equiv) is overkill. If we scale to multi-host,
switch to TSDB index with S3 backing.

## Promtail config

```yaml
clients:
  - url: http://{{ log_aggregator_host }}:3100/loki/api/v1/push
    tenant_id: ratesengine
    backoff_config:
      min_period: 500ms
      max_period: 5m
      max_retries: 10

scrape_configs:
  - job_name: systemd_journal
    journal:
      max_age: 12h
      path: /var/log/journal
      labels:
        job: systemd
        instance: {{ inventory_hostname }}
    relabel_configs:
      - source_labels: ['__journal__systemd_unit']
        target_label: unit
      - source_labels: ['__journal__hostname']
        target_label: hostname
      - source_labels: ['__journal_priority_keyword']
        target_label: severity
```

Scrapes systemd journal (every service we run is a systemd unit,
so this catches everything cleanly). Labels by unit + hostname +
severity.

## Loki config

```yaml
auth_enabled: false   # internal-only; no multi-tenancy needed

server:
  http_listen_address: 127.0.0.1
  http_listen_port: 3100
  grpc_listen_address: 127.0.0.1

common:
  ring:
    instance_addr: 127.0.0.1
    kvstore:
      store: inmemory
  replication_factor: 1   # single-host
  path_prefix: /var/lib/loki

storage_config:
  aws:
    s3: s3://{{ s3_access_key }}:{{ s3_secret_key }}@{{ s3_endpoint }}/loki-chunks
    s3forcepathstyle: true
  boltdb_shipper:
    active_index_directory: /var/lib/loki/index
    cache_location: /var/lib/loki/index_cache
    shared_store: s3

schema_config:
  configs:
    - from: 2025-01-01
      store: boltdb-shipper
      object_store: s3
      schema: v12
      index:
        prefix: loki_index_
        period: 24h

limits_config:
  retention_period: {{ loki_retention_days }}d
  ingestion_rate_mb: 10
  ingestion_burst_size_mb: 20

compactor:
  working_directory: /var/lib/loki/compactor
  shared_store: s3
  compaction_interval: 10m
  retention_enabled: true
  retention_delete_delay: 2h
```

## Inventory model

```yaml
all:
  children:
    log_aggregator:
      hosts:
        log-01: { ansible_host: 10.0.0.71 }
      vars:
        loki_retention_days: 30
        # MinIO creds via env (already configured for galexie)
        s3_endpoint: http://10.0.0.10:9000

    # Promtail runs on every host listed below
    log_shippers:
      children:
        prometheus_pair: {}
        ratesengine_api: {}
        ratesengine_aggregator: {}
        ratesengine_indexer: {}
        haproxy_lb: {}
        redis_cluster: {}
        postgres_cluster: {}
```

The role's `tasks/main.yml` branches:
- Hosts in `log_aggregator` get the server install path.
- Hosts in `log_shippers` get the Promtail agent install path.
- A host could in principle be in both (we don't recommend it,
  but it works).

## Layout

```
configs/ansible/roles/loki/
├── README.md
├── defaults/main.yml
├── handlers/main.yml
├── meta/main.yml
├── tasks/
│   ├── main.yml                       branches by group membership
│   ├── server-01-preflight.yml
│   ├── server-02-install.yml          loki binary download + dirs + minio bucket
│   ├── server-03-configure.yml        render loki-config.yaml
│   ├── server-04-systemd.yml
│   ├── server-05-firewall.yml         allow :3100 on internal CIDR
│   ├── agent-01-install.yml           promtail binary download
│   ├── agent-02-configure.yml         render promtail-config.yaml
│   └── agent-03-systemd.yml
└── templates/
    ├── loki-config.yaml.j2
    ├── loki.service.j2
    ├── promtail-config.yaml.j2
    └── promtail.service.j2
```

## Edge cases / gotchas

1. **MinIO bucket creation**: the role creates the `loki-chunks`
   bucket if absent, using the same `mc` client / IAM user
   pattern as galexie. Need a `loki-writer` IAM policy with
   PutObject + GetObject on the bucket.

2. **Promtail journal access**: requires `Group=systemd-journal`
   on the systemd unit. The role adds the promtail user to the
   group at install time.

3. **Time skew between hosts**: Loki rejects ingest with
   timestamps too-far-in-the-future. Preflight asserts time-sync
   on both server + agent hosts (chronyd / systemd-timesyncd).

4. **Disk pressure on the Loki host**: BoltDB index lives
   locally; chunks go to S3 but the index can grow. Preflight
   asserts ≥ 50 GB free on `/var` to give index room.

5. **Loki retention vs S3 lifecycle**: the compactor handles
   retention internally — don't also set a MinIO bucket lifecycle
   rule, it'd race with the compactor's delete pass and may
   delete chunks Loki still needs.

6. **Promtail position file**: tracks which journal entries have
   been shipped. Lives at `/var/lib/promtail/positions.yaml` —
   if the host's `/var/lib/promtail` is wiped, Promtail
   re-ships the entire journal (which causes Loki to reject
   duplicates as "out-of-order").

## Future: scaling to HA

When we outgrow single-host:

1. Switch `replication_factor` from 1 to 2.
2. Switch `kvstore.store` from `inmemory` to `memberlist` and
   list both peers.
3. Switch BoltDB index to TSDB (S3-backed).
4. Add a 2nd `log_aggregator` host to inventory; re-run role.
5. Promtail's `clients:` list gets both Loki URLs (it
   round-robins).
6. Optionally front both with HAProxy for clean
   client-side targeting (same role pattern as the api-tier
   HAProxy already deployed).

No migration of existing chunks needed; both Loki instances
read from the same S3 bucket.

## Effort breakdown

| Step | Estimate |
|---|---|
| `defaults/main.yml` + inventory model docs | 1 h |
| `server-01..05` task files | 3 h |
| `agent-01..03` task files | 1.5 h |
| `templates/loki-config.yaml.j2` | 1.5 h |
| `templates/promtail-config.yaml.j2` | 1 h |
| `templates/*.service.j2` | 0.5 h |
| `README.md` | 1 h |
| Smoke test | 1 h |
| CHANGELOG | 0.5 h |
| **Total** | **~11 h, ~1.5 days** |

## Once Loki lands

- Task #72 fully closes (Patroni / Redis Sentinel / HAProxy /
  Prometheus / Loki all shipped).
- Coverage matrix #11–#16 row goes green.
- Grafana role (separate; post-launch) gets a working Loki
  datasource to wire in.
- Operators can SSH-tunnel to `log-01:3100` and query via
  `logcli` for ad-hoc forensics.
- Existing alert rules with `runbook_url` annotations work
  symmetrically: alerts route via PagerDuty/Slack (Prometheus
  role); on-call queries Loki for the surrounding logs of the
  alerted service.

## Open questions

1. **Multi-tenancy**: `auth_enabled: false` makes the whole
   Loki single-tenant. If we ever expose Loki to customers
   (unlikely — internal-only), enable auth + per-region
   tenant IDs. Defer.

2. **Promtail vs Vector vs Fluent Bit**: Promtail is the
   default, simplest, and Loki-native. Vector is more powerful
   (transformations, multi-sink) but operationally heavier.
   Stay with Promtail until we need Vector.

3. **Sampling**: high-volume logs (api request logs) might
   want sampling. Promtail can pipeline-drop based on labels.
   Defer until we see actual log volume in production.
