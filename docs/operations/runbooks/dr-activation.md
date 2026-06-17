---
title: Runbook — DR (disaster-recovery) activation
last_verified: 2026-05-03
status: ratified
related:
  - docs/architecture/ha-plan.md
  - docs/adr/0008-ha-topology.md
  - docs/adr/0016-per-region-storage-strategy.md
  - docs/operations/sev-playbook.md
  - docs/operations/archival-node-bringup.md
  - docs/operations/runbooks/timescale-primary-down.md
---

# Runbook — DR (disaster-recovery) activation

The procedure for cutting traffic over to a standby region when
the primary region (R1 today) is partially or fully unrecoverable.
Sister to [`timescale-primary-down.md`](timescale-primary-down.md) §D
"Complete cluster loss (asteroid scenario)", which references this
runbook explicitly.

This is a SEV-1 procedure — open the incident channel, declare
severity, follow [`sev-playbook.md`](../sev-playbook.md) §4 for the
incident-management overlay. The steps below are the technical
flip; the IC drives + validates in parallel.

**Drilled:** the [annual DR exercise](../sev-playbook.md#83-annual-dr)
walks this end-to-end on a controlled-loss simulation; quarterly
chaos drills (sev2 / partial-region degradation) exercise pieces
of it. If you're reading this for the first time *during* an
incident, follow it from §3.

---

## 1. When to activate DR

Activate when ANY of these is true AND no faster recovery exists:

- **Primary region's storage tier is unrecoverable.** Patroni
  has no quorum-eligible replicas; pgBackRest restore is failing
  or estimated > 4 h; the disk-failure mode in
  [`timescale-primary-down.md`](timescale-primary-down.md) §A
  cannot promote a replica.
- **Primary region's network is partitioned for > 30 min and
  external monitoring confirms the partition is regional, not
  local-to-us.** Cloudflare health probe + a third-party
  network-status site both report the same regional outage.
- **Primary region's host fleet is gone** (provider-side
  catastrophic failure, datacentre power loss, etc.). Hetzner
  status page red on R1's location, no projected ETA.
- **Data corruption suspected on primary AND replica** (read
  beyond storage failure: a logical corruption that's already
  replicated). Restore from `pgBackRest` to the DR region is
  the only option that doesn't propagate the corruption.

**Don't activate when:**
- Patroni is mid-failover (give it 60 s; it's designed to handle
  this without operator intervention).
- A single Timescale primary is down but the replica is healthy
  — [`timescale-primary-down.md`](timescale-primary-down.md) §A
  is the right runbook (faster + lossless).
- Redis cluster lost a master (Sentinel handles; see
  [`scenarios/sev2-redis-sentinel-failover.md`](../drills/scenarios/sev2-redis-sentinel-failover.md)).
- One of the three regions is degraded but R1 is healthy —
  R2/R3 are read-replica-style today (per [ADR-0016](../../adr/0016-per-region-storage-strategy.md))
  and don't need DR activation; they self-heal once the partition
  clears.

The decision itself is reversible (failback in §6). The COST of
a wrong activation is ~5 s RPO data loss (whatever didn't
replicate before primary went down) + a customer-visible blip
during DNS propagation. The COST of NOT activating when you
should is sustained customer-visible outage. Bias toward
activation when the criteria are clearly met.

---

## 2. DR region pre-flight (do this BEFORE any traffic flip)

The IC's tech lead runs these checks while the comms lead drafts
the status-page update. Both happen in parallel.

### 2.1 Confirm DR region's storage is current

```sh
# From a DR-region operator workstation:
ssh root@<dr-region-postgres-primary>

# Check pgBackRest archive freshness
pgbackrest --stanza=stellarindex info | head -20
# → "archive (current)" + most-recent backup/WAL within ~5 min

# If WAL replication lag was ≤ 5 s (the SLO target):
psql -c "SELECT pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn();"
# → both LSNs should be the same or within a handful of bytes
```

If the DR region's storage is more than 30 min behind primary
AND the primary is still partially serving, **delay activation**:
let WAL ship the gap first, then proceed. If the primary is
fully gone, accept the lag and proceed (you can't get more recent
data; the lag IS the RPO).

### 2.2 Confirm DR region's MinIO archive is intact

```sh
# Galexie archive completeness — the historical-replay safety net
stellarindex-ops archive-completeness verify \
    --region <dr-region-id> \
    --tier all
# → exit 0 + "all checkpoints reachable"
```

If the DR region's MinIO archive has gaps, the historical
since-inception API will serve stale data until the gaps are
filled. Note this in the status-page update; it doesn't block
flip.

### 2.3 Confirm DR region's API/aggregator/indexer hosts are reachable

```sh
# From DR-region SSH bastion:
for host in <dr-api> <dr-aggregator> <dr-indexer>; do
    ssh root@$host 'systemctl status stellarindex-* --no-pager' | grep -E "Active|service"
done
# → every service "active (running)" or "active (waiting)"
```

If a host is down, decide: spin up a replacement (per
[`archival-node-bringup.md`](../archival-node-bringup.md)) before
flipping, OR flip with reduced redundancy in DR. Reduced
redundancy is acceptable for the duration of the incident — note
on the status page.

---

## 3. Traffic flip (the actual cutover)

### 3.1 Status-page update

Comms lead posts the **Investigating** entry per
[`sev-status-page-update.md`](sev-status-page-update.md). Suggested
wording for §"What customers see":

> We've identified an issue with our primary region and are
> failing over to our disaster-recovery region. API requests
> may briefly fail or return stale data during the flip
> (typically < 60 seconds). We'll post an update once the
> flip completes.

Don't name internal infrastructure. Don't speculate on cause
yet — that goes in the **Identified** entry after flip succeeds.

### 3.2 DNS flip

The Cloudflare DNS records pointing `api.stellarindex.io` at
the primary region's HAProxy VIPs need to point at the DR
region's HAProxy VIPs.

Two flip mechanisms (use whichever the operator's incident-
console wires up):

**A. Cloudflare load balancer (preferred):** the LB pool
already has all three regions' origins configured per
[ADR-0008](../../adr/0008-ha-topology.md) §"DNS / load balancing".
Mark the primary-region pool members as down via the
Cloudflare dashboard:

1. Cloudflare → Traffic → Load Balancers → `api-prod`
2. Edit the primary pool → set `enabled: false`
3. Save. Health-check probes drop the primary; traffic shifts to
   the DR pool within ~30 s.

**B. Manual DNS swap (fallback if Cloudflare LB is down):**
update the A record directly:

```sh
# Requires Cloudflare API token in ops vault; see deploy/ops-keys.md
cf-cli dns update stellarindex.io api A <dr-haproxy-vip-1>,<dr-haproxy-vip-2>
# TTL is 60s by design; full propagation < 2 min
```

### 3.3 Verify API is serving from DR

```sh
# Direct curl bypassing CDN — proves the origin is up
curl -sS -H 'X-Trace: dr-flip-verify' https://api.stellarindex.io/v1/healthz
# → {"status": "ok", "region": "<dr-region-id>", ...}

# Spot-check a real query
curl -sS https://api.stellarindex.io/v1/price?asset=native | jq '.data.price, .as_of'
# → non-empty price + recent timestamp
```

The `region` field in `/v1/healthz` confirms the origin we hit.
A request landing on the primary (because some CDN edge is still
caching DNS) would show the primary's region — wait for TTL
expiry rather than declaring success early.

### 3.4 Update status page

Comms lead posts **Identified** with one-line cause + that
mitigation is deployed. Customer-facing wording:

> We've completed failover to our disaster-recovery region.
> API requests are now serving normally. Some historical data
> may temporarily reflect a brief gap during the flip; we're
> investigating root cause and will post an update with full
> service confirmation within the hour.

---

## 4. Post-flip monitoring (first hour)

The DR region is now serving production. Validate the next few
metrics actually look right rather than just "running":

### 4.1 SLA probe metrics

```sh
# From operator workstation; SLO Grafana board
curl -sS https://grafana.<dr-region>.stellarindex.io/api/dashboards/uid/slo \
    -H "Authorization: Bearer $GRAFANA_TOKEN" | jq -r '...'
```

Specifically watch:
- `http_request_duration_seconds` p95 < 200 ms (matches
  service SLA F3.1)
- `http_requests_total{status=~"5.."}` rate < 0.1 % (F3.3)
- `flags.stale=true` rate < 5 % (otherwise the aggregator's
  upstream sources aren't all reaching DR)

### 4.2 Aggregator + ingest health

```sh
# Should match primary's pre-incident steady state within ~5 min
stellarindex-ops list-cursors --region <dr-region>
# → every source's last_ledger advancing
```

If the indexer's cursors aren't advancing, check Galexie's
read connectivity to MinIO — the DR region's MinIO bucket
should be writable + readable per
[ADR-0016](../../adr/0016-per-region-storage-strategy.md).

### 4.3 Customer-visible flag rates

```sh
# Aggregator's anomaly + freeze counters
curl -sS https://prometheus.<dr-region>.stellarindex.io/api/v1/query \
    --data-urlencode 'query=rate(stellarindex_anomaly_freeze_engaged_total[5m])' | jq
```

A freeze-engaged spike right after flip is normal (some pairs
will see source-class diversity drop briefly during the flip).
A SUSTAINED freeze rate above pre-incident baseline is a signal
that DR's source set is incomplete.

---

## 5. Status-page resolution

When SLA + ingest + flag rates have all returned to baseline
for ≥ 30 min, comms lead posts **Resolved**:

> Service is fully restored as of <UTC>. Total customer impact:
> <duration> with elevated error rates and one ~60 s gap during
> the failover. We'll post a full post-mortem within 72 hours
> per our SEV-1 commitment.

The full post-mortem follows
[`sev-playbook.md`](../sev-playbook.md) §6.

---

## 6. Failback to primary

DO NOT failback during the same incident shift unless the
primary's failure mode was clearly transient. Most DR
activations should run on the DR region for 24–72 hours so the
team can validate primary's underlying fix isn't itself faulty.

When the primary is verified healthy:

### 6.1 Primary catch-up

```sh
# Check WAL streaming from DR back to primary
psql -h <primary-postgres> -c "SELECT pg_is_in_recovery(), pg_last_wal_replay_lsn();"
# → in_recovery=true, replay_lsn closely tracking DR's send_lsn
```

The primary should be running as a streaming replica of DR
during the failback window. If `pg_is_in_recovery()` returns
false, the primary is still acting as a former-primary that
diverged — escalate to the IC; this needs `pg_rewind` or full
restore from DR's pgBackRest, NOT a simple flip.

### 6.2 Reverse the DNS / Cloudflare LB flip

Same procedure as §3.2 but flipping the DR pool down + primary
pool up. Run the same §3.3 verifications.

### 6.3 Post-failback monitoring

Same as §4 but watching primary's metrics. Pay extra attention
to anything that would indicate replication lag — a primary
that's "caught up" but missing recent rows would surface as
sudden `flags.stale=true` spikes on /v1/price.

---

## 7. Escalation

If any §3 step fails (DNS flip won't propagate, DR region's
healthz returns 5xx, etc.):

1. Don't roll back — the primary is presumed lost; rolling back
   to it makes things worse.
2. Page secondary on-call + engineering manager.
3. Post a status-page update acknowledging "extended outage
   during failover" without speculation on cause.
4. The fallback path is to spin up a fresh archival node in a
   third region per
   [`archival-node-bringup.md`](../archival-node-bringup.md);
   that's measured in hours, not minutes.

---

## 8. What this runbook does NOT cover

- **Bringing up a fresh region** — see
  [`archival-node-bringup.md`](../archival-node-bringup.md).
- **Per-component HA failover** (Patroni primary swap, Sentinel
  Redis swap) — see [`timescale-primary-down.md`](timescale-primary-down.md)
  and [`scenarios/sev2-redis-sentinel-failover.md`](../drills/scenarios/sev2-redis-sentinel-failover.md).
- **Annual DR exercise procedure** — same flip, but pre-
  announced + conducted on staging-equivalent traffic. See
  [`sev-playbook.md`](../sev-playbook.md) §8.3.
- **Incident-management overlay** (severity declaration,
  comms cadence, postmortem timing) — see
  [`sev-playbook.md`](../sev-playbook.md) §4.

## 9. Drift signals

Run quarterly:

- DR region's `/v1/healthz` returns the right region label.
- Cloudflare LB's primary + DR pools both have current host
  IPs (operator-supplied DNS rolls invalidate the pool config
  silently).
- pgBackRest stanza fresh in BOTH regions (not just primary).
- `dr-activation.md` itself reflects current ADR-0008 + 0016
  decisions — re-read on each ADR change to those docs.

A failed drift signal is a launch-readiness regression — file
an issue with the `dr-readiness` label.
