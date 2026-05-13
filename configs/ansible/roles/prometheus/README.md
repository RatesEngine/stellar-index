# Ansible role — `prometheus`

Deploy a 2-host Prometheus + AlertManager pair per
[`docs/architecture/ha-plan.md §7`](../../../../docs/architecture/ha-plan.md):

- 2 hosts each running Prometheus + AlertManager.
- Each Prometheus independently scrapes all targets — data
  duplication is the HA mechanism.
- AlertManagers cluster via gossip on port 9094 (TCP+UDP) and
  dedupe alerts before fanout to PagerDuty + Slack.
- Rule files synced from
  [`deploy/monitoring/rules/`](../../../../deploy/monitoring/rules/)
  (1721 LoC of alerts shipped today).

Pairs with `patroni` (#344), `redis-sentinel` (#350), and
`haproxy` (#362) — this role is the consumer of all three's
emitted metrics. Design rationale lives in
[`docs/architecture/prometheus-ansible-role-design-note.md`](../../../../docs/architecture/prometheus-ansible-role-design-note.md).

## Prerequisites

- Two Prometheus hosts named per inventory (`prom-01` /
  `prom-02` by default). Each needs:
  - Ubuntu 24.04 LTS (or 22.04).
  - ≥ 20 GB free on `/var` (TSDB at 30d retention sizes to
    ~13 GB; preflight asserts 20 GB).
  - Time sync (`chronyd` or `systemd-timesyncd`) active —
    Prometheus is sensitive to clock skew; preflight asserts.
  - Network reachability to every scrape target's metrics port
    on the internal CIDR.
  - TCP + UDP 9094 between the two prom hosts (AlertManager
    cluster gossip).

- Vault contents (all optional; leaving any empty just means
  the corresponding fanout doesn't happen):
  - `alertmanager_pagerduty_key` — PagerDuty integration key
    (used for the `page`-severity route).
  - `alertmanager_slack_webhook_url` — Slack incoming-webhook
    URL (used for the `ticket` + `informational` routes via
    `chat-fanout`).
  - `alertmanager_discord_webhook_url` — Discord webhook URL
    (used for the `ticket` + `informational` routes via
    `chat-fanout`, parallel to Slack — operators can run either,
    both, or neither). Per the proposal's commitment to
    "integrated into discord/slack" alerting.

  Note (F-1265, 2026-05-13): the template uses the shared
  `page / ticket / informational` severity vocabulary that
  matches the standalone R1 config at
  `configs/alertmanager/alertmanager.r1.yml`. Earlier doc text
  referenced `critical / warning / info`; that vocabulary is
  gone from both files.

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

    # Scrape-target groups (any one is required for preflight to pass)
    ratesengine_api:        { hosts: { ... } }
    ratesengine_aggregator: { hosts: { ... } }
    ratesengine_indexer:    { hosts: { ... } }
    haproxy_lb:             { hosts: { ... } }
    redis_cluster:          { hosts: { ... } }
    postgres_cluster:       { hosts: { ... } }
```

## Running

```sh
cd configs/ansible
# F-1266 (2026-05-13): the actual entrypoint is monitoring.yml,
# not the non-existent playbooks/prometheus.yml that earlier
# versions of this README named.
ansible-playbook -i inventory/r1.yml playbooks/monitoring.yml --tags prometheus

# Reload after rule-file edits (no daemon restart):
ansible-playbook -i inventory/r1.yml playbooks/monitoring.yml --tags prometheus,config
```

`promtool check config` validates `prometheus.yml` BEFORE reload;
`promtool check rules` validates each rule file BEFORE copy;
`amtool check-config` validates `alertmanager.yml` BEFORE reload.
A malformed render never lands in production.

## Scrape config (auto-built from inventory)

The `prometheus.yml.j2` template walks the inventory and emits
one job per service group present:

| Job | Source group | Port | Interval |
|---|---|---|---|
| `ratesengine_api` | `ratesengine_api` | 9464 | 15s |
| `ratesengine_aggregator` | `ratesengine_aggregator` | 9464 | 30s |
| `ratesengine_indexer` | `ratesengine_indexer` | 9464 | 30s |
| `haproxy` | `haproxy_lb` | 8404 | 15s |
| `redis_exporter` | `redis_cluster` | 9121 | 15s |
| `node_exporter` | every host in any group | 9100 | 30s |
| `prometheus_pair` (self-scrape) | `prometheus_pair` | 9090 | 15s |
| `alertmanager_pair` (self-scrape) | `prometheus_pair` | 9093 | 15s |

Adding a new source: add the inventory group + re-run the role
on `prometheus_pair`. No manual scrape-config edits.

## Alert routing

```
page          → PagerDuty
ticket        → chat-fanout (Slack + Discord, whichever are wired)
informational → chat-fanout (Slack + Discord, whichever are wired)
```

Inhibit rules:
- A `page`-severity alert for a given `(alertname, service)`
  mutes `ticket` + `informational` alerts for the same pair to
  avoid stacking.

When `alertmanager_pagerduty_key` is empty the `page` route
is unconfigured. When BOTH `alertmanager_slack_webhook_url` AND
`alertmanager_discord_webhook_url` are empty the chat-fanout
receiver has no destinations and alerts accumulate in the
AlertManager UI (`http://127.0.0.1:9093/` via SSH-tunnel) but
don't reach a chat channel. Setting one webhook routes warnings
and info to that channel; setting both produces parallel fanout.
The preflight task warns when neither is set.

## Storage + retention

- TSDB at `/var/lib/prometheus/data`.
- Retention: 30d (override via `prometheus_retention_days`).
- Block compaction defaults: 2h blocks, compacted to 24h.
- Disk sizing: ~13 GB at 30d for our scrape volume; preflight
  asserts ≥ 20 GB free for headroom.

## Operator UI access

```sh
# Prometheus query UI (loopback-only; SSH-tunnel)
ssh -L 9090:127.0.0.1:9090 root@prom-01
# → http://localhost:9090/

# AlertManager UI (loopback-only; SSH-tunnel)
ssh -L 9093:127.0.0.1:9093 root@prom-01
# → http://localhost:9093/
```

Don't expose `9090` or `9093` publicly — neither has built-in
auth. SSH-tunnel for ad-hoc queries; future Grafana role provides
the operator-facing dashboards layer.

## Rule-file sync

`deploy/monitoring/rules/*.yml` is the source-of-truth. The role:

1. Lists current rule files in the repo (delegate-to-localhost).
2. Lists rule files currently on the host.
3. Removes any host file no longer in the repo (cleanup pass —
   `copy:` alone wouldn't catch deletions).
4. Copies each repo rule file to `/etc/prometheus/rules.d/`,
   validating with `promtool check rules` before write.
5. Triggers Prometheus SIGHUP reload (zero-drop).

Editing rules → re-run the role with `--tags config`. No daemon
restart.

## Cluster gossip

The two AlertManagers gossip on port 9094 (TCP + UDP) to
synchronise alert state. Required for dedupe; without it, both
AlertManagers fan out independently and on-call gets paged
twice.

The `06-firewall.yml` task opens 9094 on the internal CIDRs.
Gossip uses unicast, so no multicast/VRRP-style host config
needed (unlike keepalived in the haproxy role).

## What this role does NOT cover

- **Grafana** — separate role. This role is the metrics +
  alerting side; Grafana is the visualization layer.
- **Thanos / long-term storage** — deferred per ha-plan §7;
  30d local retention covers launch.
- **Federation between regions** — needs a second region first.
- **Auto-discovery (Consul / DNS-SD)** — single-region scale
  doesn't need it; static configs are simpler to debug at SEV-1
  time.
