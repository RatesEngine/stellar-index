---
title: Chaos Wave 1 — launch-cut RETRO
date: 2026-05-03
operator: ash
target: http://localhost:8080 (make-dev stack)
build: 0fe10a3-dirty (post-#540)
---

# Chaos Wave 1 — launch-cut retro

Closes L5.5 / Task #75. Three scenarios; all three passed on first
run; no real bugs surfaced.

## Run summary

| Scenario | Outcome | Duration | Notes |
|---|---|---|---|
| 01-redis-down | ✅ pass | 2s | API healthz 200 while Redis down; recovered within 30s of restart |
| 02-timescale-down | ✅ pass | 9s | healthz 200, /v1/markets 500 (documented), recovered within 60s |
| 03-redis-network-partition | ✅ pass | 31s | 0/10 samples failed during partition; reconnected + recovered |

Reports: this directory's `chaos-run-20260503-1810*.md`.

## What broke

Nothing. Every documented graceful-degradation contract held:

- Redis-down → rate-limit middleware fail-open + healthz 200.
- Timescale-down → API stays up, `/v1/markets` correctly 500s
  (NOT a 5xx envelope leak), recovers cleanly when storage
  returns.
- Redis network partition → 30 s of partitioned operation, zero
  user-visible failures across 10 sampled requests, recovered
  cleanly on reconnect.

## Surprises

None. The stack behaved exactly as the per-runbook contracts
promise.

## Action items

None — no code changes motivated by this run. The pre-flight
sequence in `chaos-wave1-runbook.md` is accurate; the production-
safety guard refused to point at any prod-shaped target as
designed.

## Sign-off

- **Wave 1 closure criterion** (per runbook): *"all three
  scenarios passed, the retro is empty of 'we found a real
  bug', and the reports directory is committed."* → met.
- L5.5 in `launch-readiness-backlog.md` flipped 🟢 → ✅ in the
  same PR as this RETRO.

## Wave 2 deferral (out-of-scope)

HA-shaped scenarios (Patroni failover, Sentinel quorum loss,
region cutover) require a multi-region staging baremetal
deployment that won't exist until R2/R3 are provisioned (L4.14
+ L4.15). Wave 2 stays post-launch per the launch-readiness
backlog row, fed into the L5.8 region-failover chaos test once
the multi-region topology is up.
