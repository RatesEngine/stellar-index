# Ansible role — `loki`

Deploy Loki (single-binary log aggregator) on one host plus
Promtail agents on every other host that produces logs. Per
[`docs/architecture/ha-plan.md §7`](../../../../docs/architecture/ha-plan.md):

- Single host runs Loki with **MinIO S3 chunks** + BoltDB index.
- Every other host runs **Promtail** scraping the systemd journal.
- 30d retention via Loki's compactor.

**Closes Task #72** — the fifth and final sub-role after Patroni
(#344), Redis Sentinel (#350), HAProxy (#362), and Prometheus
(#363). Design rationale lives in
[`docs/architecture/loki-ansible-role-design-note.md`](../../../../docs/architecture/loki-ansible-role-design-note.md).

## Prerequisites

- One log-aggregator host named per inventory (`log-01` by
  default). Needs:
  - Ubuntu 24.04 LTS (or 22.04).
  - ≥ 50 GB free on `/var` (BoltDB index + working dirs;
    chunks live in MinIO so don't count here).
  - Time sync (`chronyd` or `systemd-timesyncd`) active.
  - Network reachability to the MinIO endpoint
    (`s3_endpoint`).
  - The `loki-chunks` MinIO bucket pre-created with a
    `loki-writer` IAM user (operator one-shot;
    `mc mb local/loki-chunks` + `mc admin policy create`).

- Every host in `log_shippers` runs Promtail. Needs:
  - `systemd-journal` group access for Promtail user (the role
    handles).
  - `/var/log/journal` populated (default on Ubuntu).
  - Outbound HTTP to the log_aggregator host on port 3100
    (firewall rule on the aggregator side handles).

## Inventory model

```yaml
all:
  children:
    log_aggregator:
      hosts:
        log-01: { ansible_host: 10.0.0.71 }
      vars:
        loki_retention_days: 30
        s3_endpoint: http://10.0.0.10:9000

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

Hosts in `log_aggregator` get the server install path; hosts in
`log_shippers` get the Promtail agent path. A host could be in
both (the role applies both task surfaces).

## Running

F-1266 (2026-05-13): `playbooks/loki.yml` is on the L4 cutover
backlog and hasn't landed yet; the role is applied ad-hoc via
tag-filtered plays today. When the playbook lands, restore the
original commands below.

```sh
cd configs/ansible
# Deploy Loki server only — playbook TBD:
# ansible-playbook -i inventory/r1.yml playbooks/loki.yml --tags loki,server

# Deploy Promtail agent only (every host in log_shippers) — playbook TBD:
# ansible-playbook -i inventory/r1.yml playbooks/loki.yml --tags loki,agent

# Both — playbook TBD:
# ansible-playbook -i inventory/r1.yml playbooks/loki.yml --tags loki
```

## Storage backend

Chunks → MinIO via S3 backend (already deployed for galexie).
The role expects:
- `loki-chunks` bucket created.
- A `loki-writer` IAM user with PutObject + GetObject on the
  bucket. **Credentials go into `/etc/default/loki` as
  `AWS_ACCESS_KEY_ID=` and `AWS_SECRET_ACCESS_KEY=`** — the
  systemd unit's `EnvironmentFile=-/etc/default/loki` reads
  them directly and the upstream S3 driver expects those exact
  variable names. F-1286 (2026-05-13): earlier prose tried to
  remap from the galexie convention's `RATESENGINE_S3_*` names
  via `Environment=AWS_ACCESS_KEY_ID=${RATESENGINE_S3_ACCESS_KEY}`,
  but systemd `Environment=` does not perform `$` expansion —
  the literal `${...}` string would have been passed as the
  access key, failing S3 chunk writes while Loki looked
  process-alive. Set the two `AWS_*` env vars in
  `/etc/default/loki` directly. Galexie's separate
  `/etc/default/galexie` still uses the `RATESENGINE_S3_*`
  convention; they're independent EnvironmentFile sources.

Index → BoltDB on local filesystem under `/var/lib/loki/index`.
Single-host so a shared index backend would be overkill.

## Operator UI access

```sh
# Loki query API (loopback-only on log_aggregator host)
ssh -L 3100:localhost:3100 root@log-01
# → http://localhost:3100/

# Loki LogCLI (runs on operator's machine)
LOKI_ADDR=http://localhost:3100 logcli query '{job="systemd"}'
```

Future Grafana role wires Loki as a datasource for the
operator-facing dashboards layer.

## Promtail journal scrape

Every Promtail-shipped log entry gets these labels:

| Label | Source |
|---|---|
| `job` | `systemd` (constant) |
| `instance` | the host's `inventory_hostname` |
| `unit` | `__journal__systemd_unit` (e.g. `ratesengine-api.service`) |
| `hostname` | `__journal__hostname` |
| `severity` | `__journal_priority_keyword` (info / warning / error / etc.) |

Drops noise from a few high-volume low-signal units
(`systemd-tmpfiles-clean`, `cron`, `systemd-logind`).

## Failure modes

| Scenario | Consequence | Recovery |
|---|---|---|
| Loki host down | New logs queue in Promtail buffers (~10k entries each); existing logs in S3 still queryable from a backup query host | Restart Loki; Promtails flush automatically |
| MinIO down | New chunks fail to write; Loki buffers up to its limit then drops with `429 Too Many Requests` | Restore MinIO; Loki retries from in-memory chunks |
| Promtail position file lost | Promtail re-ships from `journal_max_age` (default 12h) — Loki rejects duplicates as out-of-order | Lossy but bounded; not corrupting |
| Time skew across hosts | Loki rejects samples with timestamps too-far-in-future (`reject_old_samples_max_age` default 7d) | Preflight asserts time sync; check `chronyd` or `systemd-timesyncd` |

## Future: scaling to HA

Documented in the design note (`§Future: scaling to HA`):

1. Switch `loki_replication_factor` from 1 to 2.
2. Add a 2nd `log_aggregator` host to inventory.
3. Switch BoltDB index → TSDB (S3-backed).
4. Switch ring `kvstore.store` from `inmemory` → `memberlist`.
5. Promtail's `clients:` list gets both Loki URLs.

No migration of existing chunks needed — both Loki instances
read from the same S3 bucket.

## What this role does NOT cover

- **Grafana** — separate role.
- **Tempo / distributed tracing** — separate role.
- **Long-term log archival** — 30d retention; bump
  `loki_retention_days` if you need longer.
- **TLS between Promtail and Loki** — internal CIDR only;
  operator wraps in WireGuard if needed.
