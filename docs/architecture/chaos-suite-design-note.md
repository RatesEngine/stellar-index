---
title: Chaos suite — design note (Task #75)
last_verified: 2026-05-01
status: design ratified (Wave 1 shipped)
related:
  - test/chaos/README.md
  - docs/architecture/launch-readiness-backlog.md L5.5
  - docs/operations/sev-playbook.md §"Quarterly live chaos"
  - docs/architecture/k6-load-tests-design-note.md (companion: load suite)
---

# Chaos suite — design note

Forced-failure smoke for the Stellar Index stack, run as a deliberate
"break one component, assert sane behaviour" exercise. Companion to
the [k6 load suite](k6-load-tests-design-note.md) — load proves
"healthy stack stays within SLA," chaos proves "broken stack fails
in documented ways."

## Goal

One of the strongest guarantees the API makes is this:
**the API never silently serves bad data.** When a
backing service fails, the response either degrades-with-flag
(documented) or 5xxs loud (unmistakable). A 200-with-empty-`data`
or 200-with-stale-stamps is the nightmare. This suite is the
behavioural fence.

## Scope (Wave 1 — this PR)

In:

- 3 scenarios against the local docker-compose dev stack
  (`make dev`):
  - `01-redis-down.sh` — full Redis container stop. Exercises the
    rate-limit middleware's fail-open behaviour and the
    Postgres-fed read fallback.
  - `02-timescale-down.sh` — full Timescale stop. Exercises the
    "fail loud, never silent-empty" contract on `/v1/markets`.
  - `03-redis-network-partition.sh` — Redis container reachable but
    silent (network partition / pumba pause). Exercises go-redis's
    timeout path, distinct from connection-refused.
- Bash-based runner (`run.sh`) with production-safety guard.
- Shared `lib/common.sh` with logging, asserts, HTTP polling,
  docker / pumba helpers.
- Per-run markdown reports under `reports/` (gitignored).

Out (deferred to Wave 2):

- HA-shaped scenarios: Patroni replica promotion, Sentinel master
  failover, HAProxy keepalived VRRP VIP flip. These need the
  staging baremetal stack with `configs/ansible/`-deployed
  topology — the dev compose can't simulate them.
- Cross-region chaos (split-brain, cross-region clock skew).
- API pod mid-stream kill / reconnect-with-cursor — needs the SSE
  streaming surface (Task #74's load suite already touches the
  happy path, but failure-mode coverage is its own scenario).
- Aggregator tick stall + alert fire-time measurement. The
  `aggregator-silent` alert exists; this scenario would prove its
  fire-time is within SLA. Wave 2 because it requires Prometheus
  + AlertManager wired into the dev stack, which they currently
  aren't.

## Why bash, not Go

Go was the obvious first choice (the rest of the test surface is
Go). Three reasons it's not:

1. **Docker / pumba operations are shell-shaped.** A Go test that
   shells out to `docker stop` + `pumba pause` + `curl` is mostly
   `exec.Command` boilerplate around the actual chaos action.
   Direct bash is shorter and more honest.
2. **Each scenario stands alone.** Independent bash scripts mean
   each can be invoked directly during a SEV drill ("rerun the
   Redis-down scenario standalone"). A Go test runner couples them.
3. **Matches the load suite shape.** `test/load/scenarios/*.js`
   uses k6 (also non-Go). Both suites are external-tool harnesses
   driven by shell-friendly entry points; the symmetry is
   deliberate.

The `doc.go` placeholder makes the directory visible to `go doc`
and ADR-aware tooling without pulling Go code into the chaos
surface.

## Scenario matrix (Wave 1 + 2)

| ID | Scenario | Wave | Tooling | Pass criteria |
|---|---|---|---|---|
| 01 | Redis container stop | 1 | `docker stop` | API healthz 200/503; recovers in 30s |
| 02 | Timescale container stop | 1 | `docker stop` | API fails loud (5xx OR cached); recovers in 60s |
| 03 | Redis network partition | 1 | `pumba pause` / `docker network disconnect` | ≤ 1 transient failure across 30s |
| 04 | Postgres primary kill (HA) | 2 | ansible inventory + `pkill postgres` | Patroni promotes replica within 30 s |
| 05 | Redis Sentinel master kill | 2 | systemd kill on r1 | Sentinel quorum elects new master ≤ 10 s; clients reconnect ≤ 30 s |
| 06 | HAProxy + keepalived VIP flip | 2 | systemctl stop haproxy on owner | VIP migrates ≤ 5 s; in-flight requests retry-OK |
| 07 | Galexie / MinIO node failure | 2 | docker stop / systemctl stop | erasure-coded reads keep serving |
| 08 | API pod mid-stream kill | 2 | systemctl restart stellarindex-api | SSE clients reconnect with cursor ≤ 5 s |
| 09 | Aggregator tick stall | 2 | `kill -STOP $(pgrep stellarindex-aggregator)` | cached values serve until TTL; `aggregator-silent` fires within 5m |

## Production-safety guard

Every script (and the runner) refuses to execute when
`CHAOS_TARGET` matches `*production*`, `*api.stellarindex.io*`, or
`*prod.*`. The check is duplicated at the runner level AND inside
every scenario's prologue — defence in depth. Production chaos
runs out of the SEV playbook's quarterly drill, not this suite.

## Reporting

Per-run markdown under `reports/chaos-run-<UTC-timestamp>.md`. One
header block; one row per scenario. Format chosen so a human can
read it directly and so CI can grep for `❌` to detect failures
without parsing.

The reports directory is gitignored — runs are local artefacts,
not committed evidence. (Compare to the SLA-proof report in
[Task #77](../../test/load/README.md), which IS committed because
it's the contractual artefact for a release.)

## Effort breakdown

| Step | Estimate |
|---|---|
| `lib/common.sh` (helpers + asserts + reporting) | 2 h |
| `run.sh` (top-level runner + safety guard) | 1 h |
| `01-redis-down.sh` | 1 h |
| `02-timescale-down.sh` | 1 h |
| `03-redis-network-partition.sh` | 1.5 h |
| README + design note | 1.5 h |
| CHANGELOG + coverage matrix | 0.5 h |
| **Wave 1 total** | **~9 h, ~1 day** |

Wave 2's HA-shaped scenarios are gated on staging baremetal being
deployable from the ansible roles (Tasks #79 / #82 / #83 / #84
shipped that ansible surface; staging deploys are queued
post-launch).

## Open questions

1. **CI integration cadence.** Dev-stack chaos against CI's
   ephemeral docker every PR? Nightly only? On-demand via
   `workflow_dispatch`? Wave 1 ships as on-demand only — the
   docker-compose start-up cost (~30s) is too high for per-PR
   runs and the value-density per chaos run is high enough that
   nightly is enough.

2. **Should the chaos suite block a release?** No. The SLA-proof
   report (Task #77) is the per-release artefact; chaos is an
   ongoing readiness exercise. Failed chaos = file a ticket, fix
   before next release; doesn't block this one.

3. **pumba vs docker network disconnect parity.** They exercise
   subtly different go-redis branches. Wave 1 prefers pumba when
   available but accepts the docker fallback because installing
   pumba per CI runner is friction. Track whether the fallback
   ever masks a real go-redis regression; if so, make pumba a
   hard requirement.
