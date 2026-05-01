# Ansible role — `redis-sentinel`

Deploy a Sentinel-managed Redis cache cluster. Implements the
topology pinned by
[ADR-0024](../../../../docs/adr/0024-redis-ha-via-sentinel.md)
(ratifies Sentinel over Cluster mode, resolving the contradiction
in
[`docs/architecture/ha-plan.md §3.4`](../../../../docs/architecture/ha-plan.md)):

- 1 primary + 2 replicas across `cache-01` / `cache-02` / `cache-03`.
- 3 Sentinels co-located on the same hosts; quorum = 2.
- AOF every-second + RDB nightly persistence.
- Failover RTO target 15–30 s (Sentinel's
  `down-after-milliseconds=5000` + `failover-timeout=60000`).

Pairs with the `patroni` role: together they make
[`docs/operations/runbooks/redis-master-down.md`](../../../../docs/operations/runbooks/redis-master-down.md)
and
[`timescale-primary-down.md`](../../../../docs/operations/runbooks/timescale-primary-down.md)
runbooks' *automatic-failover* sections the actual default, not
aspirational.

Design rationale lives in
[`docs/architecture/redis-sentinel-ansible-role-design-note.md`](../../../../docs/architecture/redis-sentinel-ansible-role-design-note.md).

## Why Sentinel, not Cluster

Different HA modes:

- **Redis Cluster** — sharded; each master owns a hash-slot range; failover is intra-cluster.
- **Redis Sentinel** — single primary + replicas; 3+ Sentinel processes elect a new primary on failure.

Our hot-set is small (price-cache + rate-limit + SEP-1 cache +
asset-metadata + SSE registry — under a few GB); sharding adds
operational tax without solving capacity. Sentinel is simpler at
SEV-1 time. ADR-0024 captures the full reasoning.

## Prerequisites

- Three hosts named per inventory (`cache-01` / `02` / `03` by
  default). Each needs:
  - Ubuntu 24.04 LTS (or 22.04).
  - Network reachability between them on TCP `6379` (data) and
    `26379` (Sentinel).
  - `redis-server` and `redis-sentinel` available via apt (default
    on Ubuntu 22.04+).

- Vault contents:
  - `redis_password` — used as both `requirepass` (clients
    auth) and `masterauth` (replicas auth to primary).

## Inventory model

Set in your `inventory/<region>.yml`:

```yaml
all:
  children:
    redis_cluster:
      hosts:
        cache-01: { ansible_host: 10.0.0.21, redis_role: primary }
        cache-02: { ansible_host: 10.0.0.22, redis_role: replica }
        cache-03: { ansible_host: 10.0.0.23, redis_role: replica }
      vars:
        redis_sentinel_master_name: ratesengine-r1-cache
        redis_sentinel_quorum: 2
        redis_maxmemory: 4gb
```

`redis_role: primary` only sets the *initial bootstrap state* —
once running, Sentinel may fail over and a replica becomes
primary. The role's idempotency check (`redis_first_run_only=true`
by default) consults Sentinel before re-rendering configs and
refuses to overwrite a post-failover state.

## Running

```sh
cd configs/ansible
# Bring up a fresh cluster (first run)
ansible-playbook -i inventory/r1.yml playbooks/redis-cluster.yml --tags redis

# Re-apply config without restarting (apt upgrade scenario)
ansible-playbook -i inventory/r1.yml playbooks/redis-cluster.yml --tags redis,config --skip-tags restart

# Promote a specific replica (operator action — not covered by
# this role; Sentinel handles automatic failover):
ssh cache-01 redis-cli -p 26379 -a "$REDIS_PASSWORD" \
  SENTINEL failover ratesengine-r1-cache
```

## Idempotency on a live cluster

The role consults Sentinel before re-rendering configs. If
Sentinel responds (cluster is up), the role respects whatever
primary Sentinel currently believes is correct — even if that
differs from `redis_role: primary` in inventory (because
Sentinel failed over since the last deploy).

To force a config re-render against a live cluster (e.g. tuning
`maxmemory`), set `redis_first_run_only: false` in inventory for
that run. Don't ship that as a default — it's a foot-gun.

## How API + aggregator clients connect

Sentinel-aware clients discover the current primary by asking
any Sentinel — that's the entire point of Sentinel. **Don't
deploy a VIP or HAProxy in front of Redis.** This is the key
asymmetry vs Postgres: the Postgres clients aren't
Sentinel-aware (no equivalent), so Patroni needs HAProxy or
PgBouncer in front; Redis clients (`go-redis/redis/v9.NewFailoverClient`)
do the discovery themselves.

In Go (we plumb this through `internal/cachekeys` after this
role lands):

```go
client := redis.NewFailoverClient(&redis.FailoverOptions{
    MasterName:    "ratesengine-r1-cache",
    SentinelAddrs: []string{
        "cache-01.internal:26379",
        "cache-02.internal:26379",
        "cache-03.internal:26379",
    },
    Password:         os.Getenv("REDIS_PASSWORD"),
    SentinelPassword: os.Getenv("REDIS_PASSWORD"),
})
```

Net: no separate "Redis HAProxy" sub-role of Task #72.

## Rolling password rotation

`requirepass` is set from vault — rotating it is a rolling
deploy:

1. Update `redis_password` in `inventory/<region>.secrets.yml`.
2. Re-apply this role to ONE node first (`--limit cache-02`).
3. Confirm Sentinel still sees the cluster (`SENTINEL ckquorum`).
4. Roll the remaining two.

Until all three are rolled, Sentinel will misreport quorum
because the "old password" Sentinels can't auth to the "new
password" Redis. Plan ~5 minutes for the full rotation; do it
during a quiet window.

## Observability

- `redis_exporter` listens on port `9121` and exposes the standard
  Redis metrics. Add a Prometheus scrape config pointing at each
  host.
- `ratesengine_redis_sentinel_primary{instance=...}` gauge —
  rendered by `redis-sentinel-textfile-scraper.timer` every 30 s.
  Sum across hosts should always equal 1; > 1 means a split-brain
  candidate; 0 means Sentinel hasn't elected yet.

Companion alerts live alongside the Patroni alerts in
`deploy/monitoring/rules/cache.yml` (added separately —
`ratesengine_redis_master_down`, `redis_memory_high`).
