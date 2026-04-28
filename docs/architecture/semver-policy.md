---
title: SemVer + CalVer policy for Rates Engine
last_verified: 2026-04-28
status: ratified
---

# SemVer + CalVer policy

The Rates Engine ships **two version dimensions** simultaneously,
each governing a different surface:

| Surface | Versioning | Why |
|---|---|---|
| **`pkg/*` Go modules** (e.g. `pkg/client`) | SemVer (`v1.2.3`) | These imports are consumed by external Go programs. Compat promise per [ADR-0005](../adr/0005-monorepo.md). |
| **Binary releases** (`ratesengine-api`, `ratesengine-indexer`, …) | CalVer (`2026.06.30.1`) | Operators care about "when did we ship this build", not numerical compatibility. |

The two clocks tick independently. A binary release `2026.07.15.1`
may contain `pkg/client v0.4.2` AND `pkg/types v0.7.0`. The
`CHANGELOG.md` entry for that release lists the new `pkg/*`
versions it contains.

## SemVer rules for `pkg/*`

### What's covered

Every package under `pkg/` is part of the public API surface and
is bound by the rules below. **`internal/*` is NOT** — internal
packages can be refactored, renamed, or deleted in any PR.

Currently shipped:
- `pkg/client` — Go SDK for the public API ([#201](https://github.com/RatesEngine/rates-engine/pull/201))

Planned:
- `pkg/types` — wire types shared between SDK + server (deferred until refactor; today the SDK duplicates types deliberately to keep the skeleton focused)

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

The repo's release process runs `make verify-tag <tag>` before
pushing — confirms the working tree matches the tag, the
CHANGELOG has an entry, and the package's own version constant
(if any) matches.

---

## CalVer rules for binary releases

### Format

`YYYY.MM.DD.N`:

- `YYYY.MM.DD` — UTC date the release was cut
- `.N` — incrementing counter for releases on the same day (`.1`, `.2`, …)

Examples:
- `2026.06.30.1` — initial public release
- `2026.07.02.1` — second release, two days later
- `2026.07.02.2` — same day, second cut (e.g. quick rollback fix)

### Tagging

Single repo-level tag:

```sh
git tag 2026.07.15.1
git push origin 2026.07.15.1
```

The release builds every binary at this commit. `ratesengine-api
--version` and `ratesengine-indexer --version` both report
`2026.07.15.1` for that release.

### What goes in a CalVer release note

Every release note (under `## [<version>]` in CHANGELOG.md) MUST
include:

1. **Stellar protocol version** the release was tested against (e.g. `Tested against pubnet protocol 23`)
2. **`pkg/*` versions** included (e.g. `Includes pkg/client v0.4.2`)
3. **Migration notes** for any `internal/*` refactor that affects operators (config schema changes, DB migrations, runbook changes)
4. **The standard Added/Changed/Deprecated/Removed/Fixed/Security sections**

### Why CalVer for binaries

Operators answer "should I upgrade?" with "is it newer than what
I'm running?" — calendar dates make that comparison trivial.
SemVer for binaries would force us to debate whether each release
is "breaking enough" for a major bump, which doesn't help anyone
who just wants to know "is the deploy I'm running 6 weeks old?"

CalVer also avoids the trap of the SemVer treadmill where every
backwards-compatible behaviour change has to be reasoned about as
"is this minor or patch?". For binaries we don't care; for `pkg/*`
we do, and that's exactly where SemVer lives.

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
| `internal/divergence`, `internal/anomaly`, `internal/archivecompleteness` | **Low** — recent additions, expected to grow | Standard PR review |

This isn't a SemVer commitment — it's review-effort guidance. A
PR touching `internal/canonical.Trade`'s field set should land
with explicit migration notes for every consumer; a PR adding a
new source in `internal/sources/<venue>/` is the normal flow.

---

## Cross-references

- [ADR-0005](../adr/0005-monorepo.md) — monorepo / one-Go-module decision; the SemVer commitment on `pkg/*` lives here
- [`docs/discovery/repo-structure-plan.md`](../discovery/repo-structure-plan.md) §10 — original rationale for the dual-versioning split
- [`docs/operations/release-process.md`](../operations/release-process.md) — runbook the release engineer follows; implements this policy
- [`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md) — fill-in template for GitHub Release notes
- [`CHANGELOG.md`](../../CHANGELOG.md) — every release's entry follows the rules above
- [`pkg/client/doc.go`](../../pkg/client/doc.go) — package-level statement of v0.x stability promise
