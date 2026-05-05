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

A successful probe POSTs `ratesengine-<svc> ok :<port>`.

### 2. API surface smoke test — 5 min cadence

`ratesengine-smoke.timer` runs `scripts/dev/r1-smoke.sh` —
13 GETs covering health / catalogue / pricing / diagnostics —
and pings `HEALTHCHECKS_URL_SMOKE` with the full smoke output as
the ping body. Catches schema regressions that the metrics-port
probes can't see (e.g. `/v1/price` returning 200 with malformed
JSON, an OpenAPI-spec change that breaks downstream clients).
A failed run pings `${URL}/fail` instead.

### 3. SLA probe — 15 min cadence

`ratesengine-sla-probe.timer` runs `ratesengine-sla-probe`
against the local API for ~30 s and asserts the RFP latency +
freshness SLAs (p95 ≤ 200 ms, p99 ≤ 500 ms, freshness ≤ 30 s).
Pass → ping `HEALTHCHECKS_URL_SLA_PROBE`; fail → ping `${URL}/fail`.
The full JSON report rides as the ping body so operators can
read the per-endpoint percentile breakdown straight from the
Healthchecks dashboard.

Default tuning (override via `/etc/default/ratesengine-healthchecks`):
- `SLA_PROBE_BASE_URL=http://localhost:3000/v1`
- `SLA_PROBE_DURATION=30s`
- `SLA_PROBE_CONCURRENCY=2`
- `SLA_PROBE_PAIR=native,fiat:USD`

## Architecture

This complements the existing alerting layer rather than duplicating
it:

- `configs/prometheus/rules.r1/*.yml` defines per-service alerts.
- `configs/alertmanager/alertmanager.r1.yml` routes the
  `ratesengine_deadmansswitch` to a Healthchecks.io URL — covers
  "Prometheus or Alertmanager itself is broken." But that single
  watchdog can't tell you *which* service died.
- These per-binary timers fire independently, so an indexer crash
  shows up on healthchecks.io within ~2 min even if Prometheus
  is still scraping fine.

## Install on R1

```sh
# From a machine with the repo checked out:
scp -r configs/healthchecks/ root@136.243.90.96:/tmp/
ssh root@136.243.90.96 'bash /tmp/healthchecks/install.sh'
```

Then on healthchecks.io, create four Checks and paste their ping
URLs into `/etc/default/ratesengine-healthchecks`:

```sh
HEALTHCHECKS_URL_INDEXER='https://hc-ping.com/<uuid-indexer>'
HEALTHCHECKS_URL_AGGREGATOR='https://hc-ping.com/<uuid-aggregator>'
HEALTHCHECKS_URL_API='https://hc-ping.com/<uuid-api>'
HEALTHCHECKS_URL_SMOKE='https://hc-ping.com/<uuid-smoke>'
HEALTHCHECKS_URL_SLA_PROBE='https://hc-ping.com/<uuid-sla-probe>'
```

Then `systemctl restart ratesengine-heartbeat@*.timer ratesengine-smoke.timer ratesengine-sla-probe.timer`.

Suggested dashboard schedules:
- per-binary heartbeats: period 60 s, grace 120 s
- API smoke: period 5 min, grace 10 min
- SLA probe: period 15 min, grace 30 min

## Verify

```sh
systemctl list-timers 'ratesengine-heartbeat@*'
journalctl -u 'ratesengine-heartbeat@*.service' -n 30 --no-pager
```

Successful runs log nothing (Type=oneshot exits 0 silently); a
probe failure prints `heartbeat: <svc> probe FAILED on :<port>` to
stderr → journalctl. Empty URLs leave the metrics-endpoint check
running for journal coverage even before healthchecks.io is wired.
