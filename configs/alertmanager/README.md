# R1 single-host Alertmanager config

Companion to [`configs/prometheus/prometheus.r1.yml`](../prometheus/prometheus.r1.yml)
+ [`configs/prometheus/rules.r1/`](../prometheus/rules.r1/).

Two parallel apply paths produce the same routing:

- **Standalone** (this directory): `apply.sh` env-substitutes the
  YAML and reloads systemd-managed Alertmanager. Use for one-off
  config changes without an Ansible run.
- **Ansible** (recommended for new deployments + multi-region):
  [`configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`](../ansible/roles/prometheus/templates/alertmanager.yml.j2)
  renders the same shape via the
  [monitoring playbook](../ansible/playbooks/monitoring.yml).

Both paths use the `page` / `ticket` / `informational` severity
vocabulary defined in the
[severity ladder runbook](../../docs/operations/severity-ladder.md)
— matching every rule in `deploy/monitoring/rules/` +
`configs/prometheus/rules.r1/`.

## Routing

| Severity | Receiver | Cadence |
|----------|----------|---------|
| `page` | `chat-page` (Slack `#ratesengine-pages`) | every 12 h while firing |
| `ticket` | `chat-default` (Slack `#ratesengine-alerts`) | every 24 h while firing |
| `informational` | `silent` (Alertmanager UI only) | — |
| `ratesengine_deadmansswitch` | `deadmansswitch` (Healthchecks.io) | every 60 s |

The deadmansswitch is the alarm-of-last-resort — when its 60 s
heartbeat stops, Healthchecks.io pages us via a fully separate
channel, catching outages of Prometheus or Alertmanager itself.

## Apply to R1

1. **Provision the secrets file** off-disk in git
   (`/etc/default/alertmanager-secrets` on R1):

   ```sh
   # /etc/default/alertmanager-secrets — chmod 0600, root:root
   HEALTHCHECKS_DEADMANSSWITCH_URL='https://hc-ping.com/<your-uuid>'
   SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../...'
   ```

   Either URL can be left empty — the matching receiver degrades
   silently (alerts still accumulate in the Alertmanager UI).

2. **Run apply.sh** as root on R1:

   ```sh
   sudo /path/to/configs/alertmanager/apply.sh
   ```

   The script env-substitutes the YAML, validates with
   `amtool check-config`, installs to
   `/etc/prometheus/alertmanager.yml` (where the systemd unit
   expects it), and reloads `prometheus-alertmanager`.

## Verify

```sh
# Confirm the config loaded.
curl -s localhost:9093/-/healthy

# Trigger a synthetic alert to verify the chat fanout.
amtool alert add \
  --alertmanager.url=http://localhost:9093 \
  alertname=TEST_ALERT severity=ticket

# 30 seconds later, expect a Slack message in #ratesengine-alerts.
# Resolve:
amtool alert add \
  --alertmanager.url=http://localhost:9093 \
  alertname=TEST_ALERT severity=ticket --end=$(date -u +%FT%TZ)
```

## Migrate to multi-host

When R2 / R3 land, the Ansible role at
`configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
takes over. That template currently hardcodes `critical/warning/info`
matchers — adapt our `page/ticket/informational` vocabulary into
the role and decommission this directory.
