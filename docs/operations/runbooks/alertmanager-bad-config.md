---
title: Runbook — alertmanager-bad-config
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `ratesengine_alertmanager_config_bad`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_alertmanager_config_bad` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/meta.yml` |
| Typical MTTR | 5–30 min |
| Impact | AlertManager reload after a config push failed, so any rule changes since the last successful load are **not live**. Existing routes keep working from the previous in-memory config. New alerts you expected to route go nowhere. |

## Symptoms

- `alertmanager_config_last_reload_successful == 0` for ≥ 5 min.
- AlertManager log: `error loading config: ...` at the reload time.
- PRs that changed `alertmanager.yml` merged recently but the
  expected new route doesn't fire.

## Quick diagnosis (≤ 5 min)

AlertManager runs as `alertmanager.service` on `mon-01` and
`mon-02` (per the `prometheus` ansible role; ADR-0008 §3
monitoring tier). The live config is at
`/etc/alertmanager/alertmanager.yml`, rendered from the role's
`alertmanager.yml.j2`.

```sh
# Check AlertManager's own logs around the failed reload
ssh root@mon-01 "journalctl -u alertmanager -n 100 --no-pager | grep -iE 'reload|error'"

# Run amtool against the role-rendered config (must match live)
ssh root@mon-01 "amtool check-config /etc/alertmanager/alertmanager.yml"

# Diff the live config across the AM pair — they must agree
diff <(ssh root@mon-01 cat /etc/alertmanager/alertmanager.yml) \
     <(ssh root@mon-02 cat /etc/alertmanager/alertmanager.yml)
```

## Typical root causes

1. **YAML typo**. Indent error, missing colon, unquoted special
   character. `amtool check-config` catches these.

2. **Template-expansion error**. Malformed `{{ ... }}` in a
   receiver template. These validate at parse time but reference
   errors (field doesn't exist) only fire at send time — watch
   for silent "template expanded to empty string" behaviour too.

3. **Secret-resolution failure**. Webhook URLs / API keys sourced
   from a secret that didn't get mounted / the name changed.
   AlertManager refuses to load a config with a missing referenced
   secret.

4. **Version skew** — a new AlertManager binary with a syntax the
   old config doesn't use, or vice versa. `config.file` parsed
   differently across versions.

## Mitigation

- [ ] Step 1 — `amtool check-config` locally on the role's
      `alertmanager.yml.j2` rendered to a temp file. Fix syntax.
- [ ] Step 2 — ensure any vault-backed receiver creds (Slack /
      Discord webhook URLs, PagerDuty integration keys) referenced
      by the config are present and reachable.
- [ ] Step 3 — push via the normal flow: `ansible-playbook` with
      the prometheus role (don't hand-edit
      `/etc/alertmanager/alertmanager.yml` — the next role apply
      will overwrite it).
- [ ] Step 4 — force a reload (the role does this automatically
      via handler, but if needed manually):
      `ssh root@mon-01 "curl -XPOST http://localhost:9093/-/reload"`
      and the same on `mon-02`.
- [ ] Verification:
      `alertmanager_config_last_reload_successful == 1` on both
      `mon-01` and `mon-02`; the alert clears after one evaluation
      interval.

## Root cause analysis

- Git log on
  `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
  showing the breaking commit.
- AlertManager log around the failed reload.
- Was the change reviewed? (CODEOWNERS routing working?)
- CI check should catch this — add an `amtool check-config` step
  to the PR pipeline if it's not there yet.

## Known false-positive patterns

- **Reload during unit startup** — `last_reload_successful` is 0
  until the very first load completes. During a cold start this
  can briefly trip; `for: 5m` absorbs normal startup.

## Related

- `deadmansswitch.md` — the watchdog that catches a totally-broken
  AlertManager (this alert relies on AM being up enough to serve
  metrics).
- `scrape-failing.md` — if the AM metrics endpoint is what's
  failing, not the config.

## Changelog

- 2026-04-23 — initial draft.
- 2026-05-02 — diagnosis converted from kubectl ConfigMap +
  pod-logs to systemd / journalctl on `mon-01..02` running
  `alertmanager.service` per the `prometheus` ansible role
  (ADR-0008). The cited path `deploy/monitoring/alertmanager.yml`
  was wrong — the source-of-truth template is in the role
  (`alertmanager.yml.j2`); live config sits at
  `/etc/alertmanager/alertmanager.yml`.
