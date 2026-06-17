---
adr: 0005
title: Monorepo with a single Go module
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0005: Monorepo with a single Go module

> **Reality note (2026-06-12, F-1353 / D2-09).** Several release/deploy
> details below have since changed:
> - Binary releases use **SemVer**, not CalVer (see
>   `docs/architecture/semver-policy.md`); the "one SemVer + one CalVer"
>   split no longer holds.
> - **No per-binary Docker images** are published — the GHCR job was
>   dropped (no consumer); `release.yml` ships cross-compiled binaries
>   only.
> - **`deploy/k8s/` does not exist** — deployment is bare-metal systemd
>   + Ansible (`deploy/systemd/`, `configs/ansible/`).
> - Public wire-shape types live in **`pkg/client`** (`pkg/client/types.go`),
>   not a separate `pkg/types` directory.
>
> The monorepo / single-`go.mod` decision itself stands unchanged.

## Context

The Stellar Index codebase has natural component boundaries:

- `stellarindex-indexer` — ingestion pipeline.
- `stellarindex-aggregator` — VWAP/TWAP/OHLC computation.
- `stellarindex-api` — REST + SSE server.
- `stellarindex-ops` — admin CLI.
- `stellarindex-migrate` — DB migration runner.
- A Go client SDK that downstream consumers import.
- A shared `types` surface they all depend on.

Plus docs, deploy kits, migrations, OpenAPI spec, test fixtures.

Two organisational shapes exist:

1. **Multi-repo** — one repo per binary + shared types as its own
   versioned module.
2. **Monorepo** — all code in one repo, one Go module.

## Decision

Single Go module, single repository:
`github.com/StellarIndex/stellar-index`.

`internal/` holds private code (Go enforces non-importability).
`pkg/` holds the narrow public surface — the client SDK and the
stable types API consumers depend on.

## Consequences

**Positive**

- Shared types (`CanonicalTrade`, `Asset`, `Amount`) have one
  authoritative home. No multi-repo version dance when the type
  evolves.
- Cross-cutting changes (add a new asset source → update
  consumer, aggregator, API response, client SDK) land in one PR,
  reviewed atomically, merged atomically.
- One SemVer (for `pkg/*`) + one CalVer (for binary releases).
  Operators don't chase compatibility matrices.
- Lower friction for external contributors — one clone, one PR,
  one CI run.
- Docs-as-code: architecture, ADRs, runbooks, and reference all
  live alongside the code they describe, preventing drift.

**Negative**

- Build times could grow. Mitigated by Go's per-package build;
  CI path filters; fast (< 2 min) unit-test target.
- CI fanout could be noisy. Mitigated by path filters so
  docs-only PRs skip heavy jobs.
- Merge conflicts on "hot" files. Mitigated by small-PR policy +
  CODEOWNERS routing.
- Temptation to cram one-off tooling in. Enforced rule: nothing
  outside `scripts/` is a one-off script; everything in
  `internal/` must be used by `cmd/*` or `test/`.

**Operational impact**

- One release workflow; one tag scheme; one CHANGELOG.
- Docker images published in parallel per binary from the same
  commit.
- Deploy kits (`deploy/docker-compose/`, `deploy/k8s/`) version
  alongside the code they deploy.

**Downstream design impact**

- `pkg/*` is the stability boundary. Internal packages refactor
  freely; `pkg/*` evolves via SemVer.
- `internal/canonical/` is the shared-type nexus — it's the first
  Go package written, because everything depends on it.

## Alternatives considered

1. **Multi-module monorepo (`go.work`).** Rejected: added
   complexity (version pinning between internal modules, `go.work`
   coordination overhead) for negligible benefit at our team
   size.
2. **Split repos: `stellarindex-types`, `stellarindex-indexer`,
   `stellarindex-api`, `stellarindex-client-go`.** Rejected: the
   coordination tax on every cross-repo change outweighs the
   claimed isolation benefits. Revisit only if the team grows
   past ~5 contributors or if we ship a stable v1.x with
   independent feature cadences per component.
3. **Keep this repo, split client SDK into its own repo.**
   Rejected: the SDK is thin and its types are shared with server
   code. Separate repo means version skew on type evolution.

## When to revisit

Concrete triggers that would motivate a split:

- Contributor count > 5 with clear sub-team specialisation.
- A component needs an independent release cadence (e.g. security
  patches to `pkg/client` faster than API releases).
- Repo size genuinely slows local development (build time
  > 5 min for unit tests).

Absent those, stay monorepo.

## References

- Related ADRs: ADR-0003 (i128) — enforcement of invariants
  across packages benefits from monorepo; a split would require
  cross-repo custom lint.
