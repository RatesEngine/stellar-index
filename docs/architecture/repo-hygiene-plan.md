---
title: Repo Hygiene & Tech-Debt Prevention Plan
last_verified: 2026-05-03
status: ratified
---

# Repo Hygiene & Tech-Debt Prevention Plan

**Owner:** @ash.
**Ratified:** 2026-04-22.
**Binds:** every PR, every deploy, every reviewer.

This is the non-negotiable policy layer. Principles in
[docs/engineering-standards.md](../engineering-standards.md)
remain authoritative for rationale; this doc is the **enforcement
map** — what actually fails a build, what rots, and which lever
operators pull when a rule bites unfairly.

The trap this plan is designed to escape: enterprise codebases
accumulate TODOs, stale docs, quiet coupling, and "temporary" flags.
Five years later the repo is unreadable. Every rule below is a bet
that **enforcement at push time is cheaper than cleanup later**.

---

## 1. The "doc-code round-trip" invariant

**Rule:** for every piece of user-facing information, there is
exactly one source of truth. All other copies are **generated** and
marked as such.

| Surface | Source of truth | Generated copy | Regen command |
| ------- | --------------- | -------------- | ------------- |
| HTTP API | `openapi/stellar-index.v1.yaml` | `docs/reference/api/` (Redocly output) | `make docs-api` |
| Config schema | Struct tags in `internal/config/*.go` | `docs/reference/config/README.md` | `make docs-config` |
| Prometheus metrics | `Name:` fields in `internal/obs/*.go` | `docs/reference/metrics/README.md` | `make docs-metrics` |
| Canonical types | `internal/canonical/*.go` | None — Go types are their own docs | — |
| ADR list | `docs/adr/*.md` frontmatter | `docs/adr/README.md` index | Manual (rare edits) |

**Enforcement:**

- Every generated doc starts with line 1: `<!-- GENERATED FILE - DO NOT EDIT -->`. CI rejects the doc otherwise (`lint-docs.sh §7`).
- Every new config field / API route / metric must round-trip through a CI check (`lint-docs.sh §1–3`).
- `make docs` must be idempotent. CI runs `make docs && git diff --exit-code docs/reference/` — any diff fails the build.

**When the rule bites:**

- You added a struct field but forgot `doc:"..."` — CI tells you.
- You renamed an OpenAPI operation but didn't regen handlers —
  generated-doc check fails.

---

## 2. Doc freshness

**Rule:** architecture and operations docs carry a `last_verified:`
frontmatter date. Warnings at 90 days; hard failure at 180.

```markdown
---
title: HA Infrastructure Plan
last_verified: 2026-05-03
status: ratified | draft | proposed
---
```

**Enforcement:** `scripts/ci/lint-docs.sh §6`.

**Which docs get this requirement:**

| Directory | `last_verified` required? | Stale-at |
| --------- | ------------------------- | -------- |
| `docs/architecture/` | **Yes** | 90 warn, 180 fail |
| `docs/operations/` | **Yes** | 90 warn, 180 fail |
| `docs/adr/` | Frontmatter present but **immutable**; the `date:` field is when the decision was made. Never "refreshed." | never |
| `docs/reference/` | No — generated | n/a |
| `docs/development/` | Recommended, not enforced | — |

**Why:** any doc "actively claiming" to reflect current reality must
be re-verified on a cadence. 90 days is the SRE-team cycle; 180 is
the "you've ignored two warnings, something is wrong" hard stop.

**How to refresh:**

1. Re-read the doc.
2. Verify every claim against current code.
3. Update anything drifted.
4. Bump `last_verified` to today.
5. PR with `chore: refresh X doc freshness (last_verified)`.

---

## 3. ADR lifecycle

**Rule:** every architectural decision of consequence becomes an
ADR under `docs/adr/`. ADRs are immutable once accepted.

Numbered sequentially, frontmatter-structured (template at
[docs/adr/_template.md](../adr/_template.md)):

```yaml
---
adr: 0007
title: TimescaleDB for price time series
status: Accepted
date: 2026-04-29
supersedes: []
superseded_by: null
---
```

**Lifecycle states:**

- `Proposed` — in review. Can still be edited freely.
- `Accepted` — merged. Immutable. Any change is a new ADR that
  supersedes it.
- `Superseded` — an accepted ADR that a later ADR replaced. The
  `superseded_by: 0012` field is required and CI checks it
  (`lint-docs.sh §8`).
- `Rejected` — considered and explicitly declined. Kept for
  traceability.

**Enforcement:**

- ADR filenames match `NNNN-kebab-case-title.md`.
- Every `Superseded` has a valid `superseded_by`.
- A new ADR's number = `max(existing) + 1`. No gaps, no collisions.
  Status / superseded-by integrity is enforced today by
  `scripts/ci/lint-docs.sh` §8; the strict numbering-gap check is
  reviewer-enforced (no dedicated script yet).

**When do we write an ADR?**

- Choosing or replacing a significant dependency.
- A cross-cutting design choice (HA topology, auth model,
  partitioning strategy).
- A decision that binds a future reader's hands (e.g., "no Horizon
  ever").
- A decision we don't want to re-debate in three months.

**When we don't:**

- "Should this function return an error or a bool?" — just pick one.
- Internal-to-a-package refactoring choices.
- Code style rules (those live in `.golangci.yml`).

---

## 4. TODO / FIXME discipline

**Rule:** every `TODO`, `FIXME`, or `XXX` in code is of the form
`TODO(#123): description`, where `#123` is a GitHub issue.

**Enforcement:** `scripts/ci/lint-docs.sh §5`. Any comment matching
`TODO[ :]` or `FIXME[ :]` etc. without a `(#N)` fails the build.

**Why:** an unlinked TODO rots invisibly. A linked TODO rots **in the
issue tracker**, where someone triages it. The issue is what has a
deadline, owner, and context; the TODO just points.

**How to file:**

```go
// TODO(#42): switch to streaming decoder once stellar-extract
// supports it. Current impl loads the full meta into memory.
```

- Open the issue **first** (even if you immediately close it in your
  PR). Issue title = one-line description. Body = "Context: <why>,
  Path: <where>, Estimated effort: <small/medium/large>".
- Never re-use an issue number.
- If a TODO is resolved in the current PR, remove it and close the
  issue in the same PR.

**Variants:**

- `TODO(#N)` — work to do.
- `FIXME(#N)` — known bug, intentionally deferred.
- `HACK(#N)` — known-ugly workaround.

No other comment markers are allowed in code. `XXX` is especially
banned (`lint-docs.sh` flags).

**Practical exception:** `TODO(#0)` is the explicit placeholder
for "issue not yet filed" — accepted by `lint-docs.sh` so a
contributor can land a known-future-work pointer without
blocking on GitHub-issue creation. Originally scoped to
Phase-1 scaffolding with a Week-2 grace; in practice we've
kept the placeholder around for the same reason since Phase 1
closed (2026-04-22) — incremental conversion as issues get
filed. ~16 markers in the tree as of 2026-04-25; track the
counter trending down rather than a hard deadline.

---

## 5. PR size + review

**Rule:** PRs target ≤ 500 LoC changed. Larger PRs require
pre-agreement from a reviewer.

**Why:** review quality drops linearly above ~400 LoC. Big PRs
accumulate "rubber-stamp" approvals that hide bugs.

**Enforcement:**

- GitHub Action labels PRs `size/XL` at 500+. Label blocks auto-merge
  unless a reviewer applies `size/XL-approved`.
- CODEOWNERS requires review; the author never self-approves.
- `make verify` must pass locally before pushing (this is a
  contributor habit; CI re-runs the same gates).

**PR structure (in order):**

1. One logical change per PR. "Add X" and "refactor Y to support X"
   are two PRs if the refactor stands alone.
2. Commit history clean enough to squash.
3. PR description follows `.github/PULL_REQUEST_TEMPLATE.md`
   (purpose, approach, tradeoffs, test plan, rollout).
4. All CI checks green. No merging on red.
5. Approval by a CODEOWNER (not the author).
6. Author merges (not reviewer).

---

## 6. Feature flags

**Rule:** every feature flag has a scheduled **removal date** at
creation.

**Schema:**

```go
// Flag: aggregate_tier_3_sources
// Purpose: gate the Tier-3 CEX venue integrations (OKX, Bybit, Gate).
// Created: 2026-05-20
// Remove-by: 2026-08-20 (3 months)
// Owner: @ash
if flags.AggregateTier3Sources.Enabled(ctx) {
    ...
}
```

- Flags live in `internal/flags/flags.go`.
- `Remove-by:` is authoritative. A nightly CI job (post-launch)
  opens an issue when a flag has passed its date.
- Once shipped at 100 %, a flag has two weeks to be removed from
  code entirely. "Flag cleanup PR" is a standard task type.

**Why:** long-lived feature flags become quiet branches of behaviour
that diverge from the "on" path. The "remove by" date is the forcing
function.

---

## 7. Dependency hygiene

**Rule:** every direct Go dependency has either an ADR or a one-line
comment in `go.mod` explaining its role.

```go
require (
    // Stellar network SDK — ledger meta reading, XDR, ingest.
    github.com/stellar/go-stellar-sdk v0.5.0

    // Typed extraction of Stellar ledger meta into row structs.
    github.com/withObsrvr/stellar-extract v0.1.2
)
```

Other rules:

- `go mod tidy` on every push (CI verifies `go.mod` / `go.sum`
  unchanged).
- `govulncheck ./...` in CI (`ci.yml §vuln`). Fails on any advisory
  against our code.
- `gitleaks` scans every commit for accidental secret leaks.
- Dependabot opens weekly PRs; reviewer handles within 7 days.
- Major-version bumps **always** require a manual review + ADR if
  they touch a cross-cutting dep (SDK, framework).

Upstream SHAs we audited during Phase 1 live in `VERSIONS.md`.
Dependencies not in `VERSIONS.md` are "unaudited" and land via ADR
only.

---

## 8. Code quality gates (mechanical)

All enforced in `.golangci.yml` + `.github/workflows/ci.yml`.

| Gate | Tool | Fails on |
| ---- | ---- | -------- |
| Format | `gofumpt` | any unformatted file |
| Imports | `goimports -local github.com/StellarIndex/stellar-index` | bad import order |
| Vet | `go vet` | any warning |
| Static | `staticcheck` + `govet` + `errcheck` + `errorlint` + `bodyclose` + `contextcheck` + `nilerr` + `noctx` + `rowserrcheck` + `sqlclosecheck` + `wastedassign` + `ineffassign` | any finding |
| Security | `gosec` | any finding (except `G104` — we use `errcheck`) |
| Complexity | `gocognit`, `gocyclo`, `funlen` | `cognit > 15`, `cyclo > 15`, `lines > 80` |
| Tests | `go test -race` | any failing or `-race` data race |
| Coverage | `go test -coverprofile` | coverage drop on changed packages |
| Supply chain | `govulncheck`, `gitleaks` | any finding |
| OpenAPI | `spectral` | any rule violation |

CI runs these in parallel; verify.sh runs them sequentially. The
threshold for changing `.golangci.yml` is an ADR — we do not weaken
lint to unblock a PR.

---

## 9. Testing discipline

**Rule:** every bugfix gets a regression test. Every new exported
symbol gets a unit test.

Three layers:

| Layer | Where | Speed | Gate |
| ----- | ----- | ----- | ---- |
| Unit | Co-located `*_test.go` | < 2 min | Every PR. Target ≥ 70 % coverage per package; 80 % for `internal/canonical`, `internal/aggregate`. |
| Integration | `test/integration/` with `//go:build integration` | < 10 min | Label `ready-for-integration` + nightly. Uses testcontainers for Postgres/Redis/MinIO. |
| Load | `test/load/` k6 scripts | 30+ min | Pre-release + nightly against staging. |

**Fixtures:** `test/fixtures/<topic>/`. Golden-file style where
applicable. Fixture regeneration is a one-liner (`make fixtures`).
Never hand-edit a generated fixture.

**Protocol-boundary fixtures** (per Phase-1 commitment): organised
per-source under `test/fixtures/<source>/` rather than in a separate
`protocol-boundary/` tree, so a decoder's pre-/post-protocol-bump
fixtures live next to the source they exercise. The unified
`transfer/mint/burn` (CAP-67 / P23) handling is exercised through
each source's golden files; CLAUDE.md "Things that will surprise
you" calls out the post-P23 4th-topic shape.

---

## 10. Directory structure discipline

**Rule:** nothing outside `scripts/` is a one-off script. Everything
in `internal/` must be used by `cmd/*` or `test/`. Packages that
aren't don't exist yet — delete them.

**Banned:**

- `utils/`, `misc/`, `common/` catch-alls.
- Dead code that "might be useful later." Git keeps it if needed.
- Nested `internal/internal/`.

**Required structure for source connectors** (CEX / DEX / oracle / FX):

```
internal/sources/<name>/
├── README.md         what this connector does, config options,
│                     known quirks, mainnet addresses
├── events.go         event-topic decoding (on-chain) or WS message
│                     decoding (CEX)
├── decode.go         raw → internal/canonical.Trade
├── consumer.go       consumer.Source impl — wires the source
│                     into the registry
└── source_test.go
```

On-chain sources additionally carry `dispatcher_adapter.go` (the
seam to `internal/dispatcher`) and `factory_seed.go` if the
venue uses a factory-deploys-pair-contracts shape (Soroswap,
Aquarius). External (CEX / FX) sources sometimes split
`consumer.go` into `streamer.go` + `backfill.go` when the venue
separates streaming and REST backfill (e.g. binance). The five
files above are the canonical core; review the existing
connectors before adding a new one.

Plus fixtures in `test/fixtures/<name>/`.

Enforced by: reviewer + the architectural import-boundary check
in `scripts/ci/lint-imports.sh` (rejects packages that grow
unowned cross-cutting deps). A dedicated `lint-layout.sh` for the
five-file convention itself doesn't exist; it's reviewer-policed.

---

## 11. Naming conventions

**Rule:** names follow a tight set of conventions. CI enforces
where mechanical; reviewer enforces the rest.

| What | Convention |
| ---- | ---------- |
| Go packages | short, lowercase, single-word where possible |
| Go exported symbols | `CamelCase`; one-sentence godoc |
| Files | `snake_case.go`; test files `foo_test.go` |
| Environment variables | `STELLARINDEX_<AREA>_<KEY>` |
| Config keys (YAML) | `snake_case` |
| Metric names | `stellarindex_<subsystem>_<metric>_<unit>` |
| HTTP paths | `kebab-case`, plural nouns |
| JSON response fields | `snake_case` |
| Error types | `<subsystem>: <action>` prefix |
| Commit messages | imperative, `<scope>: <what>`; wrap at 72 |

No mixed-case acronym variants (e.g. `APIKey` / `ApiKey`). No
`utils_misc.go`. No singular-vs-plural inconsistency on routes.

---

## 12. Deprecation / removal

**Rule:** deprecation precedes removal by ≥ 6 months (12 months for
widely-used surfaces).

Lifecycle:

1. **Deprecate.** Mark with `Deprecated: ...` godoc comment. OpenAPI
   field `deprecated: true`. Changelog entry under "Deprecated."
2. **Alert.** Log warning on use (rate-limited). Response header
   `Sunset: 2026-10-22`.
3. **Remove.** Not before the Sunset date. PR references the
   original deprecation commit.

Apply to:

- Go exported symbols in `pkg/*`.
- HTTP endpoints.
- Config keys (YAML / env).
- CLI flags.
- Migrations.

---

## 13. Change-log discipline

**Rule:** every user-visible change gets a line in
[CHANGELOG.md](../../CHANGELOG.md) under the correct section
(Added / Changed / Deprecated / Removed / Fixed / Security).

- Keep-a-Changelog format.
- Author writes the entry in the PR; reviewer verifies it's present
  and accurate.
- `[Unreleased]` grows; on release, moves to `[X.Y.Z] - DATE` and a
  new `[Unreleased]` opens.
- **Never** silently backfill changelog after the fact.

---

## 14. Generated files

**Rule:** every generated file begins with a stable banner:

```markdown
<!-- GENERATED FILE - DO NOT EDIT -->
<!-- Source: internal/config/config.go -->
<!-- Regen: make docs-config -->
```

Or the Go equivalent:

```go
// Code generated by <tool>. DO NOT EDIT.
// Source: <source>
```

Enforced by `lint-docs.sh §7`. Generated files are excluded from
lint (`.golangci.yml` `exclusions.rules`).

---

## 15. Infra-as-code discipline

Production deployment is bare-metal + systemd + Ansible per
[ADR-0008](../adr/0008-ha-topology.md) — there is no Kubernetes
cluster. Inventory + roles live in `configs/ansible/` and unit
files in `deploy/systemd/`.

- All ansible roles in `configs/ansible/roles/<name>/`; reviewed
  line-by-line. Each role has a `README.md` plus its
  `tasks/`, `templates/`, `defaults/`, `handlers/` subtree.
- All systemd units in `deploy/systemd/<binary>.service`
  (`stellarindex-{api,indexer,aggregator}.service` plus the
  timer/oneshot units for `archive-completeness`, `sla-probe`,
  `supply-snapshot`, and `verify-archive-tier-a`).
- The dev / reference docker-compose stack lives in
  `deploy/docker-compose/`; production never reads from it.
- No inline shell heredocs in templates — move multi-line shell
  to `scripts/ops/` and call from a one-line `command:` task.
- Terraform (if introduced) has remote state with locking.
- Secrets never in Git, ever. Ansible Vault references only;
  `configs/ansible/inventory/<region>.secrets.yml` is the
  source of truth.

---

## 16. Observability discipline

- Every new handler emits structured logs at entry (level DEBUG) and
  error-return (level ERROR). No ad-hoc `fmt.Println`.
- Every new metric has a doc entry and a dashboard panel (Grafana).
- Every new alert has a runbook in `docs/operations/runbooks/`.
  No alert without a runbook.

"No alert without a runbook" is the single most effective discipline
for reducing oncall fatigue.

---

## 17. Archival discipline

When code or a doc is removed:

- Code: `git rm`. Git history is archival.
- Docs: move to `docs/_archive/<original-path>.md` with a
  `redirected-to:` frontmatter field. Never delete — historical
  context matters.

---

## 18. Agent-readability

**Rule:** AI agents (Claude, Copilot, Cursor, ...) are first-class
readers. The repo must be navigable cold.

Mechanics:

- [CLAUDE.md](../../CLAUDE.md) is the orientation map. Aggressively
  updated with each architectural shift.
- [AGENTS.md](../../AGENTS.md) is an alias (symlink-style pointer).
- Every package has a `doc.go` or `README.md` describing its role in
  one paragraph.
- Naming conventions (§11) matter more for machine readers than
  humans.
- Invariants in [CLAUDE.md](../../CLAUDE.md) Invariants section are
  duplicated as ADR frontmatter so agents can grep either place.

**Enforced by:** reviewer. A package with no `doc.go` and no obvious
purpose fails review.

---

## 19. The "hygiene bill" — the sum of all CI checks

Approximate cost: < 2 min for a typical PR (modulo race tests).

| Check | Typical time | Runs |
| ----- | ------------ | ---- |
| `gofumpt` + `goimports` | 10 s | every push |
| `golangci-lint` | 30 s | every push |
| `go vet` | 10 s | every push |
| `go test -race ./...` | 90 s | every push |
| `govulncheck` | 20 s | every push |
| `gitleaks` | 5 s | every push |
| `lint-docs.sh` | 5 s | every push |
| OpenAPI spectral lint | 10 s | PRs with `openapi/` changes |
| `make docs` idempotence | 30 s | PRs with `internal/config/`, `internal/obs/`, `openapi/` changes |

If the fast path ever exceeds 3 min, we split jobs rather than drop
checks.

---

## 20. Exit gates — when is the repo "healthy"?

Gates checked weekly by @ash as part of the Friday wrap-up.

- [ ] CI green on main for 7 consecutive days.
- [ ] No stale-doc warnings on `docs/architecture/` or `docs/operations/`.
- [ ] No `TODO(#0)` remaining in Go code (lint-docs.sh §5
      enforces this).
- [ ] Zero open `govulncheck` advisories.
- [ ] Coverage ≥ 70 % per package, ≥ 80 % for `canonical` /
      `aggregate` / `supply`.
- [ ] All feature flags have valid `Remove-by` ≤ 90 days future.
- [ ] Dependabot PR backlog ≤ 5.
- [ ] Every merged PR this week has a CHANGELOG entry.

Any unchecked → triage immediately. Never let a yellow become a red.

---

## 21. The meta-rule

**If a rule fires often and is always the wrong call, change the
rule via an ADR.** Do not suppress it locally. Do not argue in
comment threads. One suppression is a tactic; two suppressions is a
pattern; three is a broken rule.
