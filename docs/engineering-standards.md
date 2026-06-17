# Engineering standards

**Status:** Design-locked policy. Every PR, every release, every
piece of documentation, every doc reviewer, and every agent working
on this codebase is bound by this doc. The repo layout is the
skeleton; these standards are the muscle.

This project keeps the codebase free of technical debt, engineered to
an enterprise level, easily understood by AI agents, and resistant to
documentation drift. Four goals. This doc is accountable to each of
them, enumerated:

1. **Technical-debt-free** — never accumulate; pay as you go.
2. **Enterprise-grade** — operational rigour, not heroics.
3. **Agent-readable** — AI tooling can reason about the codebase
   without tribal knowledge.
4. **Doc-drift-proof** — every doc is either current, generated,
   or archived.

---

## 1. Core principle: boring over clever

Every "should we do X?" question defaults to:

> "Is there a dumber version that works? Use that."

A boring, well-documented pattern beats a clever, subtle one every
time. We reach for clever only when we've shown concretely that
boring fails — and we document that failure in an ADR so the
cleverness is justified.

The rest of this doc is mostly mechanical enforcement of this
principle.

---

## 2. Technical debt prevention

### 2.1. Definition of Done

A PR is mergeable **only** when all of these are true. CI enforces
the mechanical ones; reviewers enforce the judgement ones.

**Mechanical (CI-enforced):**

- [ ] `go vet` passes.
- [ ] `gofumpt -l` returns nothing.
- [ ] `golangci-lint run` passes (config in `.golangci.yml`).
- [ ] `goarchlint` passes — no illegal dependency crosses.
- [ ] Unit tests pass with `-race` flag.
- [ ] Coverage does not decrease on changed packages.
- [ ] `govulncheck` passes.
- [ ] `gitleaks` finds no secrets.
- [ ] New exported symbol has a doc comment (`golint` enforces).
- [ ] If `openapi/*.yaml` changed: Spectral lint passes + generated
      reference docs regenerated.
- [ ] If a config field added: `make docs-config` regenerates
      without delta.

**Judgement (reviewer-enforced):**

- [ ] New public API accompanied by usage doc (`docs/reference/` or
      `pkg/client/README.md`).
- [ ] Bug fix accompanied by a regression test.
- [ ] ADR proposed for any architectural decision (new source, new
      storage tier, new external dep).
- [ ] No `TODO` / `FIXME` without a linked issue number.
- [ ] No commented-out code except via `// nolint` with a reason.
- [ ] Runbook exists for any new alert.
- [ ] Docstrings explain **why**, not **what**.

### 2.2. Forbidden patterns

The following trigger automatic PR blocks:

- **`interface{}` / `any` in `pkg/*`** — public API must be
  strongly typed. In `internal/` allowed only with a code comment
  justifying it, nothing structural.
- **`panic()` outside `main()` and tests** — library code returns
  errors; `cmd/*` may panic only during startup.
- **`init()` functions that do more than assign constants or
  register standard handlers.** Hidden start-time magic is agent-
  hostile and test-hostile.
- **Goroutines without explicit context + shutdown** — every `go`
  statement must take `context.Context` and honour `ctx.Done()`.
- **SQL string concatenation** — parameterised queries only;
  `sqlclosecheck` lint catches unclosed rows.
- **`time.Now()` inside business logic** — clock passed in as
  dependency for testability.
- **Global mutable state** — use `sync.Map` or channel-based state
  if unavoidable; prefer explicit dependency injection.
- **Dependency with < 1 GitHub star or < 1 year of history** —
  unless justified in an ADR with the alternative considered.

### 2.3. `TODO` discipline

- Every `TODO` / `FIXME` / `XXX` in code has the form:

  ```go
  // TODO(#123): description
  ```

  where `#123` is a tracked issue. CI regex-checks this pattern.

- `TODO`s without issues are a **broken build**. We ship clean code
  or a tracked deficit, never a muddy middle.

### 2.4. Deprecation policy

1. Mark deprecated with a clear Godoc comment:

   ```go
   // Deprecated: use NewFoo (ADR-0017). Will be removed in v3.0.0.
   ```

2. Call sites in the repo migrate within the same minor version.

3. Public deprecations are documented in CHANGELOG **and** a
   dedicated `docs/reference/deprecations.md` table.

4. Removal at the next major version at the earliest — never
   sooner than 90 days after deprecation landed.

5. No "we've been planning to remove this for two years" —
   deprecations have a scheduled removal version from day one.

### 2.5. Dependency minimalism

- Every direct dep in `go.mod` has either:
  - An ADR justifying it, **or**
  - A single-line comment in `go.mod` pointing to the package
    using it (for trivial deps like `testify`).
- `go mod tidy` in CI fails the build if unused deps linger.
- `govulncheck` flags vulnerable versions; we patch within 7 days
  for HIGH/CRITICAL, 30 days for MEDIUM, at will for LOW.
- Transitive dep explosions (`go mod graph` > N nodes for a given
  leaf dep) trigger a review: is this library pulling too much?

### 2.6. Feature flag hygiene

Feature flags are **not** a tech-debt accumulator. Rules:

- Every flag has an owner, a default, and a **scheduled removal
  date** — a labelled issue that expires the flag.
- The flag registry `internal/config/flags.go` contains a struct
  with every flag + its metadata. CI builds a doc from it.
- A flag older than **90 days past its removal date** triggers a
  build warning. 180 days: build failure.
- Kill-switches (for ops) are a different flavour — they live
  forever but must be documented in a runbook.

### 2.7. No "temporary" workarounds

Every workaround for an upstream bug / limitation has:

1. A link to the upstream issue.
2. A code comment with the link.
3. A tracked issue in our tracker labelled `workaround`.
4. A removal test: when upstream fixes, CI finds the workaround
   and suggests removal.

We do not carry workarounds indefinitely.

### 2.8. Refactor-as-you-go

- If a module is confusing when you touch it, fix the confusion in
  the same PR **or** in an immediate follow-up within 1 week.
- Never "we'll clean this up later" as the last line of a PR
  description. Later is this PR or next sprint, explicitly
  scheduled.

### 2.9. Code review standards

- **Every PR is reviewed by a CODEOWNER** for the touched
  packages.
- **PRs > 500 LoC diff are split** unless reviewer explicitly
  agrees in advance.
- **Review within 24 h** (business days). Escalate in the team
  channel if stuck.
- **Approval != merge** — only the PR author merges, after
  approval, with CI green. No "emergency bypass" without the
  tech lead's explicit pre-authorised sign-off.

---

## 3. Enterprise-grade engineering practices

### 3.1. SLOs as code

- Every customer-facing endpoint has an SLO defined in
  `internal/obs/slo.go`:

  ```go
  var APIv1TradesSLO = obs.SLO{
      Name:           "api.v1.trades.latency.p95",
      Target:         200 * time.Millisecond,
      Window:         30 * 24 * time.Hour,
      ErrorBudget:    1.0 - 0.999,
      PageOnBurn:     obs.BurnRate2xOver1h,
  }
  ```

- Prometheus alerts are derived from this struct. Runbooks are
  linked from the struct's `Runbook` field.

- No ad-hoc alerts; everything flows from an SLO.

### 3.2. Runbooks, not tribal knowledge

- Every alert references a runbook path: `docs/operations/runbooks/<name>.md`.
- Every runbook references the SLO + alert it's the response to.
- CI enforces the bidirectional link.
- A runbook has a standard structure:

  ```markdown
  # Runbook: <alert name>

  ## Alert description
  ## When this fires
  ## What it means for users
  ## How to investigate (commands, dashboards)
  ## How to mitigate (commands, config toggles)
  ## How to escalate
  ## Post-mortem notes from prior firings
  ```

- Post-mortem notes append-only; old incidents provide context for
  new responders.

### 3.3. Change management

- **All production changes go through CI.** Zero SSH-to-box
  hotfixes.
- Emergency fixes have an expedited path (single reviewer, small
  PR) but still CI, still PR.
- Config changes go through `configs/*.yaml` with migrations
  testing; not "I edited it live."

### 3.4. Release discipline

- Tag = release. No "in-between unreleased in prod" state.
- Release notes are auto-generated from CHANGELOG's `[Unreleased]`
  section via the `release.yml` workflow.
- Rollback procedure documented per release in
  `docs/operations/rollback/<version>.md`.
- Every release has a "canary window" — 24 h on staging before
  production cutover.

### 3.5. On-call rotation

- At launch: @ash + @alex split 24/7.
- Post-launch: hire the third engineer, rotate 3-person weekly.
- **Never solo on-call longer than 1 week**.
- On-call escalation chain published at
  `docs/operations/escalation.md` — kept current as team changes.

### 3.6. Post-mortem policy

- Every SEV-1 → blameless post-mortem within 5 business days.
- Every SEV-2 → post-mortem within 10 business days.
- Post-mortem template in `docs/operations/post-mortems/_template.md`.
- Published post-mortems (redacting sensitive info) in
  `docs/operations/post-mortems/<date>-<slug>.md`.
- Action items from post-mortems tracked as issues; never "we'll
  remember to fix that."

### 3.7. Capacity review on every release

A 1-paragraph note in every release:

```
Traffic headroom: currently serving P of peak; capacity for N×P.
Timescale storage headroom: X TB used / Y TB capacity.
Trigger for next scale-out: Z.
```

Prevents the "we were surprised by growth" class of incident.

### 3.8. Vendor / supplier review

- **Annual review of every external dependency** (cloud, colo,
  secret manager, CDN, FX/CEX data provider).
- Check: pricing changes, API deprecations, SLA changes, security
  advisories, competitive alternatives.
- Output: `docs/operations/vendor-review-<year>.md`.

---

## 4. Agent-readability

New constraint from the modern reality of AI-assisted coding. The
repo must be navigable by an LLM (Claude, an agent framework,
a GPT-derived assistant) without that assistant needing tribal
knowledge.

### 4.1. `CLAUDE.md` at repo root

File name is `CLAUDE.md` (matches withObsrvr and others' convention
— we've seen it read by agents in the wild). Its job:

> "If an AI agent opens this repo cold, this file tells them
> everything they need to find their way."

Contents:

```markdown
# CLAUDE.md — repo orientation for AI agents

## What this repo is

Stellar Index — a Stellar protocol explorer and asset pricing API.
Ingests on-chain and off-chain price data, aggregates into
VWAP/TWAP/OHLC, serves via REST + SSE.

## Build + test commands

make dev            # stand up full stack locally
make test           # unit tests, ~2 min
make lint           # golangci-lint + gofumpt check
make build          # all binaries into bin/
make docs-all       # regenerate docs/reference/

## Repo map

cmd/                one dir per binary (indexer / aggregator / api / ops / migrate)
internal/           private packages; cannot be imported externally
  canonical/        core types: Trade, Price, Asset, Pair, Amount
  sources/<proto>/  one package per on-chain or off-chain source
  aggregate/        VWAP/TWAP/outlier/triangulation
  storage/          TimescaleDB + Redis + MinIO adapters
  api/              REST handlers (v1)
pkg/                public surface: client SDK + stable types
docs/               architecture + ADR + operations + reference
migrations/         golang-migrate SQL migrations
openapi/            the API spec (source of truth for reference docs)

## Invariants that must never be violated

1. **i128 / u128 are never int64.** Token amounts, reserves,
   prices from Soroban are `*big.Int` in Go, NUMERIC in Postgres,
   strings in JSON. See ADR-0003.
2. **Horizon is not a component.** We do not ingest from Horizon,
   run Horizon, or proxy to Horizon. See ADR-0001.
3. **MinIO, not local filesystem, for Galexie storage.** See
   ADR-0002.
4. **Every exported symbol has a Godoc comment.** Lint-enforced.
5. **No new interface{}/any in pkg/*.** See §2.2 of engineering-standards.md.

## Common tasks

- Add a new CEX connector: see docs/development/contributing-a-source.md.
- Add a new on-chain DEX: same doc.
- Investigate a price divergence: docs/operations/runbooks/price-divergence.md.
- Why is `<metric>` alerting: docs/operations/runbooks/<metric>.md.

## Known footguns

- SoroswapPair.SwapEvent has no reserves — use the sibling SyncEvent
  correlated by (ledger, tx, op_index). See internal/sources/soroswap/README.md.
- Phoenix emits 8 events per swap — use internal/sources/phoenix/correlator.go.
- Reflector v3 has no on-chain twap/x_*. We compute locally.

## Where to ask for help

- Code review: CODEOWNERS
- Operations: the #ops channel
- Architecture: ADR process (docs/adr/)
```

This file is maintained by hand; its freshness is checked in CI
(same 90-day rule).

### 4.2. Package-level `doc.go` with `CLAUDE.md`-style intro

Every `internal/` package has a `doc.go` file (Go-native equivalent
of README) with:

```go
// Package sources/soroswap indexes trade and reserve-state events
// from the Soroswap DEX on Stellar Soroban mainnet.
//
// # Architecture
//
// The package subscribes to the Soroswap factory contract's
// `("SoroswapFactory", "new_pair")` events to discover new pairs,
// then subscribes to each pair's `("SoroswapPair", "swap")` and
// `("SoroswapPair", "sync")` events. Swap events and the
// immediately-following Sync events are correlated by
// (ledger, tx_hash, op_index) to produce a CanonicalTrade with
// both the executed amounts and the post-state reserves.
//
// # Public entry points
//
//   - New(deps Deps) Source              — constructs a Source
//   - (*source).StreamLive(ctx, out)     — live subscription
//   - (*source).BackfillRange(ctx, ...)  — historical replay
//
// # Invariants
//
//   - All amounts are decoded as *big.Int (i128). Never int64.
//   - SwapEvent is always paired with a SyncEvent; orphan
//     events are logged and dropped (never fabricated).
//
// # See also
//
//   - docs/architecture/sources/soroswap.md
//   - ADR-0003 (i128 invariant)
//
package soroswap
```

This doc.go is `godoc`-rendered + AI-scannable. Same content
appears in `docs/architecture/sources/soroswap.md` but the source
of truth is the doc.go so drift is impossible.

### 4.3. Consistent naming — no abbreviations

- Exported types: `CanonicalTrade`, not `CTrade` or `CanoTrade`.
- Packages: `canonical`, `aggregate`, `storage` — one word,
  full word, always lowercase.
- Test names: `TestName_Scenario_ExpectedBehaviour` so you can
  grep for behaviour across the codebase.
- Metric names: `stellarindex_<subsystem>_<noun>_<unit>` — e.g.
  `stellarindex_ingest_trades_total{source="soroswap"}`.

### 4.4. Structured logs with stable field names

Every log call uses a slog-style structured logger. Field names
are enumerated in `internal/obs/logfields.go`:

```go
const (
    FieldSource      = "source"        // always the source name
    FieldLedgerSeq   = "ledger_seq"    // uint32
    FieldTxHash      = "tx_hash"       // string, hex
    FieldOpIndex     = "op_index"      // int
    FieldAssetKey    = "asset_key"     // canonical asset identifier
    FieldError       = "error"         // %w-wrapped
    // ...
)
```

Any new log field added in a PR must first appear here. Grep
across the repo by field name is reliable, for humans and agents.

### 4.5. Explicit error types

Errors are not just strings. We have a small error taxonomy in
`internal/canonical/errors.go`:

```go
var (
    ErrUnknownAsset     = errors.New("canonical: unknown asset")
    ErrInvalidAmount    = errors.New("canonical: invalid amount")
    ErrI128Overflow     = errors.New("canonical: i128 overflow (this is always a bug)")
    // ...
)
```

Every error returned from internal code either IS one of these
sentinels or WRAPS one. Callers use `errors.Is` to classify.

### 4.6. Machine-readable contracts everywhere

- API: OpenAPI `openapi/stellar-index.v1.yaml`. Source of truth.
  Handlers validated against it in tests.
- Config: JSON-Schema generated from Go struct tags at
  `docs/reference/config/schema.json`.
- Metrics: Prometheus registry dumped to
  `docs/reference/metrics/registry.json`.
- Events we emit: JSON Schema per event type in
  `docs/reference/events/`.

An agent writing a client never guesses shapes.

### 4.7. No hidden conventions

If a pattern is important, it's documented. Examples we make
explicit:

- **`ctx.Context` is always the first argument.** Documented in
  Go style guide we inherit.
- **Errors returned wrapped with `fmt.Errorf("some op: %w", err)`
  format.** Documented in `CONTRIBUTING.md`.
- **Every source's `New(deps Deps)` returns the `Source`
  interface, not the concrete type.** Documented in each
  package's `doc.go`.

### 4.8. Small, focused functions

- Soft limit: 50 lines per function. 100 is a code smell needing
  justification.
- Cyclomatic complexity limit via `gocognit` at 15.
- Long functions get a block-comment table of contents at the top:

  ```go
  // Reconcile walks the three phases:
  //   1. Load in-flight cursor.
  //   2. Fetch any ledger gap from bronze.
  //   3. Materialise silver.
  ```

### 4.9. Prefer composition over generics

Go generics are useful sparingly. Over-using them produces code an
LLM has a harder time reasoning about. Rule: use generics only when
the alternative is interface{}-based type-assertion. Otherwise,
concrete types.

### 4.10. Tests as documentation

- Every exported function has at least one table-driven test with
  named cases.
- Test-case names describe behaviour:

  ```go
  tests := map[string]struct{…}{
      "classic_order_book_trade_produces_canonical_trade":      {...},
      "liquidity_pool_claim_atom_produces_lp_trade":            {...},
      "protocol_18_pre_activation_skips_lp_variant":            {...},
  }
  ```

- Reading tests alone should be enough to understand the public
  API.

---

## 5. Doc-drift prevention (concrete)

The concrete mechanics of keeping documentation current, with
agent-readability additions.

### 5.1. Every doc has exactly one status

Frontmatter YAML:

```yaml
---
title: Soroswap indexing
last_verified: 2026-04-22
verified_by: ash
owners: ['@ash']
supersedes: []
status: current | generated | archived
---
```

- `current` — hand-edited, subject to 90-day staleness check.
- `generated` — regenerated by build step; hand-edits are
  clobbered.
- `archived` — frozen; no staleness check.

One of three, never unlabelled.

### 5.2. Three enforcement layers

1. **CI lint.** `scripts/ci/check-doc-freshness.sh` scans all
   `current` docs, warns at 90 days stale, fails at 180.
2. **Reviewer checklist.** Every PR touching code in a package
   must confirm the package's doc is current (box ticked in PR
   template).
3. **Quarterly sweep.** One calendar day per quarter to walk all
   `current` docs and decide: refresh, archive, delete.

### 5.3. "Never two sources of truth"

If doc A says "the default is 30s" and doc B says "the default is
60s", someone is wrong. Enforcement:

- **Configs:** one YAML with defaults; all docs regenerate from it.
- **Metrics:** one Prometheus registry; all docs regenerate from it.
- **API:** one OpenAPI spec; all docs regenerate from it.
- **Events:** one JSON Schema set; all docs regenerate.

If you find yourself copy-pasting between docs, that's the signal
to factor into a generated source.

### 5.4. "Explain why, not what"

- **What** the code does is visible in the code. Don't duplicate
  in docs.
- **Why** the code does it — the design constraint, the incident
  that motivated a check, the Stellar CAP that requires a decoder
  variant — is invisible. That's what docs are for.

A doc that describes only what the code does is dead weight and
invites drift.

### 5.5. ADR, not casual doc, for decisions

Any time a doc says "we decided" or "we chose X over Y," it is
either an ADR or contains a link to an ADR. Casual narrative about
decisions in architecture docs rots fast; ADRs don't rot because
they don't change.

### 5.6. Auto-generated docs are banner-marked

Every generated file begins:

```markdown
<!-- GENERATED FILE - DO NOT EDIT. Regenerate with `make docs-<name>`. -->
```

Hand-edits are CI-deleted on next regeneration. No "I'll just fix
the typo in the generated file" path.

### 5.7. Doc ownership tracked

CODEOWNERS applies to docs. If you own a package, you own its
architecture doc. If the package changes materially, you update
the doc in the same PR.

---

## 6. Enforcement mechanisms

Summary of how we catch violations. Each link below points to the
mechanism in the codebase.

| Standard | Mechanism | Location |
| -------- | --------- | -------- |
| Boring-over-clever | Code review | CODEOWNERS |
| Definition of Done | CI + PR template | `.github/` |
| Forbidden patterns | `golangci-lint` custom rules | `.golangci.yml` |
| TODO discipline | CI regex check | `scripts/ci/check-todo-tracking.sh` |
| Deprecation policy | CI scan on `Deprecated:` | `scripts/ci/check-deprecations.sh` |
| Dependency minimalism | `go mod tidy` + `govulncheck` | `security.yml` |
| Feature flag hygiene | CI scan of flag registry | `scripts/ci/check-flag-age.sh` |
| SLOs as code | `internal/obs/slo.go` struct + Prometheus derivation | `docker/prometheus/` |
| Runbook ↔ alert link | CI bidirectional check | `scripts/ci/check-runbook-links.sh` |
| Doc freshness | CI scan | `scripts/ci/check-doc-freshness.sh` |
| Doc-code citation validity | CI scan | `scripts/ci/check-doc-code-links.sh` |
| Never two sources of truth | Generated-file regen on release | `release.yml` |
| Generated-file banner | CI scan for banner | `scripts/ci/check-generated-banner.sh` |
| Agent orientation | `CLAUDE.md` + `doc.go` per package | convention + CI |
| Structured log fields | Grep-based check in CI | `scripts/ci/check-log-fields.sh` |

If a mechanism doesn't exist, it's a gap to be filled — we don't
rely on reviewer vigilance alone for anything in this table.

---

## 7. When standards conflict with velocity

Real-world scenarios:

### Scenario A: we found a production bug

- Hotfix is small: normal PR, expedited review, normal CI. No
  bypass.
- Hotfix must be large: engage tech lead, split into minimum-viable-fix
  + follow-up cleanup PRs. Still CI, still review.
- Never a "ship and fix standards later" — fix later never comes.

### Scenario B: a feature is needed for a demo in 2 days

- Unless the feature has a standards-violating shortcut that's
  faster, we don't skip standards.
- If the shortcut saves > 1 day AND the tech lead agrees AND the
  debt is tracked as a `P0 cleanup` issue with scheduled
  resolution within 2 weeks: documented exception in PR
  description. Shipped.
- If any of the three conditions fails: do the standards-compliant
  version, adjust scope or demo date.

### Scenario C: an external dep's API changes on short notice

- Pin the old version, add workaround with tracked removal (§2.7).
- If the old version is vulnerable: upgrade with workaround + track
  the cleanup.
- Never carry workarounds silently.

### Scenario D: someone merges a standards violation

- Not a blame event — standards slip because enforcement had a
  gap. Add the enforcement gap to the backlog.
- Violation gets a follow-up PR within the same sprint; tech lead
  pairs with author on the fix.

**Escalation path:** violations that happen > 2× in a month
trigger a root-cause discussion at the monthly retro. If the
standard is the problem (too burdensome, too vague), change the
standard via this doc's amendment process.

---

## 8. Amending this doc

- Every change to `engineering-standards.md` is a PR with the tech
  lead as approver.
- Changes land only when the team agrees in writing in the PR.
- CHANGELOG entry under `[Unreleased]` → `### Changed` documents
  the standard change.
- Standards that are relaxed have a rationale in the PR body
  (never "we got lazy").

This is itself a **living document**. It drifts if we let it.
Quarterly doc-hygiene sweep touches this file too.

---

## 9. Periodic reviews

### 9.1. Weekly (15 min, Friday)

- Run `scripts/ci/check-doc-freshness.sh` locally; note anything
  approaching 90-day stale.
- Skim the flag registry for expired flags.
- Review any `P0 cleanup` issues opened during the week.

### 9.2. Monthly (1 h, first Monday)

- Retrospective: any standards violations this month? Root cause?
- Review the runbook set: any alerts without runbooks?
- Review the dependency graph: any new deps without ADRs?

### 9.3. Quarterly (1 day, end of quarter)

- Full doc-hygiene sweep.
- Full vendor review.
- Full dependency audit.
- Full ADR review: any superseded without explicit linkage?
- `engineering-standards.md` review: still right?

### 9.4. Annually

- Full security audit (external).
- Full capacity review.
- Full licensing + redistribution review on data providers.

Each review has an output committed to
`docs/operations/reviews/<date>-<type>.md`.

---

## 10. Checklist: am I compliant?

A quick self-check before every PR and every demo. If any box is
unchecked, pause.

### Code

- [ ] `make lint` clean.
- [ ] `make test` green.
- [ ] Coverage not dropped.
- [ ] No `TODO` without issue link.
- [ ] No `interface{}` / `any` in new public API.
- [ ] Every new exported symbol has a Godoc comment.
- [ ] Every new goroutine takes `ctx` + honours `ctx.Done()`.
- [ ] Every new `time.Now()` came from an injected clock.

### Docs

- [ ] If code changed, package doc updated or `last_verified` bumped.
- [ ] If API changed, OpenAPI updated + regen'd ref.
- [ ] If config changed, struct tags + regen'd ref.
- [ ] New architectural decision has an ADR.

### Ops

- [ ] New alert has a runbook.
- [ ] New metric has a docstring and appears in
      `docs/reference/metrics/`.
- [ ] New feature flag has an expiry.

### Review-readiness

- [ ] PR description includes the "why".
- [ ] PR < 500 LoC or pre-agreed with reviewer.
- [ ] No secrets in diff, no secrets in logs.

---

## 11. Agent-specific onboarding shortcut

If you are an AI agent reading this file cold, the **fastest
orientation** to actual work is:

1. Read `CLAUDE.md` at repo root. (Not this file — that one.)
2. Run `make help` — list of targets.
3. Look at `docs/architecture/overview.md` + the most recent ADR.
4. Identify the package you'll modify; read its `doc.go` first.
5. `git log -p --since='3 months'` on that package to see recent
   patterns.
6. Write the smallest possible PR. Consult this doc only when
   uncertain.

This doc (`engineering-standards.md`) is the **policy**. `CLAUDE.md`
is the **map**. Use the map first; consult the policy when the
map isn't enough.

---

## 12. What this doc deliberately doesn't decide

- **Language-specific style** beyond what's listed here — we lean
  on `gofumpt`, `goimports -local`, and `golangci-lint`; their
  rules are the style.
- **Code of conduct** beyond the Contributor Covenant in
  `CODE_OF_CONDUCT.md`.
- **Hiring standards** — those live in hiring docs.
- **Compensation / equity / legal** — not engineering policy.

Anything not in this doc is not a standard. If something should
be a standard, open a PR amending this file.

---

## 13. Related docs

- [CLAUDE.md](../CLAUDE.md) — the repo orientation map this policy
  operates over.
- [docs/adr/](adr/) — the specific architectural choices the
  standards reinforce.
- [docs/architecture/semver-policy.md](architecture/semver-policy.md)
  — the versioning policy these standards back.
