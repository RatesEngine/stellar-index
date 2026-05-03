---
title: High-Availability Infrastructure Plan
last_verified: 2026-05-02
status: ratified — binding decisions in [ADR-0008](../adr/0008-ha-topology.md); long-form design here
---

# High-Availability Infrastructure Plan

**Owner:** @ash (arch) + @alex (ops).
**Ratification:** binding decisions accepted as
[ADR-0008](../adr/0008-ha-topology.md) on 2026-04-27. Per-component
implementation lives in the relevant ansible roles
(`configs/ansible/roles/{patroni,redis-sentinel,haproxy,prometheus,loki}/`)
and the per-region storage strategy is captured in
[ADR-0016](../adr/0016-per-region-storage-strategy.md). This plan
is the **umbrella** that binds them.

The HA story is constrained by three non-negotiable numbers from the
RFPs:

- **p95 ≤ 200 ms, p99 ≤ 500 ms** (Freighter §Performance SLA).
- **≥ 99.9 % responsiveness** (Freighter) — we commit to **99.99 %**
  per our proposal.
- **≤ 30 s data freshness** (Freighter).

Every topology decision below is traced back to which of these numbers
it protects.

---

## 1. Design principles

1. **Single-region HA first; multi-region DR second.** The 10-week
   delivery window forces us to ship a strong single-region
   deployment at launch, with cold DR in cloud. Multi-region active/
   active is explicitly out of scope for v1.
2. **Ingest must never block serving.** If the ingestion plane slows,
   the serving plane serves stale-marked responses — it does not error.
3. **Decouple hot from cold.** Anything in the 30-second serving
   hot path lives in Redis; everything older reads from TimescaleDB;
   archive + replay source is MinIO. Three tiers, three failure
   domains.
4. **No single machine's failure takes us below SLA.** Redundancy is
   N+1 at minimum for every stateful component; N+2 for stellar-core
   and stellar-rpc (ADR-0001/0004 influence).
5. **Every component has a "degraded mode" defined up front.** If
   Aquarius ingestion dies, what does `/v1/price?asset=…` return?
   The answer is in §9, not invented during an incident.
6. **We own the hardware we need to own.** Captive-core + Galexie on
   colocated R640s (per ADR). Everything stateless lives in cloud.
   Cloud is DR target; colo is primary.

---

## 2. Physical topology

**Cross-region context:** this section shows the **per-region**
layout — one full stack, as deployed in each of three regions. The
3-region architecture (primary / sync-replica / async-replica,
graceful degradation across regions) lives in
[infrastructure/multi-region-topology.md](infrastructure/multi-region-topology.md).
The phased 1 → 3 validator rollout lives in
[infrastructure/validator-rollout.md](infrastructure/validator-rollout.md).
Per-node hardware spec is in
[infrastructure/archival-node-spec.md](infrastructure/archival-node-spec.md).
At launch we run exactly **one** region (R1/London) with this
topology; R2 and R3 join over Weeks 6–8 with the same shape.

### 2.1 Primary (colo)

```
                          ┌──────────────────────────────┐
                          │ Internet (Anycast + CDN/WAF) │
                          └──────────────┬───────────────┘
                                         │
                ┌────────────────────────┴─────────────────────────┐
                │                                                  │
         ┌──────┴──────┐                                    ┌──────┴──────┐
         │  HAProxy-A  │                                    │  HAProxy-B  │
         │  (keepalived VIP)                                │             │
         └──────┬──────┘                                    └──────┬──────┘
                │                                                  │
                └─────────────────────┬────────────────────────────┘
                                      │
            ┌─────────────────────────┴─────────────────────────┐
            │           ratesengine-api pool (N=3)              │   stateless
            └─────────────────────────┬─────────────────────────┘
                                      │
       ┌──────────────────────────────┼────────────────────────────┐
       │                              │                            │
┌──────┴───────┐          ┌───────────┴──────────┐       ┌─────────┴────────┐
│ Redis cluster│          │ TimescaleDB          │       │ MinIO (erasure)  │
│ (3 masters,  │          │ Patroni-managed HA:  │       │ EC(6+3) on 9     │
│  3 replicas, │          │ 1 primary +          │       │ hosts; bucket    │
│  Sentinel)   │          │ 2 sync replicas      │       │ versioning on.   │
└──────┬───────┘          └───────────┬──────────┘       └─────────┬────────┘
       │                              │                            │
       └──────────────────────────────┼────────────────────────────┘
                                      │
                      ┌───────────────┴──────────────┐
                      │   ratesengine-aggregator     │   one active, one
                      │   (leader-elected via Redis) │   standby
                      └───────────────┬──────────────┘
                                      │
                      ┌───────────────┴──────────────┐
                      │   ratesengine-indexer pool   │   one shard per source
                      │   (per-source orchestration) │
                      └───┬──────────┬──────────┬────┘
                          │          │          │
                    ┌─────┴───┐ ┌────┴────┐ ┌───┴─────┐
                    │ core A  │ │ core B  │ │ rpc A/B │
                    │+galexie │ │+galexie │ │(captive)│
                    │  (R640) │ │ (R640)  │ │ (R640)  │
                    └─────────┘ └─────────┘ └─────────┘
```

Component counts in §3. Each box is ≥ N+1; the stellar-core nodes are
N+2 because the Tier-1 aspiration (ADR-0004) requires three independent
archives post-launch.

### 2.2 DR (cloud — AWS primary)

- Stateless services (`ratesengine-api`, `ratesengine-aggregator`)
  warm-standby in AWS. Scale-to-zero when not failing over; scale-out
  on DNS flip.
- TimescaleDB **async logical replica** (not streaming — crosses a WAN,
  pg_logical is resilient) at AWS RDS with 5-minute RPO budget.
- Redis **not replicated cross-region**: warm-standby is cold.
  Acceptable because Redis is cache; re-hydrates from Timescale after
  failover within minutes.
- MinIO **Veeam-style replicated** to S3 via `mc mirror --overwrite`;
  RPO 1h for the archive bucket. `galexie-live/` replicated at 5 min.
- stellar-core and stellar-rpc **not** replicated to cloud — they are
  rebuilt from our own MinIO archive on DR activation (~4 hours to
  CATCHUP_RECENT). This is intentional: running captive-cores in AWS
  violates our cost envelope.

### 2.3 Why colo primary, cloud DR

Already ratified in ADR-0002 alternatives: captive-cores at 8 vCPU /
32 GB / large NVMe scale are ~3× cheaper on dedicated hardware than
cloud IOPS-matched instances. The colocated R640 fleet is already
provisioned; cloud is pay-as-you-use for DR.

---

## 3. Component-by-component HA

### 3.1 stellar-core + galexie fleet

- **Instances:** 3 × R640 (`core-01`, `core-02`, `core-03`). Each
  runs captive-core in `CATCHUP_RECENT` mode + galexie in
  live-export mode writing to the shared MinIO `galexie-live/`.
- **Quorum set:** our 3 nodes vote with the public SDF + 3 Tier-1
  organisations (SDF, LOBSTR, Satoshipay) per the Tier-1 aspiration
  (ADR-0004).
- **Failure mode:** 1 captive-core down → aggregator continues
  reading from the surviving 2 nodes' Galexie output (dedup by
  `(ledger, hash)` on ingest). 2 captive-cores down → SEV-1; the
  third + SDF public quorum keeps us writing ledgers but freshness
  degrades toward 30 s ceiling.
- **RPO:** 0 for live ledger (captive-core replays on restart from
  local state); 5 min for galexie export (configurable).
- **RTO:** 2 min failover (the ingester stops reading node X and
  starts reading node Y). Captive-core restart: 10–20 min from cold
  to catchup-recent if local state is intact; 4 h if it has to
  rebuild from our MinIO archive.

### 3.2 stellar-rpc

- **Instances:** 2 × stellar-rpc on `core-01` and `core-02` (one
  captive-core each).
- **Why 2 not 3:** stellar-rpc's SQLite is not a cluster; each
  instance is independent. Two is enough for serving live-event
  subscriptions.
- **Failure mode:** 1 rpc down → `ratesengine-indexer` switches its
  `getEvents` stream to the survivor via configured
  `[stellar_rpc].endpoints` array.
- **Historic event retention:** we **do not** rely on stellar-rpc
  for historic event queries. Historic goes through Galexie → our
  own `events` hypertable. This sidesteps the SQLite ceiling
  flagged in the adversarial audit §6c.

### 3.3 TimescaleDB cluster

- **Topology:** Patroni-managed primary + 2 synchronous replicas on
  3 separate R640s (`db-01`, `db-02`, `db-03`).
  `synchronous_commit=remote_apply`, `synchronous_standby_names='ANY 1 (db-02, db-03)'`.
- **Etcd quorum:** 3 nodes for Patroni leader election.
- **PgBouncer:** a pair with keepalived VIP in front of the
  Patroni cluster. Transaction-mode pooling. Pool size sized against
  PostgreSQL `max_connections` and the api-pod count.
- **Hypertables:**
  - `trades` — partitioned by `ts` daily; retention: raw-grain
    lives for **90 days**, then compressed, retained forever at
    the post-compression rate.
  - `oracle_updates` — same shape as `trades`, smaller volume.
  - `prices_1m`, `prices_15m`, `prices_1h`, `prices_4h`,
    `prices_1d`, `prices_1w`, `prices_1mo` — continuous
    aggregates (CAGGs) with `add_continuous_aggregate_policy`.
  - `events_raw` — Soroban events, compressed after 7 days.
  - `supplies` — supply-history hypertable; retention indefinite.
  - `asset_metadata` — ordinary table (small).
- **Retention policy** (matches S6.5 / S7.2):
  - `trades` raw: 90 days uncompressed, then compressed indefinitely.
  - `prices_1m`, `prices_15m`: 30 days retention.
  - `prices_1h`, `prices_4h`, `prices_1d`, `prices_1w`,
    `prices_1mo`: **indefinite** (RFP commitment).
- **Backup:**
  - `pgBackRest` to MinIO with WAL-stream, `--type=full` weekly,
    `--type=diff` daily, `--type=incr` hourly.
  - **RPO 5 min** (WAL archiving lag SLA).
  - **Restore test:** monthly automated; reported in ops dashboard.
- **Failover:** Patroni leader election. Target RTO 60 s.
- **Connection secret:** read via secret manager at startup, never
  on disk.
- **Cross-region consistency:** API endpoints reading from the CAGG
  tables serve only **closed** buckets per
  [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md); the
  in-progress window is never exposed. This makes "all 3 regions
  return the same rate" a property of the design rather than a
  hopeful side-effect of replication latency. See the ADR for
  trade-offs and the ≤30 s freshness contract it implies.

### 3.4 Redis Sentinel cluster

> **Amended 2026-05-01** to remove the Cluster-vs-Sentinel
> contradiction the original draft had. Ratified by
> [ADR-0024](../adr/0024-redis-ha-via-sentinel.md). The
> `redis-sentinel` ansible role
> ([role docs](../../configs/ansible/roles/redis-sentinel/README.md))
> deploys this exact topology.

- **Topology:** 1 primary + 2 replicas, Redis Sentinel mode (no
  sharding). 3 Sentinels co-located on the same 3 cache hosts;
  Sentinel quorum = 2.
- **Why Sentinel, not Cluster:** our hot-set is small
  (~few GB across all categories below); sharding adds
  operational tax without solving capacity. Sentinel is simpler
  at SEV-1 time and the migration to Cluster, if we ever need
  it, is a one-time cost rather than an ongoing tax. Full
  reasoning in ADR-0024.
- **Client connection model:** clients use `go-redis/v9`'s
  `NewFailoverClient` and ask any Sentinel for the current
  primary. There is no VIP or HAProxy in front of Redis — the
  client SDK does the discovery itself. (This is why the
  Redis sub-role of Task #72 ships standalone: no companion
  HAProxy role is required for cache, only for Postgres.)
- **Data categories:**
  - **Hot prices** — key `price:<asset>` → latest aggregated price
    JSON, TTL 60 s (refreshed by aggregator).
  - **VWAP precompute** — key `vwap:<pair>:<tf>` → value+
    computed-at, TTL matches the window.
  - **Rate-limit buckets** — key `rl:<api_key>:<min>`, TTL 120 s.
  - **SEP-1 / home-domain cache** — key `toml:<domain>`, TTL 15 min
    (matching design in [sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md)).
  - **Asset-metadata cache** — key `meta:<asset>`, TTL 5 min.
  - **SSE subscriber registry** — key `sub:<channel>`, no TTL
    (heartbeat).
- **Failure mode:** master down → Sentinel failover, 15–30 s
  window. During the window `ratesengine-api` returns `stale_flag=true`
  on affected keys (pulls from Timescale as fallback).
- **Persistence:** AOF every-second. RDB nightly. We **do not**
  rely on Redis persistence for correctness — a wiped Redis
  re-hydrates from Timescale within 2 min (the
  `ratesengine-aggregator` re-warms).
- **Caveat:** token-bucket rate-limit resets on a wipe (users get
  a grace minute). Acceptable.

### 3.5 MinIO

- **Topology:** 9 nodes with EC(6+3) erasure coding. Tolerates 3
  node failures before losing availability, 6 node failures before
  losing data.
- **Buckets:**
  - `galexie-live/` — current captive-core exports, versioning on,
    object-lock mode `COMPLIANCE` disabled (we may re-export).
  - `galexie-archive/` — immutable past exports, object-lock
    `COMPLIANCE` **on**, 1-year retention.
  - `backups/` — Timescale `pgBackRest`, object-lock off.
  - `docs/` — docs site build artefacts, public-read.
- **Replication:** bucket-level replication to AWS S3 via `mc mirror`
  every 5 min for live, 1 h for archive. Runs from `ops-01` with
  circuit-breaker (if replication lag > 30 min, page).
- **Upgrade strategy:** rolling, one node at a time; Galexie
  retries transient writes with exponential backoff.

### 3.6 ratesengine-api pool

- **Instances:** 3 pods on 3 hosts, stateless, behind HAProxy.
- **Health checks:** HTTP `/healthz` (shallow: process up) and
  `/readyz` (deep: Timescale + Redis reachable). HAProxy routes only
  to `readyz=200`.
- **Autoscaling:** static 3 at launch; target 50% CPU. Scale-up
  requires an operational decision; we do not let the autoscaler
  paper over a bug.
- **Rolling deploy:** 1-at-a-time, 60 s drain, 30 s settle.
- **Graceful shutdown:** 30 s for in-flight requests + SSE
  connections (SSE peers re-connect to the new pod automatically).

### 3.7 ratesengine-aggregator

- **Instances:** 2, leader-elected via Redis `SET key NX EX 30`
  with periodic renewal.
- **Role:** reads `trades` hypertable, computes running VWAP/TWAP,
  writes to Redis hot-key + Timescale precompute tables.
- **Why leader-elected instead of sharded:** our aggregation compute
  load is small (< 1 core per second on current market volume); a
  single active instance is simpler and preserves strict ordering.
- **Failure mode:** leader dies → standby acquires lock within 30 s;
  Redis hot keys stale-flag for ≤ 30 s until the new leader writes
  fresh values.

### 3.8 ratesengine-indexer fleet

- **Topology:** one `ratesengine-indexer` process per source (SDEX,
  Soroswap, Aquarius, Phoenix, Comet, Blend, Reflector, Redstone,
  Band, CEX, FX — roughly 11 processes).
- **Cursors:** persisted in Timescale per-source
  (`cursor(<source_id>)`). On restart the indexer resumes from the
  saved cursor.
- **Backfill:** triggered via `ratesengine-ops backfill` subcommand;
  writes into the same hypertable with idempotent upserts keyed on
  `(source, ledger, tx_hash, op_index, ts)`.
- **Failure mode:** one source dies → others continue. The dead
  source's freshness timer in Prometheus breaches the 60 s alarm;
  `/v1/price` for pairs that rely on that source sets
  `reduced_redundancy=true` in the envelope.

### 3.9 ratesengine-migrate

Not a long-running process. Runs before each deploy in a
pre-start job. Uses PostgreSQL advisory lock `pg_try_advisory_lock(...)`
to prevent two migrators from racing.

### 3.10 ratesengine-ops

Admin CLI. Runs from an operator's SSH session on `ops-01`. Top-level
subcommands cover backfill, gap-detection, archive-completeness
verify/check/fix, source decoder verification, RPC probe, archive
hash-walking, and supply-snapshot generation. The authoritative
list is the binary's own help output (`ratesengine-ops --help`)
and the source at
[`cmd/ratesengine-ops/main.go`](../../cmd/ratesengine-ops/main.go);
operator runbooks under
[`docs/operations/runbooks/`](../operations/runbooks/) cite the
specific subcommand each playbook needs (e.g. `runbooks/all-ingestion-down.md`
references `ratesengine-ops backfill`).

---

## 4. Capacity planning — napkin math

These are lower-bound estimates. Week 9 load-test supersedes them.

### 4.1 Traffic envelope

Assume 50 wallets × 200 active users each = 10 000 daily actives.
Freighter's asset-detail page makes ~5 API calls per render.
Assume 10 renders per user per active day.

- Baseline: 10 000 × 10 × 5 = **500 000 requests/day** = ~6 rps.
- Peak (everyone checks during a market move): ~60 rps.
- RFP minimum: 1 000 req/min per client = ~17 rps per client.

Capacity target: **500 rps sustained, 2 000 rps burst**. That is
~30× baseline; headroom protects us through a year of growth.

### 4.2 Per-component headroom

| Component | Sustained need | Headroom target |
| --------- | -------------- | --------------- |
| `ratesengine-api` pods (Go, `net/http`) | 500 rps | 2 000 rps (4×) |
| PgBouncer | 500 qps most cached, ~100 qps actual Timescale | 1 000 qps (2×) |
| Timescale primary | 100 write-tps (trades) + ~50 read-qps | 500 write-tps (5×) |
| Redis | 5 000 ops/s (pre-+post-cache) | 50 000 ops/s (10×) |
| MinIO | 10 MB/s Galexie write, 50 MB/s backup replication | 400 MB/s (4×) |

Single-pod Go `net/http` routinely serves 10 000 rps on a modern
host for lightweight handlers. Our handlers are mostly
"Redis GET → JSON encode → return." Hitting 2 000 rps per pod with 3
pods is comfortable.

### 4.3 Storage growth

- `trades`: ~150 trades/sec sustained Stellar network-wide × 1 kB
  average row = 150 kB/s = ~13 GB/day uncompressed.
- With TimescaleDB native columnar compression: expect 10×
  reduction → ~1.3 GB/day compressed.
- At that rate: **~500 GB/year** post-compression. A single TB NVMe
  disk lasts 2 years before we start pruning.

Validated assumption: Stellar's trade volume is roughly stable
year-over-year at this order of magnitude. Re-measure post-launch.

---

## 5. Failure matrix

| Component dies | Blast radius | Behaviour | Time-to-recover |
| -------------- | ------------ | --------- | --------------- |
| 1 `ratesengine-api` pod | 33% reduced serving capacity | HAProxy routes to other 2; auto-restart | < 30 s |
| 2 `ratesengine-api` pods | 66% reduced | degraded SLA warning alert | 1–5 min manual intervention |
| Redis master | One hash slot unavailable for ~30 s | stale_flag on affected keys; readyz false during window | Sentinel failover 15–30 s |
| Timescale primary | Writes fail | Patroni elects replica; api switches read pool via PgBouncer | 30–60 s |
| PgBouncer pair | All DB access fails | Depends on keepalived VIP failover timing | 5–15 s |
| 1 stellar-core | Aggregator loses one ingest source | duplicate stream from others; dedup by hash | instant |
| All 3 stellar-core | No new ledger events | API returns stale_flag=true and 30 s-old data from cache | minutes–hours |
| 1 stellar-rpc | `getEvents` subscribers fall over to survivor | automatic | < 10 s |
| MinIO 1–3 nodes | EC(6+3) preserves reads/writes | auto-heal on replacement | hours |
| MinIO 4–6 nodes | Writes fail; reads OK | alert SEV-1 | hours–days |
| HAProxy active | Keepalived VIP failover to peer | < 2 s drop | < 2 s |
| Aggregator leader | Standby acquires leadership | stale hot-keys for ≤ 30 s | 30 s |
| Colo power | Full primary outage | manual DR activation to cloud | 4 h (per DR runbook) |
| Internet link to colo | API unreachable | DNS failover to cloud DR | 5 min |

No single-component failure breaches 99.9% monthly (≤ 43 min/month).
Two-component failures can breach; catalogued above with response
times.

---

## 6. Security posture

Not the full threat model (that lives in `docs/operations/threat-model.md`,
Week 9), but the HA-relevant items:

- **Secrets:** Vault (colo) + AWS Secrets Manager (cloud), cross-
  replicated via periodic sync. Application reads at startup via a
  sidecar; no secret ever on a disk outside Vault.
- **TLS everywhere internal and external.** Internal: mTLS between
  api↔pgbouncer↔timescale and api↔redis. External: Let's Encrypt +
  HSTS.
- **Network segmentation:** Management VLAN, data VLAN, DMZ for
  HAProxy. api pool has no egress except to Timescale, Redis, and
  logging. Indexers have egress only to pinned CEX/FX IP ranges +
  `stellar-rpc.publicnode.com` fallback.
- **HSM for validator keys** (ADR-0004) — YubiHSM-2 on two physical
  hosts.
- **Audit log:** every `ratesengine-ops` command recorded to an
  append-only bucket. Admin surface requires 2FA via the jump host.

---

## 7. Observability

- **Metrics:** Prometheus pair (primary + replica); federated from
  cloud Prometheus for DR. Retention: 30 d local, 1 y downsampled
  to MinIO via Thanos.
- **Dashboards:** Grafana — one dashboard per component + one
  "Golden Signals" board (latency p50/p95/p99, error rate,
  saturation, traffic).
- **Alerts:** AlertManager → PagerDuty. Tiers:
  - **P1:** 99.9 % SLA-breaking; pages immediately.
  - **P2:** degraded; pages during business hours + daily summary.
  - **P3:** informational; ticketed.
- **Tracing:** OpenTelemetry → Tempo. Sampling 100 % at development,
  10 % at production, 100 % on errors.
- **Logs:** structured JSON via zerolog; shipped to Loki with
  14-day retention + 1 y cold.

Alerts already sketched in `docs/operations/alerts-catalog.md` (Week 9).

---

## 8. Backup & restore

| Asset | Tool | Frequency | RPO | Retention | Restore drill |
| ----- | ---- | --------- | --- | --------- | ------------- |
| Timescale | pgBackRest → MinIO | full weekly, diff daily, incr hourly, WAL stream | 5 min | 90 d full, 3 y incr | Monthly to `db-drill-01` |
| Redis | AOF every-second | 1 s | AOF last 7 d | 7 d | Not needed (cache) |
| MinIO `galexie-live` | versioning | per write | 0 | 30 d versions | Monthly restore of one ledger window |
| MinIO `galexie-archive` | versioning + object-lock | per write | 0 | indefinite | Annual |
| Configs (in Git) | Git → GitHub | every commit | 0 | indefinite | Every deploy is a restore |
| Secrets (Vault) | Vault snapshot → S3 (encrypted) | 4× daily | 6 h | 30 d | Quarterly |

Restore time objectives:

- **Hot (Timescale point-in-time, last hour):** 10 min.
- **Warm (last week):** 2 h.
- **Cold (arbitrary historical ledger):** 8 h worst case.

---

## 9. Degradation modes (what we promise under failure)

Contractually the RFP expects us to document "what happens when prices
become unavailable, sources start to differ, etc." The API envelope
(to be specified in [api-design.md](../reference/api-design.md) §Error envelope)
carries four boolean flags:

| Flag | Meaning | When we set it |
| ---- | ------- | -------------- |
| `stale_flag` | Price > 30 s old | Redis hot key TTL expired + aggregator hasn't written new value |
| `reduced_redundancy` | Price derived from fewer sources than normal | Any configured source for this asset is unhealthy (cursor lag > 60 s) |
| `triangulated` | Price derived via a USD/BTC hop, not direct | Pair has no direct market meeting min-volume threshold |
| `divergence_warning` | Sources disagree > configured threshold | Cross-check against CoinGecko / CMC / Chainlink-HTTP fails bound |

No flag is a response-level error; they're advisory. Clients decide
whether to accept. The `price` value is always best-available;
`stale_flag=true` means "here's the last known good, fix your
decision-making accordingly."

Specific "everything is on fire" scenarios:

| Scenario | Response |
| -------- | -------- |
| Full primary-colo outage | DNS flip to cloud DR → API serves from AWS + last-synced Timescale replica (RPO 5 min) with `stale_flag=true` on every response until ingest is re-established. |
| One critical source (e.g., Reflector) offline | Affected assets get `reduced_redundancy=true`; others unaffected. |
| Divergence: Redstone vs CEX > 5% | `divergence_warning=true` on affected assets; internal alert to @ash for market-event sanity check. |
| TimescaleDB read-replica lag > 10 s | API briefly reads from primary (via PgBouncer session-mode pool); alert if sustained. |

---

## 10. Launch checklist (HA subset)

- [ ] All 3 stellar-core + galexie instances running stably for 7 days with no crashes.
- [ ] Patroni failover drilled end-to-end in staging (simulate primary OOM).
- [ ] Redis Sentinel failover drilled (kill master during load).
- [ ] Load test hits 2 000 rps with p95 ≤ 200 ms on cached endpoints.
- [ ] Restore drill: point-in-time recovery to 24 h ago, < 2 h wall-clock.
- [ ] DR drill: DNS-flip to cloud, serve for 1 h, flip back.
- [ ] Alerts catalogue reviewed — every alert has a runbook link.
- [ ] SEV-1 + SEV-2 playbooks rehearsed with a tabletop exercise.

None of these are green today (Week 1). Every line becomes a PR
checklist at its owning week.

---

## 11. Open questions — closed

The Week-1 plan called for these to land as ADRs or design docs by
end of Week 2. They have:

1. **Colo provider + physical locations** — Hetzner FSN1 (Falkenstein, DE)
   for R1; AWS for R2; Vultr for R3. See
   [r1-deployment-state.md](../operations/r1-deployment-state.md) +
   [ADR-0016](../adr/0016-per-region-storage-strategy.md).
2. **Patroni vs Stolon vs native TimescaleDB HA** — Patroni; landed
   as `configs/ansible/roles/patroni/`.
3. **MinIO EC(6+3) vs EC(4+2)** — EC(6+3); fixed in ADR-0008 §2.
4. **Cloud DR region** — AWS eu-west-1 (matching the colo latency
   profile for European users); ADR-0008 §5.
5. **Secret-manager choice** — Ansible Vault for inventory secrets;
   `configs/ansible/inventory/r1.secrets.yml` is the source of
   truth, per the playbook README.
6. **Observability stack** — self-hosted Prometheus + Grafana +
   Loki; ansible roles
   `configs/ansible/roles/{prometheus,loki}/` deploy them.

Anything new that surfaces post-ratification gets a fresh ADR rather
than an entry here.

---

## 12. Cost envelope

Order-of-magnitude; concrete per-line numbers live in the operator's
own cost spreadsheet (not checked into the repo). Below is the
shape used to size hardware in ADR-0008.

| Line | Monthly | Notes |
| ---- | ------- | ----- |
| 3 × R640 colo + power + bandwidth | $1.5–2k | existing footprint, already owned; incremental |
| 9 × MinIO nodes (smaller chassis) | $2–3k | 180 TB raw, ~120 TB usable after EC |
| 3 × Timescale hosts | already covered by R640s | |
| Cloud DR (AWS) | $1–2k warm, $5k+ on failover | RDS async + stateless scale-to-zero |
| Observability (Grafana Cloud or self-hosted) | $500 | |
| CDN (Cloudflare) | $200 | |
| Domain + TLS + GitHub | $100 | |
| **Total steady state** | **~$5–8k / month** | | 

Revenue model is out of scope (free public API; SDF grant funds).
Cost envelope checked against the proposal's budget line.

---

## 13. Appendix — tooling

- **HAProxy** — 2.9 LTS.
- **keepalived** — for VRRP VIPs.
- **Patroni** — 3.x with etcd3 DCS.
- **PgBouncer** — transaction mode.
- **Redis** — 7.x with Sentinel.
- **MinIO** — current RELEASE.* on the docker-compose profile;
  baremetal RPMs in production.
- **pgBackRest** — with MinIO as the repo backend.
- **Prometheus + AlertManager + Grafana + Loki + Tempo** — "grafana
  stack." Possibly replaced with Grafana Cloud depending on
  cost model.

All tools are Apache-2.0 / MIT / PostgreSQL / BSD-compatible. No
copyleft dependencies in the serving path.
