# AGENTS.md — Stellar Index

> Generic AI-agent orientation file. This repo's full map lives in
> [CLAUDE.md](CLAUDE.md) (same content, Claude-convention naming).
> Use whichever your agent scaffolding prefers.

## Docs index

| Doc | Contents |
| --- | -------- |
| [README.md](README.md) | Project overview, status, contact |
| [CLAUDE.md](CLAUDE.md) | Full repo orientation (layout, invariants, task recipes, footguns) |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Contribution workflow + Definition of Done |
| [SECURITY.md](SECURITY.md) | Vulnerability disclosure process |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Contributor Covenant 2.1 |
| [CHANGELOG.md](CHANGELOG.md) | Keep-a-Changelog format; Unreleased at top |
| [VERSIONS.md](VERSIONS.md) | Pinned SHAs of every upstream dependency |
| [docs/engineering-standards.md](docs/engineering-standards.md) | The enforcement policy |
| [docs/architecture/semver-policy.md](docs/architecture/semver-policy.md) | Versioning + repo-layout rationale |
| `docs/adr/` | Architecture Decision Records (numbered, immutable) |
| `docs/architecture/` | Narrative design docs |
| `docs/protocols/` | Per-protocol verification pages |

## Invariants — never violate these

See [CLAUDE.md](CLAUDE.md) for the full list with ADR cross-refs.
Short form:

1. **i128 / u128 never truncates to int64.** ADR-0003.
2. **Horizon is not in our architecture.** ADR-0001.
3. **Self-hosted storage is S3-compatible, not local filesystem.**
   ADR-0002.
4. **Monorepo with one `go.mod`.** ADR-0005.
5. **Validator track post-launch targets Tier-1.** ADR-0004.
6. **Per-source coverage invariant.** Every per-source hypertable
   must register in `DefaultGapDetectorTargets` (same PR as the
   migration). ADR-0030.

## Quick-start commands

```sh
make help              # list every target
make dev               # docker-compose up the full stack
make lint              # gofumpt + golangci-lint (architectural import boundaries: `make lint-imports`)
make test              # unit tests with race
make verify            # canonical pre-push gate (fmt, vet, lint, docs, test) — short of integration + load + chaos
make docs-all          # regenerate reference docs from OpenAPI + struct tags + obs/*.go metric Name: fields
```

## When in doubt

- Smallest-possible PR that advances one thing.
- Read the nearest `doc.go` or `README.md` before you touch code.
- Decisions go in `docs/adr/`, not architecture docs.
- Every `TODO` has a linked issue.
- Every amount is `*big.Int`, not `int64`.
