---
title: Runbook — alertmanager-bad-config
last_verified: 2026-04-23
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

```sh
# Check AlertManager's own logs
kubectl -n monitoring logs deploy/alertmanager --tail=100 | grep -iE 'reload|error'

# Run amtool against the in-repo config
amtool check-config deploy/monitoring/alertmanager.yml

# Diff the live config vs the repo copy
kubectl -n monitoring get cm alertmanager-config -o jsonpath='{.data.alertmanager\.yml}' \
  | diff - deploy/monitoring/alertmanager.yml
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

- [ ] Step 1 — `amtool check-config` locally on the repo config.
      Fix syntax.
- [ ] Step 2 — ensure secrets referenced are present.
- [ ] Step 3 — push the fix via the normal GitOps flow (don't edit
      the live ConfigMap — it'll be clobbered on next reconcile).
- [ ] Step 4 — force a reload: `curl -XPOST http://alertmanager:9093/-/reload`.
- [ ] Verification:
      `alertmanager_config_last_reload_successful == 1`; the
      alert clears after one evaluation interval.

## Root cause analysis

- Git log on `deploy/monitoring/alertmanager.yml` showing the
  breaking commit.
- AlertManager log around the failed reload.
- Was the change reviewed? (CODEOWNERS routing working?)
- CI check should catch this — add an `amtool check-config` step
  to the PR pipeline if it's not there yet.

## Known false-positive patterns

- **Reload during pod startup** — `last_reload_successful` is 0
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
