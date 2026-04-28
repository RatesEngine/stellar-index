---
title: Release process — cutting a Rates Engine binary release
last_verified: 2026-04-28
status: living doc
---

# Release process

End-to-end procedure for cutting a Rates Engine binary release. This
is the runbook the on-rotation release engineer follows; it
implements the policy ratified in
[`docs/architecture/semver-policy.md`](../architecture/semver-policy.md).

CalVer tag format: `YYYY.MM.DD.N` (UTC date + same-day counter).

## Pre-flight

Done **before** cutting the tag — discovering any of these failed
mid-release wastes a tag and forces a `.N+1` cut.

1. **`main` is green.** The latest commit's CI run is all-passing on
   GitHub. No "merged with optional check failures" — every required
   AND optional job must be green.
2. **Working tree matches `main`.** `git checkout main && git pull
   --ff-only origin main`.
3. **CHANGELOG.md `[Unreleased]` is curated.** Walk it top to bottom
   and confirm every entry has a PR citation, every section heading
   that has no entries has been deleted, and that the order matches
   user-relevance (operator-visible at the top, internal refactors
   at the bottom).
4. **`pkg/*` version bumps are tagged.** If this release ships a new
   `pkg/client` version, that module's tag (`pkg/client/vX.Y.Z`)
   already exists on `main` from an earlier landed PR — **do not**
   bump `pkg/*` versions in the same commit as a CalVer release.
5. **goreleaser dry-run is clean.** `make release-dryrun` produces
   binaries for every target without errors.
6. **Stellar protocol is documented.** The protocol version the
   release was tested against is known (e.g. `23` for post-Whisk).
   Pulled from `stellar-core --version` on a test node, or from the
   pubnet block-explorer header.

## Cut

1. **Decide the tag.** Today's UTC date with the next available
   `.N`. Example: `2026.07.15.1` for the first release on 2026-07-15;
   `2026.07.15.2` if `.1` already exists (e.g. a quick rollback fix).
2. **Promote the CHANGELOG `[Unreleased]` block.** In a one-commit
   PR:
   - Replace `## [Unreleased]` with `## [YYYY.MM.DD.N] — YYYY-MM-DD`
   - Add a fresh empty `## [Unreleased]` block above it
   - At the bottom of the file, update the version-comparison links
     to point at the new tag
   - Title the PR `release: YYYY.MM.DD.N`
3. **Merge the release PR.** Squash-merge once CI is green. **Do
   not** tag before this PR has landed on `main` — the tag must
   point at the commit that contains the promoted CHANGELOG block.
4. **Create the tag.**
   ```sh
   git checkout main && git pull --ff-only origin main
   git tag YYYY.MM.DD.N
   git push origin YYYY.MM.DD.N
   ```
5. **Draft the GitHub Release.**
   ```sh
   cp .github/RELEASE_NOTES_TEMPLATE.md /tmp/release-notes.md
   # edit /tmp/release-notes.md — fill in every section
   gh release create YYYY.MM.DD.N \
     --title "Rates Engine YYYY.MM.DD.N" \
     --notes-file /tmp/release-notes.md \
     --verify-tag
   ```
   The release-notes content should be a near-verbatim copy of the
   CHANGELOG section for this version, expanded with the
   "Tested against" / "`pkg/*` versions" / "Migration notes" blocks
   from the template.
6. **Wait for goreleaser.** The release workflow (`.github/workflows/release.yml`)
   builds binaries for every target, signs them, and attaches them
   to the GitHub Release. Confirm every expected artefact is present
   before announcing.

## Post-flight

1. **Announce.** Post the release URL to the operator channel +
   `#rates-engine-public` if applicable.
2. **Update `docs/operations/r1-deployment-state.md`** with the
   running version and any operator action that was taken (e.g.
   migration step, config edit).
3. **Watch dashboards for 1 h.** The standard SLO board + the
   per-pair freshness panel. Any anomaly within the first hour gets
   the same triage as a normal incident — file a SEV before
   considering rollback.
4. **Rollback path** (if needed): re-tag the previous CalVer release
   as latest in your deploy automation, redeploy, file a SEV-2
   minimum and a postmortem in `docs/operations/postmortems/`.

## Hotfix releases

Same procedure as above, with these differences:

- Branch from the previous release tag (not `main`), apply the fix,
  cut a new `.N` tag on the same day OR a new date if the day has
  changed
- The CHANGELOG entry under the hotfix tag references the originating
  incident's postmortem
- Post-flight notification flags this as a hotfix and includes the
  scope of what changed (one-line + link to PR)

Hotfixes never include unrelated work. If a fix needs additional
changes that aren't strictly required, those go into the next
regular release — never a hotfix.

## Cross-references

- [`docs/architecture/semver-policy.md`](../architecture/semver-policy.md) — the policy this runbook implements
- [`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md) — the template release engineers fill in
- [`CHANGELOG.md`](../../CHANGELOG.md) — every release's entry follows the same structure
- [`docs/operations/sev-playbook.md`](sev-playbook.md) — incident response if a release misbehaves
