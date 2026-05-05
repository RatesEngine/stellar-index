# Per-binary Healthchecks.io heartbeats

Closes the gap noted in the launch-readiness backlog:

> Healthchecks.io covers galexie/minio/postgres only;
> indexer/aggregator/api not watched.

Three systemd `.timer` instantiations of a single template service
each ping a separate Healthchecks.io URL on a 60 s cadence after
verifying the corresponding metrics endpoint responds:

| Binary | Probe target | Failure semantics |
|--------|--------------|-------------------|
| indexer    | `localhost:9464/metrics` | curl exit ≠ 0 → `${URL}/fail` |
| aggregator | `localhost:9465/metrics` | curl exit ≠ 0 → `${URL}/fail` |
| api        | `localhost:3000/metrics` | curl exit ≠ 0 → `${URL}/fail` |

A successful probe POSTs `ratesengine-<svc> ok :<port>` to the
ping URL so the dashboard's "last ping" entry is useful at a
glance.

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

Then on healthchecks.io, create three Checks (one per binary) and
paste their ping URLs into `/etc/default/ratesengine-healthchecks`:

```sh
HEALTHCHECKS_URL_INDEXER='https://hc-ping.com/<uuid-indexer>'
HEALTHCHECKS_URL_AGGREGATOR='https://hc-ping.com/<uuid-aggregator>'
HEALTHCHECKS_URL_API='https://hc-ping.com/<uuid-api>'
```

Then `systemctl restart ratesengine-heartbeat@*.timer`. Suggested
dashboard-side schedule: period 60 s, grace 120 s.

## Verify

```sh
systemctl list-timers 'ratesengine-heartbeat@*'
journalctl -u 'ratesengine-heartbeat@*.service' -n 30 --no-pager
```

Successful runs log nothing (Type=oneshot exits 0 silently); a
probe failure prints `heartbeat: <svc> probe FAILED on :<port>` to
stderr → journalctl. Empty URLs leave the metrics-endpoint check
running for journal coverage even before healthchecks.io is wired.
