---
title: Public-flip strategy — publishing Rates Engine to a public repo at v1.0
last_verified: 2026-04-30
status: living doc
---

# Public-flip strategy

The Rates Engine source must go public — it's a binding commitment
in [`docs/stellar-rfp.md`](../stellar-rfp.md). This doc captures
**how** we make that flip, and the prep work that must be done
before it.

The single binding decision: **publish to a NEW public repo, do NOT
rewrite-and-force-push the existing private repo.**

## Why a new repo, not history rewrite

The private repo is the source of truth and is irreplaceable. It
holds the only copy of the discovery archive, the WASM-audit
evidence trail, the per-region operational notes, and four months
of CI build history. A history rewrite ("squash everything into one
commit, force-push main") has a non-zero probability of corrupting
that record — and if the rewrite is wrong AND the backup fails, we
lose everything.

A new-repo publish gives us **zero force-push risk**, preserves the
private audit trail intact, and produces a clean public artefact at
the same time. The cost — two repos coexisting — is negligible.

## What "the new repo" looks like

- Org: `RatesEngine` (org already created)
- Name: `rates-engine` (matches the Go module path
  `github.com/RatesEngine/rates-engine` already used internally)
- License: Apache-2.0 (same as private)
- Default branch: `main`
- Initial commit message: `Initial public release — Rates Engine v1.0`
  containing the entire working tree at the v1.0 commit, no history
- Releases: starts CalVer at `2026.06.30.1` or whatever the launch
  tag is — the public repo's release cadence picks up where private
  goes silent (no parallel releases on both)

## Pre-flip checklist

Done as ordinary forward-going PRs against the private repo, before
the flip. Each row's "evidence" column points at the file or PR that
satisfies it.

| ✓ | Item | Evidence |
|---|---|---|
| ☑ | Postgres password from CTX legacy probe scrubbed from working tree | PR #169 |
| ☑ | r1 public IP scrubbed from working tree | PR #169 |
| ☑ | `configs/ansible/inventory/r1.yml` removed from tracked files (added to `.gitignore`) | PR #169 |
| ☑ | `SECURITY.md` lists `security@ratesengine.net` as the public reporting address (not an internal alias) | `SECURITY.md:9` (verified 2026-04-30) |
| ☑ | `CODEOWNERS` uses external @-handles only — no internal-only logins | `CODEOWNERS` (only `@ash`, verified 2026-04-30) |
| ☑ | `README.md` reads as a public landing page — what the project does, who it's for, getting-started link, badge for license + CI | `README.md` (verified 2026-04-30) |
| ☑ | `CONTRIBUTING.md` welcomes external contributors (issue triage SLA, PR review SLA, code-of-conduct link) | `CONTRIBUTING.md` (verified 2026-04-30) |
| ☑ | `CODE_OF_CONDUCT.md` is the standard Contributor Covenant | `CODE_OF_CONDUCT.md` (Contributor Covenant v2.1, verified 2026-04-30) |
| ☑ | `LICENSE` is Apache-2.0 | `LICENSE` (Apache 2.0, verified 2026-04-30) |
| ☑ | `.github/dependabot.yml` has no internal-registry references | `.github/dependabot.yml` (verified 2026-04-30 — only public registries) |
| ☑ | Every CI workflow in `.github/workflows/` runs on the public repo without internal secrets | `.github/workflows/{ci,api-docs}.yml` (verified 2026-04-30 — no `secrets.` references) |
| ☑ | `CLAUDE.md` reads cleanly without referencing the private discovery archive paths or internal-only operator names | `CLAUDE.md` (reviewed 2026-04-30 — pattern scan + manual spot-checks; 0 private references; 2 non-blocking editorial recs noted) |
| ☑ | `docs/operations/r1-deployment-state.md` does not include credentials, API keys, or unredacted IPs | `docs/operations/r1-deployment-state.md` (verified 2026-04-30 — credentials are pointers only, no IPs in file) |
| ☑ | Every ADR's "Status" reflects current state (no stale "Proposed" on accepted ADRs) | `docs/adr/` (all 0001-0021 are `Accepted`, verified 2026-04-30; 0012 is reserved-future per multi-region-topology.md) |
| ☑ | `docs/discovery/` archive is OK to publish — no sensitive customer data in the RFPs or proposal correspondence | `docs/discovery/` (reviewed 2026-04-30 — 9-pattern sensitivity scan across all 48 files; 0 hits in credential/PII categories; 6 hits across qualitative categories all benign on inspection) |
| ☑ | Final secret scan with `gitleaks detect --source .` returns clean | `gitleaks 8.30.1` — 0 leaks across 553 commits, scanned 2026-04-30 |

**All 16 rows verified 2026-04-30.** Both originally-deferred
human-in-the-loop reviews now have written verdicts (citations
in the rows above). Checklist is execution-ready; the next step
is the cut-over mechanics in §below.

## Cut-over mechanics

```sh
# 1. Tag and verify the v1.0 source on private
cd ~/code/ratesengine
git checkout main && git pull --ff-only
gitleaks detect --source . --redact --exit-code 1   # last secret scan
make test                                           # one final green build

# 2. Fork into a fresh working dir with no history
cd ~/code
git clone --no-local --no-hardlinks ratesengine ratesengine-public
cd ratesengine-public

# 3. Orphan-branch the public initial commit
git checkout --orphan public-v1
git add -A
git commit -m "Initial public release — Rates Engine v1.0

This is the first public release of the Rates Engine source code.
History prior to this commit lives in a private development repo
that is not published; CalVer release notes from this point forward
live in this repository.

See CHANGELOG.md and docs/architecture/semver-policy.md."

# 4. Verify working tree matches private at v1.0
diff -r --brief --exclude=.git ../ratesengine . | head -50
# Expect: zero diff. Anything reported is a publish-time slip-up.

# 5. Create the GitHub repo + push
gh repo create RatesEngine/rates-engine \
    --public \
    --description "Stellar-network pricing API: ingest, aggregate, serve VWAP/TWAP/OHLC" \
    --license Apache-2.0
git remote remove origin
git remote add origin git@github.com:RatesEngine/rates-engine.git
git push -u origin public-v1:main

# 6. Tag the release on the public repo
git tag YYYY.MM.DD.N
git push origin YYYY.MM.DD.N
gh release create YYYY.MM.DD.N \
    --title "Rates Engine YYYY.MM.DD.N — Initial public release" \
    --notes-file /tmp/release-notes.md \
    --verify-tag
```

The clone-`--no-local` step is deliberate: a `git clone` with
hardlinks shares object storage, which means an `--orphan` operation
on the clone could affect the private repo's reflog. `--no-local
--no-hardlinks` produces a fully-independent copy.

## Post-flip

1. **Branch protection.** On the new public repo, require status
   checks (every job currently in `.github/workflows/`), require PR
   review (1 approver minimum), forbid force-push to `main`,
   forbid deletion of `main`.
2. **Re-create CI secrets.** Anything that was a GitHub Actions
   secret in private (e.g. AWS credentials for the goreleaser job)
   needs to be re-added in the public repo's settings. Audit-log
   the addition.
3. **Re-create issue templates / labels.** GitHub does not migrate
   these on a clone-and-push. Re-import from the private repo's
   `.github/` directory.
4. **DNS cutover.**
   - `docs.ratesengine.net` → public-repo GitHub Pages (or our
     equivalent — see L3.15 self-service onboarding)
   - `status.ratesengine.net` → status page (L4.11)
5. **Stop CI on private.** Set workflows to `workflow_dispatch`-only
   on the private repo, so it stops auto-burning Actions minutes.
   Keep the repo itself alive — it remains the audit trail.
6. **Customer announcement.** Stellar RFP contacts get the link;
   `#rates-engine-public` Slack channel announcement; tweet from
   the project handle if applicable.
7. **Decommission cron jobs / Renovate / Dependabot** scoped to the
   private repo (re-scope to public).

## Two-repo coexistence

After the flip, both repos exist:

- **Private (`ash/code/ratesengine`)** — full history, internal
  audit trail, day-to-day work continues here. New work lands here
  first; later mirrored to public via merge PR (see below).
- **Public (`RatesEngine/rates-engine`)** — clean derived artefact;
  external PRs land here and get backported privately if they
  require additional internal-context discussion before merge.

Mirror cadence: weekly batch-merge from private → public for
unblocked changes; immediate mirror for security fixes (so the
public repo never lags on a CVE patch).

If a piece of work is purely internal (e.g. operational runbook
updates that reference private IPs), it stays on private only —
never gets mirrored. The mirror discipline is part of the L6.5
documentation sweep that precedes flip.

## Cross-references

- [`docs/architecture/semver-policy.md`](../architecture/semver-policy.md) — versioning contract that binds public + private the same way
- [`docs/operations/release-process.md`](release-process.md) — release runbook that applies post-flip
- [`docs/architecture/launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md) §Finalization — L6.3 (this doc) and L6.4 (production cutover, which depends on this)
- [`SECURITY.md`](../../SECURITY.md) — must be public-ready before flip
- [`CONTRIBUTING.md`](../../CONTRIBUTING.md) — must welcome external contributors before flip
