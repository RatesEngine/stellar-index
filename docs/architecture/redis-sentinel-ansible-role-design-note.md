---
title: Redis Sentinel ansible role — design note
last_verified: 2026-05-02
status: shipped (Task #72 / #79 — configs/ansible/roles/redis-sentinel)
related:
  - docs/architecture/ha-plan.md §3.4 (Redis cluster topology)
  - docs/operations/runbooks/redis-master-down.md (the runbook this role makes work)
  - docs/architecture/patroni-ansible-role-design-note.md (sister role; same pattern)
---

# Redis Sentinel ansible role — design note

**Working draft on local-only branch
`design/redis-sentinel-ansible-role-design-note`. Bootstraps the
Redis Sentinel sub-role of Task #72 — second-priority after the
Patroni piece.** Pairs with the Patroni note: both roles
collectively make `redis-master-down.md` and
`timescale-primary-down.md` runbooks' "happy path" sections
actually apply.

## Tension in the existing design — Cluster vs Sentinel

`ha-plan.md §3.4` calls the topology **"Redis-Cluster mode (hash
slots). 3 masters + 3 replicas. 3 sentinels on independent hosts
for failover vote."**

This is internally inconsistent. **Redis Cluster and Redis
Sentinel are two different HA modes**:

- **Redis Cluster** — sharded; each master owns a range of hash
  slots; failover handled internally by other cluster nodes
  voting; no separate Sentinel processes.
- **Redis Sentinel** — single primary + replicas (no sharding);
  3+ Sentinel processes monitor the primary and elect a new one
  on failure.

You don't run Sentinel against a Redis Cluster. The original
ha-plan author probably wrote "Cluster mode" meaning "clustered
deployment" colloquially.

**This role implements Redis Sentinel** (3 nodes, 1 primary + 2
replicas, 3 Sentinels co-located on the same hosts). Reasons:

1. Our hot-data set is small (price-cache + rate-limit + SEP-1
   cache + asset-metadata + SSE registry — all under a few GB
   per ha-plan §3.4). Sharding adds complexity without solving
   any current capacity problem.
2. Sentinel is operationally simpler — fewer moving parts to
   debug at SEV-1 time.
3. If we outgrow Sentinel's capacity ceiling later, the
   migration to Cluster is a one-time pain, not an ongoing tax.

Add a corresponding ADR (or amendment to ha-plan §3.4) when this
role lands so the source-of-truth stops contradicting itself.

## What's pinned vs what this role decides

| Decision | Source | Value |
|---|---|---|
| Topology | This role decides | Sentinel: 1 primary + 2 replicas + 3 Sentinels (one per host) |
| Persistence | ha-plan §3.4 | AOF every-second + RDB nightly |
| Failover RTO | ha-plan §3.4 | 15-30 s |
| Cross-region replication | ha-plan §3.4 | Explicitly NO — cache-only, re-hydrates from Timescale |
| Front | (open) | Recommend a small client-side discovery library (`internal/cachekeys` already abstracts the cache; teach it to consult Sentinel for the current primary) — sidesteps the need for HAProxy or VIP for Redis specifically |
| Auth | This role decides | `requirepass` + `masterauth` set from vault; no public listener |

## Layout

```
configs/ansible/roles/redis-sentinel/
├── README.md                          per-role docs
├── defaults/main.yml                  inventory-overridable defaults
├── handlers/main.yml                  systemctl restart handlers
├── meta/main.yml                      no dependencies
├── tasks/
│   ├── main.yml
│   ├── 01-preflight.yml               OS check, disk, sysctl tuning
│   ├── 02-install.yml                 apt install redis-server
│   ├── 03-redis-configure.yml         render /etc/redis/redis.conf
│   │                                   (replicaof, requirepass, auth)
│   ├── 04-sentinel-configure.yml      render /etc/redis/sentinel.conf
│   ├── 05-systemd.yml                 redis.service + redis-sentinel.service
│   ├── 06-firewall.yml                allow 6379 (data) + 26379 (sentinel)
│   │                                   internal-only; deny external
│   └── 07-monitoring.yml              redis_exporter (prometheus) wiring
└── templates/
    ├── redis.conf.j2                  the data-plane config
    ├── sentinel.conf.j2               the sentinel config
    └── redis-systemd-override.conf.j2 systemd hardening overlay
```

Pattern matches the Patroni role's structure for consistency.

## Inventory model

```yaml
# inventory/r1.yml (extended)
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
        redis_persistence_dir: /var/lib/redis
        redis_aof_appendfsync: everysec
```

`redis_role: primary` only sets the *initial bootstrap* — once
running, Sentinel may fail over and a replica becomes primary.
The role's idempotency handles re-runs against a cluster that's
already failed over (don't force the original primary back).

## Key template — redis.conf.j2

```jinja
# Render-time config — managed by ansible. Don't edit.
bind {{ ansible_host }} 127.0.0.1
port 6379
protected-mode yes
requirepass {{ redis_password }}
masterauth {{ redis_password }}

# Persistence (ha-plan §3.4)
appendonly yes
appendfsync {{ redis_aof_appendfsync | default('everysec') }}
save 900 1
save 300 10
save 60 10000

# Memory + eviction
maxmemory {{ redis_maxmemory | default('4gb') }}
maxmemory-policy allkeys-lru

{% if redis_role == 'replica' %}
# Replica config — Sentinel will reconcile if primary changes
replicaof {{ hostvars[groups['redis_cluster'] | select('match', '.*primary.*') | first].ansible_host }} 6379
{% endif %}

# Logging
loglevel notice
logfile /var/log/redis/redis-server.log
```

## Key template — sentinel.conf.j2

```jinja
port 26379
bind {{ ansible_host }} 127.0.0.1

sentinel monitor {{ redis_sentinel_master_name }} \
  {{ hostvars[groups['redis_cluster'] | select('match', '.*primary.*') | first].ansible_host }} \
  6379 {{ redis_sentinel_quorum }}

sentinel auth-pass {{ redis_sentinel_master_name }} {{ redis_password }}
sentinel down-after-milliseconds {{ redis_sentinel_master_name }} 5000
sentinel failover-timeout {{ redis_sentinel_master_name }} 60000
sentinel parallel-syncs {{ redis_sentinel_master_name }} 1

dir /var/lib/redis-sentinel
logfile /var/log/redis/redis-sentinel.log
```

## Bootstrap sequence

Three nodes, run in the order primary → replica → replica.
Idempotency check: each node's `redis-cli ping` returns PONG
before declaring success.

| Run | Host | Action |
|---|---|---|
| 1 | cache-01 | install redis + sentinel; render configs; start services |
| 1 | cache-02 | same; replicates from cache-01 once it sees the primary |
| 1 | cache-03 | same |
| 1 | (any) | Sentinel quorum forms across the 3 hosts; primary elected |
| 2+ | any | redis-cli ping → PONG. No-op. |

**Detection of "already running and possibly failed over":**

```yaml
- name: detect current primary via Sentinel
  command: >
    redis-cli -h {{ ansible_host }} -p 26379
    -a "{{ redis_password }}"
    SENTINEL get-master-addr-by-name {{ redis_sentinel_master_name }}
  register: sentinel_state
  failed_when: false
  changed_when: false
```

If Sentinel returns the current primary, never overwrite the
existing `replicaof` directive — let Sentinel keep doing its
job. Only render fresh configs on first deploy.

## How API + aggregator clients connect

**Don't use a VIP / HAProxy for Redis.** Sentinel-aware clients
discover the current primary by asking any Sentinel — that's the
whole point of Sentinel.

In Go, `go-redis/redis/v9` supports this via:

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

`internal/cachekeys` (or whichever package owns the connection
factory) gets a small change: read the Sentinel addresses + master
name from config, instantiate `FailoverClient` instead of plain
`Client`. The cache-key API surface doesn't change.

This sidesteps the HAProxy / keepalived complexity that
**Patroni** does need (Postgres clients aren't Sentinel-aware).
Net: no separate "Redis HAProxy" sub-role of Task #72; that
sub-role focuses on Postgres + API only.

## Once Redis Sentinel lands, what changes elsewhere

`docs/operations/runbooks/redis-master-down.md`:
- Mitigation steps shift from "manually identify a replica and
  promote it" to "wait for Sentinel; ~15-30 s. Verify with
  `redis-cli SENTINEL get-master-addr-by-name`."
- Quick-diagnosis adds the Sentinel quorum check.

`internal/cachekeys/` (or wherever `redis.NewClient` is called):
- Switch from `redis.NewClient` to `redis.NewFailoverClient`.
- Config reads `redis.sentinel_addrs` + `redis.master_name`.

`configs/example.toml`:
- `[storage]` block adds `redis.sentinel_addrs` and
  `redis.master_name`. Existing `redis_addr` migrates to a
  legacy fallback.

`ha-plan.md §3.4`:
- Reword the topology line to acknowledge Sentinel vs Cluster
  is a deliberate choice, not "Cluster mode" colloquial.
- Drop the contradiction between "Cluster mode" and "Sentinels."

Coverage matrix #12 (Redis sub-role) flips ✅.

## Edge cases / gotchas

1. **Sentinel split-brain.** With 3 Sentinels and `quorum=2`, a
   2-1 partition lets the larger side promote — correct
   behaviour. With 2 Sentinels alive (the 3rd partitioned away)
   plus the primary on the partitioned side, Sentinel fails over
   to one of the 2 visible replicas. When the 3rd Sentinel +
   old primary rejoin, Sentinel reconciles them as replicas of
   the new primary.

2. **AOF rewrite during failover.** A long AOF rewrite on the
   primary can stretch the failover detection window
   (`down-after-milliseconds 5000` shouldn't be tripped by AOF
   rewrite, but worth alerting on if it happens).

3. **Replicas serving stale reads during failover.** Default
   `replica-serve-stale-data yes` means clients can read
   pre-failover data for the failover window. Acceptable for
   our cache (we have stale_flag handling) but worth
   documenting in the API handler comments.

4. **`requirepass` on rolling deploy.** If the password changes
   in inventory, all 3 nodes must be rolled with the new
   password before Sentinel re-converges. Document the rolling
   procedure in the role's README.

5. **Persistence + `appendfsync everysec` semantics.** A 1-s
   data loss window on power loss is policy-acceptable per
   ha-plan §3.4 ("we do not rely on Redis persistence for
   correctness"). But on AOF *truncation* the in-flight
   transactions are lost — Redis recovers to a consistent state.
   Document so operators understand recovery isn't byte-exact.

6. **Sentinel co-located vs dedicated hosts.** ha-plan §3.4
   says "3 sentinels on independent hosts." This role
   co-locates Sentinel on the same 3 cache hosts (smaller infra
   footprint, fate-shares with the data plane — which is fine
   because if a cache host is down the Sentinel on it being
   down is irrelevant). Five-Sentinel deployments split them
   off to separate hosts; we don't need that.

## Effort breakdown

| Step | Estimate |
|---|---|
| `defaults/main.yml` + inventory model docs | 1 h |
| `01-preflight.yml` (sysctl tuning, disk) | 1 h |
| `02-install.yml` + `03-redis-configure.yml` | 2 h |
| `04-sentinel-configure.yml` | 2 h |
| `05-systemd.yml` (override drop-in for hardening) | 1 h |
| `06-firewall.yml` + `07-monitoring.yml` | 1 h |
| Bootstrap detection + idempotency (Sentinel-aware) | 2 h |
| `templates/redis.conf.j2` + `sentinel.conf.j2` | 2 h |
| `internal/cachekeys` switch to `FailoverClient` | 2 h |
| `configs/example.toml` update | 0.5 h |
| `redis-master-down.md` runbook updates | 1 h |
| ha-plan §3.4 amendment + ADR if needed | 1 h |
| `README.md` (operator-facing) | 1 h |
| Local Vagrant 3-VM smoke test | 3 h |
| CHANGELOG | 0.5 h |
| **Total** | **~21 h, 2.5-3 days** |

The matrix's bundled "~1 week for all five sub-roles of #72" is
unrealistic given Patroni alone is 3-4 days; Patroni + Redis
Sentinel together is ~7 days = exactly one week.

## Implementation PR shape (suggested)

Single PR for Redis Sentinel alone, ~600-1000 LoC across YAML +
Jinja + a small Go change. Sub-commits:

1. `feat(ansible): redis-sentinel role scaffold + defaults`
2. `feat(ansible): install + redis.conf + sentinel.conf templates`
3. `feat(ansible): systemd units + firewall + monitoring`
4. `feat(ansible): bootstrap idempotency (Sentinel-aware)`
5. `feat(cachekeys): switch to redis FailoverClient`
6. `docs: redis-master-down runbook updates for Sentinel-driven failover`
7. `docs: ha-plan §3.4 amendment (Sentinel vs Cluster terminology)`
8. `chore: configs/example.toml redis.sentinel_addrs + master_name`
9. `chore: CHANGELOG`

Pairs with a Vagrant smoke test under
`test/ansible/redis-sentinel/` — same pattern as the proposed
Patroni test setup.

## Open questions for the implementer

1. **Should we add an ADR amendment / new ADR for the
   Sentinel-vs-Cluster choice?** The current ha-plan has the
   contradiction; this role's PR is a natural place to ratify
   the Sentinel choice. Recommend new ADR-0024 "Redis HA via
   Sentinel" rather than amending ha-plan §3.4 — preserves the
   audit trail.

2. **Auth: `requirepass` + `masterauth` only, or also TLS?**
   Internal-network-only deployment makes TLS overhead
   debatable. Recommend skip TLS for v1 launch; add TODO for
   Phase-3 multi-region (where Redis would not span regions
   anyway, but the principle of in-flight encryption matters).

3. **`redis_exporter` vs Redis's own `INFO` exposure?** Both
   work. Recommend `redis_exporter` for parity with the rest
   of the Prometheus stack and the existing
   `ratesengine_redis_*` metric naming.

4. **Sentinel notifications**: Sentinel can call out to a
   script on `+failover-state-end-of-loop` events. Worth wiring
   to a webhook that posts to the incident channel? Recommend
   yes, to a small dedicated channel (`#rates-engine-redis`)
   so on-call is paged via PagerDuty *and* sees the failover
   narrative in chat.

5. **Capacity monitoring**: Redis hits 80% of `maxmemory`
   triggers a slow-path eviction storm. Add an alert at 75%
   (warn) and 90% (page)? Recommend yes; landing in
   `deploy/monitoring/rules/cache.yml` alongside the existing
   `redis_master_down` rule.
