---
title: SEV-2 tabletop — Redis Sentinel master failover under live traffic
last_verified: 2026-05-03
status: ratified
severity: P2
exercises_runbook: ../../runbooks/redis-fanout-broken.md
playbook_section: ../../sev-playbook.md#4-response-flow
---

# SEV-2 tabletop — Redis Sentinel master failover under live traffic

Scripted scenario for the monthly tabletop drill. ~30 min for
3 people. Exercises the Redis HA path (ADR-0024,
`configs/ansible/roles/redis-sentinel`) end-to-end across the
endpoints that depend on Redis: `/v1/price` (closed-bucket VWAP
cache + freeze markers + confidence + triangulation), `/v1/account/*`
(API-key validator), `/v1/assets/{id}/metadata` (SEP-1 cache).

This is the canonical drill for the Redis-dependent surface —
unlike Timescale (where loss is service-down), Redis loss
**degrades** rather than kills. Most of the validation criteria
test whether the team correctly distinguishes degraded-vs-down.

## Initial conditions

Read aloud at drill setup.

- All Rates Engine services up. SLA probe metrics within target.
  Redis Sentinel cluster (`cache-01` master, `cache-02` +
  `cache-03` replicas) all reachable; `redis_exporter` shows
  `up=1` on all three.
- It is **17:45 UTC, Wednesday**. Steady-state traffic — peak
  US trading hours. ~1.2 k requests/min mostly on `/v1/price`.
- Oncall is `<participant 1>`. Backup is `<participant 2>`.
  Engineering manager is in another meeting; reachable on Slack.

## Trigger event

Read aloud at drill T+0.

> At 17:46 UTC, `cache-01` (the current Redis master) becomes
> unresponsive — say, an OOM-killer hit it after a rogue
> SCAN-with-MATCH from a manual debug session blew its memory
> budget. Sentinel detects within 30 s and begins promoting
> `cache-02`. The promotion takes ~5 s; during that window
> every API replica's connection to Redis times out.
>
> First user-visible signal: `/v1/price` p99 spikes from 80 ms
> to 1.8 s for ~10 s as connections re-establish. `flags.frozen`
> stops firing on degraded pairs (the `freeze:<asset>:<quote>`
> markers can't be read until the new master accepts traffic).
> `flags.divergence_warning` likewise pauses.

## Injection timeline

Drill leader reads each beat in order; pauses after each for
participants to narrate.

| T+ | Beat |
| --- | --- |
| 0:00 | `redis_master_unreachable` fires (Sentinel can't reach `cache-01` for 10 s). |
| 0:30 | `redis_failover_in_progress` fires (Sentinel promoting `cache-02`). API-side connection-pool errors spike in `ratesengine_ratelimit_fail_open_total`. |
| 1:00 | `cache-02` accepts writes; replicas re-attach. `flags.frozen` paths re-enable as the cache catches up. |
| 2:00 | A customer (Freighter) DMs: "we got 503s on a few `/v1/price` calls, are you OK?" |
| 5:00 | API metrics return to baseline, BUT `flags.frozen` is firing on a pair that wasn't frozen pre-failover — was the marker stale data? Or did the aggregator legitimately freeze it during the outage? |
| 10:00 | `cache-01` recovers (operator restart) and rejoins as a replica. Sentinel does NOT fail back automatically (per ADR-0024). |
| 20:00 | A second customer asks via Discord whether their stored API keys are affected (they're not — keys live in Redis but the validator's cache is read-through, not write-through; the master swap doesn't lose any record). |

## Expected response per the playbook

Drill leader compares participant narratives against this
sequence.

### Within 5 minutes (per [§2 Timelines](../../sev-playbook.md#2-timelines-the-contractual-promises))

- Oncall acknowledges PagerDuty page.
- Oncall opens `#incident-<YYYY-MM-DD>-redis-failover` channel.
- Initial post: "We're seeing brief 503s on `/v1/price` from a
  Redis cache failover; recovering automatically."
- Status page set to *Degraded performance* on **Pricing API**
  (NOT *Major outage* — the API is still serving, just briefly
  noisier).

### Within 15 minutes — diagnose

- Confirm via `/v1/readyz` that the `redis` check is now back
  to `status: ok` after a brief flap.
- Confirm via `redis-cli -p 26379 -a "$REDIS_PASSWORD" SENTINEL get-master-addr-by-name ratesengine-r1-cache`
  that the new master is correctly promoted. (F-1273,
  2026-05-13: the `-a "$REDIS_PASSWORD"` is required since wave 106
  added `requirepass` to the Sentinel listener; earlier versions
  of this drill omitted both the password and the master-name
  arg.)
- Identify root cause via `cache-01` host logs + `redis-server`
  stderr — likely OOM-kill on the rogue debug session.
- Decide whether to **fail back to cache-01** (no — let it stay
  a replica per ADR-0024) or **leave cache-02 as the new
  primary**.

### Within 30 minutes — verify side-effects

- `/v1/price` 5xx rate back to baseline (≤ 0.1%).
- `/v1/account/me` still serves correctly (validator hit Redis
  for the lookup; new master serves the same records).
- `flags.frozen` markers re-populated for any pair that the
  aggregator actively re-flagged during the outage. Spot-check
  one against `redis-cli GET freeze:<asset>:<quote>`.
- Run the canary: `curl -sS https://api.ratesengine.net/v1/price?asset=native | jq '.flags'`.

### Within 1 hour — communicate

- Status page transitions *Degraded performance* → *Operational*
  with a "monitoring" note.
- Customer-facing post in the operator channel summarising:
  "10s of brief 5xx during a planned-into-design Redis failover.
  No data loss. No customer action required."

### Within 24 hours — postmortem

Postmortem doc per [§6](../../sev-playbook.md#6-after-the-incident).
Action items filed with owners.

## Validation criteria

Score `pass` / `partial` / `fail`. Aim for ≥ 80% pass.

| # | Criterion |
| --- | --- |
| 1 | Did oncall correctly classify this as **SEV-2** (degraded) not SEV-1 (down)? |
| 2 | Did anyone confirm via `/v1/readyz` (not just metrics) that the failover completed? |
| 3 | Did the team correctly identify that `flags.frozen` resuming firing post-failover is **expected behaviour**, not a regression? |
| 4 | Did anyone verify the API-key validator path still served (Redis is the validator's source of truth, but reads survive a master swap)? |
| 5 | Did the team check `redis-cli ... SENTINEL get-master-addr-by-name` rather than just trusting the dashboard? |
| 6 | Did the team explicitly NOT fail back to `cache-01` per ADR-0024's "let Sentinel pick" rule, even though the engineer's instinct is to "restore the original topology"? |
| 7 | Status-page severity correct (Degraded performance, not Major outage)? |
| 8 | Customer-comms post avoided alarmist language? |

## Common gaps surfaced (from prior simulations)

- **Oncall over-classifies as SEV-1.** Brief 503s feel like a
  full outage. The runbook needs to lead with the
  classification rule: "if `/v1/readyz` recovers within 60 s
  and 5xx rate returns to baseline within 5 min, this is SEV-2."
  Action item template: "Add classification flowchart to
  redis-fanout-broken.md."

- **Team waits for `cache-01` to fail back.** ADR-0024 says
  Sentinel-driven failover is one-way until manual ops decide
  otherwise. Action item template: "Reinforce ADR-0024 §
  'fail-forward only' in the runbook + roleplay."

- **`flags.frozen` reading post-failover.** If the freeze
  marker TTL was longer than the failover window, the new
  master will serve a stale marker. The aggregator's next tick
  will re-evaluate and either renew or clear the marker.
  Operators need to know this so they don't react to a "frozen"
  flag during the recovery window. Action item template:
  "Document expected post-failover marker behaviour in
  ADR-0019 ⚠ Failover."

## Variant scenarios

- **Both replicas down variant.** Cluster has only the master
  + a single replica, master fails — Sentinel can't get quorum.
  Tests the `redis_quorum_lost` alert + the manual recovery
  path. Promotes to SEV-1 mid-drill.
- **Sentinel split-brain variant.** Network partition between
  the two Sentinel hosts. Tests the runbook's split-brain
  resolution + ADR-0024 §"three-node minimum" justification.

## Pairs with

- [SEV-1 Timescale primary failover](sev1-timescale-primary-failover.md)
  — different stateful tier; back-to-back drill exercises both
  HA paths in 90 min.
