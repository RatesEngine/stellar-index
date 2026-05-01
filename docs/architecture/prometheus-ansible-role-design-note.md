---
title: Prometheus + AlertManager ansible role — design note
last_verified: 2026-05-01
status: design ratified (Task #72 — Prometheus sub-role)
related:
  - docs/architecture/ha-plan.md §7 (observability)
  - docs/architecture/{patroni,redis-sentinel,haproxy}-ansible-role-design-note.md (sister roles)
  - deploy/monitoring/rules/ (existing 1721 LoC of alert rules)
---

# Prometheus + AlertManager ansible role — design note

> Bootstraps the fourth sub-role of Task #72 after Patroni
> (#344), Redis Sentinel (#350), and HAProxy (#362). Closes the
> "metrics surface that emits → metrics surface that scrapes"
> seam for the launch-readiness HA path: the previous three
> roles all emit Prometheus metrics; this role is the consumer.

## Scope (decided)

- **In scope**:
  - 2-host Prometheus pair per ha-plan §7 (primary + replica),
    each running its own Prometheus + AlertManager.
  - Alertmanager cluster gossip between the two hosts for dedupe.
  - Scrape configs built per inventory group (ratesengine_api,
    ratesengine_aggregator, ratesengine_indexer, haproxy_lb,
    redis_cluster, postgres_cluster, plus a generic
    `node_exporter_targets` group).
  - All 17 existing rule files in `deploy/monitoring/rules/`
    loaded as-is.
  - Loopback-only API (`127.0.0.1:9090` + `9093`); operators
    SSH-tunnel.

- **Out of scope** (later):
  - **Thanos** for long-term storage (1-year downsample to
    MinIO per ha-plan §7). Deferred — 30d local retention is
    enough for launch.
  - **Federation from cloud Prometheus** for DR. Deferred —
    needs a second region first.
  - **Grafana** — separate role; this one stops at the metrics
    + alerting side.
  - **PagerDuty integration secret** — config plumbed but the
    actual integration-key lives in vault; operator supplies.
  - **Slack alerts** — config plumbed similarly; operator supplies
    webhook URL.

## Topology

Per ha-plan §7, both hosts run identical Prometheus + AlertManager.
Each Prometheus scrapes all targets independently (data
duplication is the HA mechanism). Alertmanagers cluster via gossip
on port 9094 and dedupe alerts before fanout.

```
       PagerDuty / Slack
              ▲
              │
  ┌───────────┴───────────┐
  │  alertmanager cluster │
  │  (gossip :9094)       │
  └─────────┬─────────────┘
            │ alerts
  ┌─────────┴─────────────┐         ┌────────────────────┐
  │ prom-01               │         │ prom-02            │
  │ Prometheus :9090      │         │ Prometheus :9090   │
  │ AlertManager :9093    │         │ AlertManager :9093 │
  └─────────┬─────────────┘         └─────────┬──────────┘
            │                                 │
            └────────────┬────────────────────┘
                         │ scrape
            ┌────────────┴────────────┐
            │  every host's metrics:  │
            │  - ratesengine-* :9464  │
            │  - haproxy :8404        │
            │  - redis-exporter :9121 │
            │  - node-exporter :9100  │
            │  - patroni REST :8008   │
            └─────────────────────────┘
```

## Scrape targets

Built dynamically from inventory groups; each target gets a
sensible scrape interval + label set:

| Source | Target | Port | Interval | Path |
|---|---|---|---|---|
| ratesengine-api | every host in `ratesengine_api` | 9464 | 15s | `/metrics` |
| ratesengine-aggregator | every host in `ratesengine_aggregator` | 9464 | 30s | `/metrics` |
| ratesengine-indexer | every host in `ratesengine_indexer` | 9464 | 30s | `/metrics` |
| HAProxy | every host in `haproxy_lb` | 8404 | 15s | `/metrics` (built-in 2.4+) |
| redis_exporter | every host in `redis_cluster` | 9121 | 30s | `/metrics` |
| Patroni textfile | via `node_exporter` on every host in `postgres_cluster` | 9100 | 30s | `/metrics` (textfile collector picks up /var/lib/node_exporter/textfile_collector/*.prom) |
| node_exporter | every host (any inventory group) | 9100 | 30s | `/metrics` |
| AlertManager (self) | every host in `prometheus_pair` | 9093 | 30s | `/metrics` |
| Prometheus (self) | every host in `prometheus_pair` | 9090 | 30s | `/metrics` |

The role's `prometheus.yml.j2` walks the inventory and emits
sections per group. Adding a new source = adding the group +
re-applying.

## Why two single-Prom hosts (not a clustered Prometheus)

Prometheus doesn't natively cluster — it's deliberately a
single-writer single-reader design. The "HA" pattern is two
independent instances scraping the same targets; queries hit
either, alerts dedupe at AlertManager.

This shape:
- Keeps each Prometheus simple and stateless-ish (its only state
  is its TSDB on local disk).
- Tolerates one host's full failure with zero data loss (the
  other has the same scrape history minus a few seconds at the
  partition window).
- Doesn't need Thanos or sidecars to operate.

## Storage + retention

- TSDB on `/var/lib/prometheus/data` (operator-mountable to a
  faster disk if needed; default fine for launch).
- Retention: 30d (set via `--storage.tsdb.retention.time=30d`).
- Block compaction defaults (2h blocks, compacted to 24h).
- Disk sizing: ~150 KB/sample-batch × ~500 series × 4 samples/min
  × 30d ≈ ~13 GB. Plenty of headroom on a standard 100 GB disk.

## AlertManager configuration

Two AlertManagers form a cluster via `--cluster.peer` flags
pointing at each other. They dedupe alerts before fanout. Routes:

```yaml
route:
  receiver: 'pagerduty-default'
  group_by: ['alertname', 'severity']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 12h
  routes:
    - match:
        severity: critical
      receiver: 'pagerduty-default'
    - match:
        severity: warning
      receiver: 'slack-default'
    - match:
        severity: info
      receiver: 'slack-default'

receivers:
  - name: 'pagerduty-default'
    pagerduty_configs:
      - service_key: '{{ alertmanager_pagerduty_key }}'
  - name: 'slack-default'
    slack_configs:
      - api_url: '{{ alertmanager_slack_webhook_url }}'
        channel: '{{ alertmanager_slack_channel | default("#alerts") }}'
        title: '{{ '{{' }} .GroupLabels.alertname {{ '}}' }}'
```

`alertmanager_pagerduty_key` and `alertmanager_slack_webhook_url`
are vault-supplied. When absent, the role still installs but the
notification routes are unconfigured — alerts accumulate in the
AlertManager UI but don't fan out. (Useful for non-prod.)

## Inventory model

```yaml
all:
  children:
    prometheus_pair:
      hosts:
        prom-01: { ansible_host: 10.0.0.61 }
        prom-02: { ansible_host: 10.0.0.62 }
      vars:
        prometheus_retention_days: 30
        alertmanager_slack_channel: "#ratesengine-alerts"
        # vault: alertmanager_pagerduty_key, alertmanager_slack_webhook_url
    ratesengine_api:        { hosts: { ... } }
    ratesengine_aggregator: { hosts: { ... } }
    ratesengine_indexer:    { hosts: { ... } }
    haproxy_lb:             { hosts: { ... } }
    redis_cluster:          { hosts: { ... } }
    postgres_cluster:       { hosts: { ... } }
```

Every host that runs a service AND has node_exporter installed
appears in both its service-specific group and an implicit
"every host in inventory" set.

## Layout

```
configs/ansible/roles/prometheus/
├── README.md                          per-role docs
├── defaults/main.yml                  inventory-overridable defaults
├── handlers/main.yml                  reload-prometheus, restart-alertmanager
├── meta/main.yml                      no dependencies
├── tasks/
│   ├── main.yml
│   ├── 01-preflight.yml               OS check, disk space
│   ├── 02-install.yml                 download Prometheus + AlertManager binaries; users; dirs
│   ├── 03-prometheus-configure.yml    render prometheus.yml + sync rule files from deploy/monitoring/rules/
│   ├── 04-alertmanager-configure.yml  render alertmanager.yml
│   ├── 05-systemd.yml                 systemd units for both daemons
│   ├── 06-firewall.yml                allow 9090/9093/9094 between prom-pair; loopback for queries
│   └── 07-monitoring.yml              self-scrape (Prometheus monitors Prometheus + AlertManager)
└── templates/
    ├── prometheus.yml.j2
    ├── alertmanager.yml.j2
    ├── prometheus.service.j2
    ├── alertmanager.service.j2
    └── node-exporter-textfile.j2     (only for hosts that don't already get textfile from another role)
```

## Edge cases / gotchas

1. **Rule-file sync from repo**: `deploy/monitoring/rules/*.yml`
   is the source-of-truth for alerting rules. The role copies
   them as-is into `/etc/prometheus/rules.d/` on each Prometheus
   host. Editing rules → re-run the role; reload picks up
   changes via SIGHUP without restart.

2. **AlertManager cluster gossip**: requires unicast TCP on
   port 9094 between the two hosts. Firewall rule covers it.
   AWS / cloud-firewall caveat: if the LAN doesn't allow
   broadcast, the unicast `--cluster.peer` flags work fine —
   no multicast required (unlike VRRP).

3. **Promtool config validation**: the role validates
   `prometheus.yml` with `promtool check config` before
   reload. A malformed render never lands.

4. **Scrape-target inventory drift**: when a new service host
   is added to inventory, this role MUST be re-run on the
   prometheus_pair hosts to re-render the scrape config. There's
   no auto-discovery (we deliberately don't run Consul / DNS-SD
   at this scale).

5. **PagerDuty + Slack vault truthiness**: the role tolerates
   missing vault entries — emits a `WARN` at preflight and
   leaves the notification routes empty. A misconfigured
   PagerDuty + missing operator → alerts go nowhere. Document
   loudly in the README.

6. **Time sync**: Prometheus relies on host clocks; preflight
   asserts `chronyd` or `systemd-timesyncd` is active.

## Effort breakdown

| Step | Estimate |
|---|---|
| `defaults/main.yml` + inventory model docs | 1 h |
| `01-preflight.yml` | 1 h |
| `02-install.yml` | 1.5 h |
| `03-prometheus-configure.yml` | 2 h |
| `04-alertmanager-configure.yml` | 1.5 h |
| `05-systemd.yml` | 1 h |
| `06-firewall.yml` | 0.5 h |
| `07-monitoring.yml` (self-scrape) | 0.5 h |
| `templates/prometheus.yml.j2` (inventory-driven scrape configs) | 2 h |
| `templates/alertmanager.yml.j2` | 1 h |
| `templates/*.service.j2` (systemd units) | 1 h |
| `README.md` (operator-facing) | 1.5 h |
| Smoke test | 1 h |
| CHANGELOG | 0.5 h |
| **Total** | **~15 h, ~2 days** |

## Once Prometheus lands

- Coverage matrix #11–#16 row narrows by one: Prometheus shipped;
  Loki remaining.
- AlertManager URL becomes the target for k6 spike scenario's
  `silenceForRun` — fully closes that loop.
- Every existing alert rule file is now actually loaded into
  production (most have been written but not deployed).

## Open questions

1. **Rule-file sync mechanism**: `copy:` from repo vs `synchronize:` /
   rsync. `copy:` is simpler but doesn't handle deletions
   (deleting a rule file from the repo doesn't delete it from
   the host). Recommend a `file: state=absent` cleanup pass for
   the rules.d/ directory before the copy.

2. **Alertmanager template for PagerDuty event severity**:
   Severity from rule labels → PagerDuty event severity field.
   Defer to operator; this role just plumbs the integration key.

3. **Federation between the two Prometheus instances**: not
   needed for launch (queries hit either; data is duplicated).
   Add if a query layer in front needs cross-host views.
