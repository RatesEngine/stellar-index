# Findings Register

| ID | Severity | Title | Surface | Evidence | Disposition |
| --- | --- | --- | --- | --- | --- |
| F-0501 | low | Monitoring README still claims CI does not run `promtool check rules` | `deploy/monitoring/README.md`, `.github/workflows/ci.yml` | [EV-0004](evidence/log.md) | **closed 2026-05-03** — `deploy/monitoring/README.md` rewritten to describe the `monitoring-rules` CI job; rule-firing unit-test gap stays acknowledged as a future follow-up. |
| F-0502 | low | OpenAPI drift linter still treats live `/v1/price/stream` as “planned” | `scripts/ci/lint-docs.sh`, `internal/api/v1/server.go` | [EV-0005](evidence/log.md) | **closed-pending-merge** — fix shipped in PR #472 (`ci(lint-docs): drop stale planned_regex exemption for /price/stream`). |
| F-0503 | low | `ratesengine-ops supply snapshot` still emits stale “computers ship later” messaging for classic and SEP-41 support | `cmd/ratesengine-ops/supply.go`, `internal/supply/*`, `cmd/ratesengine-aggregator/main.go` | [EV-0006](evidence/log.md) | **closed-pending-merge** — fix shipped in PR #527 (`docs(ops/supply): error message no longer claims computers unshipped`). Error rewritten to point at the aggregator-resident goroutine path; `-asset` flag help also updated. |
| N-0504 | note | Prior high-severity gaps around Blend runtime wiring, divergence reference wiring, and Freighter F2 fields are materially remediated in this snapshot | runtime wiring, registry, config docs, API tests | [EV-0007](evidence/log.md), [EV-0008](evidence/log.md) | closed-note |
