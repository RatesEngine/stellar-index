---
title: Runbook — host-memory-high
last_verified: 2026-05-03
status: draft
severity: P3
---

# Runbook — `ratesengine_host_memory_high`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_host_memory_high` |
| Severity | P3 (informational, can escalate quickly) |
| Detected by | `deploy/monitoring/rules/infra.yml` |
| Typical MTTR | 15 min – hours |
| Impact | OOMKill risk. If the kernel's OOM-killer runs it'll take whichever process it picks — usually the biggest allocator — potentially causing a dependent-service outage. |

## Symptoms

- `(MemTotal - MemAvailable) / MemTotal > 0.90` for ≥ 10 min.
- Swap may start being used (if enabled — on most of our hosts
  it isn't).
- Subsequent alerts: `host-cpu-high` (swapping is CPU-visible),
  eventually a service alert when OOM-killer fires.

## Quick diagnosis (≤ 5 min)

```sh
# Top memory users (per-process and per-cgroup)
ssh <host> 'ps auxww --sort=-%mem | head -10'
ssh <host> 'systemd-cgtop --order=memory --iterations=2 -n 20'

# What's the breakdown? Page cache vs RSS vs slab?
ssh <host> 'free -h; cat /proc/meminfo | head -30'

# Has OOM-killer already fired?
ssh <host> 'dmesg -T | grep -i "Out of memory\|killed process" | tail'
```

## Typical root causes

1. **Postgres `shared_buffers` + `work_mem` adding up.** Per-
   backend `work_mem` × `max_connections` is the usual way
   Postgres memory explodes.
   - Signal: `ps` shows many postgres backends each using
     hundreds of MB.
   - Mitigation: lower `work_mem` (requires restart or SET for new
     sessions); use PgBouncer to reduce backend count.

2. **Go runtime heap growth** — a memory leak or retention bug.
   - Signal: the binary's RSS climbs monotonically over hours/days.
   - Mitigation: pprof heap dump, restart the pod while the fix
     ships. For the indexer this loses no data; for the API it's
     transparent (rolling restart).

3. **File-cache eating "available" memory that's not really
   available**. Linux's `MemAvailable` is usually correct but
   specific workloads (big mmap, tmpfs with ulimit) can create
   divergence.
   - Signal: `free -h` shows large `buff/cache` and low `available`.
   - Mitigation: usually benign — page cache is reclaimable. But
     if `available` is low AND applications are getting ENOMEM,
     that's a real problem.

4. **ZFS ARC.** On Postgres hosts we cap ARC at ~50 % of RAM, but
   a default-configured host can let ARC grow unbounded.
   - Signal: `arcstat` / `/proc/spl/kstat/zfs/arcstats` shows ARC
     size close to RAM size.
   - Mitigation: set `zfs_arc_max` to a sane cap and reload.

## Mitigation

- [ ] Step 1 — identify the consumer (above).
- [ ] Step 2 — if a service is leaking: restart it (buys time)
      and file an incident.
- [ ] Step 3 — if Postgres: tune `work_mem`; drain the connection
      pool via PgBouncer; consider graceful primary-replica
      swap to reset backends.
- [ ] Step 4 — if genuine undersize: scale up the host or move
      workloads.
- [ ] Verification: `available` memory climbs back to > 20 %; no
      OOM events in the next hour.

## Known false-positive patterns

- **ZFS ARC looking like "used" memory.** `MemAvailable` in the
  Linux kernel accounts for reclaimable page cache but historically
  not ZFS ARC. On newer kernels (6.x) this is fixed; older kernels
  can report 90 %+ used when half of that is ARC and immediately
  reclaimable. Check `arcstat` before panicking.
- **Freshly started process warming up**. `free -h` shows low
  available for the first few minutes while caches populate;
  stabilises.

## Related

- `host-cpu-high.md` — swapping shows up as CPU too.
- `timescale-primary-down.md` — OOM-kill of Postgres is a specific
  path to this.
- `host-down.md` — if the host itself goes (OOM-killer gets init?).

## Changelog

- 2026-04-23 — initial draft.
