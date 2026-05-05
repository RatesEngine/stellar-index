---
title: SemVer policy for Rates Engine
last_verified: 2026-05-05
status: ratified
---

# SemVer policy

The Rates Engine ships **two version dimensions**, each governed by
SemVer (`vX.Y.Z`) but with independent tag namespaces and
independent semantics.

| Surface | Tag form | Bump rules |
|---|---|---|
| **`pkg/*` Go modules** (e.g. `pkg/client`) | `pkg/<name>/vX.Y.Z` | Standard Go-module SemVer (API surface) |
| **Binary releases** (`ratesengine-api`, `ratesengine-indexer`, …) | `vX.Y.Z` (root tag) | Operator-impact SemVer (config / wire / behaviour) |

The two clocks tick independently. A binary release `v0.4.0` may
contain `pkg/client v0.2.1` while bundling unchanged versions of
any other `pkg/*` modules. The `CHANGELOG.md` entry for that
release lists the new `pkg/*` versions it contains.

## SemVer rules for `pkg/*`

### What's covered

Every package under `pkg/` is part of the public API surface and
is bound by the rules below. **`internal/*` is NOT** — internal
packages can be refactored, renamed, or deleted in any PR.

Currently shipped:
- `pkg/client` — Go SDK for the public API
  ([#201](https://github.com/RatesEngine/rates-engine/pull/201)).
  Wire-shape types (`Envelope`, `Flags`, `Pagination`,
  `AssetDetail`, …) live in `pkg/client/types.go` rather than a
  separate `pkg/types` package — see CLAUDE.md "Repo map" for the
  rationale. The server's `internal/api/v1` defines its own
  envelope intentionally; the duplication is the SemVer firewall
  between the SDK's public surface and internal handler shapes.

### What constitutes a breaking change

Any of the following bumps the **major** version:

1. Removing or renaming a public identifier (type, function, variable, constant, method)
2. Removing a struct field, method receiver, or interface method
3. Changing a function/method signature in a non-additive way (changing parameter types, return types, or order)
4. Adding a method to an interface (existing implementers stop satisfying the interface)
5. Changing the JSON wire shape produced by a public type's `MarshalJSON` (or its generated default)
6. Tightening input validation in a way that rejects previously-accepted inputs
7. Changing the documented error semantics — e.g. a function that previously returned `nil, ErrNotFound` now returns `nil, nil`

Any of the following bumps the **minor** version:

1. Adding a new exported identifier
2. Adding a new field to a struct (with a sensible zero value)
3. Loosening input validation
4. Adding a new optional configuration field
5. Adding a new error sentinel that's a *more specific* version of an existing one (callers using `errors.Is` against the existing sentinel still match the new one)

Any of the following is **patch**-only:

1. Bug fixes that preserve documented behaviour
2. Performance improvements with no API change
3. Documentation-only changes
4. Test-only changes
5. Internal refactoring with no `pkg/*` impact

### Pre-v1.0 (`v0.x`) policy

`pkg/client` is currently `v0.1.0`. Until we tag `v1.0.0`:

- Breaking changes are allowed but MUST be called out in `CHANGELOG.md` under the version where they land
- Each breaking change should bump the *minor* version (`v0.1 → v0.2`), not the major — Go modules treat `v0.x` as inherently unstable per the spec
- Public-facing release notes flag every breaking change loudly

When we tag `v1.0.0` (target: end of public-launch week), the
contract becomes binding — breaking changes after that require a
new major version (`v2.0.0`).

### Deprecation policy

When a `pkg/*` identifier is destined for removal:

1. Mark the identifier with a `// Deprecated: <reason>. Use <replacement>.` godoc comment in the same release
2. Keep it in place for at least **one minor version**
3. Remove only at the next **major version** boundary
4. CHANGELOG entry under the deprecating release calls it out; release notes for the removing release reiterate it

Example:

```go
// Deprecated: use Client.PriceTip instead. Removed in v2.0.0.
func (c *Client) PriceLive(ctx context.Context, asset string) (*Envelope[PriceSnapshot], error) {
    return c.PriceTip(ctx, PriceQuery{Asset: asset})
}
```

### Tagging mechanics

Go modules take version info from git tags of the form
`pkg/<name>/v<major>.<minor>.<patch>`:

```sh
# Bump pkg/client to v0.2.0
git tag pkg/client/v0.2.0
git push origin pkg/client/v0.2.0
```

Pre-tag manual checks (the
[release runbook](../operations/release-process.md) §"Pre-flight"
captures the same set for the binary clock):

- Working tree matches `main` and the tagged commit (`git status`
  is clean; `git log -1` is the commit you intend to tag).
- `CHANGELOG.md` has an entry under the new version with the PRs
  it includes.
- The package's own version constant (if any) matches the tag.
- `make test` is green at the tagged commit.

---

## SemVer rules for binary releases

### Format

`vX.Y.Z`:

- **`X` (major)** — bumped when an operator MUST take action beyond the standard restart to upgrade (config schema break, removed endpoint, removed CLI flag, manual data backfill required, breaking wire-shape change)
- **`Y` (minor)** — bumped on additive changes that need no operator action (new endpoint, new optional config field, new source connector, new aggregation behaviour with safe defaults)
- **`Z` (patch)** — bumped on operator-invisible changes (bug fixes, performance, internal refactoring, doc-only)

Examples:
- `v0.1.0` — initial public release
- `v0.2.0` — adds new SSE endpoint (additive)
- `v0.2.1` — patch fix for an aggregator off-by-one
- `v1.0.0` — first stable cut, contract becomes binding

### Pre-v1.0 (`v0.x`) policy

Until we tag `v1.0.0`:

- Breaking changes bump the **minor** version (`v0.1.x → v0.2.0`), matching the `pkg/*` pre-v1 convention. Major bump is reserved for the v1.0 cut.
- The CHANGELOG entry under the breaking version MUST call out the operator action explicitly (config edit, migration, etc.).
- Release notes lead with the breaking change in the summary paragraph.

### What constitutes a breaking change for binaries

Any of the following bumps minor (pre-v1) or major (post-v1):

1. **Config schema break** — a field in `/etc/ratesengine.toml` is removed, renamed, or its default semantics change in a way that affects existing operator configs
2. **API wire-shape change** — JSON response shape changes for an existing endpoint (field removed, field renamed, type changed)
3. **API endpoint removal or rename** — operators with hardcoded URLs break
4. **CLI flag removal** — operators with hardcoded systemd unit `ExecStart=` lines break
5. **DB migration that requires manual backfill** — `ratesengine-migrate up` is not sufficient; operator must run a separate one-off SQL/script
6. **Source-connector removal** — an enabled source goes away; operators relying on its data must reconfigure
7. **Behaviour change in fallback semantics** — VWAP→TWAP→last-trade fallback chain behaves differently in a way operators must learn

Any of the following bumps the **minor** version (additive):

1. New API endpoint
2. New CLI flag (with safe default if omitted)
3. New `/etc/ratesengine.toml` field (with safe default if omitted)
4. New source connector (`enabled = false` by default — see `[external]` block convention)
5. New aggregation feature behind an opt-in flag
6. New migration that runs forward-only via `ratesengine-migrate up`
7. New observability metric

Any of the following is **patch**-only:

1. Bug fixes that preserve documented behaviour
2. Performance improvements with no operator-visible change
3. Internal refactoring (`internal/*` churn)
4. Documentation-only changes
5. Test-only changes
6. Dependency bumps that don't change behaviour
7. Re-deploy of identical functionality (e.g. rebuild from same code with newer Go toolchain)

### Tagging

Single repo-level tag at the commit you want to release:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The release builds every binary at this commit. `ratesengine-api
--version` and `ratesengine-indexer --version` both report
`v0.2.0` for that release. The Makefile's `git describe --tags
--always --dirty` populates `internal/version.Version` at build
time via `-ldflags`.

### What goes in a binary release note

Every release note (under `## [<version>]` in CHANGELOG.md) MUST
include:

1. **Stellar protocol version** the release was tested against (e.g. `Tested against pubnet protocol 23`)
2. **`pkg/*` versions** included (e.g. `Includes pkg/client v0.4.2`)
3. **Migration notes** for any change that affects operators (config schema additions, DB migrations, runbook changes). If none, write "None."
4. **The standard Added/Changed/Deprecated/Removed/Fixed/Security sections**
5. **Operator action required: yes/no** on the first line — operators reading at-a-glance need to know whether the upgrade is "restart and done" or "edit config first"

### Why SemVer (not CalVer) for binaries

We considered CalVer (`YYYY.MM.DD.N`) and switched to SemVer for the
binary clock to match the `pkg/*` clock and to give operators a
single mental model: **"is this a `vX.0.0` cut? must I edit my
config?"** is more useful than "is this newer than what I'm running?"
when releases land 2-3× per week.

The release-process runbook still records every cut's UTC date in
the CHANGELOG section header so the calendar dimension is preserved
in human-readable form (`## [v0.2.0] — 2026-07-15`).

---

## Stability tiers within `internal/*`

`internal/*` is not version-controlled in the SemVer sense, but
some packages are more refactor-safe than others:

| Package | Stability | Refactor cost |
|---|---|---|
| `internal/canonical` | **High** — changes ripple through every source | Coordinated rename PR |
| `internal/api/v1` | **High** — wire-shape changes break clients | New endpoint instead of field-shape change |
| `internal/aggregate` | **Medium** — internal consumers only | Standard PR review |
| `internal/sources/*` | **Low** — per-source decoders churn frequently | Author + CODEOWNER review |
| `internal/divergence`, `internal/aggregate/anomaly`, `internal/aggregate/baseline`, `internal/aggregate/confidence`, `internal/aggregate/freeze`, `internal/archivecompleteness` | **Low** — recent additions, expected to grow | Standard PR review |

This isn't a SemVer commitment — it's review-effort guidance. A
PR touching `internal/canonical.Trade`'s field set should land
with explicit migration notes for every consumer; a PR adding a
new source in `internal/sources/<venue>/` is the normal flow.

---

## Cross-references

- [ADR-0005](../adr/0005-monorepo.md) — monorepo / one-Go-module decision; the SemVer commitment on `pkg/*` lives here
- [`docs/discovery/repo-structure-plan.md`](../discovery/repo-structure-plan.md) §10 — original rationale for the dual-versioning split (then CalVer; superseded by this doc 2026-05-05)
- [`docs/operations/release-process.md`](../operations/release-process.md) — runbook the release engineer follows; implements this policy
- [`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md) — fill-in template for GitHub Release notes
- [`CHANGELOG.md`](../../CHANGELOG.md) — every release's entry follows the rules above
- [`pkg/client/doc.go`](../../pkg/client/doc.go) — package-level statement of v0.x stability promise
