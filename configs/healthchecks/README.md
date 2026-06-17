# Healthchecks.io heartbeats + API smoke

Closes the gap noted in the launch-readiness backlog:

> Healthchecks.io covers galexie/minio/postgres only;
> indexer/aggregator/api not watched.

Two complementary timer flavours:

### 1. Per-binary heartbeats — 60 s cadence

Three systemd `.timer` instantiations of a single template service
each ping a Healthchecks.io URL after verifying the corresponding
metrics endpoint responds:

| Binary | Probe target | Failure semantics |
|--------|--------------|-------------------|
| indexer    | `localhost:9464/metrics` | curl exit ≠ 0 → `${URL}/fail` |
| aggregator | `localhost:9465/metrics` | curl exit ≠ 0 → `${URL}/fail` |
| api        | `localhost:3000/metrics` | curl exit ≠ 0 → `${URL}/fail` |

A successful probe POSTs `stellarindex-<svc> ok :<port>`.

### 2. API surface smoke test — 5 min cadence

`stellarindex-smoke.timer` runs `scripts/dev/r1-smoke.sh` —
13 GETs covering health / catalogue / pricing / diagnostics —
and pings `HEALTHCHECKS_URL_SMOKE` with the full smoke output as
the ping body. Catches schema regressions that the metrics-port
probes can't see (e.g. `/v1/price` returning 200 with malformed
JSON, an OpenAPI-spec change that breaks downstream clients).
A failed run pings `${URL}/fail` instead.

### 3. SLA probe — 15 min cadence

`stellarindex-sla-probe.timer` runs `stellarindex-sla-probe`
against the local API for ~30 s and asserts the latency +
freshness SLAs (p95 ≤ 200 ms, p99 ≤ 500 ms, freshness ≤ 30 s).
Pass → ping `HEALTHCHECKS_URL_SLA_PROBE`; fail → ping `${URL}/fail`.
The full JSON report rides as the ping body so operators can
read the per-endpoint percentile breakdown straight from the
Healthchecks dashboard.

Default tuning (override via `/etc/default/stellarindex-healthchecks`):
- `SLA_PROBE_BASE_URL=http://localhost:3000/v1`
- `SLA_PROBE_DURATION=30s`
- `SLA_PROBE_CONCURRENCY=2`
- `SLA_PROBE_PAIR=native,fiat:USD`

## Architecture

This complements the existing alerting layer rather than duplicating
it:

- `configs/prometheus/rules.r1/*.yml` defines per-service alerts.
- `configs/alertmanager/alertmanager.r1.yml` routes the
  `stellarindex_deadmansswitch` to a Healthchecks.io URL — covers
  "Prometheus or Alertmanager itself is broken." But that single
  watchdog can't tell you *which* service died.
- These per-binary timers fire independently, so an indexer crash
  shows up on healthchecks.io within ~2 min even if Prometheus
  is still scraping fine.

## Install on R1

### Production: Ansible role (idempotent)

```sh
ansible-playbook -i configs/ansible/inventory/r1.yml \
  configs/ansible/playbooks/archival-node.yml \
  --tags healthchecks
```

The role at `configs/ansible/roles/archival-node/tasks/17-stellarindex-healthchecks.yml`
copies the wrapper scripts + systemd units to r1, provisions the
env-file placeholder (only if missing — operator URLs are
preserved across applies), enables the timers, and notifies a
per-group restart handler so a change to `smoke.sh` only
restarts `stellarindex-smoke.timer` (not the heartbeat or
sla-probe timers). Closes the drift gap F-0137 caught in the
2026-05-26 audit, where every edit under this directory needed a
manual `install.sh` re-run.

### Ad-hoc: manual installer (new host without inventory yet)

```sh
# From a machine with the repo checked out:
scp -r configs/healthchecks/ root@136.243.90.96:/tmp/
ssh root@136.243.90.96 'bash /tmp/healthchecks/install.sh'
```

Same outcome on a single host, but no drift tracking — only use
this when the host isn't yet in Ansible inventory.

Then on healthchecks.io, create **five Checks** (F-1267,
2026-05-13 — was four before the SLA-probe timer joined the
heartbeat fleet) and paste their ping URLs into
`/etc/default/stellarindex-healthchecks`:

```sh
HEALTHCHECKS_URL_INDEXER='https://hc-ping.com/<uuid-indexer>'
HEALTHCHECKS_URL_AGGREGATOR='https://hc-ping.com/<uuid-aggregator>'
HEALTHCHECKS_URL_API='https://hc-ping.com/<uuid-api>'
HEALTHCHECKS_URL_SMOKE='https://hc-ping.com/<uuid-smoke>'
HEALTHCHECKS_URL_SLA_PROBE='https://hc-ping.com/<uuid-sla-probe>'
```

That's 3 binary heartbeats (indexer / aggregator / api) +
1 smoke timer + 1 SLA-probe timer = 5 total.

Then `systemctl restart stellarindex-heartbeat@*.timer stellarindex-smoke.timer stellarindex-sla-probe.timer`.

Suggested dashboard schedules:
- per-binary heartbeats: period 60 s, grace 120 s
- API smoke: period 5 min, grace 10 min
- SLA probe: period 15 min, grace 30 min

## Verify

```sh
systemctl list-timers 'stellarindex-heartbeat@*'
journalctl -u 'stellarindex-heartbeat@*.service' -n 30 --no-pager
```

Successful runs log nothing (Type=oneshot exits 0 silently); a
probe failure prints `heartbeat: <svc> probe FAILED on :<port>` to
stderr → journalctl. Empty URLs leave the metrics-endpoint check
running for journal coverage even before healthchecks.io is wired.
