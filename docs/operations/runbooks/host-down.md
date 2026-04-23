---
title: Runbook — host-down
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_host_down`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_host_down` |
| Severity | P2 (ticket — but can auto-escalate if it takes a service down) |
| Detected by | `deploy/monitoring/rules/infra.yml` |
| Typical MTTR | 15 min – hours (depends on remote-hands availability) |
| Impact | `node_exporter` hasn't scraped for 2+ min. Either the host is genuinely down — in which case every service on that host is also down, and you'll see cascading alerts — or just the exporter is broken. |

## Symptoms

- `up{job="node", instance=<host>} == 0` for ≥ 2 min.
- Cascading service alerts on services pinned to that host (every
  API pod on it, any Redis / Postgres member on it).
- If this is the *only* host-level alert firing, it's probably
  the exporter (not the box).

## Quick diagnosis (≤ 5 min)

```sh
# Can we reach the host at all?
ping -c 3 <host>
ssh <host> uptime   # or your bastion equivalent

# From inside (if you can SSH):
systemctl status node_exporter
journalctl -u node_exporter -n 50

# If SSH fails, try the BMC / IPMI / colo-provider console.
# If it fails there too, it's almost certainly power / network.
```

## Typical root causes

1. **node_exporter died but host is fine.** Exporter unit
   crashed; everything else keeps humming. Check with ssh +
   systemctl.

2. **Full-host reboot** — planned or unplanned. Check uptime; if
   `uptime` says < 5 min, that's the answer.

3. **Network-level isolation** — switch failure, NIC gone, BGP
   session dropped. The host is alive but we can't talk to it.
   From the host's local console you should be able to ping its
   default gateway.

4. **Hardware failure** — PSU, disk controller, mainboard. The
   IPMI console will usually show something (POST errors, bad
   DIMM, failed RAID).

5. **Kernel panic / hang.** IPMI console shows panic stack; SSH
   fails; pings fail. Reboot via IPMI is the only path back.

## Mitigation

- [ ] Step 1 — decide if it's the exporter or the host (above).
- [ ] Step 2 — if just the exporter: `systemctl restart
      node_exporter` and watch for it to re-scrape.
- [ ] Step 3 — if the host is down but services on it have
      already failed over (API pods rescheduled, Postgres
      replica promoted): the urgency drops to "repair the box
      at a civilised hour".
- [ ] Step 4 — if a service-level alert hasn't cleared because the
      missing host was hosting something critical (primary DB,
      only validator): pivot immediately to the service's
      runbook (`timescale-primary-down.md` etc.) — that's the
      real fire.
- [ ] Step 5 — cordon/taint the node in k8s so the scheduler
      doesn't keep retrying it: `kubectl cordon <node>`.
- [ ] Verification: `up{job="node",instance=<host>}` returns to 1;
      any orphaned pods rescheduled or the host's back online.

## Root cause analysis

- IPMI / console logs from the incident window.
- Syslog from neighbour hosts / upstream switch (did they see the
  same link go down?).
- Smartctl logs on the host's drives when it's back — was it a
  disk-failure cascade?

## Known false-positive patterns

- **node_exporter OOMed on a starved box.** If the host is thrashing
  (see `host-memory-high.md`), node_exporter is often the first to
  get killed. The alert is technically accurate ("we lost visibility")
  but the underlying problem is memory, not host liveness.
- **Prometheus scrape-path issue** — DNS, Consul/K8s service
  discovery, firewall rule change. `up == 0` for the whole
  `job="node"` across multiple instances simultaneously points
  at the scraper, not the targets.

## Related

- `host-cpu-high.md` / `host-memory-high.md` — resource-side
  precursors to a full host down.
- `scrape-failing.md` — when it's actually the scrape path.
- Per-service runbooks for whatever was on this host.

## Changelog

- 2026-04-23 — initial draft.
