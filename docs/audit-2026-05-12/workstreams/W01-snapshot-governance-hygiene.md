# W01 — Snapshot, governance, repo hygiene

## Scope

Audit the repo as a managed artifact: governance docs, root files,
ignored vs tracked status, residue, repo-shape claims.

In scope:
- root files (README, CLAUDE, AGENTS, CONTRIBUTING, CODE_OF_CONDUCT,
  SECURITY, CODEOWNERS, VERSIONS, LICENSE, CHANGELOG,
  commitlint.config.js)
- `.gitignore` / `.gitleaks.toml` / `.golangci.yml` / `.spectral.yaml`
- `.github/` content (workflows, dependabot, templates) at the
  repo-management layer (CI runtime is W03)
- `.discovery-repos/` purpose + import-leak claim
- repo residue: pre-built binaries, `.DS_Store`, `.wrangler/`,
  audit residue, dirty-worktree state at audit kick-off

Out of scope:
- workflow content (W03)
- per-package import boundaries (W02 + W04)

## Inputs

- `inventory/repo-snapshot.md` — anchor commit + counts
- `inventory/area-counts.md` — per-top-level file counts
- `evidence/log.md` EV-1201, EV-1202
- `evidence/tree-observations.md` T-1201..T-1212

## Per-file checklist

| File | Check | Status | Evidence | Finding |
| --- | --- | --- | --- | --- |
| `README.md` | claims about scope, install, usage all true | | | |
| `CLAUDE.md` | every "things that will surprise you" caveat re-tested against code; the `0012` ADR gap is documented; r1-deployment-state claim of stellar-core removed re-checked | | | |
| `AGENTS.md` | onboarding instructions still accurate | | | |
| `CONTRIBUTING.md` | every step works against current Makefile | | | |
| `CODE_OF_CONDUCT.md` | unchanged | | | |
| `SECURITY.md` | disclosure address valid; PGP key (if any) current | | | |
| `CODEOWNERS` | every path has at least one owner; no stale owners | | | |
| `VERSIONS.md` | every pinned SHA still resolvable upstream (W04 verifies) | | | |
| `LICENSE` | Apache-2.0 unchanged | | | |
| `CHANGELOG.md` | all entries cite real PRs (spot-check rc.42..rc.48) | | | |
| `commitlint.config.js` | rules enforced by CI? `git log --oneline` shows compliance? | | | |
| `.gitignore` | covers `.DS_Store`, `.wrangler/`, build outputs, node_modules; no over-broad exclusion | | | |
| `.gitleaks.toml` | rules cover all secret patterns we have (api keys, oauth tokens, AWS creds, JWT secrets); allowlist not too permissive | | | |
| `.golangci.yml` | enabled linters cover gocognit, errcheck, govet, staticcheck, etc.; baseline file for grandfathered violations? | | | |
| `.spectral.yaml` | OpenAPI lint rules enabled | | | |
| `.github/dependabot.yml` | covers go modules + actions + pnpm | | | |
| `.github/PULL_REQUEST_TEMPLATE.md` | usable | | | |
| `.github/RELEASE_NOTES_TEMPLATE.md` | usable | | | |
| `.github/ISSUE_TEMPLATE/*` | exist? | | | |

## Repo-residue checks

| Check | Status | Notes |
| --- | --- | --- |
| Pre-built binaries `ratesengine-{api,aggregator,indexer}` at repo root tracked or ignored? | | T-1202 |
| `.DS_Store` tracked? | | macOS residue |
| `.wrangler/` tracked? | | Cloudflare local cache |
| `bin/linux-amd64/` tracked or ignored? | | Build output |
| Worktree state at audit kick-off | dirty (audit workspace creation) | EV-1201 |

## Discovery-repo claim

| Claim | Verified | Evidence |
| --- | --- | --- |
| 29 cloned upstream projects under `.discovery-repos/` | | T-1203 |
| None imported by product code | | W04 grep |
| Excluded by EX-1204 from per-file walk | | 06-exclusions-register.md |

## Adversarial vectors

- A: D2.2 (Pre-built binary at repo root committed; reviewer trusts it)
- A: D2.3 (GHCR image signing not enforced — covered in W03/W19, listed here for cross-ref)

## Cross-workstream dependencies

- Feeds into W02 (architecture truth) for any doc claim about
  package layout
- Feeds into W03 (CI/CD) for `.github/` workflow runtime audit
- Feeds into W04 (supply chain) for VERSIONS.md + dependabot
- Feeds into W19 (security) for `.gitleaks.toml`

## Closure criteria

- Every per-file check has a terminal status
- All repo-residue checks resolved (with finding if anything
  is wrong-tracked)
- Discovery-repo import-leak check completed (with W04)
- Findings raised:
