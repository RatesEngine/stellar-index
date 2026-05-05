# Prometheus configs — single-host R1

R1 is the canary deployment for the Rates Engine production stack
through pre-launch. The full HA Prometheus topology lives at
[`configs/ansible/roles/prometheus`](../ansible/roles/prometheus/)
and expects two Prometheus + AlertManager hosts (`prom-01` and
`prom-02`); R1 alone can't satisfy that.

This directory holds the **single-host** configs that are running
on R1 as of 2026-05-05 (paired with the system-package install of
`prometheus` + `prometheus-alertmanager`). When R2 / R3 land, switch
to the ansible role topology.

## Files

- `prometheus.r1.yml` — `/etc/prometheus/prometheus.yml` on r1.
  Scrapes node_exporter + ratesengine-{indexer,aggregator,api} +
  caddy + galexie. Sends alerts to local Alertmanager on `:9093`.

## Operator install (on a fresh R1-shaped box)

```sh
apt-get install -y prometheus prometheus-alertmanager

# Single-host: disable cluster mode (Alertmanager refuses to
# start otherwise on a host with no RFC1918 IP — like a Hetzner
# bare-metal box on a pure public IP).
sed -i 's|^ARGS=.*|ARGS="--cluster.listen-address="|' \
  /etc/default/prometheus-alertmanager
systemctl restart prometheus-alertmanager

# Drop our config + reload
cp configs/prometheus/prometheus.r1.yml /etc/prometheus/prometheus.yml
mkdir -p /etc/prometheus/rules.d
promtool check config /etc/prometheus/prometheus.yml
systemctl reload prometheus

# Verify all 7 targets are UP
sleep 15
curl -sS localhost:9090/api/v1/targets \
  | jq -r '.data.activeTargets[] | "\(.labels.job) \(.health)"'
```

Expected: `caddy up | galexie up | node_exporter up | prometheus up | ratesengine-aggregator up | ratesengine-api up | ratesengine-indexer up`.

## Alert routing

`/etc/prometheus/alertmanager.yml` ships with a default that has
no real receivers. Operator adds webhook secrets when ready. The
existing alert rule files in
[`deploy/monitoring/rules/`](../../deploy/monitoring/rules/) were
written for the multi-host topology and reference labels (`region`,
`replica`) the single-host scrape doesn't emit; **don't symlink
them blindly into `/etc/prometheus/rules.d/`** — review per-rule
first so we don't get alert spam on labels that match nothing.

## Web UI

R1's Prometheus is on port 9090 and Alertmanager on 9093, both
listening on `0.0.0.0`. The host has no firewall today (per
r1-deployment-state §"Important but not urgent" #3), so these
are publicly reachable. Once we land Caddy in front of them too
(post-launch follow-up), they'll be HTTPS-only via
`prometheus.ratesengine.net` etc.

For now operator access:
- `ssh -L 9090:localhost:9090 root@136.243.90.96` and visit
  `http://localhost:9090` in a browser.
- Same shape for 9093 (Alertmanager).

## Why a single-host stop-gap

- Without Prometheus, the SLO alert rules in
  `deploy/monitoring/rules/` are unfired. SEV-1 / SEV-2 detection
  by alert is non-functional.
- The full HA role (`configs/ansible/roles/prometheus`) requires
  R2 prometheus host before it can be applied — and R2 itself is
  blocked on the L4.14 multi-region work.
- Single-host gets us **alerts firing into journald + Alertmanager
  log + (future) webhook fanout** today. The HA upgrade is a
  config-replacement, not a re-architecture.

## Migration to the HA role

When R2 lands:
1. Stop the system-package `prometheus` + `prometheus-alertmanager`
   on r1: `systemctl stop prometheus prometheus-alertmanager`.
2. Apply the ansible role with `prom_hosts = [r1, r2]` —
   the role installs upstream Prometheus binaries to a different
   path, so no conflict.
3. Delete `/etc/prometheus/` system-package files.
4. Re-route Caddy / DNS to the new endpoints.

The scrape targets list (`scrape_configs:` block) carries forward
unchanged — the role's `targets:` template should be seeded from
the contents of `prometheus.r1.yml` when the migration runs.
