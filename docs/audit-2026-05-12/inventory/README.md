# Inventory

This directory contains the auto-generated inventories that
ground every other audit artifact in the workspace.

## Generation

Run from anywhere:

```sh
bash docs/audit-2026-05-12/inventory/generate.sh
```

Re-run whenever the worktree changes materially. Captured
outputs:

- `repo-snapshot.md` — commit SHA, file counts, dirty caveat
- `area-counts.md` — per-top-level-directory file counts
- `file-coverage.tsv` — every tracked file with its workstream
  assignment + a `todo` status placeholder. **This is the
  audit's bottom-up scoreboard.** Move every row to a terminal
  status (`done` / `excluded` / `blocked`).
- `api-route-inventory.md` — every HTTP route registered under
  `internal/api/`, anchored to source file:line. Fed into R04.
- `migration-inventory.md` — every up + down migration with
  byte counts and a status column. Fed into W09 / R02.
- `source-decoder-inventory.md` — every on-chain decoder package
  with file/test counts and BackfillSafe lookup. Fed into W07 / W24.
- `external-source-inventory.md` — every external CEX/FX/aggregator/
  oracle adapter. Fed into W08.
- `workflow-inventory.md` — every CI workflow file with triggers + jobs.
  Fed into W03 / R03.
- `runbook-inventory.md` — every runbook with byte count and
  matching alert(s). Fed into W14 / R07.
- `adr-inventory.md` — every ADR with title + status. Fed into R06.
- `alert-rule-inventory.md` — every Prometheus alert rule with
  matching-runbook check. Fed into W14 / R07.
- `metric-name-inventory.md` — every `Name: "..."` declaration
  found under `internal/`, useful for matching emitted metrics
  to alert expressions. Has noise (matches non-metric struct
  fields) — auditor must filter.
- `dependency-inventory.md` — direct Go modules + per-web-app
  pnpm dependencies + reference to `VERSIONS.md`. Fed into W04.
- `docker-systemd-inventory.md` — every Dockerfile (with USER /
  HEALTHCHECK lookups) + every systemd unit. Fed into W18.
- `webfrontend-inventory.md` — every page file across the three
  web frontends. Fed into W17.

## Scope Notes

- `.discovery-repos/*` is excluded from go-file count (it's
  upstream reference checkouts).
- `web/*/node_modules/` is excluded from file counts.
- The `metric-name-inventory.md` heuristic catches non-metric
  struct fields named `Name`. Audit expectation: filter to
  metrics by inspecting the surrounding context.
- The `api-route-inventory.md` heuristic catches mux/router
  patterns; manual verification required for each row.

## Cadence

- Re-run before starting each new audit session.
- Re-run after any major repo movement (new package, new
  workflow, new runbook).
- Always re-run before declaring a workstream `done` to ensure
  the file-coverage row counts match.
