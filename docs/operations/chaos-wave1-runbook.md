---
title: Chaos suite Wave 1 — dev-stack execution (L5.5)
last_verified: 2026-05-03
status: operator runbook
---

# Chaos suite Wave 1 — dev-stack execution

Operator runbook for closing **L5.5 / Task #75** in the
launch-readiness backlog. The suite **code** is shipped under
`test/chaos/`; this doc covers the **execution + recording** the
launch-readiness row asks for.

## What Wave 1 covers

Wave 1 is dev-stack smoke. Goal: every documented graceful-
degradation path actually graceful-degrades. Three scenarios:

| Scenario | What it kills | Expected behaviour | Runbook the scenario validates |
| --- | --- | --- | --- |
| `01-redis-down` | Redis container | API serves `/v1/healthz` 200 + `/v1/price/*` returns 200 or documented 503 (rate-limit middleware fails open; VWAP path falls through to Postgres) | [`runbooks/redis-master-down.md`](runbooks/redis-master-down.md) |
| `02-timescale-down` | Timescale container | API readiness flips, `/v1/price` returns 503 with structured envelope (no 5xx leak); recovery within 30s of Timescale restart | [`runbooks/database-down.md`](runbooks/database-down.md) — if missing, see decode-errors.md |
| `03-redis-network-partition` | iptables-drops Redis from API host | Same as `01-redis-down` but without Redis fully down — rate-limit fail-open + cache-bypass | Same as 01 |

**Wave 2** (HA-shaped scenarios on staging baremetal — Patroni
failover, Sentinel quorum loss, region-cutover) is post-launch
per L5.5's row.

## Pre-flight

```sh
# 1. Bring the dev stack up
make dev
# (waits for "all services healthy" — typically ~90s)

# 2. Smoke the API
curl -sf http://localhost:8080/v1/healthz | jq .
# Expect: {"status":"ok", ...}

# 3. Smoke a price (so the cache + Postgres both have data
#    to fall through to)
curl -sf "http://localhost:8080/v1/price?base=native&quote=fiat:USD" | jq .
# Expect: an Envelope with a non-zero price.
```

If pre-flight fails, fix before starting chaos — a chaos run on a
broken stack is noise.

## Running the suite

```sh
# All three scenarios in order:
./test/chaos/run.sh

# A subset:
./test/chaos/run.sh 01 03

# Custom target (e.g. staging, NOT production):
CHAOS_TARGET=http://staging.ratesengine.net:8080 ./test/chaos/run.sh
```

The runner refuses to fire against `*.ratesengine.net` production
hosts by design — see `run.sh` head.

Per-scenario output streams to stdout and is also captured in
`test/chaos/reports/<UTC-timestamp>/<scenario>.log`. Final
summary table prints to stdout when the runner exits.

## Recording the run (the launch-blocking artefact)

The launch-readiness backlog wants **a recorded execution**, not
just code. Per run, capture:

1. **The reports directory.** `test/chaos/reports/<timestamp>/`
   contains per-scenario logs.
2. **The pass/fail summary table.** Save the runner's final
   stdout block.
3. **A short retro.** Append to
   `test/chaos/reports/<timestamp>/RETRO.md` with:
   - What broke that the runbook didn't cover.
   - What graceful-degradation behaviour surprised us.
   - Any code changes the run motivated (link PR numbers).

A "successful" Wave 1 closure means: all three scenarios passed,
the retro is empty of "we found a real bug", and the reports
directory is committed (or its summary captured in the
launch-readiness sign-off).

## Pass criteria

For each scenario:

- **Pass** — exit 0 from the scenario script. The fail-condition
  comments at the top of each `scenarios/0X-*.sh` define the
  bar; the script asserts each one.
- **Fail (real)** — exit 1, scenario script asserts a
  documented behaviour didn't happen. Open a ticket against the
  matching runbook + the affected code path.
- **Fail (flaky)** — exit 1 but re-runs cleanly. Flag in the
  retro; usually a docker-compose timing issue. Fix the
  scenario's wait-for-stable loop, not the production code.

## When something breaks

If the run finds a real bug:

1. Stop the run (`Ctrl-C` is safe — scenarios clean up after
   themselves via `trap`).
2. Capture logs from the affected service:
   ```sh
   docker compose -f deploy/docker-compose/dev.yml logs <service> > /tmp/<service>.log
   ```
3. File the bug under the matching runbook's "open issues"
   section; link the chaos-report directory.
4. Re-run after the fix lands.

The point of Wave 1 isn't to find new bugs — the existing
runbooks claim a graceful-degradation contract; chaos
double-checks the claim. Finding a discrepancy means the
runbook needs updating OR the code does.

## Verification (pre-launch checklist)

- [ ] Wave 1 ran cleanly on the dev stack at least once during
      the launch sprint.
- [ ] `test/chaos/reports/<launch-cut-timestamp>/` directory
      exists and contains 3 passing logs + RETRO.md.
- [ ] Any code-side fixes found during the run have landed +
      reports directory updated to "all green".
- [ ] L5.5 in [launch-readiness-backlog.md](../architecture/launch-readiness-backlog.md)
      flipped from 🟢 → ✅ once the above checks all pass.

## Cross-references

- Suite code: [`test/chaos/`](../../test/chaos/)
- Backlog row: L5.5 in [launch-readiness-backlog.md](../architecture/launch-readiness-backlog.md)
- Design intent: [`chaos-suite-design-note.md`](../architecture/chaos-suite-design-note.md)
- SEV escalation (what to do when chaos finds a real bug mid-launch):
  [sev-playbook.md](sev-playbook.md)
