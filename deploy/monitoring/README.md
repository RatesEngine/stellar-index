# Rates Engine — monitoring rule files

Prometheus alerting rules that correspond 1:1 to the rows in
[docs/operations/alerts-catalog.md](../../docs/operations/alerts-catalog.md).
Loaded by AlertManager; routed to PagerDuty per
[sev-playbook.md §3](../../docs/operations/sev-playbook.md#3-detection-channels).

## Layout

```
deploy/monitoring/
├── README.md                   (this file)
├── rules/
│   ├── ingestion.yml           Source / orchestrator / cursor alerts
│   ├── storage.yml             Postgres + TimescaleDB + backup alerts
│   ├── cache.yml               Redis alerts
│   ├── api.yml                 HTTP serving-plane alerts
│   ├── stellar.yml             stellar-core / stellar-rpc / archive alerts
│   ├── divergence.yml          price-quality / oracle-stale alerts
│   ├── aggregator.yml          aggregator-silent / outlier-storm / class-drop-spike
│   ├── infra.yml               host / disk / ZFS / NVMe alerts
│   └── meta.yml                Prometheus self-health + deadmansswitch
```

## Severity labels

Every alert carries:

- `severity: page` → SEV-1 (P1) — wakes oncall.
- `severity: ticket` → SEV-2 (P2) — business-hours page, after-hours ticket.
- `severity: informational` → SEV-3 (P3) — ticketed, weekly review.

AlertManager routes by label (see its config, TBD).

## Validating locally

```sh
# Install promtool (bundled with prometheus binary distribution):
brew install prometheus
# or from the GitHub release.

# Validate every rule file parses + has no warnings:
make monitoring-check
# which runs:
promtool check rules deploy/monitoring/rules/*.yml

# Unit-test a rule (given a synthetic metric input):
promtool test rules test/monitoring/<name>_test.yml
```

CI runs both. No rule merges unless `promtool check rules` and all
`promtool test rules` pass.

## Adding an alert

Per [repo-hygiene-plan.md §16](../../docs/architecture/repo-hygiene-plan.md#16-observability-discipline):

1. Expose the metric in `internal/obs/*.go` (Prometheus registry).
2. Add the rule to the appropriate file under `rules/`.
3. Write the runbook at `docs/operations/runbooks/<name>.md` (copy
   `_template.md`).
4. Add a row to `docs/operations/alerts-catalog.md`.
5. Write a unit test at `test/monitoring/<name>_test.yml`.

All five in one PR. The `scripts/ci/lint-docs.sh` script fails the
build if any rule's `runbook_url` points at a missing runbook
file (rule §9, "Every alert rule's runbook_url must point to an
existing file") — so a fully-wired alert with a missing runbook
won't merge.

## Labels convention

Every rule carries these labels for AlertManager routing:

| Label | Values | Purpose |
| ----- | ------ | ------- |
| `severity` | `page` / `ticket` / `informational` | routing tier |
| `team` | `ratesengine` | downstream filtering |
| `component` | `ingestion` / `storage` / `cache` / `api` / `stellar` / `infra` / `meta` | dashboard grouping |
| `runbook_url` | `https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/runbooks/<name>.md` | direct link from the page |

Annotations (not labels) carry human-readable metadata:

- `summary` — one-line headline for the page.
- `description` — 2–3 line explanation, populated with label substitutions.
