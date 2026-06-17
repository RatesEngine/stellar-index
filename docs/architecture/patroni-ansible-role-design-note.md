---
title: Patroni ansible role — design note
last_verified: 2026-05-02
status: shipped (Task #72 — configs/ansible/roles/patroni)
related:
  - docs/architecture/ha-plan.md §3.3 (TimescaleDB cluster topology)
  - docs/operations/runbooks/timescale-primary-down.md (the runbook this role makes work)
  - docs/operations/drills/scenarios/sev1-timescale-primary-failover.md (drill that flagged the absence)
implementation: configs/ansible/roles/patroni/
---

# Patroni ansible role — design note

Companions the role implementation at
[`configs/ansible/roles/patroni/`](../../configs/ansible/roles/patroni/).
Pairs with the SEV-1 drill scenario which explicitly called out
Patroni's absence as the gap making manual failover painful.

Task #72 covers five roles total (Patroni / Redis / HAProxy /
Prometheus / Loki). This note is **Patroni only** — it's the
heaviest of the five and worth its own design pass. The other
four can take similar notes when picked up.

## Why Patroni first

The SEV-1 tabletop drill scenario flagged this concretely:
*"Failover path is operator-knowledge today. Patroni hasn't
landed. A real failover today is a manual `pg_basebackup` flip;
the runbook documents the steps but they're slow under stress."*

Without Patroni, `timescale-primary-down.md` Mitigation §A
("Automatic Patroni failover — the happy path") doesn't apply
and on-call has to walk Mitigation §B (manual replica
promotion). Time difference: ~60 s vs ~15 min, with the manual
path being error-prone under pressure.

Closing #72's Patroni piece is the highest-leverage single
delivery in the launch-readiness set.

## What's already decided in ha-plan §3.3

The architectural choices are pinned — this role implements
them, doesn't redebate them:

| Decision | Value |
|---|---|
| Topology | Primary + 2 synchronous replicas across `db-01` / `db-02` / `db-03` |
| Sync mode | `synchronous_commit=remote_apply`, `synchronous_standby_names='ANY 1 (db-02, db-03)'` |
| DCS | etcd, 3-node quorum |
| Front | PgBouncer pair + keepalived VIP (separate sub-role of Task #72) |
| Postgres version | 15 + TimescaleDB extension |
| Backup | pgBackRest to MinIO; WAL-stream + weekly full + daily diff + hourly incr |
| Failover RTO | 60 s |

The role's job is to deploy Patroni and etcd in this exact
shape; the choices above are inputs, not outputs.

## Scope: what this role does and doesn't

**In scope:**
- Install Patroni 3.x via apt (PGDG / Patroni's own apt repo).
- Install etcd 3.x via apt.
- Render Patroni config (`/etc/patroni/patroni.yml`) per inventory.
- Render etcd config (`/etc/default/etcd` + `/etc/etcd/etcd.conf`).
- Bootstrap the cluster idempotently (only the first run on
  `db-01` initialises; subsequent runs on `db-02` / `db-03` join).
- Wire systemd units (`patroni.service`, `etcd.service`) with
  proper restart semantics.
- Open firewall ports for Patroni's REST API (8008) + etcd
  (2379/2380) on the internal network only.
- Wire `node_exporter` textfile collectors so the existing
  `stellarindex_timescale_primary_down` alert continues firing.
- Add a one-shot `patroni-bootstrap-restore` task for the
  initial-from-pgBackRest bring-up on a fresh cluster.

**Out of scope** (separate sub-roles of #72 or future tasks):
- PgBouncer pair + keepalived VIP — separate `pgbouncer` role.
- pgBackRest setup (already partially in `archival-node` role).
- Postgres tuning beyond what Patroni's bootstrap uses (handled
  by the existing `archival-node` role's postgres tasks).
- TimescaleDB CAGG / hypertable bootstrap (handled by
  `stellarindex-migrate` separately).

## Why Patroni vs the alternatives

Decision history captured in ha-plan.md (§Patroni vs Stolon
vs native TimescaleDB HA):

- **Patroni 3.x** — chosen. Mature; SDF/exchanges already
  ship Patroni-fronted Postgres; etcd DCS is well-trodden.
- **Stolon** — close second. Heavier deploy story; less
  community runbook content; we'd be the first Stellar-side
  team running it.
- **TimescaleDB native HA** (multi-node) — distinct product,
  heavier tradeoffs around CAGG consistency. Mentioned in
  TimescaleDB docs but not what we want for the
  symmetric-replica-with-failover topology.

The Patroni decision is settled; no need to revisit during
implementation.

## Layout

```
configs/ansible/roles/patroni/
├── README.md                          per-role docs
├── defaults/main.yml                  inventory-overridable defaults
├── handlers/main.yml                  systemctl restart handlers
├── meta/main.yml                      depends-on the existing
│                                       archival-node role's
│                                       postgres + zfs sub-tasks
├── tasks/
│   ├── main.yml                       includes the per-step files
│   ├── 01-preflight.yml               check OS, etcd quorum size,
│   │                                   inventory sanity
│   ├── 02-etcd-install.yml            apt install etcd
│   ├── 03-etcd-configure.yml          render etcd config from
│   │                                   inventory cluster spec
│   ├── 04-etcd-systemd.yml            wire systemd unit + start
│   ├── 05-patroni-install.yml         apt install patroni
│   ├── 06-patroni-configure.yml       render /etc/patroni/patroni.yml
│   ├── 07-patroni-systemd.yml         wire systemd unit
│   ├── 08-patroni-bootstrap.yml       only on `db-01`: initdb +
│   │                                   pgBackRest restore option
│   ├── 09-patroni-join.yml            on db-02/03: join existing
│   │                                   cluster (no-op when already
│   │                                   joined)
│   ├── 10-firewall.yml                allow internal-network
│   │                                   2379/2380/8008
│   └── 11-monitoring.yml              node_exporter textfile
│                                       wiring
└── templates/
    ├── etcd.conf.j2                   etcd config
    ├── etcd-systemd.j2                etcd unit (override)
    ├── patroni.yml.j2                 the main Patroni config
    └── patroni-systemd.j2             patroni unit
```

## Inventory model

Per-region inventory file gains the new keys:

```yaml
# inventory/r1.yml (extended)
all:
  hosts:
    db-01:
      ansible_host: 10.0.0.11
      patroni_role: bootstrap
      etcd_role: bootstrap
    db-02:
      ansible_host: 10.0.0.12
      patroni_role: replica
      etcd_role: peer
    db-03:
      ansible_host: 10.0.0.13
      patroni_role: replica
      etcd_role: peer
  vars:
    patroni_cluster_name: stellarindex-r1
    etcd_cluster_token: stellarindex-etcd-r1
    patroni_postgres_version: 15
    patroni_data_dir: /var/lib/postgresql/15/main
    patroni_synchronous_mode: true
    patroni_synchronous_node_count: 1
```

The role's `defaults/main.yml` provides safe values for
everything; inventory overrides per-region (different cluster
names + tokens per region prevents cross-region accidents).

## Key template — patroni.yml.j2

The most important file. Sketch (Jinja2 around the canonical
Patroni config shape):

```yaml
scope: {{ patroni_cluster_name }}
namespace: /service/
name: {{ inventory_hostname }}

restapi:
  listen: 0.0.0.0:8008
  connect_address: {{ ansible_host }}:8008

etcd3:
  hosts: {{ groups['all'] | map('extract', hostvars, 'ansible_host') | map('regex_replace', '$', ':2379') | list }}

bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
    synchronous_mode: {{ patroni_synchronous_mode | default(true) | to_json }}
    synchronous_node_count: {{ patroni_synchronous_node_count | default(1) }}
    postgresql:
      use_pg_rewind: true
      parameters:
        wal_level: replica
        hot_standby: 'on'
        synchronous_commit: remote_apply
        synchronous_standby_names: 'ANY {{ patroni_synchronous_node_count | default(1) }} ({{ groups['all'] | reject('equalto', 'db-01') | join(',') }})'
        max_connections: 200
        max_replication_slots: 10
        max_wal_senders: 10
        wal_keep_size: 1024MB
        archive_mode: 'on'
        archive_command: 'pgbackrest --stanza=stellarindex archive-push %p'
        # TimescaleDB:
        shared_preload_libraries: 'timescaledb,pg_stat_statements'

  initdb:
    - encoding: UTF8
    - data-checksums

  pg_hba:
    - host  replication  replicator  10.0.0.0/8  md5
    - host  all          all         10.0.0.0/8  md5

postgresql:
  listen: 0.0.0.0:5432
  connect_address: {{ ansible_host }}:5432
  data_dir: {{ patroni_data_dir }}
  bin_dir: /usr/lib/postgresql/{{ patroni_postgres_version }}/bin
  pgpass: /tmp/pgpass0
  authentication:
    replication:
      username: replicator
      password: '{{ patroni_replicator_password }}'
    superuser:
      username: postgres
      password: '{{ patroni_postgres_password }}'

  parameters:
    unix_socket_directories: '/var/run/postgresql'

tags:
  nofailover: false
  noloadbalance: false
  clonefrom: false
```

**Secrets** (`patroni_replicator_password`, `patroni_postgres_password`)
come from `inventory/<region>.secrets.yml` encrypted via
ansible-vault — same pattern as the existing role's secrets.

## Bootstrap sequence (the trickiest part)

Three node states across three runs. Idempotency requires the
role detect "already joined" vs "needs init."

| Run | Host | State | Action |
|---|---|---|---|
| 1 | db-01 | fresh | `initdb` → become primary; etcd: bootstrap leader |
| 1 | db-02 | fresh | join existing Patroni cluster as replica; etcd: join as peer |
| 1 | db-03 | fresh | same as db-02 |
| 2+ | any | already running | no-op (Patroni / etcd services already up + healthy) |

**Detection:** task 08-patroni-bootstrap.yml `when:` clause:

```yaml
- name: detect existing patroni state
  uri:
    url: "http://localhost:8008/cluster"
    status_code: [200, 503]
  register: patroni_state
  failed_when: false

- name: bootstrap patroni primary
  block:
    - ...
  when:
    - inventory_hostname == 'db-01'
    - patroni_state.status != 200
```

Same shape for etcd via its `/health` endpoint. Both services
must be reachable on a fresh node before the role considers
itself successful.

## Restore-from-backup path

Operators bringing up a fresh cluster against an EXISTING
backup (e.g. DR rebuild) need:

```yaml
# Inventory
patroni_bootstrap_method: pgbackrest        # default: initdb
patroni_pgbackrest_stanza: stellarindex
patroni_pgbackrest_restore_target: latest   # or "time:2026-04-30 14:00:00"
```

Task 08 then runs `pgbackrest --stanza=$STANZA restore` instead
of `initdb`. Patroni picks up the restored cluster as primary.
Replicas then `pg_basebackup` from the restored primary as
normal.

This makes the SEV playbook's annual DR exercise (§8.3) simple:
flip `patroni_bootstrap_method: pgbackrest` in the DR-region
inventory, run the playbook, point the API at the new region.

## Once Patroni lands, what changes elsewhere

`docs/operations/runbooks/timescale-primary-down.md`:
- Mitigation §A becomes the **default path** rather than the
  aspirational one. Step-by-step `patronictl` commands replace
  the manual `pg_basebackup` flip in §B.
- Quick-diagnosis adds the `/cluster` REST endpoint as the
  first check.
- Recovery time changes from ~15 min (manual) to ~60 s
  (Patroni-driven).

`docs/operations/drills/scenarios/sev1-timescale-primary-failover.md`:
- Validation criterion #6 ("Did anyone reference Patroni
  hasn't landed?") becomes obsolete — closes that gap.
- Recovery beat (T+25:00 in the timeline) shortens — Patroni
  promotes within 60s of the primary going down, regardless of
  what the team is doing.

`internal/storage/timescale/`:
- Connection string switches from
  `postgres://...@db-primary.internal:5432/...`
  to
  `postgres://...@db-vip.internal:6432/...` (PgBouncer VIP in
  front of the Patroni cluster). Read-only API queries can
  optionally route to the read pool via a separate
  `PgBouncer-read` VIP.

Coverage matrix #11 (Patroni) flips ✅.

## Edge cases / gotchas

1. **Etcd 3-node quorum loses 2 of 3.** Patroni cannot make
   leader-election decisions and refuses writes (correct
   behaviour). Recovery requires bringing back a third etcd
   node before primary writes resume. Drill scenario opportunity.

2. **Synchronous replication blocks on a slow replica.** With
   `synchronous_commit=remote_apply` + `ANY 1 of 2`, the
   primary waits for at least one replica to apply each commit.
   If both replicas slow, primary write latency climbs.
   Mitigation: monitor `pg_stat_replication` for replica lag,
   alert at sustained lag > 5 s.

3. **TimescaleDB extension version skew during failover.** If
   the standby is on a different TimescaleDB version than the
   primary, post-failover `CREATE EXTENSION` reconciliation
   needs care. Role pins `timescaledb` apt package version
   identically across all three nodes.

4. **pg_rewind requires the divergent node be cleanly shut
   down.** A crashed primary that comes back online may not be
   `pg_rewind`-eligible. Patroni handles this by re-imaging
   from `pg_basebackup` in that case (slower but correct).
   Document expected timing in the runbook.

5. **etcd's TLS story.** This design ships etcd with
   internal-network-only listening + firewall-restricted ports,
   no TLS between etcd and Patroni. For Phase-3 multi-region
   deployments where etcd might span regions, TLS becomes
   mandatory. Mark TODO in inventory comments; add to
   `validator-rollout.md` Phase-3 success criteria.

6. **Patroni REST API is unauthenticated by default.** Role
   gates it behind firewall + Basic Auth via the
   `restapi.authentication` block. Credentials in vault.

## Effort breakdown

| Step | Estimate |
|---|---|
| `defaults/main.yml` + inventory model docs | 1 h |
| `01-preflight.yml` (sanity checks) | 1 h |
| etcd install + configure + systemd (3 tasks) | 3 h |
| Patroni install + configure + systemd (3 tasks) | 4 h |
| Bootstrap detection + idempotency | 3 h |
| Restore-from-backup path | 2 h |
| Firewall + monitoring wiring | 2 h |
| `templates/patroni.yml.j2` (the heavy template) | 2 h |
| `README.md` (operator-facing) | 1 h |
| Local Vagrant 3-VM smoke test | 4 h |
| timescale-primary-down.md updates | 1 h |
| sev1-timescale-primary-failover.md drill scenario updates | 1 h |
| CHANGELOG + ha-plan §3.3 status update | 1 h |
| **Total** | **~26 h, 3-4 days** |

The matrix's "~1 week" estimate for Task #72 was for *all five*
roles. Patroni alone is ~3-4 days. The other four (Redis
Sentinel, HAProxy, Prometheus stack, Loki) are each smaller
(~1-1.5 days) so the full #72 lands in ~1.5 weeks total —
slightly over the original 1-week estimate.

## Implementation PR shape (suggested)

Single PR for Patroni alone, ~800-1200 LoC across YAML + Jinja
templates + docs. Sub-commits:

1. `feat(ansible): patroni role scaffold + defaults + meta`
2. `feat(ansible): etcd install + config + systemd`
3. `feat(ansible): patroni install + config + systemd`
4. `feat(ansible): bootstrap detection + idempotency`
5. `feat(ansible): pgBackRest restore path`
6. `feat(ansible): firewall + node_exporter wiring`
7. `docs: timescale-primary-down runbook updates for Patroni`
8. `docs: sev1 drill scenario updates + ha-plan status flip`
9. `chore: CHANGELOG + Coverage matrix #72 row partial close`

CI runs once. Adds a Vagrant-based smoke test under
`test/ansible/patroni/` so the role's bootstrap sequence is
verifiable without a real cluster.

## Open questions for the implementer

1. **Should the role use Patroni's official PyPI install or
   the apt repo?** Apt is simpler but lags by 1-2 minor
   versions; PyPI is current but adds Python venv complexity.
   Default: apt. Document the venv path as a future tightening.

2. **Etcd 3.5 vs 3.6?** 3.6 has better RAFT performance + TLS
   defaults but less Patroni community-tested-against.
   Recommendation: 3.5 (boring is good for the Postgres DCS).

3. **Should we run a 5-node etcd cluster instead of 3-node?**
   3-node tolerates 1 failure; 5-node tolerates 2. Per ha-plan
   §3.3 we picked 3 — keep aligned. Reconsider only if r1 has
   capacity for 2 more etcd-only VMs.

4. **PgBouncer-side health checks and routing**: should the
   PgBouncer role (separate sub-role of #72) consume Patroni's
   `/leader` REST endpoint to route writes to the current
   primary, or rely on the keepalived VIP? Patroni-aware
   routing is more robust; keepalived is simpler. Recommend
   Patroni-aware via a small sidecar that updates PgBouncer's
   server config on leader change (similar to HAProxy + Patroni
   integrations).

5. **How does this interact with the per-region storage
   strategy (ADR-0016)?** Each region has its own Patroni
   cluster (3 nodes per region). Cross-region replication is
   NOT in scope for this role — the API serves only-closed
   buckets per ADR-0015 + ADR-0018, so each region's data is
   independently consistent. The `etcd_cluster_token` per region
   prevents accidental cross-region cluster joins. Document this
   assumption in the README.
