---
title: Runbook — zfs-degraded
last_verified: 2026-04-23
status: draft
severity: P1
---

# Runbook — `ratesengine_zfs_pool_degraded`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_zfs_pool_degraded` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/infra.yml` |
| Typical MTTR | 30 min – hours (resilver time depends on data size) |
| Impact | raidz2 tolerates 2 drive failures; we alert on 1. At DEGRADED we still serve reads and writes. At FAULTED we've exhausted redundancy. Act while we still have margin. |

## Symptoms

- `node_zfs_pool_state{state=~"DEGRADED|FAULTED|UNAVAIL"} > 0`
  for ≥ 60 s.
- `zpool status` shows one (or more) drives in FAULTED or OFFLINE.
- Often pairs with `nvme-smart.md` firing on the same host — the
  IO errors escalated to a full drive fail.

## Quick diagnosis (≤ 5 min)

```sh
ssh <host> 'zpool status -v'
# Look for lines like:
#   state: DEGRADED
#   action: Attach the missing device and online it...
#   NAME                STATE     READ WRITE CKSUM
#   tank                DEGRADED     0     0     0
#     raidz2-0          DEGRADED     0     0     0
#       nvme0n1         ONLINE       0     0     0
#       nvme1n1         FAULTED      0     0     0  too many errors

# Which drive physically?
ssh <host> 'ls -l /dev/disk/by-id/ | grep nvme1n1'
ssh <host> 'nvme list'   # serial → slot mapping via chassis docs

# Resilver status if replacement already in progress
ssh <host> 'zpool status | grep -A5 resilver'
```

## Typical root causes

1. **Drive hardware failure.** The usual story — wear, thermal,
   controller. `nvme-smart.md` will have been firing on this drive
   for a while if you're paying attention.

2. **SATA/NVMe controller flake** — drive is fine but the slot /
   cable / controller is dropping it.

3. **Power event** — brownout, PSU swap mid-operation.

## Mitigation

- [ ] Step 1 — **verify remaining redundancy**. raidz2 tolerates 2
      failures; if only 1 is FAULTED you have one margin. If 2
      are FAULTED you're at the edge — any further failure = data
      loss. **Stop here and escalate to SEV-1 if already at edge.**
- [ ] Step 2 — physically replace the drive. Remote-hands swaps
      it in the correct slot.
- [ ] Step 3 — tell ZFS to resilver:
      ```sh
      zpool replace tank <old-drive-id> <new-drive-id>
      ```
- [ ] Step 4 — monitor resilver progress. ETA in `zpool status`.
      Don't let anything stress the pool during resilver (pause
      compression, scrub, backup).
- [ ] Step 5 — if a second drive fails during resilver: now you're
      one failure away from data loss. At this point we'd pivot
      to failover-to-replica and treat this host as a write-off
      for the incident.
- [ ] Verification: `zpool status` shows ONLINE, no errors, pool
      returns to HEALTHY.

## Root cause analysis

- Smartctl logs from the failed drive (before it's discarded —
  the vendor's warranty process may need them).
- When was the drive installed? (Track in
  `docs/operations/inventory.md`.)
- Did `nvme-smart.md` or `nvme-thermal.md` fire earlier? Was
  action taken?
- Are other drives on the same host showing elevated SMART
  warnings?

## Known false-positive patterns

- **During a planned drive replacement** the alert fires
  momentarily as the pool transitions DEGRADED → resilvering →
  HEALTHY. Silence during scheduled maintenance windows.
- **Hot-spare swap** triggers a brief DEGRADED before the spare
  is accepted into the vdev.

## Related

- `nvme-smart.md` — precursor warnings.
- `nvme-thermal.md` — another precursor.
- `db-disk-full.md` — running tight on capacity amplifies
  recovery stress.
- Tier-1 posture: `docs/adr/0004-three-validator-aspiration.md` —
  we're committed to independent history archives, which means
  drive failures on one of the three validator hosts must not
  cascade to the others.

## Changelog

- 2026-04-23 — initial draft.
