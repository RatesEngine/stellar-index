# Contributing to Rates Engine

Thanks for your interest in contributing.

This project takes enterprise-grade engineering standards seriously.
Before you write a line of code, please skim:

1. **[CLAUDE.md](CLAUDE.md)** — repo orientation, layout, invariants.
2. **[docs/discovery/engineering-standards.md](docs/discovery/engineering-standards.md)** — the policy.
3. **[docs/discovery/repo-structure-plan.md](docs/discovery/repo-structure-plan.md)** — the layout rationale.

This document is a summary; the full policy is in the links above.

---

## Before you start

### Sign the CLA

(To be added — currently Apache-2.0 contributions imply standard
ICLA terms; formal CLA may be introduced as the project matures.)

### Discuss substantive changes first

For anything larger than a bugfix or a typo, **open an issue or
draft PR first**. Architectural changes require an
[ADR](docs/adr/) draft before code review.

### Pre-v1 reality

We are pre-v1. Breaking changes to internal APIs are allowed on
any PR, provided consumers in this repo are updated in the same
PR. Changes to `pkg/*` break only on major version bumps (see
CHANGELOG).

---

## Development setup

### Prerequisites

- Go 1.25 or later.
- Docker + Docker Compose (for integration tests and local stack).
- Make.

### First-time setup

```sh
git clone https://github.com/RatesEngine/rates-engine
cd rates-engine
make deps      # download Go modules + tools
make verify    # canonical pre-push gate — fmt, vet, lint, doc-lint,
               # import-lint, openapi-url-lint, monitoring-rules
               # (graceful-skip if promtool is missing locally),
               # unit tests, integration-build smoke
```

### Local full stack

```sh
make dev            # docker-compose up Postgres+Timescale, Redis, MinIO
make dev-seed       # load fixture data
make dev-teardown   # shut it all down
```

If `make dev` fails out of the box, that's a bug — file an issue.

---

## The workflow

1. **Branch from `main`.** No long-lived feature branches.
2. **Small PRs.** Soft limit 500 LoC. PRs larger than this need
   pre-agreement from a reviewer.
3. **Run the canonical pre-push gate.**
   ```sh
   make verify
   ```
   This runs `scripts/dev/verify.sh` — fmt, vet, lint, doc-lint,
   import-lint, openapi-url-lint, monitoring-rule validation
   (graceful-skip if promtool isn't installed), unit tests, and
   the integration-build smoke check (compile-only, no Docker).
   Mirrors the parallel jobs CI runs on every PR; running it
   locally surfaces failures one at a time before push. Don't
   substitute `make lint && make test` — it skips the doc /
   import / openapi / monitoring lints that CI enforces.
4. **Open a PR**; fill in the template.
5. **CI must be green.** No merging with red CI.
6. **A CODEOWNER must review.** See [CODEOWNERS](CODEOWNERS).
7. **Author merges after approval** (not the reviewer). Rebase +
   squash if your commits are messy.

---

## Definition of Done

A PR is merge-ready only when **all** are true. The mechanical
checks are enforced by CI; the judgement checks by your reviewer.

### Mechanical

- [ ] `make verify` passes (covers `make lint`, `make test -race`,
      doc / import / openapi / monitoring lints, and the
      integration-build smoke check).
- [ ] Coverage does not decrease on changed packages.
- [ ] `govulncheck` / `gitleaks` / `gosec` clean.
- [ ] Every new exported symbol has a Godoc comment.
- [ ] Every `TODO` / `FIXME` is in the form `TODO(#123): …`.
- [ ] If OpenAPI changed, reference docs regenerate cleanly.
- [ ] If config changed, config reference regenerates cleanly.

### Judgement

- [ ] Docstrings explain **why**, not **what**.
- [ ] If it's a bugfix, there's a regression test.
- [ ] If it's an architectural change, there's an ADR.
- [ ] Every new alert has a runbook.
- [ ] New feature flags have a scheduled removal date.

Full rules: [engineering-standards.md §2.1](docs/discovery/engineering-standards.md).

---

## Adding a source connector

CEX, DEX, AMM, FX, oracle — same pattern. See:

- [CLAUDE.md](CLAUDE.md) → "Common task recipes."
- [docs/architecture/ingest-pipeline.md](docs/architecture/ingest-pipeline.md)
  — the binding rules for source packages (pure decoders, no
  goroutines, no RPC clients, dispatcher owns routing).

Five-file convention per source:

```
internal/sources/<name>/
├── README.md
├── events.go       (event/topic decoding for on-chain; WS message decoding for CEX)
├── decode.go       (raw → internal/canonical.Trade)
├── consumer.go     (consumer.Source impl — wires the source into the registry)
└── source_test.go
```

On-chain sources additionally carry `dispatcher_adapter.go` (the
seam to `internal/dispatcher`) and a `factory_seed.go` for any
factory-deployed pair-contract enumeration. External (CEX / FX)
sources may have `streamer.go` + `backfill.go` instead of one
`consumer.go` when the venue separates streaming and REST
backfill (e.g. binance). The five files above are the canonical
core; review the existing connectors before adding a new one.

Plus fixtures in `test/fixtures/<name>/`.

**Coverage invariant (ADR-0030).** If your source writes to a new per-source hypertable (any `CREATE TABLE *_events|*_liquidity|*_positions|*_emissions|*_admin|*_transfers|*_swaps|*_stake_events|*_supply_events|*_auctions` migration), you MUST register it in `internal/storage/timescale/per_source_gaps.go`'s `DefaultGapDetectorTargets` in the same PR. CI's `TestGapDetectorTargetsCoverAllPerSourceHypertables` fails otherwise. A new Soroban source's PR should also add a dedicated `<source>-backfill` subcommand following the `blend_backfill.go` pattern so the orchestrator (`ratesengine-ops drain-cascade-window`) can repair it.

---

## Commit style

- Imperative mood, present tense (`"add soroswap swap decoder"` not
  `"added soroswap swap decoder"`).
- Scope prefix where useful: `sources/soroswap: correlate swap+sync`.
- Body wraps at 72 chars.
- Sign commits (`git commit -S`) — branch protection enforces.

Squash-merge is the default; preserve logical history only if every
commit is individually green + reviewable.

---

## Testing

Three layers:

- **Unit tests** — co-located with code (`foo.go` + `foo_test.go`).
  Run on every PR. Target 80%+ coverage per package.
- **Integration tests** — `test/integration/`, behind
  `// +build integration`. Testcontainers for real Postgres +
  Redis + MinIO. Run on label `ready-for-integration` + nightly.
- **Load tests** — `test/load/`, k6 scripts. Run pre-release.

See [engineering-standards.md §9 testing](docs/discovery/engineering-standards.md).

---

## Documentation

Every PR that changes code **must** update:

- The package's `doc.go` (or its `README.md`) if the public API
  changed.
- The user-facing doc under `docs/` if behaviour changed.

**You cannot skip docs.** "Docs drift" is the death spiral of
enterprise codebases. See
[engineering-standards.md §5](docs/discovery/engineering-standards.md).

---

## Security issues

**Do not open public issues for security concerns.** Email
`security@ratesengine.net` — see [SECURITY.md](SECURITY.md)
for the full disclosure process.

---

## Code of conduct

We follow the [Contributor Covenant](CODE_OF_CONDUCT.md). Be
respectful. Assume good faith. Ask before assuming you know why
someone wrote something a particular way.

---

## Getting unstuck

- `CODEOWNERS` tells you who to ping for review.
- ADRs in `docs/adr/` explain the major design choices.
- `docs/discovery/` holds the Phase-1 audit artefacts and is a
  useful reference when you're trying to understand why something
  works the way it does.
- If none of the above helps, open a discussion issue.

---

## What to work on

- Issues tagged `good-first-issue` are intentionally small and
  scoped for new contributors.
- `help-wanted` signals something the maintainers would welcome
  help on.
- `P0 cleanup` issues are tech-debt items we've committed to
  addressing on a schedule — helpful to pick up to earn reviewer
  trust.

Welcome aboard.
