---
title: Runbook — deadmansswitch
last_verified: 2026-04-23
status: draft
severity: P1
---

# Runbook — `ratesengine_deadmansswitch`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_deadmansswitch` |
| Semantics | **Inverted** — this alert fires constantly by design; you page when it **stops** firing. |
| Severity | P1 when it stops (escalated via the external watchdog) |
| Detected by | `deploy/monitoring/rules/meta.yml` |
| Typical MTTR | Whatever it takes to bring Prometheus or AlertManager back up — minutes to an hour |
| Impact | If this stops firing, we've lost our primary alerting pipeline. Every other alert is invisible until it's restored — we could be in an outage and not know. |

## How it works

- `expr: vector(1)` — always truthy, always firing.
- Routed to an **external watchdog** (healthchecks.io or similar)
  via AlertManager. That watchdog expects a heartbeat every minute.
- If the watchdog stops receiving → **it pages us** on a separate
  channel (SMS, different email, a different oncall tool — whatever
  our secondary is).

**The "alert" you're looking at here (in Prometheus) is the
*positive* case.** It's firing means the pipeline is healthy. The
page we respond to is the watchdog's silence, not the alert itself.

## Symptoms (when it fails)

- You got paged by the secondary channel: "deadmansswitch
  heartbeat missed".
- Prometheus/AlertManager dashboards may be green or offline from
  your POV — both are possible.
- Primary oncall tool is silent (paradoxically reassuring — that's
  what we were testing).

## Quick diagnosis (≤ 5 min)

```sh
# Can we reach Prometheus?
curl -s http://prometheus:9090/-/healthy

# Can we reach AlertManager?
curl -s http://alertmanager:9093/-/healthy

# From the watchdog's POV, when did it last hear from us?
# (Go to healthchecks.io / whatever provider's dashboard.)

# Is the deadmansswitch route still configured?
amtool --alertmanager.url=http://alertmanager:9093 config routes show
```

## Typical root causes

1. **Prometheus is down / unreachable.** Can't evaluate the
   `vector(1)` expression, can't fire the alert.

2. **AlertManager is down / unreachable.** Can't route it to the
   watchdog.

3. **Network path to the watchdog broken.** Prometheus fires →
   AlertManager routes → outbound HTTP fails (DNS, proxy, TLS).

4. **Someone silenced the alert** in AlertManager. Deadmansswitch
   should never be silenced; if it is, that's a config mistake.

5. **The `ratesengine.meta` rule group is disabled** (misconfig
   or rule-loading error). `alertmanager_config_last_reload_successful`
   / `prometheus_rule_group_iterations_total` will tell you.

## Mitigation

- [ ] Step 1 — find which component is down (above).
- [ ] Step 2 — restore it. This runbook defers to the per-component
      ones (`api-down.md` is for our API, but for Prometheus/AM
      itself follow their operator/chart's runbook — usually just
      a pod restart).
- [ ] Step 3 — confirm the watchdog starts receiving heartbeats
      again (watch the provider dashboard).
- [ ] Step 4 — do NOT ack the secondary page until you've
      verified the primary channel is functional end-to-end. Send
      a test alert through AlertManager if in doubt.
- [ ] Verification: watchdog's "last ping" shows within the last
      minute; other alerts can route through AlertManager; a
      test alert fires and clears cleanly.

## Known false-positive patterns

- **Watchdog provider outage.** If healthchecks.io is down, it
  can't hear us. Cross-check from an independent network (your
  phone) that the provider is up.
- **Network egress filter** — a firewall change that blocks
  outbound to the watchdog domain will silence us without
  anything being wrong on our side. Whitelist the hostname
  explicitly.

## Related

- `alertmanager-bad-config.md` — common root cause.
- `scrape-failing.md` — when Prometheus loses its targets.
- The watchdog provider's own status page (bookmark it).

## Changelog

- 2026-04-23 — initial draft. Emphasises the inverted semantics —
  this is the "test that our tests work" alert.
