# Loki + Promtail configs — single-host R1

Pre-multi-region stop-gap, paired with the single-host Prometheus
under [`configs/prometheus/`](../prometheus/). The full HA Loki
topology lives at
[`configs/ansible/roles/loki`](../ansible/roles/loki/) and expects
multiple Loki + Promtail hosts. R1 alone runs a single instance
of each via system packages from the Grafana APT repo.

## Files

- `loki.r1.yml` — `/etc/loki/config.yml` on r1. Default packaged
  config; works as-is for single-host. HTTP listener on `:3100`.
- `promtail.r1.yml` — `/etc/promtail/config.yml` on r1. Tails
  the systemd journal, ships every unit's entries to local Loki
  with `host=r1` + `unit=<name>` + `job=<name-without-.service>`
  labels. Filter is `.+` (everything in the journal) — at R1's
  scale the storage cost is negligible and operators filter at
  query time in Grafana.

## Operator install (on a fresh R1-shaped box)

```sh
# Grafana APT repo
mkdir -p /etc/apt/keyrings
curl -fsSL https://apt.grafana.com/gpg.key | gpg --dearmor -o /etc/apt/keyrings/grafana.gpg
echo "deb [signed-by=/etc/apt/keyrings/grafana.gpg] https://apt.grafana.com stable main" \
  > /etc/apt/sources.list.d/grafana.list
apt-get update
apt-get install -y loki promtail

# Promtail needs systemd-journal access (default user is in
# nogroup, can't read /run/log/journal/). Grant it.
usermod -a -G systemd-journal promtail

# Promtail also needs to write a positions file under
# /var/lib/promtail; default packaging owner-mismatches.
chown -R promtail:nogroup /var/lib/promtail

# Drop our configs + restart
cp configs/loki/loki.r1.yml /etc/loki/config.yml
cp configs/loki/promtail.r1.yml /etc/promtail/config.yml
systemctl restart loki promtail

# Verify logs flowing (~15 sec for first scrape cycle)
sleep 15
curl -sS http://localhost:3100/loki/api/v1/label/job/values \
  | jq -r '.data | join(", ")'
```

Expected jobs: `caddy, galexie, loki, prometheus, prometheus-alertmanager,
prometheus-node-exporter, promtail, ratesengine-aggregator,
ratesengine-api, ratesengine-indexer, ssh, user@0`.

## Querying

Loki's HTTP API on `:3100`:

```sh
# Last 5 minutes of indexer logs containing the word "error"
curl -sS 'http://localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={job="ratesengine-indexer"} |= "error"' \
  --data-urlencode 'start='$(date -d '5 minutes ago' +%s)'000000000' \
  --data-urlencode 'end='$(date +%s)'000000000' \
  --data-urlencode 'limit=20'
```

Or via Grafana's Explore tab pointing at `http://localhost:3100`
as the Loki datasource (Grafana itself isn't deployed yet —
post-launch follow-up).

## Web UI

R1's Loki HTTP API is on `:3100`, listening on `0.0.0.0`. No
firewall (per r1-deployment-state §"Important but not urgent" #3),
so it's publicly reachable. Once Caddy fronts it (post-launch
follow-up), it'll be HTTPS-only via `loki.ratesengine.net` etc.

For now operator access:
- `ssh -L 3100:localhost:3100 root@136.243.90.96`
- Then point a local Grafana at `http://localhost:3100`.

## Why a single-host stop-gap

- Without log aggregation, `journalctl` on r1 is the only way to
  search logs. Cross-process queries ("show me every error in
  the last hour across indexer + aggregator + api") require
  three terminal panes instead of one Grafana query.
- The full HA role (`configs/ansible/roles/loki`) requires R2
  before it can be applied; R2 itself is blocked on L4.14
  multi-region work.
- Single-host gets us **all journald entries queryable with
  Grafana-Explore-shaped LogQL** today. The HA upgrade is a
  config-replacement.

## Migration to the HA role

When R2 lands:
1. Stop the system-package services on r1: `systemctl stop loki promtail`.
2. Apply the ansible role with `loki_hosts = [r1, r2]` —
   the role installs upstream Loki binaries to a different
   path, so no conflict.
3. Delete `/etc/loki/` and `/etc/promtail/` system-package files.
4. Re-route Caddy / DNS to the new endpoints.
