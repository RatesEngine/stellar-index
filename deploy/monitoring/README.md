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
│   ├── aggregator.yml          aggregator-silent / outlier-storm / class-drop-spike
│   ├── anomaly.yml             freeze-engaged / freeze-sustained
│   ├── api.yml                 HTTP serving-plane alerts
│   ├── archive-completeness.yml archive-files-missing / completeness-stale
│   ├── cache.yml               Redis alerts
│   ├── divergence.yml          price-quality / oracle-stale alerts
│   ├── infra.yml               host / disk / ZFS / NVMe alerts
│   ├── ingestion.yml           Source / cursor / decode / orphan / insert alerts
│   ├── meta.yml                Prometheus self-health + deadmansswitch
│   ├── sla-probe.yml           SLA-probe p95 / freshness / unit-failed (Freighter SLA)
│   ├── slo.yml                 Multi-window SLO burn-rate alerts (ADR-0009)
│   ├── stellar.yml             stellar-core / stellar-rpc / archive alerts (inert on r1 — see runbooks' deployment-posture callouts)
│   ├── storage.yml             Postgres + TimescaleDB + backup alerts
│   ├── supply.yml              SAC cross-check divergence
│   ├── supply-refresh.yml      Aggregator-resident supply-refresh stalled / error-dominant
│   ├── supply-snapshot.yml     systemd-timer-path supply-snapshot stale / circulating-zero / unit-failed
│   └── verify-archive.yml      verify-archive run-stale / unit-failed
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

# Alert-rule unit tests are planned but not wired in this repo yet.
# There is currently no checked-in test/monitoring/ tree.
```

CI does NOT currently run `promtool check rules` or `promtool test rules`.
The only enforced control today is the documentation/runbook drift check in
`scripts/ci/lint-docs.sh`. Run `make monitoring-check` locally before merging
rule changes.

## Adding an alert

Per [repo-hygiene-plan.md §16](../../docs/architecture/repo-hygiene-plan.md#16-observability-discipline):

1. Expose the metric in `internal/obs/*.go` (Prometheus registry).
2. Add the rule to the appropriate file under `rules/`.
3. Write the runbook at `docs/operations/runbooks/<name>.md` (copy
   `_template.md`).
4. Add a row to `docs/operations/alerts-catalog.md`.
5. If/when the repo adds rule tests, place them under `test/monitoring/`.

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
| `component` | `ingestion` / `storage` / `cache` / `api` / `stellar` / `infra` / `meta` / `aggregator` / `archive` / `divergence` / `supply` | dashboard grouping |
| `runbook_url` | `https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/runbooks/<name>.md` | direct link from the page |

Annotations (not labels) carry human-readable metadata:

- `summary` — one-line headline for the page.
- `description` — 2–3 line explanation, populated with label substitutions.
