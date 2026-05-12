# W25 — Generated artifacts and drift

## Scope

Every artifact in the repo that is produced by a script or build
step rather than hand-edited. Drift between the generator's
intent and the checked-in output is its own failure mode — it
gives reviewers false confidence in stale shapes.

In scope:
- `openapi/rates-engine.v1.yaml` — sole API surface spec; verify
  the generation source and re-generate to detect drift
- `docs/reference/api/*` — auto-generated API reference
- `docs/reference/config/*` — auto-generated config reference
- `docs/reference/metrics/*` — auto-generated metric reference
- `examples/postman/rates-engine.postman_collection.json` —
  generated from OpenAPI?
- `examples/curl/*.sh` — hand-written but supposed to track API
- `pkg/client/types.go`, `endpoints.go` — generated or
  hand-rolled? if hand-rolled, drift detection vs OpenAPI
- `web/explorer/out/`, `web/dashboard/out/`, `web/status/out/` —
  Next.js static export output (excluded from per-file walk per
  EX-1212, but covered here as drift)
- `web/*/pnpm-lock.yaml` — generated lockfiles; verify
  regeneration is deterministic
- `go.sum` — generated; `go mod verify`
- `inventory/file-coverage.tsv` and other inventory artifacts in
  this audit (meta drift)
- alert-catalog markdown vs alert rules (`docs/operations/alerts-catalog.md`
  vs `deploy/monitoring/rules/*.yml`)

Out of scope:
- Hand-authored docs (W16)
- Compiled binaries (W03 build)

## Inputs

- `Makefile` `docs-all` / `docs-api` / `docs-postman` targets
- `scripts/dev/docs-api.sh`, `scripts/dev/docs-postman.sh`
- `scripts/ci/lint-docs.sh` (drift gate)
- `inventory/api-route-inventory.md`
- `inventory/migration-inventory.md`
- `inventory/metric-name-inventory.md`

## Per-generator audit

| Generator | Source of truth | Generated output | Re-gen produces same bytes? | Status |
| --- | --- | --- | --- | --- |
| `make docs-api` | OpenAPI + struct tags | `docs/reference/api/*` | | |
| `make docs-postman` | OpenAPI | `examples/postman/rates-engine.postman_collection.json` | | |
| `make docs-config` | struct tags | `docs/reference/config/*` | | |
| `make docs-metrics` | `internal/obs/metrics.go` `Name:` fields | `docs/reference/metrics/*` | | |
| `go mod tidy` | `go.mod` | `go.sum` | | |
| `pnpm install --frozen-lockfile` (per app) | `package.json` | `pnpm-lock.yaml` | | |
| `make web-build` (per app) | `web/<app>/src` + env | `web/<app>/out/*` | | |
| `bash docs/audit-2026-05-12/inventory/generate.sh` | `git ls-files` | inventory artifacts | | |

## Drift checks

For every generator, run:

```sh
make <generator>
git diff --stat -- <generated path>
```

Any non-empty diff = the checked-in artifact was stale at
audit time. File a finding in W25 with severity per impact:
- OpenAPI drift = `high` (downstream client impact)
- Reference docs drift = `medium`
- Postman drift = `medium`
- Inventory drift in this audit = `low` (just regenerate)

## Generation source completeness

For each generated output, verify the *source* declares every
public surface:

- OpenAPI lists every route in `internal/api/v1/server.go` (R04)
- Metrics reference lists every `Name: "..."` registered by
  any binary
- Config reference lists every field in `internal/config`

## Adversarial vectors

- F1.* (wrong-data on user-facing page) is amplified by stale
  generated docs that misrepresent live behavior
- D1.4 pnpm lockfile unstable across CI runs

## Cross-workstream dependencies

- W03 owns the build path that runs the generators
- W04 owns lockfile audit
- W11 owns OpenAPI consumer side (handlers)
- W14 owns metrics-reference source side
- W16 owns hand-authored docs
- W17 owns frontend build outputs

## Closure criteria

- Every generator row has a fresh-output verdict
- Every drift hit is filed
- Every generation source confirmed complete vs runtime surface
