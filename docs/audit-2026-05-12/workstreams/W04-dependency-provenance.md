# W04 — Dependency, provenance, supply chain

## Scope

Trust boundary of every third-party byte that enters our build,
runtime, or developer environment.

In scope:
- direct Go modules in `go.mod`
- transitive Go module surface via `go list -m all`
- pinned upstream SHAs in `VERSIONS.md`
- `go.sum` integrity (`go mod verify`)
- discovery-repo checkouts (29 of them under `.discovery-repos/`)
  — confirm zero product-code imports
- `web/{explorer,dashboard,status}/pnpm-lock.yaml`
- GitHub Action versions in workflows (pin policy)
- Docker base images (pin policy)
- govulncheck CI integration
- gitleaks CI integration

## Inputs

- `inventory/dependency-inventory.md`
- `inventory/workflow-inventory.md`
- `VERSIONS.md`
- `.gitleaks.toml`

## Per-direct-dep audit

For each row in `go.mod` `require` block, capture:

| Module | Version | Released | Maintainer status | Known CVEs | Used by | Risk |
| --- | --- | --- | --- | --- | --- | --- |
| _populate from inventory_ | | | | | | |

Special attention:
- `github.com/stellar/go-stellar-sdk` (replaced archived monorepo)
- any `cdp-pipeline-workflow` import (CLAUDE.md: "we do not inherit from it")
- any direct `horizonclient` import (ADR-0001 forbid)

## Discovery-repo import-leak grep

Run:

```sh
go list -deps ./... | grep -i discovery-repos
grep -RnE 'import\s*"[^"]*\.discovery-repos' --include='*.go' .
```

Expected: zero hits. Any hit = critical finding.

## Pinned-SHA verification

For each row in `VERSIONS.md`, prove the SHA still resolves
upstream and (optionally) matches the claimed commit message:

```sh
# template per upstream:
git ls-remote https://github.com/<org>/<repo> | grep <sha>
```

| Pinned upstream | SHA | Resolves | Matches claim |
| --- | --- | --- | --- |
| _populate_ | | | |

## go.sum integrity

```sh
go mod verify
go mod download -x
```

Capture output. Any mismatch = high finding.

## govulncheck

```sh
govulncheck ./...
```

Capture output. Any new CVE = finding (severity per CVSS).
Verify CI also runs this on PR (per W03 — currently `GOVULNCHECK_VERSION` is in CI env vars but no job invokes it).

## gitleaks

```sh
gitleaks detect --redact --no-banner
```

Capture output. Any leak = critical. Verify CI also runs gitleaks
(claim in `.gitleaks.toml`; check workflow content).

## Action pin policy

For each `uses: <action>@<ref>` in `.github/workflows/*.yml`:

| Action | Ref | Pinned by SHA? | Notes |
| --- | --- | --- | --- |
| `actions/checkout@v6` | major | no | upstream rotation risk |
| `actions/setup-go@v6` | major | no | |
| `golangci/golangci-lint-action@v9` | major | no | |
| _… enumerate_ | | | |

## Docker base-image pin policy

For each `FROM` directive in `docker/*.Dockerfile`:

| Dockerfile | FROM | Pinned by digest? | Notes |
| --- | --- | --- | --- |
| _populate_ | | | |

## pnpm lockfile audit

```sh
cd web/explorer && pnpm audit --prod --json | jq '.metadata.vulnerabilities'
cd web/dashboard && pnpm audit --prod --json | jq '.metadata.vulnerabilities'
cd web/status && pnpm audit --prod --json | jq '.metadata.vulnerabilities'
```

Capture per-app vulnerability counts. Any high/critical = finding.

## Adversarial vectors

- D1.1 Upstream Go module compromise
- D1.2 GitHub Action transitive compromise
- D1.3 Docker base image rebuild surprise
- D1.4 pnpm lockfile drift across CI runs
- D2.1 `.discovery-repos/*` accidentally imported
- D2.2 Pre-built binary at repo root
- D2.3 GHCR image signing not enforced

## Cross-workstream dependencies

- W01 owns the `.discovery-repos/` purpose claim
- W03 covers workflow content; W04 covers pinning policy within
- W18 covers Docker runtime; W04 covers Docker base-image pinning
- W19 owns post-build secret hygiene; W04 owns supply-side

## Closure criteria

- Every `go.mod require` row scored
- Discovery-repo import grep returns zero
- `go mod verify` succeeds
- `gitleaks detect` returns zero (or finding raised)
- `govulncheck ./...` returns zero (or finding raised)
- Action pin and Docker base pin tables complete
- pnpm audit output captured for all three frontends
