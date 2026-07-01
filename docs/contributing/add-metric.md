# Checklist — add a Prometheus metric

Reference: the wave-88/89/90/91 paired series (`divergence_refresh_*`).

- [ ] `internal/obs/metrics.go` — declare the typed `*Vec` var near the bottom. For IO workers use
      the **paired convention** (there is no helper — write two vars): `<name>Total{…,outcome}`
      counter + `<name>DurationSeconds{…,outcome}` histogram. Copy `DivergenceRefreshTotal` /
      `DivergenceRefreshDurationSeconds` verbatim as the template.
- [ ] Register in **`registerAppMetrics()`** (or `registerAppMetricsTail()` — whichever keeps
      `funlen` happy), **NOT** `init()` directly.
- [ ] If the counter's label set is bounded and alerts use `rate()`/`increase()`, pre-seed zero
      series in the `registerAppMetricsTail` loop so `absent()`/`==0` checks work before first fire.
- [ ] Wire it at the observation point.
- [ ] Document in `docs/reference/metrics/README.md` — a when-to-look-at-this prose block. **This
      file is hand-edited, not generated** (`make docs-metrics` is a no-op).
- [ ] If it alerts: add a rule to **both** `deploy/monitoring/rules/<area>.yml` and
      `configs/prometheus/rules.r1/<area>.yml`, each pointing at a runbook
      `docs/operations/runbooks/<alert>.md`.
- [ ] Regression test with `obstest.HistogramSampleCount` (`internal/obstest/`).

**Guards:** `make lint-docs` (`lint-metric-refs.sh`) fails if a registered metric is undocumented;
the `monitoring-rules` CI job promtool-validates both rule dirs. **Done when:** the metric appears
in `/metrics`; `bash scripts/dev/verify.sh` is green.
