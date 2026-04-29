---
title: Runbook — verify-archive-run-stale
last_verified: 2026-04-29
status: ratified
severity: P2
---

# Runbook — `ratesengine_verify_archive_run_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_verify_archive_run_stale` |
| Severity | P2 (page) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/verify-archive.yml` |
| Typical MTTR | 1 hour (diagnose + re-enable timer or kick off fresh run) |
| Impact | Cross-region trust degrades. R2/R3 trust R1 for chain-integrity (ADR-0016); the longer R1 goes without a clean nightly verify, the further from "byte-identical bytes everywhere" the fleet drifts. After 48h consider flipping `flags.reduced_redundancy=true` on R2/R3 API responses. |

## Symptoms

- The `verify-archive-tier-a.timer` last-trigger timestamp is more
  than 36h ago (24h cadence + 12h cushion).
- Sustained for 10 minutes — rules out a node_exporter scrape blip.
- Likely accompanied by `ratesengine_verify_archive_unit_failed`
  on each preceding night — the ticket-level alert that should
  have caught this earlier.

## Quick diagnosis (≤ 5 min)

```sh
# Is the timer enabled and active?
ssh r1 'systemctl status verify-archive-tier-a.timer'

# What's the last-trigger timestamp + the next scheduled run?
ssh r1 'systemctl list-timers verify-archive-tier-a.timer --all'

# Has the service been failing? Look across the recent runs.
ssh r1 'journalctl -u verify-archive-tier-a.service --since "3 days ago" \
  | grep -E "Started|Finished|FAILED|Failed|exit-code"'
```

Three branches:

| State | Cause | Action |
| ----- | ----- | ------ |
| `inactive (dead)` and last-trigger > 36h ago | Timer disabled (operator forgot after a maintenance window) | Re-enable: `systemctl enable --now verify-archive-tier-a.timer` |
| `active (waiting)` but last `Finished` is > 36h | Every recent run failed | Walk through `verify-archive-unit-failed.md` for the underlying mode |
| `active (running)` for hours | Current run is hung | Investigate per-chunk progress; consider increasing `-max-runtime` |

## Mitigation (≤ 15 min)

- [ ] **If the timer is disabled**, re-enable it:
  ```sh
  ssh r1 'systemctl enable --now verify-archive-tier-a.timer'
  ```
  This is the entire fix for the most-common cause (a missed re-enable after maintenance). Verify the next-trigger timestamp is < 24h ahead.
- [ ] **If runs are failing**, follow the [`verify-archive-unit-failed.md`](verify-archive-unit-failed.md) runbook to bring a single manual run back to green. The timer fires automatically nightly; one clean run is enough to clear this alert.
- [ ] **If a run is hung**, capture per-chunk progress and decide whether to wait or kill:
  ```sh
  ssh r1 'journalctl -u verify-archive-tier-a.service -f' &
  # Monitor heartbeat output. If progress is steady but slow, increase
  # max-runtime via /etc/default/ratesengine-ops; if frozen at one
  # ledger, kill and investigate.
  ```
- [ ] **Communicate degradation**: while this alert is firing, R2/R3 should consider setting `flags.reduced_redundancy=true` on their API responses. Per ADR-0017 §"Graceful degradation" the flag fires when R1's last successful verify is older than the documented threshold; with the L4.10 per-region trust wiring still pending the operator may need to flip a config value manually.
- [ ] **Verification**: a clean run completes within 24h; the alert clears.

## Root cause analysis

The page-level alert means R1's nightly chain-integrity anchor has been broken for over a day. Postmortem questions:

1. **Why did the ticket-level `_unit_failed` alert not get actioned?** Was the ticket missed, or was the failure self-fixing for too long before this page fired? Adjust SLA if needed.
2. **Was there a coincident upstream issue?** Cross-reference public Stellar archive incidents.
3. **Did `archive-completeness` also drift?** A missing-file gap can starve verify-archive indefinitely; both timers should be in sync.

Gather:

- Three nights of `journalctl -u verify-archive-tier-a.service`
- Same window of `archive-completeness.service` logs
- Public Stellar status archive snapshots
- The full `last_verified` chain on relevant ADRs (we may have invalidated a documented invariant)

## Known false-positive patterns

- **Planned long maintenance window** that disabled the timer for > 36h. Post-maintenance, re-enable + accept one staleness alert. Not a real issue.
- **Clock skew on the Prometheus side** — `time()` reads slightly different from the unit-state `last-trigger` source. Unusual; investigate node_exporter health if it persists.

## Related

- `verify-archive-unit-failed.md` — the ticket-level alert that
  should have caught the underlying issue before this page fires.
- ADR-0016 — per-region trust model.
- ADR-0017 — archive completeness invariants.
- `docs/operations/archival-node-bringup.md` §"Per-region trust + verification model"

## Changelog

- 2026-04-29 — initial draft alongside the L4.12 systemd-timer ship.
