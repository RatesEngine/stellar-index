# Ansible role — `patroni`

Deploy a Patroni-managed Postgres + TimescaleDB cluster with
etcd as the DCS. Implements the topology pinned in
[`docs/architecture/ha-plan.md §3.3`](../../../../docs/architecture/ha-plan.md):

- 1 primary + 2 synchronous replicas across `db-01` / `db-02` /
  `db-03`.
- 3-node etcd quorum.
- `synchronous_commit=remote_apply`,
  `synchronous_standby_names='ANY 1 (db-02, db-03)'`.
- Failover RTO target 60 s.

This role is **stage 1** of a two-stage Postgres front
(stage 2 is the PgBouncer / HAProxy front role, separate).
Implementing this role makes the
[`timescale-primary-down.md`](../../../../docs/operations/runbooks/timescale-primary-down.md)
runbook's *§A Automatic Patroni failover* path the actual default,
rather than aspirational.

Design rationale lives in
[`docs/architecture/patroni-ansible-role-design-note.md`](../../../../docs/architecture/patroni-ansible-role-design-note.md)
(local-only branch — pushed alongside the implementation when this
role lands as a complete PR).

## Prerequisites

- Three hosts named per inventory (`db-01` / `02` / `03` by
  default). Each needs:
  - Ubuntu 24.04 LTS (or 22.04).
  - Network reachability between them on ports 5432 (Postgres),
    8008 (Patroni REST), 2379 + 2380 (etcd).
  - The `archival-node` role's preflight + zfs steps already
    applied (this role builds on the postgres install from that
    role).

- Vault contents:
  - `patroni_replicator_password` — Postgres replication user.
  - `patroni_postgres_password` — Postgres superuser.
  - (optional) `patroni_rest_basic_auth` — Patroni REST API
    Basic Auth, defaults disabled if vault entry absent.

## Inventory model

Set in your `inventory/<region>.yml`:

```yaml
all:
  children:
    postgres_cluster:
      hosts:
        db-01: { ansible_host: 10.0.0.11, patroni_role: bootstrap, etcd_role: bootstrap }
        db-02: { ansible_host: 10.0.0.12, patroni_role: replica,   etcd_role: peer }
        db-03: { ansible_host: 10.0.0.13, patroni_role: replica,   etcd_role: peer }
      vars:
        patroni_cluster_name: ratesengine-r1
        etcd_cluster_token: ratesengine-etcd-r1
        patroni_postgres_version: 15
        patroni_data_dir: /var/lib/postgresql/15/main
```

`patroni_role: bootstrap` only sets the *initial bootstrap
state* — once running, Patroni may fail over and a replica
becomes primary. The role's idempotency check skips bootstrap
on subsequent runs.

## Running

```sh
cd configs/ansible
# Bring up a fresh cluster (first run)
ansible-playbook -i inventory/r1.yml playbooks/postgres-cluster.yml --tags patroni

# Re-apply config without restarting
ansible-playbook -i inventory/r1.yml playbooks/postgres-cluster.yml --tags patroni,config --skip-tags restart

# Promote a specific replica (operator action — not covered by
# this role's playbook):
ssh db-02 patronictl -c /etc/patroni/patroni.yml failover ratesengine-r1
```

## Restore-from-backup path

For DR rebuilds: set in inventory and run:

```yaml
patroni_bootstrap_method: pgbackrest        # default: initdb
patroni_pgbackrest_stanza: ratesengine
patroni_pgbackrest_restore_target: latest   # or "time:2026-04-30 14:00:00"
```

## Smoke test

After the playbook finishes:

```sh
# Cluster state
ssh db-01 patronictl -c /etc/patroni/patroni.yml list

# Expected: 3 rows, one with role=Leader, two with role=Replica,
# all running.
```

## What this role does, at a glance

1. **Preflight** — verify OS, RAM ≥ 32 GB, the prerequisite role
   (postgres install) ran.
2. **etcd** — install + configure + start the 3-node quorum.
3. **Patroni** — install + render config + start.
4. **Bootstrap** — on `db-01`, initialise the cluster (or
   restore from pgBackRest); on `db-02` / `03`, join as replicas.
5. **Firewall** — open 5432 / 8008 / 2379 / 2380 to the internal
   network only.
6. **Monitoring** — wire `node_exporter` textfile collectors so
   the existing `ratesengine_timescale_primary_down` alert
   continues firing.

## When this role IS NOT enough

- **PgBouncer / HAProxy front** — separate role (Task #72 part 3).
  Without it, API and indexer clients connect directly to
  Patroni's primary IP — which moves on failover. The
  Patroni-aware client wrapper does work, but PgBouncer is the
  recommended productionising step.
- **TimescaleDB CAGG / hypertable bootstrap** — handled by
  `cmd/ratesengine-migrate`, not this role.
- **pgBackRest** — already partially in `archival-node` role's
  postgres tasks; this role only invokes its `restore` mode for
  DR bring-up.

## See also

- [`docs/architecture/ha-plan.md §3.3`](../../../../docs/architecture/ha-plan.md) — topology.
- [`docs/operations/runbooks/timescale-primary-down.md`](../../../../docs/operations/runbooks/timescale-primary-down.md) — the runbook this role makes work.
- [`docs/operations/drills/scenarios/sev1-timescale-primary-failover.md`](../../../../docs/operations/drills/scenarios/sev1-timescale-primary-failover.md) — the SEV-1 drill scenario whose Validation #6 closes when this role lands.
- ADR-0008 (HA topology) — the architectural ratification of the 3-node-DB-cluster choice.
