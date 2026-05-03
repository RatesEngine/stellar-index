---
title: Runbook — redis-replication
last_verified: 2026-05-02
status: draft
severity: P2
---

# Runbook — `ratesengine_redis_replication_broken`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_redis_replication_broken` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/cache.yml` |
| Typical MTTR | 15–45 min |
| Impact | No immediate customer impact — reads and writes continue on the master. But Sentinel needs at least one healthy replica to promote on failover; without that, **a master failure becomes a full cache outage** (`redis-master-down.md`). |

## Symptoms

- `redis_connected_slaves < redis_expected_slaves` on the master
  for ≥ 2 min. We expect 2 replicas per master in production
  (HA plan §3.4).
- `redis-cli info replication` on the master shows
  `connected_slaves:` lower than configured.
- Sentinel logs: `+sdown slave ...`.

## Quick diagnosis (≤ 5 min)

```sh
# View from the master
redis-cli -h redis-master info replication

# Each replica's own view
for shard in redis-1 redis-2; do
  echo "=== $shard ==="
  redis-cli -h $shard info replication | grep -E 'role|master_link_status|slave_read_only'
done

# Is a replica wedged on an initial sync?
redis-cli -h redis-1 info replication | grep -E 'master_sync_in_progress|master_sync_total_bytes|master_sync_left_bytes'

# Sentinel's view
redis-cli -h redis-sentinel -p 26379 sentinel replicas <mastername>
```

## Typical root causes

1. **Replica process died.** Hardware / OOM / crash. Check pod
   state + logs on the affected replica.

2. **Replica is behind the master's repl-backlog and can't catch up
   incrementally**, so it's doing a full sync — during which it's
   counted as connected but `master_link_status:down` can flap.
   - Signal: `master_sync_in_progress:1` on the replica.
   - Mitigation: wait. Full sync on a multi-GB Redis takes minutes.
     If it doesn't complete, your `repl-backlog-size` or
     `client-output-buffer-limit replica` are too small — bump
     both.

3. **Network-level flapping** between master and replica. TCP
   connection repeatedly drops.
   - Signal: master log shows repeated `Connecting to MASTER ... /
     Partial resynchronization not possible` cycles.
   - Mitigation: network diagnosis (MTU, packet loss, firewall
     between zones).

4. **Authentication drift.** After secret rotation, one replica's
   `requirepass` / `masterauth` didn't get updated.
   - Signal: replica log says `NOAUTH Authentication required`.
   - Mitigation: re-roll the replica with the correct secret.

## Mitigation

- [ ] Step 1 — identify which replica is missing and why (above).
- [ ] Step 2 — if redis-server on a replica host is down:
      `ssh root@cache-NN "systemctl restart redis-server"`.
      Sentinel detects the recovered replica and re-adds it to
      replication on its next discovery cycle.
- [ ] Step 3 — if sync is in progress: monitor
      `master_sync_left_bytes`; ETA = leftBytes / network bandwidth.
- [ ] Step 4 — if auth drift: restart the replica with the correct
      secret mounted.
- [ ] Verification: `connected_slaves` on the master returns to the
      expected count; Sentinel's replica list shows all healthy.

## Root cause analysis

- Master log around the disconnect.
- Replica log of the affected instance.
- Sentinel log across the window (sdown / odown events).
- Was there a network/firewall change deploy around the incident
  window?

## Known false-positive patterns

- **Rolling StatefulSet restart**: a replica is briefly absent
  while its pod respawns. PodDisruptionBudget keeps the quorum
  intact, but the `for: 2m` threshold can trip during a deliberately
  slow rollout. Silence the alert for the duration of a planned
  maintenance.
- **New replica provisioning**: adding a third replica to a shard
  produces a period of `connected_slaves == expected - 1` while
  initial-sync completes. Expected.

## Related

- `redis-master-down.md` — the downstream consequence if a master
  fails while replication is broken.
- HA plan §3.4 Redis topology.

## Changelog

- 2026-04-23 — initial draft.
