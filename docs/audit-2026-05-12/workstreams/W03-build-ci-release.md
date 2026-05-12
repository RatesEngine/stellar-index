# W03 — Build, CI/CD, release controls

## Scope

From source code to deployable artifact + everything that runs
the build, lints, tests, docs generation, and release
sequencing.

In scope:
- `Makefile` — every target
- `scripts/dev/verify.sh`, `scripts/dev/cut-release.sh`,
  `scripts/dev/wait-pr-checks.sh`, `scripts/dev/r1-smoke.sh`,
  `scripts/dev/audit-public-api.sh`, `scripts/dev/verify-cdn.sh`,
  `scripts/dev/verify-cross-region.sh`, `scripts/dev/docs-api.sh`,
  `scripts/dev/docs-postman.sh`, `scripts/dev/encode-topics`,
  `scripts/dev/decode-scval`, capture scripts (4)
- `scripts/ci/lint-docs.sh`, `lint-imports.sh`,
  `lint-imports.baseline`, `lint-openapi-urls`, `verify-launch-ready`
- `scripts/ops/cf-pages-bootstrap.sh`, `circulation-fetch/`,
  `fx-history-backfill/`, `pre-launch-check.sh`,
  `recompute-usd-volume-soroban.sql`

Note: `scripts/ci/lint-openapi-urls/`, `scripts/ci/verify-launch-ready/`,
`scripts/dev/decode-scval/`, `scripts/dev/encode-topics/`,
`scripts/ops/circulation-fetch/`, `scripts/ops/fx-history-backfill/`
are Go-program subdirectories (not shell scripts). Each contains
a `main.go`. Treat each as a mini-binary subject to W03 build path
and W15 test density.
- `.github/workflows/*.yml` (10 files): `api-audit.yml`,
  `api-docs.yml`, `ci.yml`, `deploy.yml`, `docs-deploy.yml`,
  `explorer-deploy.yml`, `k6-weekly.yml`, `release-validate.yml`,
  `release.yml`, `status-page.yml`
- `docker/*.Dockerfile` — 6 files
- `commitlint.config.js` enforcement

Out of scope:
- container deployment & systemd (W18)
- runbook content (W14)

## Inputs

- `inventory/workflow-inventory.md`
- `inventory/docker-systemd-inventory.md`

## Per-Makefile-target check

| Target | Reachable | Behaviour matches docs | Status |
| --- | --- | --- | --- |
| `help` | | | |
| `dev` | | | |
| `test` | | | |
| `test-integration` | | | |
| `test-integration-build` | | | |
| `lint` | | | |
| `fmt` | | | |
| `vet` | | | |
| `build` | | | |
| `docs-all` / `docs-api` / `docs-postman` | | | |
| `verify` | | | |
| `monitoring-check` | | | |
| `web-{typecheck,lint,build}` | | | |
| `dashboard-{typecheck,lint,build}` | | | |
| `status-{typecheck,lint,build}` | | | |

## Per-workflow audit

For each workflow file, capture:

| Workflow | Triggers | Jobs | Secrets used | Cost model | Notes |
| --- | --- | --- | --- | --- | --- |
| `ci.yml` | PR-only (paths-ignored for docs); push-to-main bypassed by design | lint, test, doc-checks, import-checks, monitoring-rules, openapi-lint, web-* … | none claimed | per-PR matrix | verify cancel-in-progress + concurrency group |
| `api-audit.yml` | | | | | |
| `api-docs.yml` | | | | | |
| `deploy.yml` | manual workflow_dispatch (region, version, binaries) | stage→backup→install→restart→health probe→rollback | SSH key, regional vars | rare invocation | re-walk per `docs/operations/deploy-workflow.md` |
| `docs-deploy.yml` | | | | | |
| `explorer-deploy.yml` | | | | | |
| `k6-weekly.yml` | cron weekly | k6 load run | | per-week | budgets vs measured? |
| `release-validate.yml` | | | | | |
| `release.yml` | tag push | cross-compile binaries → SHA256SUMS → GitHub Release → GHCR push | GHCR creds, signing? | per-tag | `evidence/log.md`: rc.48 just succeeded |
| `status-page.yml` | | | | | |

## Per-Dockerfile audit

| Dockerfile | Base image | USER | HEALTHCHECK | ENTRYPOINT/CMD | Multi-arch | SBOM |
| --- | --- | --- | --- | --- | --- | --- |
| `docker/ratesengine-aggregator.Dockerfile` | | | | | | |
| `docker/ratesengine-api.Dockerfile` | | | | | | |
| `docker/ratesengine-indexer.Dockerfile` | | | | | | |
| `docker/ratesengine-migrate.Dockerfile` | | | | | | |
| `docker/ratesengine-ops.Dockerfile` | | | | | | |
| `docker/ratesengine-sla-probe.Dockerfile` | | | | | | |

## Verify-flow parity

| Check | `make verify` (local) | CI job | Parity |
| --- | --- | --- | --- |
| gofumpt | `make fmt` | `lint` job | |
| golangci-lint | `make lint` | `lint` job | |
| go vet | `make vet` | `lint` job | |
| go test -race | `make test` | `test` job | |
| integration build | `make test-integration-build` | _verify_ | |
| integration run | local-only | _verify_ | |
| doc lint | `lint-docs.sh` | `doc-checks` | |
| import lint | `lint-imports.sh` | `import-checks` | |
| OpenAPI URL lint | `lint-openapi-urls` | _verify_ | |
| monitoring rules | `make monitoring-check` (graceful skip if no promtool) | `monitoring-rules` job | |
| web-* | local skip if no pnpm | per-frontend job | |
| gitleaks | _verify in CI_ | | |
| govulncheck | _verify in CI_ | | |

## Adversarial vectors

- D1.2 GitHub Action transitive compromise — pin policy
- D1.3 Docker base image pulled by tag — unpinned regression
- E2.1 release.yml trigger — branch protection on main?
- E2.3 Deploy workflow with unverified binary
- E2.2 GHCR push credentials in workflow secret

## Cross-workstream dependencies

- W04 verifies action SHA pinning + Dockerfile base-image pinning
- W14 verifies `monitoring-rules` job actually runs `promtool check rules`
- W18 verifies `deploy.yml` matches systemd unit semantics
- W22 verifies the public-flip-eve checklist references release-process.md

## Closure criteria

- Every Makefile target verified
- Every workflow row has triggers/jobs/secrets/cost recorded
- Every Dockerfile row complete
- Verify-flow parity table 100% green or finding raised
- `release.yml` rc.48 outcome captured (it succeeded at audit time per background notification)
- Branch-protection state captured (probe via `gh api` if available; else exclusion EX-1205)
